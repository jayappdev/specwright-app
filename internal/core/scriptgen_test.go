package core

import (
	"strings"
	"testing"
)

func TestGenerateScript(t *testing.T) {
	h, _ := newHub(t)
	if _, err := h.GenerateScript(ScriptRequest{}); err == nil {
		t.Fatal("an empty description must be refused")
	}
	d, err := h.GenerateScript(ScriptRequest{Description: "Count the TODO comments in a folder", Language: "bash"})
	if err != nil {
		t.Fatal(err)
	}
	if d.FileName != "count-todos.sh" || !strings.HasPrefix(d.Content, "#!/usr/bin/env bash") || d.Summary == "" {
		t.Fatalf("%+v", d)
	}
	// revising keeps the user's file name
	r, err := h.GenerateScript(ScriptRequest{Description: "print only the number", FileName: "todos.sh", Current: d.Content})
	if err != nil {
		t.Fatal(err)
	}
	if r.FileName != "todos.sh" || !strings.Contains(r.Content, "tr -d") {
		t.Fatalf("%+v", r)
	}
}

func TestParseScriptDraft(t *testing.T) {
	d, err := parseScriptDraft("Here it is:\n```python\n#!/usr/bin/env python3\nprint(1)\n```", ScriptRequest{})
	if err != nil || d.FileName != "script.py" || d.Content != "#!/usr/bin/env python3\nprint(1)\n" {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := parseScriptDraft("Sorry, I can't.", ScriptRequest{}); err == nil {
		t.Fatal("no script must be an error")
	}
	d, _ = parseScriptDraft(`{"fileName":"../../etc/x.sh","content":"#!/bin/sh\necho"}`, ScriptRequest{})
	if d.FileName != "x.sh" {
		t.Fatalf("file name must stay a plain name: %q", d.FileName)
	}
}
