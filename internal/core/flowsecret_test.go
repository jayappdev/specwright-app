package core

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// A secret input reaches the tools that use {{inputs.name}}, and nothing
// else: the run's state, the Input step's output, run history and saved
// results show a mask, and tool output that echoes it is redacted.
func TestFlowSecretInput(t *testing.T) {
	h, ws := flowHub(t)
	const secret = "s3cr3t-Value-42"
	if _, err := h.SaveExtension(extension.Extension{Name: "count-chars", Desc: "Count", Lang: "Bash", Scope: "project", Approved: true,
		Params: []extension.Param{{Name: "v", Type: "string", Required: true}}, Command: "bash -c 'printf %s \"$1\" | wc -c | tr -d \" \"' _ {{v}}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ApproveExtension("count-chars"); err != nil {
		t.Fatal(err)
	}
	f := flow.New("login", "Login")
	f.Inputs = []flow.Input{{Name: "user", Type: "text"}, {Name: "token", Label: "API token", Type: "secret", Required: true}}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "use", Type: "tool", Tool: "echo-arg", Args: map[string]string{"v": "len={{inputs.token}}"}, X: 200},
		flow.Node{ID: "len", Type: "tool", Tool: "count-chars", Args: map[string]string{"v": "{{inputs.token}}"}, X: 200, Y: 200},
		flow.Node{ID: "out", Type: "output", Format: "txt", Template: "{{steps.start}} key={{inputs.token}}", X: 400, Actions: []flow.Action{{Type: "save", Path: "out/login.txt"}, {Type: "show"}}})
	f.Edges = []flow.Edge{{From: "start", To: "use"}, {From: "start", To: "len"}, {From: "start", To: "out"}}
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	if _, err := h.RunFlow("login", FlowRunOptions{Review: "none", Inputs: map[string]string{"user": "ann"}}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatal("a missing required secret must be refused:", err)
	}
	st, err := h.RunFlow("login", FlowRunOptions{Review: "none", Inputs: map[string]string{"user": "ann", "token": secret}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Options.Inputs["token"] != flow.SecretMask {
		t.Fatalf("state shows %q", st.Options.Inputs["token"])
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" {
		t.Fatalf("%s %s %+v", st.Status, st.Error, st.Steps)
	}
	outs := map[string]string{}
	for _, s := range st.Steps {
		outs[s.ID] = s.Output
	}
	// the tool got the value (its echo is redacted, not the mask)
	if !strings.Contains(outs["use"], "len=[REDACTED]") {
		t.Fatalf("use: %q", outs["use"])
	}
	if strings.TrimSpace(outs["len"]) != "15" {
		t.Fatalf("the tool did not get the real value: %q", outs["len"])
	}
	if !strings.Contains(outs["start"], flow.SecretMask) || !strings.Contains(outs["start"], "ann") {
		t.Fatalf("start: %q", outs["start"])
	}
	b, _ := json.Marshal(st)
	if strings.Contains(string(b), secret) {
		i := strings.Index(string(b), secret)
		t.Fatalf("the state leaks the secret: …%s…", string(b)[max(0, i-300):i+20])
	}
	runs, _ := h.Runs(20)
	for _, r := range runs {
		if strings.Contains(r.Ask, secret) || strings.Contains(r.Title, secret) {
			t.Fatalf("history leaks it: %+v", r)
		}
	}
	if _, err := h.Results(st.ID); err != nil {
		t.Fatal(err)
	}
	_ = filepath.WalkDir(ws, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if c, _ := os.ReadFile(p); strings.Contains(string(c), secret) {
				t.Errorf("%s holds the secret", p)
			}
		}
		return nil
	})
	_ = filepath.WalkDir(h.Store.Dir(), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if c, _ := os.ReadFile(p); strings.Contains(string(c), secret) {
				t.Errorf("%s holds the secret", p)
			}
		}
		return nil
	})

	// a secret cannot have a saved default, and cannot drive "once per line"
	f.Inputs[1].Default = "x"
	bad := false
	for _, is := range h.ValidateFlow(*f) {
		bad = bad || (is.Level == "error" && strings.Contains(is.Message, "secret"))
	}
	if !bad {
		t.Fatal("a secret with a default must be an error")
	}
	if _, err := h.RunFlow("login", FlowRunOptions{Review: "none", Inputs: map[string]string{"token": "a\nb"}, ForEach: &ForEach{Input: "token", Split: "lines"}}); err == nil {
		t.Fatal("once per line of a secret must be refused")
	}
}

// secretFlow saves a flow whose tool counts the characters of the secret
// input "token" (proof the tool got the real value, without printing it).
func secretFlow(t *testing.T, h *Hub, id string, in flow.Input) {
	t.Helper()
	if _, err := h.SaveExtension(extension.Extension{Name: "count-chars", Desc: "Count", Lang: "Bash", Scope: "project", Approved: true,
		Params: []extension.Param{{Name: "v", Type: "string", Required: true}}, Command: "bash -c 'printf %s \"$1\" | wc -c | tr -d \" \"' _ {{v}}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ApproveExtension("count-chars"); err != nil {
		t.Fatal(err)
	}
	f := flow.New(id, id)
	in.Name, in.Type = "token", "secret"
	f.Inputs = []flow.Input{in}
	f.Nodes = append(f.Nodes, flow.Node{ID: "len", Type: "tool", Tool: "count-chars", Args: map[string]string{"v": "{{inputs.token}}"}, X: 200})
	f.Edges = []flow.Edge{{From: "start", To: "len"}}
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
}

func runLen(t *testing.T, h *Hub, id string, o FlowRunOptions) (string, error) {
	t.Helper()
	o.Review = "none"
	st, err := h.RunFlow(id, o)
	if err != nil {
		return "", err
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" {
		return "", errors.New(st.Error)
	}
	for _, s := range st.Steps {
		if s.ID == "len" {
			return strings.TrimSpace(s.Output), nil
		}
	}
	return "", errors.New("no len step")
}

func TestFlowSecretSources(t *testing.T) {
	h, _ := flowHub(t)
	kc := stubKeychain(t)

	// keychain: SpecWright's entry named after the input, or any service/account
	secretFlow(t, h, "kc", flow.Input{Source: "keychain", Required: true})
	if _, err := runLen(t, h, "kc", FlowRunOptions{}); err == nil || !strings.Contains(err.Error(), "Keychain · SpecWright / token") {
		t.Fatalf("missing entry: %v", err)
	}
	st, _ := h.FlowSecretStatus("kc")
	if len(st) != 1 || st[0].Found || st[0].Where != "Keychain · SpecWright / token" {
		t.Fatalf("%+v", st)
	}
	kc["SpecWright/token"] = "abcdefgh"
	kcForget("token")
	if got, err := runLen(t, h, "kc", FlowRunOptions{}); err != nil || got != "8" {
		t.Fatalf("keychain: %q %v", got, err)
	}
	if st, _ := h.FlowSecretStatus("kc"); !st[0].Found {
		t.Fatalf("%+v", st)
	}
	// typed in the run form wins, and "Save to Keychain" keeps it
	if got, err := runLen(t, h, "kc", FlowRunOptions{Inputs: map[string]string{"token": "typed-12345"}, SaveSecrets: []string{"token"}}); err != nil || got != "11" {
		t.Fatalf("typed: %q %v", got, err)
	}
	if kc["SpecWright/token"] != "typed-12345" {
		t.Fatalf("not saved: %v", kc)
	}
	secretFlow(t, h, "kc2", flow.Input{Source: "keychain", Keychain: &flow.KeychainRef{Service: "corp-db", Account: "svc_reporting"}})
	kc["corp-db/svc_reporting"] = "db-pass-xyz"
	if got, err := runLen(t, h, "kc2", FlowRunOptions{}); err != nil || got != "11" {
		t.Fatalf("other entry: %q %v", got, err)
	}

	// env, and auto (keychain, then env)
	t.Setenv("SW_TEST_TOKEN", "from-env")
	secretFlow(t, h, "env", flow.Input{Source: "env", Env: "SW_TEST_TOKEN"})
	if got, err := runLen(t, h, "env", FlowRunOptions{}); err != nil || got != "8" {
		t.Fatalf("env: %q %v", got, err)
	}
	secretFlow(t, h, "auto", flow.Input{Source: "auto", Env: "SW_TEST_TOKEN", Keychain: &flow.KeychainRef{Account: "auto-tok"}, Required: true})
	if got, err := runLen(t, h, "auto", FlowRunOptions{}); err != nil || got != "8" {
		t.Fatalf("auto from env: %q %v", got, err)
	}
	kc["SpecWright/auto-tok"] = "kc-wins-12"
	kcForgetRef(flow.KeychainRef{Service: "SpecWright", Account: "auto-tok"})
	if got, err := runLen(t, h, "auto", FlowRunOptions{}); err != nil || got != "10" {
		t.Fatalf("auto from keychain: %q %v", got, err)
	}

	// prompt: asked once the run has started
	asked := 0
	h.SetSecretAsker(func(_ context.Context, r SecretRequest) (map[string]string, error) {
		asked++
		if len(r.Secrets) != 1 || r.Secrets[0].Name != "token" {
			t.Errorf("%+v", r)
		}
		return map[string]string{"token": "prompted-1"}, nil
	})
	secretFlow(t, h, "ask", flow.Input{Source: "prompt", Required: true})
	if got, err := runLen(t, h, "ask", FlowRunOptions{}); err != nil || got != "10" || asked != 1 {
		t.Fatalf("prompt: %q %v asked %d", got, err, asked)
	}
	h.SetSecretAsker(func(context.Context, SecretRequest) (map[string]string, error) {
		return nil, errors.New("you declined to provide token")
	})
	if _, err := runLen(t, h, "ask", FlowRunOptions{}); err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("declined: %v", err)
	}
}

func TestFlowSecretWarnings(t *testing.T) {
	f := flow.New("w", "W")
	f.Inputs = []flow.Input{{Name: "pw", Type: "secret"}, {Name: "bad", Type: "secret", Source: "env", Env: "1x"}}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "ai", Type: "ai", Prompt: "Log in with {{inputs.pw}}"},
		flow.Node{ID: "t", Type: "tool", Tool: "x", Args: map[string]string{"password": "hunter2", "user": "ann", "api_key": "{{inputs.pw}}"}})
	var msgs []string
	for _, is := range flow.Validate(f, nil) {
		msgs = append(msgs, is.Level+": "+is.Message)
	}
	all := strings.Join(msgs, "\n")
	for _, want := range []string{"sends the secret \"pw\" to the AI model", "› password: this looks like a password", "valid name"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "› api_key") || strings.Contains(all, "› user") {
		t.Errorf("false alarm:\n%s", all)
	}
}
