package core

// Flow runs: calls out of a step — tools, shell commands and the agent.

import (
	"codegen/internal/bigout"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"codegen/internal/agent"
	"codegen/internal/extension"
	"codegen/internal/store"
)

func (h *Hub) flowTool(ctx context.Context, root string, fr *flowRun, step, name string, args map[string]any, timeout time.Duration, input ...flowInput) (string, error) {
	var e *extension.Extension
	for _, x := range h.Callable() {
		if x.Name == name {
			x := x
			e = &x
		}
	}
	builtinTool := IsBuiltinTool(name)
	if builtinTool {
		b, err := h.flowBuiltin(name)
		if err != nil {
			return "", err
		}
		e = b
	}
	if e == nil {
		return "", fmt.Errorf("the tool %s is not available or not approved", name)
	}
	id := fmt.Sprintf("%s-%d", step, time.Now().UnixNano())
	h.flowPublish(fr, "tool.call", name+" "+argSummary(args), map[string]any{"_step": step, "tool": name, "args": args, "id": id})
	called := time.Now()
	flowID := ""
	if fr.flow != nil {
		flowID = fr.flow.ID
	}
	record := func(ok bool, ms int64, msg string) { // tool reliability (toolstats.go)
		h.recordToolCall(store.ToolCall{Workspace: root, Tool: name, Source: "flow", OK: ok, DurationMS: ms, Error: msg, Args: args, RunID: fr.st.ID, Flow: flowID, Step: strings.TrimSuffix(step, "-out")})
	}
	fail := func(err error) (string, error) {
		h.flowPublish(fr, "tool.result", name+" failed", map[string]any{"_step": step, "tool": name, "id": id, "ok": false, "output": err.Error()})
		record(false, time.Since(called).Milliseconds(), err.Error())
		return "", err
	}
	var res extension.RunResult
	if builtinTool {
		// built-ins take their data through their fields; the connected
		// steps' output is still saved for {{input_file}}
		res = h.runFlowBuiltin(ctx, e, args, timeout)
	} else {
		r, err := func() (extension.RunResult, error) {
			if err := e.Runnable(); err != nil {
				return extension.RunResult{}, err
			}
			// the arguments as the tool declares them: types, allowed values, defaults
			checked, probs := extension.CheckArgs(*e, args)
			if len(probs) > 0 {
				return extension.RunResult{}, fmt.Errorf("the arguments do not fit %s: %s", name, strings.Join(probs, "; "))
			}
			args = checked
			if extension.HasDeps(*e) {
				if err := h.Prepare(ctx, *e); err != nil {
					return extension.RunResult{}, fmt.Errorf("installing dependencies failed: %v", err)
				}
			}
			secrets, err := h.ResolveSecrets(ctx, *e)
			if err != nil {
				return extension.RunResult{}, fmt.Errorf("credentials: %v", err)
			}
			// the previous steps' output, always handed over: on standard input and
			// as a file named by $SPECWRIGHT_INPUT_FILE (tools can use either)
			env := h.Env()
			var stdin *string
			if len(input) > 0 && input[0].text != "" {
				in := input[0]
				stdin = &in.text
				if in.file == "" {
					if p, done := h.flowInputFile(fr, step, in.text); p != "" {
						defer done()
						in.file = p
					}
				}
				if in.file != "" {
					env = append(env, "SPECWRIGHT_INPUT_FILE="+in.file)
				}
				env = append(env, "SPECWRIGHT_STEP="+step, "SPECWRIGHT_RUN="+fr.st.ID)
			}
			return extension.ExecInput(ctx, root, *e, args, secrets, env, timeout, stdin), nil
		}()
		if err != nil {
			return fail(err)
		}
		res = r
	}
	// the AI the tool used itself (its own claude / copilot calls)
	if au, ev, used := toolAIUsage(name, res.AI); used {
		fr.update(func(st *FlowRunState) {
			st.Usage.Add(au)
			if s := fr.step(strings.TrimSuffix(step, "-out")); s != nil {
				s.Usage.Add(au)
			}
		})
		ev.Data["_step"] = step
		h.flowPublish(fr, ev.Type, ev.Summary, ev.Data)
	}
	// keep what the tool was called with and what it returned
	files := map[string]any{}
	base := stepFilesDir(fr.st.ID, strings.TrimSuffix(step, "-out"))
	prefix := ""
	fr.update(func(st *FlowRunState) {
		if fs := fr.step(step); fs == nil || fs.Type == "output" {
			prefix = unsafeFile.ReplaceAllString(name, "_") + "-" // an output action's tool
		}
	})
	if b, err := json.MarshalIndent(args, "", "  "); err == nil {
		if rel, err := h.writeRunFile(base+"/"+prefix+"args.json", string(b)); err == nil {
			files["args_file"] = rel
		}
	}
	if rel, err := h.writeRunFile(base+"/"+prefix+"output."+contentExt(res.Output), res.Output); err == nil {
		files["output_file"] = rel
		if prefix == "" {
			fr.update(func(st *FlowRunState) {
				if s := fr.step(step); s != nil {
					s.OutputFile = rel
				}
			})
		}
	}
	if len(input) > 0 && input[0].file != "" {
		if rel, err := filepath.Rel(h.Root(), input[0].file); err == nil {
			files["input_file"] = filepath.ToSlash(rel)
		}
	}
	// the activity event shows the start; the whole output is in output_file
	shown := res.Output
	if len(shown) > 64<<10 {
		shown = bigout.SafeCut(shown, 64<<10) + fmt.Sprintf("\n… (%s in all; the whole output is in the step's output file)", bigout.Human(len(res.Output)))
	}
	data := map[string]any{"_step": step, "tool": name, "id": id, "ok": res.OK, "output": shown, "exit_code": res.ExitCode, "duration_ms": res.DurationMS}
	for k, v := range files {
		data[k] = v
	}
	h.flowPublish(fr, "tool.result", fmt.Sprintf("%s %s · %dms", name, map[bool]string{true: "ok", false: "failed"}[res.OK], res.DurationMS), data)
	record(res.OK, res.DurationMS, firstNonEmpty(res.Error, map[bool]string{true: "", false: fmt.Sprintf("exit code %d", res.ExitCode)}[res.OK]))
	if !res.OK {
		msg := firstNonEmpty(res.Error, fmt.Sprintf("exit code %d", res.ExitCode))
		if t := strings.TrimSpace(res.Output); t != "" {
			if len(t) > 600 {
				t = "…" + strings.ToValidUTF8(t[len(t)-600:], "")
			}
			msg += ": " + t
		}
		return res.Output, errors.New(msg)
	}
	if res.Truncated {
		// passing on the first 32 MB would hand the next step broken data
		return res.Output, fmt.Errorf("%s printed more than %s; the next step would get broken data. Make the tool write large results to a file and print its path", name, bigout.Human(extension.MaxToolOutput))
	}
	return res.Output, nil
}

func argSummary(args map[string]any) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := fmt.Sprint(args[k])
		if len(v) > 40 {
			v = v[:40] + "…"
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}

// flowCommand runs a shell command with data on stdin.
func (h *Hub) flowCommand(ctx context.Context, root string, fr *flowRun, step, command, stdin string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("no command")
	}
	id := fmt.Sprintf("%s-cmd-%d", step, time.Now().UnixNano())
	h.flowPublish(fr, "tool.call", "$ "+command, map[string]any{"_step": step, "tool": "command", "args": map[string]any{"command": command}, "id": id})
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command)
	}
	cmd.Dir = root
	cmd.Env = append(os.Environ(), h.agentPackageEnv(root)...)
	cmd.Stdin = strings.NewReader(stdin)
	start := time.Now()
	out, err := cmd.Output()
	ok := err == nil
	msg := string(out)
	if ee, isExit := err.(*exec.ExitError); isExit && len(ee.Stderr) > 0 {
		msg = strings.TrimSpace(string(ee.Stderr))
	}
	h.flowPublish(fr, "tool.result", fmt.Sprintf("command %s · %dms", map[bool]string{true: "ok", false: "failed"}[ok], time.Since(start).Milliseconds()),
		map[string]any{"_step": step, "tool": "command", "id": id, "ok": ok, "output": msg, "duration_ms": time.Since(start).Milliseconds()})
	if err != nil {
		return "", fmt.Errorf("%s: %v %s", command, err, strings.TrimSpace(msg))
	}
	return string(out), nil
}

// flowAgent runs one AI prompt with the chosen agent, forwarding its
// events into the run and returning its answer.
func (h *Hub) flowAgent(ctx context.Context, root string, fr *flowRun, step, prompt, model string) (string, store.Usage, error) {
	var u store.Usage
	h.mu.RLock()
	name := h.agentName
	if model == "" {
		model = h.model
	}
	h.mu.RUnlock()
	drv, err := h.Agents.Get(name)
	if err != nil {
		return "", u, err
	}
	mcp, done, err := h.mcpConfig(true)
	if err != nil {
		return "", u, err
	}
	defer done()
	var text strings.Builder
	var mu sync.Mutex
	emit := func(ev agent.Event) {
		mu.Lock()
		switch ev.Type {
		case "model.request":
			u.InputTokens += toInt(ev.Data["input_tokens"])
			u.CachedTokens += toInt(ev.Data["cached_tokens"])
			u.ModelCalls++
			if m, _ := ev.Data["model"].(string); m != "" {
				fr.update(func(st *FlowRunState) { st.Model = m })
			}
		case "model.response":
			u.OutputTokens += toInt(ev.Data["output_tokens"])
		case "model.text":
			if t, _ := ev.Data["text"].(string); t != "" {
				if text.Len() > 0 {
					text.WriteString("\n\n")
				}
				text.WriteString(t)
			}
		case "tool.call":
			u.ToolCalls++
		case "tool.result":
			if ok, _ := ev.Data["ok"].(bool); !ok {
				u.ToolErrors++
			}
		case "run.usage":
			if c, ok := ev.Data["cost_usd"].(float64); ok && c > 0 {
				u.CostUSD += c
			}
			u.PremiumRequests += toInt(ev.Data["premium_requests"])
			if in := toInt(ev.Data["input_tokens"]); in > 0 {
				u.InputTokens += in
				u.OutputTokens += toInt(ev.Data["output_tokens"])
				u.CachedTokens += toInt(ev.Data["cached_tokens"])
			}
		}
		mu.Unlock()
		data := map[string]any{"_step": step}
		for k, v := range ev.Data {
			data[k] = v
		}
		typ := ev.Type
		if typ == "run.usage" {
			typ = "model.usage"
		}
		h.flowPublish(fr, typ, ev.Summary, data)
	}
	res, err := drv.Run(ctx, agent.Request{Purpose: "flow", Step: step, Prompt: prompt, Dir: root, Model: model, Env: h.childEnv(), MCPConfig: mcp,
		AllowTools: []string{"mcp__agentbridge"}}, emit)
	if res.CostUSD > 0 && u.CostUSD == 0 {
		u.CostUSD = res.CostUSD
	}
	if res.PremiumRequests > 0 && u.PremiumRequests == 0 {
		u.PremiumRequests = res.PremiumRequests
	}
	out := strings.TrimSpace(res.Text)
	if out == "" {
		out = strings.TrimSpace(text.String())
	}
	if err != nil {
		return out, u, err
	}
	if out == "" {
		return "", u, errors.New("the agent returned no answer")
	}
	return out, u, nil
}

// --- review and iteration -------------------------------------------------------
