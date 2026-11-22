package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"codegen/internal/bus"
	"codegen/internal/extension"
)

// Secret sources (extension.Secret.Source):
//
//	auto (or "")  this session's value → environment variable → system keychain → ask the user
//	env           environment variable only (good for CI and headless runs)
//	keychain      the system keychain only (macOS Keychain, Linux Secret Service)
//	ask           ask the user every run, unless they chose "remember"
//	session       only a value entered in the app for this session
//
// Values are never written to disk. Every resolved value is registered with
// the redactor so it cannot reach events, run history or the model.

// SecretState tells the UI where a secret will come from right now.
type SecretState struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Env    string `json:"env"`    // variable that is read
	EnvSet bool   `json:"envSet"` // that variable is set
	Via    string `json:"via"`    // session | env | keychain | ask | missing | optional (not set, not needed)
	// Optional: passed when it has a value, never asked for or missing
	Optional bool `json:"optional,omitempty"`
}

// SecretRequest is published as a "secret.request" event when a tool
// needs values from the user before it can run.
type SecretRequest struct {
	ID      string             `json:"id"`
	Tool    string             `json:"tool"`
	Secrets []extension.Secret `json:"secrets"`
	Reason  string             `json:"reason"`
}

type secretAnswer struct {
	values   map[string]string
	remember bool
	cancel   bool
}

// AskSecretsTimeout is how long a tool call waits for the user.
var AskSecretsTimeout = 10 * time.Minute

// secretsState holds secret values and prompts, under their own lock so a
// keychain lookup or a waiting prompt does not hold the hub's lock.
type secretsState struct {
	mu       sync.RWMutex
	values   map[string]string                                               // session values ("remember for this session"), by name
	resolved map[string]string                                               // values resolved for tool runs, for leak checks
	pending  map[string]chan secretAnswer                                    // prompts waiting for the UI, by request id
	ask      func(context.Context, SecretRequest) (map[string]string, error) // non-UI asker (headless)
}

func envName(s extension.Secret) string {
	if s.Env != "" {
		return s.Env
	}
	return s.Name
}

// --- environment ---------------------------------------------------------

var (
	shellEnvOnce sync.Once
	shellEnv     map[string]string
)

// loginShellEnv reads the user's login-shell environment once. An app
// started from Finder or the Dock does not inherit variables exported in
// ~/.zshrc or ~/.bash_profile, so without this "read from the environment"
// would find nothing.
func loginShellEnv() map[string]string {
	shellEnvOnce.Do(func() {
		shellEnv = map[string]string{}
		if runtime.GOOS == "windows" {
			return
		}
		sh := os.Getenv("SHELL")
		if sh == "" {
			sh = "/bin/zsh"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		// -i and -l load the user's rc files; the marker skips anything
		// they print before env runs
		out, err := exec.CommandContext(ctx, sh, "-ilc", "printf '\\n__SPECWRIGHT_ENV__\\n'; env -0").Output()
		if err != nil && len(out) == 0 {
			return
		}
		if i := bytes.Index(out, []byte("__SPECWRIGHT_ENV__\n")); i >= 0 {
			out = out[i+len("__SPECWRIGHT_ENV__\n"):]
		}
		for _, kv := range bytes.Split(out, []byte{0}) {
			if k, v, ok := strings.Cut(string(kv), "="); ok && k != "" {
				shellEnv[k] = v
			}
		}
	})
	return shellEnv
}

// lookupEnv reads the app's own environment, then the login shell's.
func lookupEnv(name string) (string, bool) {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v, true
	}
	v, ok := loginShellEnv()[name]
	return v, ok && v != ""
}

// UseLoginShellPath adds the login shell's PATH entries to this process,
// so a Finder-launched app finds claude, copilot, node, go and mvn.
func UseLoginShellPath() {
	sp := loginShellEnv()["PATH"]
	if sp == "" {
		return
	}
	have := map[string]bool{}
	cur := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))
	for _, p := range cur {
		have[p] = true
	}
	var add []string
	for _, p := range strings.Split(sp, string(os.PathListSeparator)) {
		if p != "" && !have[p] {
			add = append(add, p)
			have[p] = true
		}
	}
	if len(add) > 0 {
		_ = os.Setenv("PATH", strings.Join(append(add, cur...), string(os.PathListSeparator)))
	}
}

// --- state for the UI ----------------------------------------------------

// SecretInfo reports, for each secret, where its value would come from.
func (h *Hub) SecretInfo(secrets []extension.Secret) []SecretState {
	h.sec.mu.RLock()
	defer h.sec.mu.RUnlock()
	out := make([]SecretState, 0, len(secrets))
	for _, s := range secrets {
		st := SecretState{Name: s.Name, Source: firstNonEmpty(s.Source, "auto"), Env: envName(s)}
		_, st.EnvSet = lookupEnv(st.Env)
		_, inSession := h.sec.values[s.Name]
		inKeychain := false
		if !inSession && !st.EnvSet && (st.Source == "auto" || st.Source == "keychain") {
			_, inKeychain = kcGet(s.Name)
		}
		switch st.Source {
		case "env":
			st.Via = map[bool]string{true: "env", false: "missing"}[st.EnvSet]
		case "keychain":
			st.Via = map[bool]string{true: "keychain", false: "missing"}[inKeychain || inSession]
			if inSession {
				st.Via = "session"
			}
		case "session":
			st.Via = map[bool]string{true: "session", false: "missing"}[inSession]
		case "ask":
			st.Via = map[bool]string{true: "session", false: "ask"}[inSession]
		default:
			switch {
			case inSession:
				st.Via = "session"
			case st.EnvSet:
				st.Via = "env"
			case inKeychain:
				st.Via = "keychain"
			default:
				st.Via = "ask"
			}
		}
		if s.Optional {
			st.Optional = true
			if st.Via == "ask" || st.Via == "missing" {
				st.Via = "optional" // not set, and not needed: never asked for
			}
		}
		out = append(out, st)
	}
	return out
}

// SecretStatus reports which secrets will have a value without asking
// (kept for older callers; SecretInfo has the detail).
func (h *Hub) SecretStatus(names []string) map[string]bool {
	var ss []extension.Secret
	for _, n := range names {
		ss = append(ss, extension.Secret{Name: n})
	}
	out := map[string]bool{}
	for _, st := range h.SecretInfo(ss) {
		out[st.Name] = st.Via == "session" || st.Via == "env" || st.Via == "keychain"
	}
	return out
}

// --- resolution ----------------------------------------------------------

// ResolveSecrets implements extension.Provider: the values e needs, asking
// the user (through a "secret.request" event) for any that must be typed in.
func (h *Hub) ResolveSecrets(ctx context.Context, e extension.Extension) (map[string]string, error) {
	vals := map[string]string{}
	var ask []extension.Secret
	var missing []string
	h.sec.mu.RLock()
	for _, s := range e.Secrets {
		src := firstNonEmpty(s.Source, "auto")
		sv, inSession := h.sec.values[s.Name]
		if s.Optional { // passed when known, never asked for
			if inSession {
				vals[s.Name] = sv
			} else if v, ok := lookupEnv(envName(s)); ok && src != "keychain" {
				vals[s.Name] = v
			} else if v, ok := kcGet(s.Name); ok {
				vals[s.Name] = v
			}
			continue
		}
		switch {
		case (src == "auto" || src == "session" || src == "ask" || src == "keychain") && inSession:
			vals[s.Name] = sv
		case src == "keychain":
			if v, ok := kcGet(s.Name); ok {
				vals[s.Name] = v
			} else {
				missing = append(missing, fmt.Sprintf("no keychain entry for %s (service %q, account %q)", s.Name, keychainService, s.Name))
			}
		case src == "auto" || src == "env":
			if v, ok := lookupEnv(envName(s)); ok {
				vals[s.Name] = v
			} else if v, ok := kcGet(s.Name); ok && src == "auto" {
				vals[s.Name] = v
			} else if src == "env" {
				missing = append(missing, fmt.Sprintf("environment variable %s is not set (needed for %s)", envName(s), s.Name))
			} else {
				ask = append(ask, s)
			}
		case src == "ask":
			ask = append(ask, s)
		default: // session
			missing = append(missing, s.Name+" has no value for this session; enter it under Tools › Secrets")
		}
	}
	asker := h.sec.ask
	h.sec.mu.RUnlock()
	if len(missing) > 0 {
		return nil, errors.New(strings.Join(missing, "; "))
	}
	if len(ask) > 0 {
		if asker == nil {
			asker = h.askThroughUI
		}
		got, err := asker(ctx, SecretRequest{Tool: e.Name, Secrets: ask, Reason: e.Desc})
		if err != nil {
			return nil, err
		}
		for _, s := range ask {
			if v := got[s.Name]; v != "" {
				vals[s.Name] = v
			} else {
				return nil, fmt.Errorf("%s was not provided", s.Name)
			}
		}
	}
	h.sec.mu.Lock()
	if h.sec.resolved == nil {
		h.sec.resolved = map[string]string{}
	}
	for k, v := range vals {
		h.Store.Redactor().Add(v)
		h.sec.resolved[k] = v
	}
	h.sec.mu.Unlock()
	return vals, nil
}

// SetSecretAsker replaces the UI prompt (the headless runner cannot show one).
func (h *Hub) SetSecretAsker(f func(context.Context, SecretRequest) (map[string]string, error)) {
	h.sec.mu.Lock()
	h.sec.ask = f
	h.sec.mu.Unlock()
}

func (h *Hub) askThroughUI(ctx context.Context, req SecretRequest) (map[string]string, error) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	req.ID = hex.EncodeToString(b)
	ch := make(chan secretAnswer, 1)
	h.sec.mu.Lock()
	if h.sec.pending == nil {
		h.sec.pending = map[string]chan secretAnswer{}
	}
	h.sec.pending[req.ID] = ch
	h.sec.mu.Unlock()
	defer func() { h.sec.mu.Lock(); delete(h.sec.pending, req.ID); h.sec.mu.Unlock() }()

	var names []string
	for _, s := range req.Secrets {
		names = append(names, s.Name)
	}
	ev := bus.Event{Type: "secret.request", Summary: req.Tool + " needs " + strings.Join(names, ", "),
		Data: map[string]any{"id": req.ID, "tool": req.Tool, "secrets": req.Secrets, "reason": req.Reason}}
	if fr, step := flowRunFrom(ctx); fr != "" {
		ev.RunID, ev.Step = fr, step
	} else if a := h.activeRun(); a != nil {
		ev.RunID, ev.Step = a.Session.ID, a.Session.Step
	}
	h.Bus.Publish(ev)
	done := func(ok bool) {
		h.Bus.Publish(bus.Event{Type: "secret.resolved", RunID: ev.RunID, Step: ev.Step, Summary: map[bool]string{true: "provided", false: "not provided"}[ok], Data: map[string]any{"id": req.ID, "ok": ok}})
	}
	t := time.NewTimer(AskSecretsTimeout)
	defer t.Stop()
	select {
	case a := <-ch:
		if a.cancel {
			done(false)
			return nil, fmt.Errorf("you declined to provide %s", strings.Join(names, ", "))
		}
		if a.remember {
			h.sec.mu.Lock()
			for k, v := range a.values {
				if v != "" {
					h.sec.values[k] = v
				}
			}
			h.sec.mu.Unlock()
		}
		done(true)
		return a.values, nil
	case <-t.C:
		done(false)
		return nil, fmt.Errorf("no answer for %s within %s", strings.Join(names, ", "), AskSecretsTimeout)
	case <-ctx.Done():
		done(false)
		return nil, ctx.Err()
	}
}

// AnswerSecrets delivers the user's values for a pending request.
func (h *Hub) AnswerSecrets(id string, values map[string]string, remember bool) error {
	h.sec.mu.RLock()
	ch := h.sec.pending[id]
	h.sec.mu.RUnlock()
	if ch == nil {
		return errors.New("that request is no longer waiting (it may have timed out)")
	}
	for _, v := range values {
		h.Store.Redactor().Add(v)
	}
	select {
	case ch <- secretAnswer{values: values, remember: remember}:
		return nil
	default:
		return errors.New("already answered")
	}
}

// CancelSecrets declines a pending request; the tool call fails.
func (h *Hub) CancelSecrets(id string) error {
	h.sec.mu.RLock()
	ch := h.sec.pending[id]
	h.sec.mu.RUnlock()
	if ch == nil {
		return nil
	}
	select {
	case ch <- secretAnswer{cancel: true}:
	default:
	}
	return nil
}

// TerminalAsker asks for secrets on the controlling terminal (headless).
// Input is read from /dev/tty, so piped stdin is not consumed.
func TerminalAsker(ctx context.Context, req SecretRequest) (map[string]string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		var names []string
		for _, s := range req.Secrets {
			names = append(names, envName(s))
		}
		return nil, fmt.Errorf("%s needs %s: set it in the environment (no terminal to ask on)", req.Tool, strings.Join(names, ", "))
	}
	defer tty.Close()
	stty := func(arg string) {
		c := exec.CommandContext(ctx, "stty", arg)
		c.Stdin = tty
		_ = c.Run()
	}
	stty("-echo") // do not show the value
	defer stty("echo")
	r := bufio.NewReader(tty)
	out := map[string]string{}
	for _, s := range req.Secrets {
		fmt.Fprintf(tty, "%s needs %s (%s): ", req.Tool, s.Name, s.Desc)
		line, err := r.ReadString('\n')
		fmt.Fprintln(tty)
		if err != nil {
			return nil, err
		}
		out[s.Name] = strings.TrimRight(line, "\r\n")
	}
	return out, nil
}
