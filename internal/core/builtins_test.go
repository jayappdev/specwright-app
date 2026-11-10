package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codegen/internal/extension"
)

func TestBuiltinsOverMCP(t *testing.T) {
	usePolicy(t, "")
	h, ws := newHub(t)
	_ = os.WriteFile(filepath.Join(ws, "Main.java"), []byte("class Main {\n  // TODO greet\n}\n"), 0o644)
	for _, kv := range h.Bridge.Env() {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"Main.java"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"edit_file","arguments":{"path":"Main.java","old_text":"TODO greet","new_text":"greets the user"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"../outside.txt"}}}`,
	}, "\n")
	var out bytes.Buffer
	if err := extension.ServeMCP(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var list struct {
		Result struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"result"`
	}
	_ = json.Unmarshal([]byte(lines[0]), &list)
	names := map[string]bool{}
	for _, tl := range list.Result.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"read_file", "edit_file", "text_search", "file_search", "run_command", "extension_managed"} {
		if !names[want] {
			t.Fatalf("tools/list misses %s: %v", want, names)
		}
	}
	if !strings.Contains(lines[1], `1 | class Main {`) || !strings.Contains(lines[2], "Edited Main.java") || !strings.Contains(lines[3], `"isError":true`) {
		t.Fatalf("%s\n%s\n%s", lines[1], lines[2], lines[3])
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "Main.java")); !strings.Contains(string(b), "greets the user") {
		t.Fatal("edit not written")
	}

	// extensions off: built-ins stay, extension_managed goes
	t.Setenv("CODEGEN_BRIDGE_EXT", "0")
	out.Reset()
	_ = extension.ServeMCP(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`), &out)
	if strings.Contains(out.String(), "extension_managed") || !strings.Contains(out.String(), "read_file") {
		t.Fatal(out.String())
	}
}

func TestBuiltinPolicyAndPlayground(t *testing.T) {
	usePolicy(t, `{"disabledBuiltinTools":["run_command"]}`)
	h, ws := newHub(t)
	_ = os.WriteFile(filepath.Join(ws, "notes.md"), []byte("TODO: ship it\n"), 0o644)
	for _, bt := range h.BuiltinTools() {
		if bt.Name == "run_command" && bt.Enabled {
			t.Fatal("policy must disable run_command")
		}
	}
	if _, err := h.RunBuiltin(context.Background(), "run_command", map[string]any{"command": "echo hi"}); err == nil {
		t.Fatal("disabled tool must refuse")
	}
	// a playground prompt (tools off) still gets built-ins
	ch, unsub := h.Bus.Subscribe(4096, nil)
	defer unsub()
	id, err := h.RunPrompt("Find the TODOs", PromptOptions{Tools: false})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	to := time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case e := <-ch:
			if e.RunID == id && e.Type == "builtin.result" {
				got[e.Str("name")] = e.Data["ok"] == true
				if e.Str("name") == "text_search" && !strings.Contains(e.Str("output"), "notes.md") {
					t.Fatalf("search output: %s", e.Str("output"))
				}
			}
			if e.RunID == id && e.Type == "run.end" {
				done = true
			}
		case <-to:
			t.Fatal("timed out")
		}
	}
	if !got["list_directory"] || !got["text_search"] {
		t.Fatalf("built-ins not used: %v", got)
	}
}

func TestBuiltinsAdminOnlyInUI(t *testing.T) {
	h, _ := newHub(t)
	if h.BuiltinsVisible() {
		t.Fatal("built-ins visible without admin mode")
	}
	if _, err := h.CallBuiltin("list_directory", nil); !errors.Is(err, ErrBuiltinsAdminOnly) {
		t.Fatalf("CallBuiltin outside admin mode: %v", err)
	}
	// agents are unaffected
	if _, err := h.RunBuiltin(context.Background(), "list_directory", nil); err != nil {
		t.Fatalf("RunBuiltin (agent path): %v", err)
	}
	if _, err := h.CreateAdminKey("Ada Admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if !h.BuiltinsVisible() {
		t.Fatal("built-ins hidden in admin mode")
	}
	if _, err := h.CallBuiltin("list_directory", nil); err != nil {
		t.Fatalf("CallBuiltin in admin mode: %v", err)
	}
}
