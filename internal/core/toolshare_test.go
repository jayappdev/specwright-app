package core

import (
	"encoding/json"
	"strings"
	"testing"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

func TestToolExportImport(t *testing.T) {
	src, _ := newHub(t)
	if _, err := src.SaveExtension(extension.Extension{Name: "greet", Desc: "Say hi", Lang: "Bash", Scope: "project",
		Params:  []extension.Param{{Name: "who", Type: "string", Required: true}},
		Secrets: []extension.Secret{{Name: "GREETING", Source: "env"}},
		Command: "bash .codegen/tools/greet.sh {{who}}", Script: "#!/usr/bin/env bash\necho \"$GREETING, $1\"\n"}); err != nil {
		t.Fatal(err)
	}
	src.SetToolTags("greet", []string{"demo"})
	exp, err := src.ExportTools([]string{"greet"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	file := exp.File
	for _, leak := range []string{"approved", "hash", "lastTest"} {
		if strings.Contains(file, `"`+leak+`"`) {
			t.Errorf("the file carries %q", leak)
		}
	}
	if !strings.Contains(file, `echo \"$GREETING, $1\"`) || !strings.Contains(file, `"demo"`) {
		t.Fatalf("file: %s", file)
	}

	dst, _ := newHub(t)
	pv, err := dst.PreviewToolImport(file)
	items := pv.Tools
	if err != nil || len(items) != 1 || items[0].Exists || items[0].Tool.Script == "" {
		t.Fatalf("preview: %+v %v", items, err)
	}
	res, err := dst.ImportTools(file, nil)
	if err != nil || len(res.Imported) != 1 || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "GREETING") {
		t.Fatalf("import: %+v %v", res, err)
	}
	e, err := dst.exts.Get("greet")
	if err != nil || e.Approved || e.Status != "needs approval" || e.Runnable() == nil {
		t.Fatalf("imported tool must need approval: %+v %v", e, err)
	}
	if dst.ToolTags()["greet"][0] != "demo" {
		t.Fatal("tags not imported")
	}
	// importing again: the name is taken; default skips, copy renames, replace overwrites
	pv, _ = dst.PreviewToolImport(file)
	items = pv.Tools
	if !items[0].Exists || items[0].Suggest != "greet-2" {
		t.Fatalf("second preview: %+v", items[0])
	}
	if res, _ := dst.ImportTools(file, nil); len(res.Skipped) != 1 {
		t.Fatalf("default for a taken name is skip: %+v", res)
	}
	if res, err := dst.ImportTools(file, map[string]string{"greet": "copy:greet-2"}); err != nil || res.Imported[0] != "greet-2" {
		t.Fatalf("copy: %+v %v", res, err)
	}
	if _, err := dst.ImportTools(file, map[string]string{"greet": "import"}); err == nil {
		t.Fatal("import over a taken name must fail")
	}
	// approving = saving it in the editor
	if _, err := dst.SaveExtension(e); err != nil {
		t.Fatal(err)
	}
	if e, _ := dst.exts.Get("greet"); !e.Approved || e.Runnable() != nil {
		t.Fatalf("saved tool is approved: %+v", e)
	}
	// bad files
	for _, bad := range []string{"", "{}", `{"format":"specwright-tools","version":1,"tools":[{"name":"Bad Name","command":"x"}]}`, `{"format":"specwright-flow"}`} {
		if _, err := dst.PreviewToolImport(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestFlowExportCarriesTools(t *testing.T) {
	src, _ := newHub(t)
	if _, err := src.SaveExtension(extension.Extension{Name: "echo-arg", Desc: "Echo", Lang: "Bash", Scope: "project",
		Params: []extension.Param{{Name: "v", Type: "string", Required: true}}, Command: "echo {{v}}"}); err != nil {
		t.Fatal(err)
	}
	f, _ := src.NewFlow("Uses a tool")
	f.Nodes = append(f.Nodes, flow.Node{ID: "e", Type: "tool", Tool: "echo-arg", Args: map[string]string{"v": "hi"}, X: 200},
		flow.Node{ID: "r", Type: "tool", Tool: "builtin:read_file", Args: map[string]string{"path": "a"}, X: 400})
	f.Edges = []flow.Edge{{From: "start", To: "e"}, {From: "e", To: "r"}}
	if _, err := src.SaveFlow(*f); err != nil {
		t.Fatal(err)
	}
	data, err := src.ExportFlow(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	json.Unmarshal([]byte(data), &m)
	if !strings.Contains(string(m["tools"]), `"echo-arg"`) || strings.Contains(string(m["tools"]), "read_file") {
		t.Fatalf("tools: %s", m["tools"])
	}

	dst, _ := newHub(t)
	st, err := dst.ImportFlow(data)
	if err != nil {
		t.Fatal(err)
	}
	if e, err := dst.exts.Get("echo-arg"); err != nil || e.Approved {
		t.Fatalf("tool not imported as needing approval: %+v %v", e, err)
	}
	approval := false
	for _, is := range st.Issues {
		approval = approval || strings.Contains(is.Message, "echo-arg is not approved yet")
	}
	if !approval {
		t.Fatalf("the flow should say the tool needs approval: %+v", st.Issues)
	}
	if !strings.Contains(strings.Join(st.Notes, " | "), "imported tool(s) echo-arg") {
		t.Fatalf("notes: %q", st.Notes)
	}
}
