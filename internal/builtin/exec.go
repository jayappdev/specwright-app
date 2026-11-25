package builtin

import (
	"codegen/internal/bigout"
	"encoding/json"

	"bytes"
	"codegen/internal/flow"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const maxCmdOutput = 64 << 10

// ring keeps the last n bytes written to it.
type ring struct {
	mu    sync.Mutex
	buf   []byte
	n     int
	total int
}

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total += len(p)
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.n {
		r.buf = r.buf[len(r.buf)-r.n:]
	}
	return len(p), nil
}

func (r *ring) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := string(r.buf)
	if r.total > r.n {
		// the kept tail starts after the first line break (or at least at a character)
		if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)/5 {
			s = s[i+1:]
		}
		s = strings.ToValidUTF8(s, "")
		s = fmt.Sprintf("[… first %s of output dropped]\n", human(int64(r.total-len(s)))) + s
	}
	return s
}

type proc struct {
	id      string
	command string
	cmd     *exec.Cmd
	out     *ring
	started time.Time
	done    chan struct{}
	exit    int
	err     error
	cancel  context.CancelFunc
}

func shellCmd(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	sh := os.Getenv("SHELL")
	if sh == "" || strings.HasSuffix(sh, "fish") {
		sh = "/bin/sh"
	}
	return exec.CommandContext(ctx, sh, "-c", command)
}

func (b *Box) startCmd(command, dir string, timeout time.Duration) *proc {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := shellCmd(ctx, command)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	if b.env != nil {
		cmd.Env = append(cmd.Env, b.env()...)
	}
	cmd.Env = append(cmd.Env, "CI=1", "NO_COLOR=1", "TERM=dumb", "PAGER=cat", "GIT_PAGER=cat")
	cmd.Stdin = nil
	r := &ring{n: maxCmdOutput}
	cmd.Stdout, cmd.Stderr = r, r
	cmd.WaitDelay = 2 * time.Second
	pr := &proc{command: command, cmd: cmd, out: r, started: time.Now(), done: make(chan struct{}), cancel: cancel}
	if err := cmd.Start(); err != nil {
		pr.err, pr.exit = err, -1
		close(pr.done)
		cancel()
		return pr
	}
	go func() {
		pr.err = cmd.Wait()
		if cmd.ProcessState != nil {
			pr.exit = cmd.ProcessState.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			pr.err = fmt.Errorf("timed out after %s and was stopped", timeout)
		}
		cancel()
		close(pr.done)
	}()
	return pr
}

func describe(pr *proc) string {
	select {
	case <-pr.done:
		status := fmt.Sprintf("exit code %d", pr.exit)
		if pr.err != nil && pr.exit <= 0 {
			status = pr.err.Error()
		} else if pr.err != nil && strings.Contains(pr.err.Error(), "timed out") {
			status += " (" + pr.err.Error() + ")"
		}
		out := strings.TrimRight(pr.out.String(), "\n")
		if out == "" {
			out = "(no output)"
		}
		return fmt.Sprintf("$ %s\n%s\n[%s · %s]", pr.command, out, status, time.Since(pr.started).Round(10*time.Millisecond))
	default:
		return fmt.Sprintf("$ %s\n%s\n[still running · %s · id %s]", pr.command, strings.TrimRight(pr.out.String(), "\n"), time.Since(pr.started).Round(time.Second), pr.id)
	}
}

func runCommand(ctx context.Context, b *Box, a args) (string, error) {
	command, err := a.need("command")
	if err != nil {
		return "", err
	}
	cwd := a.str("cwd")
	if cwd == "" && b.cwd != nil {
		cwd = b.cwd()
	}
	dir, _, err := b.resolve(cwd)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", errors.New("cwd must be an existing directory in the workspace")
	}
	bg := a.flag("background")
	timeout := time.Duration(a.num("timeout_seconds", map[bool]int{true: 1800, false: 120}[bg], 1, 3600)) * time.Second
	pr := b.startCmd(command, dir, timeout)
	if bg {
		b.mu.Lock()
		b.nextProc++
		pr.id = fmt.Sprintf("cmd-%d", b.nextProc)
		b.procs[pr.id] = pr
		b.mu.Unlock()
		select {
		case <-pr.done:
		case <-time.After(1500 * time.Millisecond):
		}
		return describe(pr) + "\nUse get_command_output with id " + pr.id + " to read more output or stop it.", nil
	}
	select {
	case <-pr.done:
	case <-ctx.Done():
		pr.cancel()
		<-pr.done
	}
	return describe(pr), nil
}

func getCommandOutput(_ context.Context, b *Box, a args) (string, error) {
	id := a.str("id")
	b.mu.Lock()
	if id == "" {
		var ids []string
		for _, k := range sortedKeys(b.procs) {
			select {
			case <-b.procs[k].done:
				ids = append(ids, k+" (finished): "+b.procs[k].command)
			default:
				ids = append(ids, k+" (running): "+b.procs[k].command)
			}
		}
		b.mu.Unlock()
		if len(ids) == 0 {
			return "No background commands.", nil
		}
		return "Background commands:\n" + strings.Join(ids, "\n"), nil
	}
	pr := b.procs[id]
	b.mu.Unlock()
	if pr == nil {
		return "", fmt.Errorf("no background command %q; call without id to list them", id)
	}
	if a.flag("stop") {
		pr.cancel()
		select {
		case <-pr.done:
		case <-time.After(3 * time.Second):
		}
	} else if w := a.num("wait_seconds", 0, 0, 120); w > 0 {
		select {
		case <-pr.done:
		case <-time.After(time.Duration(w) * time.Second):
		}
	}
	return describe(pr), nil
}

// --- workspace_changes ----------------------------------------------------------

func workspaceChanges(ctx context.Context, b *Box, a args) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errors.New("git is not installed")
	}
	root := b.Root()
	git := func(args ...string) (string, error) {
		c := exec.CommandContext(ctx, "git", append([]string{"-c", "core.quotepath=false", "--no-pager"}, args...)...)
		c.Dir = root
		var out bytes.Buffer
		c.Stdout, c.Stderr = &out, &out
		err := c.Run()
		return out.String(), err
	}
	if out, err := git("rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return "The workspace is not a git repository, so there are no source control changes to show.", nil
	}
	var pathArgs []string
	if p := a.str("path"); p != "" {
		_, rel, err := b.resolve(p)
		if err != nil {
			return "", err
		}
		pathArgs = []string{"--", rel}
	}
	status, _ := git(append([]string{"status", "--porcelain=v1", "--branch", "--untracked-files=all"}, pathArgs...)...)
	var out strings.Builder
	out.WriteString("git status\n" + status)
	if a.flag("include_diff") || a.str("include_diff") == "" {
		diff, _ := git(append([]string{"diff", "--stat", "--patch", "--no-color", "--unified=2", "HEAD"}, pathArgs...)...)
		if strings.TrimSpace(diff) == "" {
			diff, _ = git(append([]string{"diff", "--stat", "--patch", "--no-color", "--unified=2"}, pathArgs...)...)
		}
		limit := a.num("max_bytes", 40000, 1000, 400000)
		if len(diff) > limit {
			diff = diff[:limit] + fmt.Sprintf("\n… diff truncated at %s; pass path to see one file\n", human(int64(limit)))
		}
		if strings.TrimSpace(diff) != "" {
			out.WriteString("\ngit diff (tracked files, against HEAD)\n" + diff)
		}
	}
	return out.String(), nil
}

// --- get_problems ----------------------------------------------------------------

var problemRE = regexp.MustCompile(`(?i)(?:^|\s|\[)([\w./\\-]+\.[a-z0-9]{1,6})[:(\[](\d+)(?:[:,](\d+))?\)?\]?[:\s-]*(error|warning|warn|fatal|err)?\b`)

func getProblems(ctx context.Context, b *Box, _ args) (string, error) {
	if b.problems == nil {
		return "", errors.New("no build command is configured for this workspace")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd, out, err := b.problems(ctx)
	var errs, warns []string
	for _, l := range strings.Split(out, "\n") {
		ll := strings.ToLower(l)
		isErr := strings.Contains(ll, "error") || strings.Contains(ll, "fatal") || strings.Contains(ll, "cannot find symbol")
		isWarn := strings.Contains(ll, "warning") || strings.Contains(ll, "[warn")
		if !isErr && !isWarn {
			continue
		}
		if m := problemRE.FindStringSubmatch(l); m != nil || isErr {
			if isErr {
				errs = append(errs, strings.TrimSpace(l))
			} else {
				warns = append(warns, strings.TrimSpace(l))
			}
		}
	}
	var o strings.Builder
	fmt.Fprintf(&o, "$ %s → %s\n", cmd, map[bool]string{true: "failed", false: "succeeded"}[err != nil])
	cap := func(xs []string) []string {
		if len(xs) > 80 {
			return append(xs[:80], fmt.Sprintf("… %d more", len(xs)-80))
		}
		return xs
	}
	fmt.Fprintf(&o, "%s, %s\n", plural(len(errs), "error", "errors"), plural(len(warns), "warning", "warnings"))
	if len(errs) > 0 {
		o.WriteString("\nErrors\n  " + strings.Join(cap(errs), "\n  ") + "\n")
	}
	if len(warns) > 0 {
		o.WriteString("\nWarnings\n  " + strings.Join(cap(warns), "\n  ") + "\n")
	}
	if err != nil && len(errs) == 0 {
		tail := out
		if len(tail) > 4000 {
			tail = "…" + tail[len(tail)-4000:]
		}
		o.WriteString("\nThe build failed without recognisable error lines; last output:\n" + tail + "\n")
	}
	return o.String(), nil
}

// --- fetch_webpage ---------------------------------------------------------------

var (
	reDrop  = regexp.MustCompile(`(?is)<(script|style|noscript|svg|head|template)[^>]*>.*?</(?:script|style|noscript|svg|head|template)>`)
	reBlock = regexp.MustCompile(`(?i)</?(p|div|br|li|ul|ol|h[1-6]|tr|table|section|article|header|footer|pre|blockquote)[^>]*>`)
	reTag   = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace = regexp.MustCompile(`[ \t\r\f\v]+`)
	reLines = regexp.MustCompile(`\n\s*\n+`)
	reTitle = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

// HTMLToText is a page's title and its text (no tags, scripts or styles).
func HTMLToText(s string) (title, text string) { return htmlToText(s) }

func htmlToText(s string) (title, text string) {
	if m := reTitle.FindStringSubmatch(s); m != nil {
		title = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	s = reDrop.ReplaceAllString(s, " ")
	s = reBlock.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = reSpace.ReplaceAllString(s, " ")
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return title, reLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
}

func fetchWebpage(ctx context.Context, b *Box, a args) (string, error) {
	raw, err := a.need("url")
	if err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("url must be an absolute http(s) address")
	}
	format := strings.ToLower(strings.TrimSpace(a.str("format")))
	switch format {
	case "", "text", "json", "raw":
	default:
		return "", fmt.Errorf("format must be text, json or raw, not %q", format)
	}
	// text is read by the AI: short by default; json and raw are data for
	// later steps: whole by default
	limit := a.num("max_chars", 20000, 500, 200000)
	if format == "json" || format == "raw" {
		limit = a.num("max_chars", fetchDataLimit, 500, fetchDataLimit)
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	req.Header.Set("User-Agent", "SpecWright/1 (agent tool fetch_webpage)")
	req.Header.Set("Accept", "text/html,text/plain,application/json,text/markdown;q=0.9,*/*;q=0.5")
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = proxyFrom(b)
	resp, err := (&http.Client{Transport: tr, Timeout: 25 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, fetchDataLimit+1))
	if format == "raw" { // the body exactly as the server sent it
		return fetchRaw(resp, body, limit)
	}
	ct := resp.Header.Get("Content-Type")
	text, title := string(body), ""
	if strings.Contains(ct, "html") || strings.HasPrefix(strings.TrimSpace(text), "<") && strings.Contains(strings.ToLower(text[:min(len(text), 500)]), "<html") {
		title, text = htmlToText(text)
	}
	if format == "json" {
		return fetchJSON(resp, body, title, text, limit), nil
	}
	if isBinary(body) {
		return fmt.Sprintf("%s → %s, %s binary content (%s); not shown", u, resp.Status, ct, human(int64(len(body)))), nil
	}
	more := ""
	if len(text) > limit {
		full := text
		text = bigout.SafeCut(full, limit)
		more = fmt.Sprintf("\n… %s more; raise max_chars (up to 200000) to read further", bigout.Human(len(full)-len(text)))
		if rel, err := bigout.Save(b.root, "fetch_webpage", "text", full); err == nil {
			more += fmt.Sprintf(", or read the whole text from %s with read_file (start_line / end_line)", rel)
		}
	}
	head := fmt.Sprintf("%s → %s", resp.Request.URL, resp.Status)
	if title != "" {
		head += "\nTitle: " + title
	}
	return head + "\n\n" + text + more, nil
}

// fetchDataLimit caps a response read for json and raw (5 MB).
const fetchDataLimit = 5 << 20

// fetchRaw is the response body exactly as sent — for later steps that
// parse it (JSON, CSV, XML). An error status, a binary body or a body over
// the limit fail, rather than giving data a parser would choke on.
func fetchRaw(resp *http.Response, body []byte, limit int) (string, error) {
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return "", fmt.Errorf("%s returned %s: %s", resp.Request.URL, resp.Status, snippet)
	}
	if isBinary(body) {
		return "", fmt.Errorf("%s returned binary content (%s, %s); raw gives text only", resp.Request.URL, resp.Header.Get("Content-Type"), human(int64(len(body))))
	}
	if len(body) > limit {
		return "", fmt.Errorf("the response is larger than %d characters (max_chars); raise max_chars (up to %d)", limit, fetchDataLimit)
	}
	return string(body), nil
}

// FetchJSONShape is the shape of fetch_webpage's output with format=json.
var FetchJSONShape = &flow.Schema{Type: "object", Required: []string{"url", "status", "ok", "contentType", "headers", "truncated"}, Properties: flow.Props{
	{Name: "url", Schema: &flow.Schema{Type: "string", Description: "the final URL, after redirects"}},
	{Name: "status", Schema: &flow.Schema{Type: "integer", Description: "HTTP status code, e.g. 200"}},
	{Name: "statusText", Schema: &flow.Schema{Type: "string", Description: "e.g. 200 OK"}},
	{Name: "ok", Schema: &flow.Schema{Type: "boolean", Description: "true for a 2xx status"}},
	{Name: "contentType", Schema: &flow.Schema{Type: "string"}},
	{Name: "title", Schema: &flow.Schema{Type: "string", Description: "an HTML page's title"}},
	{Name: "headers", Schema: &flow.Schema{Type: "object", Description: "response headers, lower-case names"}},
	{Name: "json", Schema: &flow.Schema{Description: "the body parsed, when it is JSON"}},
	{Name: "body", Schema: &flow.Schema{Type: "string", Description: "the body as text, when it is not JSON (HTML as readable text)"}},
	{Name: "size", Schema: &flow.Schema{Type: "integer", Description: "body size in bytes"}},
	{Name: "truncated", Schema: &flow.Schema{Type: "boolean", Description: "the body was longer than max_chars"}},
	{Name: "binary", Schema: &flow.Schema{Type: "boolean", Description: "binary content, not included"}},
}}

// fetchJSON is fetch_webpage's response as a JSON object, so later steps
// can read the status and the data by field. Error statuses are data here,
// not failures: a later step can check "ok".
func fetchJSON(resp *http.Response, body []byte, title, text string, limit int) string {
	out := map[string]any{"url": resp.Request.URL.String(), "status": resp.StatusCode, "statusText": resp.Status, "ok": resp.StatusCode >= 200 && resp.StatusCode < 300,
		"contentType": resp.Header.Get("Content-Type"), "size": len(body), "truncated": false}
	hs := map[string]string{}
	for k, v := range resp.Header {
		if len(v) > 0 && !strings.EqualFold(k, "Set-Cookie") { // never pass cookies on
			hs[strings.ToLower(k)] = strings.Join(v, ", ")
		}
	}
	out["headers"] = hs
	if title != "" {
		out["title"] = title
	}
	switch {
	case isBinary(body):
		out["binary"] = true
	default:
		var v any
		if t := strings.TrimSpace(string(body)); (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Unmarshal(body, &v) == nil {
			if len(t) <= limit {
				out["json"] = v
				break
			}
			out["truncated"] = true // too long to pass on whole: its text, cut at a line
			out["body"] = bigout.SafeCut(t, limit)
			break
		}
		if len(text) > limit {
			out["truncated"] = true
			text = bigout.SafeCut(text, limit)
		}
		out["body"] = text
	}
	b, _ := json.MarshalIndent(orderedFetch(out), "", "  ")
	return string(b)
}

// orderedFetch keeps the fields in the shape's order.
func orderedFetch(m map[string]any) any {
	keys := []string{"url", "status", "statusText", "ok", "contentType", "title", "headers", "json", "body", "size", "truncated", "binary"}
	var b strings.Builder
	b.WriteByte('{')
	first := true
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		kj, _ := json.Marshal(k)
		vj, _ := json.Marshal(v)
		b.Write(kj)
		b.WriteByte(':')
		b.Write(vj)
	}
	b.WriteByte('}')
	return json.RawMessage(b.String())
}

// proxyFrom applies the workspace's proxy settings (HTTPS_PROXY etc. from
// the tool environment) to fetches.
func proxyFrom(b *Box) func(*http.Request) (*url.URL, error) {
	env := map[string]string{}
	if b.env != nil {
		for _, kv := range b.env() {
			if k, v, ok := strings.Cut(kv, "="); ok {
				env[strings.ToUpper(k)] = v
			}
		}
	}
	return func(r *http.Request) (*url.URL, error) {
		get := func(k string) string {
			if v := env[k]; v != "" {
				return v
			}
			return os.Getenv(k)
		}
		host := r.URL.Hostname()
		for _, np := range strings.Split(get("NO_PROXY"), ",") {
			np = strings.TrimPrefix(strings.TrimSpace(np), ".")
			if np != "" && (host == np || strings.HasSuffix(host, "."+np) || np == "*") {
				return nil, nil
			}
		}
		p := get("HTTPS_PROXY")
		if r.URL.Scheme == "http" {
			p = get("HTTP_PROXY")
		}
		if p == "" {
			return nil, nil
		}
		return url.Parse(p)
	}
}

// --- todo_list ----------------------------------------------------------------------

// Todo is one item of the agent's working todo list.
type Todo struct {
	Title  string `json:"title"`
	Status string `json:"status"` // pending | in_progress | done
}

func todoList(_ context.Context, b *Box, a args) (string, error) {
	root := b.Root()
	b.mu.Lock()
	defer b.mu.Unlock()
	if raw, ok := a["items"].([]any); ok {
		var items []Todo
		for _, r := range raw {
			switch v := r.(type) {
			case string:
				items = append(items, Todo{Title: v, Status: "pending"})
			case map[string]any:
				t := Todo{Title: args(v).str("title"), Status: args(v).str("status")}
				if t.Status != "in_progress" && t.Status != "done" {
					t.Status = "pending"
				}
				if t.Title != "" {
					items = append(items, t)
				}
			}
		}
		b.todos[root] = items
	}
	items := b.todos[root]
	if len(items) == 0 {
		return "The todo list is empty. Call todo_list with items to set it.", nil
	}
	var out strings.Builder
	done := 0
	for i, t := range items {
		mark := map[string]string{"done": "[x]", "in_progress": "[~]"}[t.Status]
		if mark == "" {
			mark = "[ ]"
		}
		if t.Status == "done" {
			done++
		}
		fmt.Fprintf(&out, "%d. %s %s\n", i+1, mark, t.Title)
	}
	return fmt.Sprintf("Todo list (%d of %d done):\n%s", done, len(items), out.String()), nil
}
