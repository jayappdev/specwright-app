package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"codegen/internal/core"
	"codegen/internal/flow"
)

// A brand-new machine: an empty app store and an empty project. Nothing
// the UI loads may contain a JSON null where a list belongs.
func TestFreshMachineReturnsNoNullLists(t *testing.T) {
	h, err := core.New(core.Options{StoreDir: filepath.Join(t.TempDir(), "store"), Agent: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	a := &App{hub: h}
	ws := filepath.Join(t.TempDir(), "empty project")
	_ = os.MkdirAll(ws, 0o755)
	st, err := a.OpenWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	listNull := regexp.MustCompile(`"(\w+)":null`)
	check := func(name string, v any) {
		t.Helper()
		b, _ := json.Marshal(v)
		if string(b) == "null" {
			t.Errorf("%s returned null", name)
		}
		for _, m := range listNull.FindAllStringSubmatch(string(b), -1) {
			t.Errorf("%s has %q: null", name, m[1])
		}
	}
	check("OpenWorkspace", st)
	files, _ := a.ListFiles()
	check("ListFiles", files)
	m, _ := a.Metrics()
	check("Metrics", m)
	runs, _ := a.Runs(50)
	check("Runs", runs)
	exts, _ := a.Extensions()
	check("Extensions", exts)
	libs, _ := a.Libraries()
	check("Libraries", libs)
	flows, _ := a.Flows()
	check("Flows", flows)
	fr, _ := a.FlowRuns(50)
	check("FlowRuns", fr)
	rr, _ := a.RecentRuns(50)
	check("RecentRuns", rr)
	check("ManagedTools", a.ManagedTools())
	check("ManagedLibraries", a.ManagedLibraries())
	check("ValidateFlow", a.ValidateFlow(flow.Flow{}))
	wf, _ := a.Workflow()
	check("Workflow", wf)
}

func TestFillLeavesBytesAndRawJSON(t *testing.T) {
	type in struct {
		Names []string
		Raw   json.RawMessage
		Bytes []byte
		Inner *struct{ List []int }
		M     map[string]int
	}
	v := val(in{Inner: &struct{ List []int }{}})
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"Names":[],"Raw":null,"Bytes":null,"Inner":{"List":[]},"M":{}}` {
		t.Fatal(string(b))
	}
}
