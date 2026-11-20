package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAskUser(t *testing.T) {
	var got AskRequest
	b := New(Config{Ask: func(_ context.Context, r AskRequest) (AskReply, error) {
		got = r
		return AskReply{By: "person", Answers: []Answer{
			{ID: "scope", Selected: []string{"Only billing", "Not an option"}},
			{ID: "langs", Selected: []string{"Go", "Java"}},
			{ID: "name", Other: "  my own  "},
		}}, nil
	}})
	b.SetRoot(t.TempDir())
	qs := `[{"id":"scope","text":"What should I refactor?","choices":["Everything","Only billing"],"recommended":"Only billing","why":"smaller change"},
	        {"id":"langs","question":"Which languages?","options":["Go","Java","Rust"],"multiple":true,"recommended":["Go","Java"]},
	        {"id":"name","question":"Module name?","options":["core","base"],"allow_other":true}]`
	out, err := b.Call(context.Background(), "ask_user", map[string]any{"questions": qs, "title": "Before I start", "timeout_seconds": 60})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Before I start" || got.Timeout != time.Minute || len(got.Questions) != 3 || got.Questions[0].Question != "What should I refactor?" ||
		got.Questions[0].Recommended[0] != "Only billing" || !got.Questions[2].AllowOther || got.Questions[0].Why != "smaller change" {
		t.Fatalf("the dialog got %+v", got)
	}
	var res struct {
		AnsweredBy string `json:"answered_by"`
		Answers    []struct {
			ID                  string   `json:"id"`
			Selected            []string `json:"selected"`
			Other               string   `json:"other"`
			FollowedRecommended bool     `json:"followed_recommended"`
		} `json:"answers"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	a := res.Answers
	if res.AnsweredBy != "person" || strings.Join(a[0].Selected, ",") != "Only billing" || !a[0].FollowedRecommended ||
		strings.Join(a[1].Selected, ",") != "Go,Java" || !a[1].FollowedRecommended || a[2].Other != "my own" || a[2].FollowedRecommended {
		t.Fatalf("answers: %s", out)
	}

	// nobody answers: the recommendations, or a failure
	b.ask = func(context.Context, AskRequest) (AskReply, error) { return AskReply{}, ErrNoAnswer }
	out, err = b.Call(context.Background(), "ask_user", map[string]any{"questions": qs})
	if err != nil || !strings.Contains(out, `"answered_by": "recommended"`) || !strings.Contains(out, "Nobody answered") {
		t.Fatalf("%s %v", out, err)
	}
	if _, err := b.Call(context.Background(), "ask_user", map[string]any{"questions": qs, "if_no_answer": "fail"}); err == nil {
		t.Fatal("if_no_answer fail should fail")
	}
	for _, bad := range []string{`[]`, `[{"question":"x","options":["a"]}]`, `[{"question":"x","options":["a","b"],"recommended":"c"}]`,
		`[{"question":"x","options":["a","b"],"recommended":["a","b"]}]`, `not json`} {
		if _, err := b.Call(context.Background(), "ask_user", map[string]any{"questions": bad}); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
}
