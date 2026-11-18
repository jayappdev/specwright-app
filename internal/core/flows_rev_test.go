package core

import (
	"os"
	"strings"
	"testing"

	"codegen/internal/flow"
)

// An agent (or another window) changes the flow's file while it is open in
// the designer: the designer's next save must not write its stale copy over it.
func TestSaveFlowRefusesStaleCopy(t *testing.T) {
	h, ws := flowHub(t)
	open, err := h.LoadFlow("notes")
	if err != nil || open.Rev == "" {
		t.Fatal(err, open.Rev)
	}
	// saving what was loaded is fine, and gives the new revision
	st, err := h.SaveFlow(*open)
	if err != nil || st.Flow.Rev == "" || st.Flow.Rev != flow.DiskRev(ws, "notes") {
		t.Fatalf("%v %q", err, st.Flow.Rev)
	}
	mine := *st.Flow
	// the agent edits the file directly
	p := flow.Path(ws, "notes")
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), `"Release notes"`, `"Release notes (by the agent)"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	mine.Description = "my stale edit"
	if _, err := h.SaveFlow(mine); err == nil || !strings.Contains(err.Error(), ErrFlowChanged) {
		t.Fatal("a stale copy must be refused:", err)
	}
	f, _ := flow.Load(ws, "notes")
	if f.Name != "Release notes (by the agent)" {
		t.Fatal("the agent's change must survive:", f.Name)
	}
	// keep mine: the UI takes the file's revision and saves again
	mine.Rev = f.Rev
	if _, err := h.SaveFlow(mine); err != nil {
		t.Fatal(err)
	}
	// a flow without a revision (new, imported) saves as before
	nf := flow.New("fresh", "Fresh")
	if _, err := h.SaveFlow(*nf); err != nil {
		t.Fatal(err)
	}
}
