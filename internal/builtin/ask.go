package builtin

// ask_user: ask a person multiple-choice questions and get their answers.
// The caller (an agent, a flow step, the Playground) gives the questions,
// each with its options and the recommended answer; SpecWright shows them
// in a dialog (Config.Ask), the person chooses (or writes their own when
// allowed) and sends; the answers come back as JSON and are recorded with
// the run. When nobody answers in time, the recommended answers are taken
// (or the call fails, as the caller chooses), and the result says so.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Question is one multiple-choice question.
type Question struct {
	ID          string   `json:"id"`
	Question    string   `json:"question"`
	Options     []string `json:"options"`
	Multiple    bool     `json:"multiple,omitempty"`    // more than one option may be chosen
	Recommended []string `json:"recommended,omitempty"` // options from Options
	AllowOther  bool     `json:"allowOther,omitempty"`  // the person may write their own answer
	Why         string   `json:"why,omitempty"`         // why the recommendation, shown with it
}

// AskRequest is what the dialog shows.
type AskRequest struct {
	Title     string        `json:"title,omitempty"`
	Intro     string        `json:"intro,omitempty"`
	Questions []Question    `json:"questions"`
	Timeout   time.Duration `json:"-"`
}

// Answer is a person's answer to one question.
type Answer struct {
	ID       string   `json:"id"`
	Selected []string `json:"selected"`
	Other    string   `json:"other,omitempty"`
}

// AskReply is how the questions were answered.
type AskReply struct {
	Answers []Answer `json:"answers"`
	// By: "person", or "recommended" when nobody answered in time (or the
	// person chose to go with the recommendations without looking).
	By string `json:"by"`
}

// ErrNoAnswer is returned by Config.Ask when nobody answered in time.
var ErrNoAnswer = errors.New("nobody answered")

const (
	maxQuestions = 25
	maxOptions   = 20
)

// parseQuestions reads the questions argument: an array, or its JSON text.
func parseQuestions(v any) ([]Question, error) {
	var raw []byte
	switch x := v.(type) {
	case nil:
		return nil, errors.New("give questions: an array of {id, question, options, recommended}")
	case string:
		raw = []byte(strings.TrimSpace(x))
	default:
		raw, _ = json.Marshal(x)
	}
	// read leniently: the shapes agents write (recommended as one string,
	// allow_other, text for question, options as objects)
	var loose []map[string]any
	if err := json.Unmarshal(raw, &loose); err != nil {
		return nil, fmt.Errorf("questions must be a JSON array of objects: %v", err)
	}
	var qs []Question
	for _, m := range loose {
		b, _ := json.Marshal(normalizeQuestion(m))
		var q Question
		if err := json.Unmarshal(b, &q); err != nil {
			return nil, fmt.Errorf("a question: %v", err)
		}
		qs = append(qs, q)
	}
	if len(qs) == 0 {
		return nil, errors.New("give at least one question")
	}
	if len(qs) > maxQuestions {
		return nil, fmt.Errorf("at most %d questions at once", maxQuestions)
	}
	seen := map[string]bool{}
	for i := range qs {
		q := &qs[i]
		q.Question = strings.TrimSpace(q.Question)
		if q.Question == "" {
			return nil, fmt.Errorf("question %d has no text", i+1)
		}
		if strings.TrimSpace(q.ID) == "" {
			q.ID = fmt.Sprintf("q%d", i+1)
		}
		if seen[q.ID] {
			return nil, fmt.Errorf("two questions have the id %q", q.ID)
		}
		seen[q.ID] = true
		var opts []string
		for _, o := range q.Options {
			if o = strings.TrimSpace(o); o != "" && !slices.Contains(opts, o) {
				opts = append(opts, o)
			}
		}
		if len(opts) < 2 && !q.AllowOther {
			return nil, fmt.Errorf("%q needs at least two options (or allowOther)", q.Question)
		}
		if len(opts) > maxOptions {
			return nil, fmt.Errorf("%q has more than %d options", q.Question, maxOptions)
		}
		q.Options = opts
		var rec []string
		for _, r := range q.Recommended {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			if !slices.Contains(opts, r) {
				return nil, fmt.Errorf("%q: the recommended answer %q is not one of its options", q.Question, r)
			}
			rec = append(rec, r)
		}
		if len(rec) > 1 && !q.Multiple {
			return nil, fmt.Errorf("%q: one recommended answer (or set multiple)", q.Question)
		}
		q.Recommended = rec
	}
	return qs, nil
}

// normalizeQuestion accepts the shapes agents tend to write: recommended
// as a string, allow_other, text instead of question, options as objects.
func normalizeQuestion(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		switch strings.ToLower(strings.ReplaceAll(k, "_", "")) {
		case "id", "key", "name":
			out["id"] = fmt.Sprint(v)
		case "question", "text", "prompt", "title":
			out["question"] = fmt.Sprint(v)
		case "options", "choices", "answers":
			var opts []string
			if a, ok := v.([]any); ok {
				for _, o := range a {
					switch x := o.(type) {
					case string:
						opts = append(opts, x)
					case map[string]any:
						for _, f := range []string{"label", "value", "text", "option"} {
							if s, ok := x[f].(string); ok {
								opts = append(opts, s)
								break
							}
						}
					}
				}
			}
			out["options"] = opts
		case "recommended", "recommendation", "default", "suggested":
			switch x := v.(type) {
			case string:
				out["recommended"] = []string{x}
			case []any:
				var rs []string
				for _, r := range x {
					rs = append(rs, fmt.Sprint(r))
				}
				out["recommended"] = rs
			}
		case "multiple", "multi", "multiselect", "multichoice":
			out["multiple"] = v == true || v == "true"
		case "allowother", "other", "freetext":
			out["allowOther"] = v == true || v == "true"
		case "why", "reason", "rationale":
			out["why"] = fmt.Sprint(v)
		}
	}
	return out
}

// recommendedReply answers every question with its recommendation (the
// first option when there is none).
func recommendedReply(qs []Question) AskReply {
	r := AskReply{By: "recommended"}
	for _, q := range qs {
		sel := q.Recommended
		if len(sel) == 0 && len(q.Options) > 0 {
			sel = q.Options[:1]
		}
		r.Answers = append(r.Answers, Answer{ID: q.ID, Selected: append([]string{}, sel...)})
	}
	return r
}

func askUser(ctx context.Context, b *Box, a args) (string, error) {
	qs, err := parseQuestions(a["questions"])
	if err != nil {
		return "", err
	}
	if b.ask == nil {
		return "", errors.New("asking a person is not available here")
	}
	req := AskRequest{Title: strings.TrimSpace(a.str("title")), Intro: strings.TrimSpace(a.str("intro")), Questions: qs,
		Timeout: time.Duration(a.num("timeout_seconds", 600, 30, 86400)) * time.Second}
	onTimeout := strings.ToLower(firstStr(a.str("if_no_answer"), "recommended"))
	reply, err := b.ask(ctx, req)
	note := ""
	switch {
	case errors.Is(err, ErrNoAnswer) && onTimeout != "fail":
		reply = recommendedReply(qs)
		note = fmt.Sprintf("Nobody answered within %s: the recommended answers were taken.", req.Timeout)
	case err != nil:
		return "", err
	}
	// each answer next to its question, only options that exist
	type full struct {
		ID                  string   `json:"id"`
		Question            string   `json:"question"`
		Selected            []string `json:"selected"`
		Other               string   `json:"other,omitempty"`
		Recommended         []string `json:"recommended,omitempty"`
		FollowedRecommended bool     `json:"followed_recommended"`
	}
	byID := map[string]Answer{}
	for _, x := range reply.Answers {
		byID[x.ID] = x
	}
	out := struct {
		AnsweredBy string `json:"answered_by"`
		Note       string `json:"note,omitempty"`
		Answers    []full `json:"answers"`
	}{AnsweredBy: firstStr(reply.By, "person"), Note: note}
	for _, q := range qs {
		x := byID[q.ID]
		var sel []string
		for _, s := range x.Selected {
			if slices.Contains(q.Options, s) && !slices.Contains(sel, s) {
				sel = append(sel, s)
			}
		}
		if !q.Multiple && len(sel) > 1 {
			sel = sel[:1]
		}
		if sel == nil {
			sel = []string{}
		}
		other := ""
		if q.AllowOther {
			other = strings.TrimSpace(x.Other)
		}
		follow := len(q.Recommended) > 0 && other == "" && len(sel) == len(q.Recommended)
		for _, r := range q.Recommended {
			follow = follow && slices.Contains(sel, r)
		}
		out.Answers = append(out.Answers, full{ID: q.ID, Question: q.Question, Selected: sel, Other: other, Recommended: q.Recommended, FollowedRecommended: follow})
	}
	bs, _ := json.MarshalIndent(out, "", "  ")
	return string(bs), nil
}
