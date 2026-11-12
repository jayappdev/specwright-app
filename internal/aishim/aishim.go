// Package aishim lets SpecWright count the AI that a tool uses itself.
//
// A tool may call an agent CLI on its own (`claude -p …`, `copilot -p …`).
// Those calls happen outside SpecWright's agent drivers, so their tokens,
// cost and premium requests would be missing from the activity log and
// the usage totals. While a tool runs, SpecWright puts a folder at the
// front of its PATH with small `claude` and `copilot` launchers that start
// this executable as `SpecWright aishim <cli> <args…>`. The shim runs the
// real CLI with the tool's own arguments, asks it for its usage, writes
// one line per call to $SPECWRIGHT_AI_USAGE, and gives the tool exactly
// the output it would have got:
//
//   - copilot -p: adds --usage-output-file (when the CLI supports it);
//     stdout and stderr pass through untouched. Older CLIs: the "Total
//     usage est" line on stderr is read instead.
//   - claude -p (text output): runs with --output-format json and prints
//     only the answer, as text mode does; json and stream-json output pass
//     through and are read on the way.
//   - anything else (interactive use, --help, …) passes straight through.
package aishim

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Environment variables the shim reads.
const (
	EnvDir   = "SPECWRIGHT_AISHIM_DIR" // the launchers' folder (left out when looking for the real CLI)
	EnvUsage = "SPECWRIGHT_AI_USAGE"   // where usage lines are appended
)

// CLIs are the agent CLIs the shim stands in for.
var CLIs = []string{"claude", "copilot"}

// Record is one AI call made by a tool.
type Record struct {
	CLI             string  `json:"cli"`
	Model           string  `json:"model,omitempty"`
	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	CachedTokens    int     `json:"cached_tokens"`
	PremiumRequests float64 `json:"premium_requests"`
	AIUnits         float64 `json:"ai_units,omitempty"`
	CostUSD         float64 `json:"cost_usd"`
	DurationMS      int64   `json:"duration_ms"`
	OK              bool    `json:"ok"`
}

// Install writes the launchers into dir, each starting exe with "aishim".
func Install(dir, exe string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, c := range CLIs {
		var name, body string
		if runtime.GOOS == "windows" {
			name, body = c+".cmd", fmt.Sprintf("@echo off\r\n\"%s\" aishim %s %%*\r\n", exe, c)
		} else {
			name, body = c, fmt.Sprintf("#!/bin/sh\nexec '%s' aishim %s \"$@\"\n", strings.ReplaceAll(exe, "'", `'\''`), c)
		}
		p := filepath.Join(dir, name)
		if old, err := os.ReadFile(p); err == nil && string(old) == body {
			continue
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ReadUsage reads (and removes) a usage file.
func ReadUsage(path string) []Record {
	b, err := os.ReadFile(path)
	_ = os.Remove(path)
	if err != nil {
		return nil
	}
	var out []Record
	for _, l := range strings.Split(string(b), "\n") {
		var r Record
		if json.Unmarshal([]byte(l), &r) == nil && r.CLI != "" {
			out = append(out, r)
		}
	}
	return out
}

// Main runs the shim: args are the CLI name and its arguments. It returns
// the exit code.
func Main(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "aishim: which CLI?")
		return 2
	}
	cli, rest := args[0], args[1:]
	path := withoutShim(os.Getenv("PATH"), os.Getenv(EnvDir))
	_ = os.Setenv("PATH", path) // the real CLI, and anything it starts, never sees the shim
	real, err := exec.LookPath(cli)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: command not found\n", cli)
		return 127
	}
	usageFile := os.Getenv(EnvUsage)
	if usageFile == "" || !promptMode(cli, rest) {
		return run(exec.Command(real, rest...), nil)
	}
	switch cli {
	case "copilot":
		return copilot(real, rest, usageFile)
	case "claude":
		return claude(real, rest, usageFile)
	}
	return run(exec.Command(real, rest...), nil)
}

func withoutShim(path, dir string) string {
	if dir == "" {
		return path
	}
	clean := filepath.Clean(dir)
	var keep []string
	for _, p := range filepath.SplitList(path) {
		if p != "" && filepath.Clean(p) != clean {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

// promptMode says whether the call is a one-shot prompt (the only kind
// whose usage is collected).
func promptMode(cli string, args []string) bool {
	for _, a := range args {
		switch {
		case a == "-p", a == "--prompt", strings.HasPrefix(a, "--prompt="):
			return true
		case cli == "claude" && a == "--print":
			return true
		}
	}
	return false
}

// flagValue finds --name value or --name=value.
func flagValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

// run starts cmd with the shim's stdin/stdout/stderr (unless set), passes
// on interrupts, and returns the exit code.
func run(cmd *exec.Cmd, after func()) int {
	if cmd.Stdin == nil {
		cmd.Stdin = os.Stdin
	}
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 127
	}
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt, termSignal())
	go func() {
		for s := range sig {
			_ = cmd.Process.Signal(s)
		}
	}()
	err := cmd.Wait()
	signal.Stop(sig)
	close(sig)
	if after != nil {
		after()
	}
	if cmd.ProcessState != nil {
		if c := cmd.ProcessState.ExitCode(); c >= 0 {
			return c
		}
	}
	if err != nil {
		return 1
	}
	return 0
}

func record(file string, r Record) {
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// --- copilot -----------------------------------------------------------------

var premiumRE = regexp.MustCompile(`(?i)total usage est:\s*([\d.]+)\s+premium request`)

func copilot(real string, args []string, usageFile string) int {
	start := time.Now()
	uf, given := flagValue(args, "--usage-output-file")
	if !given && supportsUsageFile(real) {
		tmp, err := os.CreateTemp("", "copilot-usage-*.json")
		if err == nil {
			uf = tmp.Name()
			tmp.Close()
			defer os.Remove(uf)
			args = append(append([]string{}, args...), "--usage-output-file", uf)
		}
	}
	var errTail tailBuffer
	cmd := exec.Command(real, args...)
	cmd.Stderr = io.MultiWriter(os.Stderr, &errTail)
	code := run(cmd, nil)
	r := Record{CLI: "copilot", DurationMS: time.Since(start).Milliseconds(), OK: code == 0}
	if uf != "" {
		readCopilotUsage(uf, &r)
	}
	if r.PremiumRequests == 0 {
		if m := premiumRE.FindStringSubmatch(errTail.String()); m != nil {
			r.PremiumRequests, _ = strconv.ParseFloat(m[1], 64)
		}
	}
	record(usageFile, r)
	return code
}

func readCopilotUsage(path string, r *Record) {
	var u struct {
		Premium float64 `json:"totalPremiumRequestCost"`
		NanoAiu float64 `json:"totalNanoAiu"`
		Models  map[string]struct {
			Usage struct {
				In        int `json:"inputTokens"`
				Out       int `json:"outputTokens"`
				CacheRead int `json:"cacheReadTokens"`
			} `json:"usage"`
		} `json:"modelMetrics"`
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &u) != nil {
		return
	}
	r.PremiumRequests, r.AIUnits = u.Premium, u.NanoAiu/1e9
	var models []string
	for m, mm := range u.Models {
		r.InputTokens += mm.Usage.In
		r.OutputTokens += mm.Usage.Out
		r.CachedTokens += mm.Usage.CacheRead
		models = append(models, m)
	}
	r.Model = strings.Join(models, ", ")
}

// supportsUsageFile asks the CLI's help once per installed version.
func supportsUsageFile(real string) bool {
	fi, err := os.Stat(real)
	if err != nil {
		return false
	}
	key := fmt.Sprintf("%s|%d|%d", real, fi.Size(), fi.ModTime().UnixNano())
	cache := ""
	if d := os.Getenv(EnvDir); d != "" {
		cache = filepath.Join(d, ".copilot-flags")
		if b, err := os.ReadFile(cache); err == nil {
			if v, ok := strings.CutPrefix(string(b), key+"="); ok {
				return v == "yes"
			}
		}
	}
	out, _ := exec.Command(real, "--help").CombinedOutput()
	ok := bytes.Contains(out, []byte("--usage-output-file"))
	if cache != "" {
		_ = os.WriteFile(cache, []byte(key+"="+map[bool]string{true: "yes", false: "no"}[ok]), 0o644)
	}
	return ok
}

// --- claude ------------------------------------------------------------------

type claudeResult struct {
	Type    string  `json:"type"`
	IsError bool    `json:"is_error"`
	Result  string  `json:"result"`
	Cost    float64 `json:"total_cost_usd"`
	Usage   struct {
		In            int `json:"input_tokens"`
		Out           int `json:"output_tokens"`
		CacheRead     int `json:"cache_read_input_tokens"`
		CacheCreation int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	ModelUsage map[string]json.RawMessage `json:"modelUsage"`
}

func (c claudeResult) fill(r *Record) {
	r.InputTokens = c.Usage.In + c.Usage.CacheRead + c.Usage.CacheCreation
	r.OutputTokens, r.CachedTokens, r.CostUSD = c.Usage.Out, c.Usage.CacheRead, c.Cost
	var models []string
	for m := range c.ModelUsage {
		models = append(models, m)
	}
	r.Model = strings.Join(models, ", ")
}

func claude(real string, args []string, usageFile string) int {
	start := time.Now()
	format, _ := flagValue(args, "--output-format")
	r := Record{CLI: "claude"}
	var code int
	switch format {
	case "", "text":
		// ask for JSON, hand the tool the plain answer as text mode would
		a := make([]string, 0, len(args)+2)
		for i := 0; i < len(args); i++ {
			if args[i] == "--output-format" {
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--output-format=") {
				continue
			}
			a = append(a, args[i])
		}
		a = append(a, "--output-format", "json")
		var out bytes.Buffer
		cmd := exec.Command(real, a...)
		cmd.Stdout = &out
		code = run(cmd, nil)
		var res claudeResult
		if json.Unmarshal(bytes.TrimSpace(out.Bytes()), &res) == nil && res.Type == "result" {
			res.fill(&r)
			text := res.Result
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			if res.IsError {
				os.Stderr.WriteString(text)
			} else {
				os.Stdout.WriteString(text)
			}
		} else {
			os.Stdout.Write(out.Bytes()) // not what we expected: hand it over as is
		}
	default: // json / stream-json: pass through, read the result on the way
		pr, pw := io.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			sc := bufio.NewScanner(pr)
			sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
			var all strings.Builder
			for sc.Scan() {
				l := sc.Text()
				var res claudeResult
				if json.Unmarshal([]byte(l), &res) == nil && res.Type == "result" {
					res.fill(&r)
				}
				if format == "json" {
					all.WriteString(l)
				}
			}
			if format == "json" && r.InputTokens == 0 {
				var res claudeResult
				if json.Unmarshal([]byte(all.String()), &res) == nil {
					res.fill(&r)
				}
			}
			_, _ = io.Copy(io.Discard, pr)
		}()
		cmd := exec.Command(real, args...)
		cmd.Stdout = io.MultiWriter(os.Stdout, pw)
		code = run(cmd, nil)
		pw.Close()
		<-done
	}
	r.DurationMS, r.OK = time.Since(start).Milliseconds(), code == 0
	record(usageFile, r)
	return code
}

// tailBuffer keeps the last 16 KB written to it.
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 16<<10 {
		t.b = t.b[len(t.b)-16<<10:]
	}
	return len(p), nil
}
func (t *tailBuffer) String() string { return string(t.b) }
