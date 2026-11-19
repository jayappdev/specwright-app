package flow

import (
	"strings"
	"testing"
)

func TestParseAIDecision(t *testing.T) {
	n := Node{Type: "decision", Decide: "ai", Branches: []Branch{{ID: "ship", Label: "Ship it"}, {ID: "hold", Label: "Hold"}}}
	for reply, want := range map[string]AIDecision{
		`{"path": "ship", "reason": "All green [S1].", "confidence": 92}`:       {Path: "ship", Reason: "All green [S1].", Confidence: 92},
		"Here it is:\n```json\n{\"path\": \"Hold\", \"confidence\": 0.4}\n```":  {Path: "hold", Confidence: 40},
		`{"path": "otherwise", "reason": "Neither fits.", "confidence": "75%"}`: {Path: ElseBranch, Reason: "Neither fits.", Confidence: 75},
		`{"path": "ship", "confidence": 140}`:                                   {Path: "ship", Confidence: 100},
	} {
		got, err := ParseAIDecision(reply, n)
		if err != nil || got != want {
			t.Errorf("%q: %+v %v, want %+v", reply, got, err, want)
		}
	}
	for _, bad := range []string{`{"path": "deploy"}`, "ship it!"} {
		if _, err := ParseAIDecision(bad, n); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestAIDecisionChecks(t *testing.T) {
	f := New("d", "D")
	f.Nodes = append(f.Nodes, Node{ID: "dec", Type: "decision", Decide: "ai", Confidence: 120, Unsure: "maybe",
		Branches: []Branch{{ID: "a", Label: "A"}, {ID: "b"}}})
	f.Edges = []Edge{{From: "start", To: "dec"}}
	var all []string
	for _, is := range Validate(f, nil) {
		all = append(all, is.Message)
	}
	msgs := strings.Join(all, "\n")
	for _, want := range []string{"write the question", "confidence is 0 to 100", "when unsure it asks", "give the path b a name"} {
		if !strings.Contains(msgs, want) {
			t.Errorf("missing %q in\n%s", want, msgs)
		}
	}
	if strings.Contains(msgs, "condition") {
		t.Fatalf("an AI decision needs no conditions:\n%s", msgs)
	}
}
