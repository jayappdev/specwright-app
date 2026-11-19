package extension

import (
	"encoding/json"
	"slices"
	"testing"
)

// the error from a generated tool: "cannot unmarshal number into Go struct
// field Extension.params.6.default of type string"
func TestParamTakesScalars(t *testing.T) {
	src := `{"name": "report", "params": [
		{"name": "limit", "type": "number", "default": 10, "example": 25},
		{"name": "verbose", "type": "boolean", "default": false, "required": "true"},
		{"name": "level", "type": "string", "enum": [1, 2, "high"], "default": "2"},
		{"name": "filter", "type": "json", "default": {"status": "Done", "max": 5}},
		{"name": "tags", "type": "array", "default": ["a", "b"], "example": null},
		{"name": "path", "type": "string", "required": true, "desc": "a file"}
	]}`
	var e Extension
	if err := json.Unmarshal([]byte(src), &e); err != nil {
		t.Fatal(err)
	}
	p := e.Params
	if len(p) != 6 {
		t.Fatalf("%+v", p)
	}
	for i, want := range []string{"10", "false", "2", `{"status":"Done","max":5}`, `["a","b"]`, ""} {
		if p[i].Default != want {
			t.Errorf("param %s default %q, want %q", p[i].Name, p[i].Default, want)
		}
	}
	if p[0].Example != "25" || p[4].Example != "" {
		t.Fatalf("examples %q %q", p[0].Example, p[4].Example)
	}
	if !slices.Equal(p[2].Enum, []string{"1", "2", "high"}) {
		t.Fatalf("%q", p[2].Enum)
	}
	if !p[1].Required || p[0].Required || !p[5].Required || p[5].Desc != "a file" || p[5].Type != "string" {
		t.Fatalf("%+v", p)
	}
	// a parameter written by SpecWright reads back the same
	b, _ := json.Marshal(p[3])
	var back Param
	if err := json.Unmarshal(b, &back); err != nil || back.Default != p[3].Default || back.Name != "filter" {
		t.Fatalf("%v %+v", err, back)
	}
}
