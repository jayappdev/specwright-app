package extension

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Provider is what the bridge needs from the app.
type Provider interface {
	// Callable lists the extensions agents may call right now.
	Callable() []Extension
	// ResolveSecrets returns the secret values e needs (from this session,
	// the environment, or by asking the user). Values are never stored.
	ResolveSecrets(ctx context.Context, e Extension) (map[string]string, error)
	// Root is the workspace the extension runs in.
	Root() string
	// Observe is told about every call and its result.
	Observe(name string, args map[string]any, res RunResult)
	// Prepare installs a tool's dependencies if they are missing.
	Prepare(ctx context.Context, e Extension) error
	// Env is the proxy/CA/registry environment tools run with.
	Env() []string
	// Builtins lists the enabled built-in tools; RunBuiltin runs one.
	Builtins() []BuiltinInfo
	RunBuiltin(ctx context.Context, name string, args map[string]any) (string, error)
	// AskPermission asks the user whether the agent may use a tool it has
	// no standing permission for (Claude's permission prompts).
	AskPermission(ctx context.Context, req PermissionRequest) PermissionAnswer
}

// PermissionRequest is one permission prompt from an agent CLI.
type PermissionRequest struct {
	// Agent identifies one agent process (one run): "allow for this run"
	// answers are remembered per Agent.
	Agent string         `json:"agent"`
	Tool  string         `json:"tool"`
	Input map[string]any `json:"input"`
}

// PermissionAnswer is the user's decision.
type PermissionAnswer struct {
	Allow   bool   `json:"allow"`
	Message string `json:"message,omitempty"` // why it was denied (shown to the model)
}

// Bridge is a loopback-only HTTP server the agentbridge MCP process calls.
// A random bearer token, passed to the MCP process through its environment,
// keeps other local processes out.
type Bridge struct {
	p     Provider
	ln    net.Listener
	srv   *http.Server
	token string
	mu    sync.Mutex
	extra map[string]Extension // temporary, for "Test with AI"
}

// StartBridge listens on 127.0.0.1 at a random port.
func StartBridge(p Provider) (*Bridge, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	br := &Bridge{p: p, ln: ln, token: hex.EncodeToString(b), extra: map[string]Extension{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /list", br.auth(br.handleList))
	mux.HandleFunc("POST /call", br.auth(br.handleCall))
	mux.HandleFunc("GET /builtins", br.auth(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, br.p.Builtins()) }))
	mux.HandleFunc("POST /builtin", br.auth(br.handleBuiltin))
	mux.HandleFunc("POST /permission", br.auth(br.handlePermission))
	br.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = br.srv.Serve(ln) }()
	return br, nil
}

// URL is the bridge base URL.
func (b *Bridge) URL() string { return "http://" + b.ln.Addr().String() }

// Token is the bearer token.
func (b *Bridge) Token() string { return b.token }

// Env returns the variables the MCP process (and the mock agent) need.
func (b *Bridge) Env() []string {
	return []string{"CODEGEN_BRIDGE_URL=" + b.URL(), "CODEGEN_BRIDGE_TOKEN=" + b.token}
}

// Close stops the server.
func (b *Bridge) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return b.srv.Shutdown(ctx)
}

// AllowTemporary makes e callable (unapproved) until the returned func runs.
// Used while the user tests a draft they are looking at.
func (b *Bridge) AllowTemporary(e Extension) func() {
	b.mu.Lock()
	b.extra[e.Name] = e
	b.mu.Unlock()
	return func() { b.mu.Lock(); delete(b.extra, e.Name); b.mu.Unlock() }
}

func (b *Bridge) auth(h http.HandlerFunc) http.HandlerFunc {
	want := "Bearer " + b.token
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// ToolFilter is an optional Provider method: whether the run in progress
// may call a tool (e.g. only the tools chosen for a spec workflow).
type ToolFilter interface {
	AllowedNow(name string) bool
}

func (b *Bridge) callable() map[string]Extension {
	out := map[string]Extension{}
	f, _ := b.p.(ToolFilter)
	for _, e := range b.p.Callable() {
		if f == nil || f.AllowedNow(e.Name) {
			out[e.Name] = e
		}
	}
	b.mu.Lock()
	for k, e := range b.extra {
		out[k] = e
	}
	b.mu.Unlock()
	return out
}

// ToolInfo is what the MCP server shows the model.
type ToolInfo struct {
	Name   string  `json:"name"`
	Desc   string  `json:"desc"`
	Params []Param `json:"params"`
}

func (b *Bridge) handleList(w http.ResponseWriter, _ *http.Request) {
	var out []ToolInfo
	for _, e := range b.callable() {
		out = append(out, ToolInfo{Name: e.Name, Desc: e.Desc, Params: e.Params})
	}
	writeJSON(w, out)
}

func (b *Bridge) handleCall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, RunResult{Error: "bad request: " + err.Error()})
		return
	}
	e, ok := b.callable()[req.Name]
	if !ok {
		writeJSON(w, RunResult{Error: fmt.Sprintf("no callable extension named %q", req.Name)})
		return
	}
	b.mu.Lock()
	_, temp := b.extra[req.Name]
	b.mu.Unlock()
	if !temp {
		if err := e.Runnable(); err != nil {
			writeJSON(w, RunResult{Error: err.Error()})
			return
		}
	}
	if HasDeps(e) {
		if err := b.p.Prepare(r.Context(), e); err != nil {
			res := RunResult{Error: "installing dependencies failed: " + err.Error()}
			b.p.Observe(e.Name, req.Arguments, res)
			writeJSON(w, res)
			return
		}
	}
	secrets, err := b.p.ResolveSecrets(r.Context(), e)
	if err != nil {
		res := RunResult{Error: "secrets: " + err.Error()}
		b.p.Observe(e.Name, req.Arguments, res)
		writeJSON(w, res)
		return
	}
	res := Exec(r.Context(), b.p.Root(), e, req.Arguments, secrets, b.p.Env(), 30*time.Second)
	b.p.Observe(e.Name, req.Arguments, res)
	writeJSON(w, res)
}

func (b *Bridge) handleBuiltin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&req); err != nil {
		writeJSON(w, BuiltinResult{Error: "bad request: " + err.Error(), Output: "bad request"})
		return
	}
	out, err := b.p.RunBuiltin(r.Context(), req.Name, req.Arguments)
	if err != nil {
		writeJSON(w, BuiltinResult{Output: err.Error(), Error: err.Error()})
		return
	}
	writeJSON(w, BuiltinResult{Output: out})
}

func (b *Bridge) handlePermission(w http.ResponseWriter, r *http.Request) {
	var req PermissionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeJSON(w, PermissionAnswer{Message: "bad request: " + err.Error()})
		return
	}
	writeJSON(w, b.p.AskPermission(r.Context(), req))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// WriteMCPConfig writes a private MCP config naming the agentbridge server
// (this executable started with the "agentbridge" argument) and returns its
// path. The caller removes the file when the run ends.
// extensions=false hides extension_managed (built-in tools are always offered).
func (b *Bridge) WriteMCPConfig(dir string, extensions bool) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	env := map[string]string{}
	for _, kv := range b.Env() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				env[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	if !extensions {
		env["CODEGEN_BRIDGE_EXT"] = "0"
	}
	cfg := map[string]any{"mcpServers": map[string]any{
		"agentbridge": map[string]any{"type": "stdio", "command": exe, "args": []string{"agentbridge"}, "env": env, "tools": []string{"*"}},
	}}
	bs, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "mcp-*.json")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return "", err
	}
	if _, err := f.Write(bs); err != nil {
		return "", err
	}
	return filepath.Clean(f.Name()), nil
}
