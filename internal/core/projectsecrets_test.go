package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/flow"
)

func TestProjectSecretsAndRefs(t *testing.T) {
	kc := stubKeychain(t)
	h, ws := flowHub(t)

	// a project secret: encrypted on disk, out of git, never listed with its value
	if err := h.SetProjectSecret("API_TOKEN", "tok-123-secret", "the API"); err != nil {
		t.Fatal(err)
	}
	if err := h.SetProjectSecret("bad name", "x", ""); err == nil {
		t.Fatal("a bad name was taken")
	}
	raw, _ := os.ReadFile(filepath.Join(ws, ".codegen", "secrets", "secrets.json"))
	if len(raw) == 0 || strings.Contains(string(raw), "tok-123-secret") {
		t.Fatalf("the file: %s", raw)
	}
	if ign, _ := os.ReadFile(filepath.Join(ws, ".codegen", "secrets", ".gitignore")); !strings.Contains(string(ign), "*") {
		t.Fatal("not git-ignored")
	}
	list, _ := h.ProjectSecrets()
	if len(list) != 1 || list[0].Name != "API_TOKEN" || list[0].Desc != "the API" || !list[0].Readable {
		t.Fatalf("%+v", list)
	}
	// a description change keeps the value
	if err := h.SetProjectSecret("API_TOKEN", "", "renamed"); err != nil {
		t.Fatal(err)
	}
	if v, _ := h.projectSecret(ws, "API_TOKEN"); v != "tok-123-secret" {
		t.Fatalf("value %q", v)
	}

	// a flow reading a variable, a project secret and a keychain entry
	kc["SpecWright/DB_PASS"] = "kc-pass-456"
	t.Setenv("ZONE", "z9")
	f := flow.New("refs", "Refs")
	f.Vars = []flow.Var{{Name: "REGION", Value: "eu"}}
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "echo", Type: "tool", Tool: "echo-arg", Args: map[string]string{"v": "{{env.REGION}} {{env.ZONE}} {{project_secret.API_TOKEN}} {{keychain.DB_PASS}}"}},
		flow.Node{ID: "o", Type: "output", Format: "md", Template: "{{steps.echo}}", Actions: []flow.Action{{Type: "show"}}})
	f.Edges = []flow.Edge{{From: "start", To: "echo"}, {From: "echo", To: "o"}}
	if _, err := h.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	if !KeychainAvailable() {
		t.Skip("no keychain tool on this computer")
	}
	st, err := h.RunFlow("refs", FlowRunOptions{Review: "none"})
	if err != nil {
		t.Fatal(err)
	}
	st = waitFlow(t, h, st.ID, func(s FlowRunState) bool { return s.Status == "done" || s.Status == "failed" })
	if st.Status != "done" {
		t.Fatalf("%s %s", st.Status, st.Error)
	}
	out := ""
	for _, s := range st.Steps {
		if s.ID == "echo" {
			out = strings.TrimSpace(s.Output)
		}
	}
	// the values reached the tool (the secrets, hidden in what is kept)
	if out != "eu z9 [REDACTED] [REDACTED]" {
		t.Fatalf("the tool got %q", out)
	}

	// a missing secret stops the run before it starts
	if err := h.DeleteProjectSecret("API_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.RunFlow("refs", FlowRunOptions{Review: "none"}); err == nil || !strings.Contains(err.Error(), "no project secret API_TOKEN") {
		t.Fatalf("ran without its secret: %v", err)
	}
}
