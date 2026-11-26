package core

import (
	"context"
	"strings"
	"testing"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// a DB_* secret family: ${DB_SALES} in the arguments is resolved (asked
// for here), passed to the tool; a secret the app holds outside the family
// is not
func TestSecretFamilyFromArguments(t *testing.T) {
	h, _ := flowHub(t)
	// a secret the app holds for other tools (this session's values)
	h.sec.mu.Lock()
	if h.sec.values == nil {
		h.sec.values = map[string]string{}
	}
	h.sec.values["OTHER_TOKEN"] = "leak"
	h.sec.mu.Unlock()
	var asked []string
	h.SetSecretAsker(func(_ context.Context, r SecretRequest) (map[string]string, error) {
		out := map[string]string{}
		for _, s := range r.Secrets {
			asked = append(asked, s.Name)
			out[s.Name] = "pw-" + s.Name
		}
		return out, nil
	})
	e := extension.Extension{Name: "db-echo", Desc: "Echo", Lang: "Bash", Scope: "project", Approved: true,
		Params:  []extension.Param{{Name: "dsn", Type: "string", Required: true}},
		Secrets: []extension.Secret{{Name: "DB_*", Desc: "Database passwords", Optional: true}},
		Command: `sh -c 'echo "sales=$DB_SALES hr=$DB_HR other=$OTHER_TOKEN"' {{dsn}}`}
	saved, err := h.SaveExtension(e)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.RunToolWith(saved, map[string]any{"dsn": "a=${DB_SALES} b=${DB_HR} c=${OTHER_TOKEN} d=${DB_}"}, nil, nil)
	if err != nil || !run.OK {
		t.Fatalf("%+v %v", run, err)
	}
	// values are redacted in what the run shows; what was asked shows the names
	if strings.Join(asked, ",") != "DB_SALES,DB_HR" {
		t.Fatalf("asked for %v", asked)
	}
	if strings.Contains(run.Output, "leak") {
		t.Fatalf("a secret outside the family reached the tool: %s", run.Output)
	}
	// the family is never missing and never asked for by itself
	st := h.SecretInfo(saved.Secrets)
	if len(st) != 1 || !st[0].Optional || st[0].Via != "optional" {
		t.Fatalf("%+v", st)
	}
	// a flow's credentials list the members its steps name, ready to save before a run
	f := flow.New("fam", "Family")
	f.Nodes = append(f.Nodes, flow.Node{ID: "q", Type: "tool", Tool: "db-echo", Args: map[string]string{"dsn": "x=${DB_WAREHOUSE}"}})
	f.Edges = append(f.Edges, flow.Edge{From: "start", To: "q"})
	if err := flow.Save(h.Root(), f); err != nil {
		t.Fatal(err)
	}
	creds, err := h.FlowCredentials("fam")
	if err != nil || len(creds) != 1 || creds[0].Name != "DB_WAREHOUSE" || creds[0].Optional || creds[0].Tools[0] != "db-echo" {
		t.Fatalf("%+v %v", creds, err)
	}
	x := extension.WithArgSecrets(saved, map[string]any{"q": "${DB_A} ${DB_A} ${GITHUB_TOKEN}"})
	if len(x.Secrets) != 2 || x.Secrets[1].Name != "DB_A" || !strings.Contains(x.Secrets[1].Desc, "Database passwords") || len(saved.Secrets) != 1 {
		t.Fatalf("%+v", x.Secrets)
	}
}
