package flow

// Chatflows: a flow with a Chat step (docs/design/2026-11-24-chatflows.md).
//
// The steps that feed the Chat step are its setup: they run once when a
// chat session starts, and their results stay in the session. Each branch
// leaving the Chat step is one tool the chat's AI can call: its first step
// names it, and its steps run in order. The Chat step holds the assistant's
// instructions, starter questions, model and cost limit. The flow's Vars
// and Secrets are resolved when a session starts and given to every step
// the session runs; secrets are named here, never stored.

import (
	"regexp"
	"sort"
	"strings"
)

// Var is an environment variable a Chatflow sets (not a secret).
type Var struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Desc  string `json:"desc,omitempty"`
}

// SecretVar is a secret a Chatflow needs, by name: its value comes from
// the session, the keychain or the environment, or is asked for once.
type SecretVar struct {
	Name string `json:"name"`
	Desc string `json:"desc,omitempty"`
}

// ChatTool is one branch leaving the Chat step: what the chat's AI can run.
type ChatTool struct {
	ID    string   `json:"id"`    // its first step
	Title string   `json:"title"` // the first step's title (or id)
	Desc  string   `json:"desc,omitempty"`
	Steps []string `json:"steps"` // the branch's steps, in run order
}

var varNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// ChatNode is the flow's Chat step, or nil.
func (f *Flow) ChatNode() *Node {
	for i := range f.Nodes {
		if f.Nodes[i].Type == "chat" {
			return &f.Nodes[i]
		}
	}
	return nil
}

// IsChatflow reports whether f is a Chatflow (it has a Chat step).
func (f *Flow) IsChatflow() bool { return f.ChatNode() != nil }

// ancestors: every step with a path to id (not id).
func (f *Flow) ancestors(id string) map[string]bool {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(to string) {
		for _, e := range f.Edges {
			if e.To == to && !seen[e.From] {
				seen[e.From] = true
				walk(e.From)
			}
		}
	}
	walk(id)
	return seen
}

// descendants: every step id reaches (not id).
func (f *Flow) descendants(id string) map[string]bool {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(from string) {
		for _, e := range f.Edges {
			if e.From == from && !seen[e.To] {
				seen[e.To] = true
				walk(e.To)
			}
		}
	}
	walk(id)
	return seen
}

// inOrder lists the given steps in the flow's run order.
func (f *Flow) inOrder(set map[string]bool) []string {
	order, err := f.Order()
	var out []string
	if err == nil {
		for _, id := range order {
			if set[id] {
				out = append(out, id)
			}
		}
		return out
	}
	for id := range set { // a cycle: Validate says so; keep a stable order
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ChatSetup lists the Chatflow's setup steps (those feeding the Chat
// step, the Inputs step included), in run order.
func (f *Flow) ChatSetup() []string {
	c := f.ChatNode()
	if c == nil {
		return nil
	}
	return f.inOrder(f.ancestors(c.ID))
}

// ChatTools lists the chat's tools: one per step the Chat step connects
// to, with that step and everything after it.
func (f *Flow) ChatTools() []ChatTool {
	c := f.ChatNode()
	if c == nil {
		return nil
	}
	var out []ChatTool
	for _, e := range f.Edges {
		if e.From != c.ID {
			continue
		}
		n := f.Node(e.To)
		if n == nil {
			continue
		}
		set := f.descendants(n.ID)
		set[n.ID] = true
		t := ChatTool{ID: n.ID, Title: strings.TrimSpace(n.Title), Desc: strings.TrimSpace(n.Note), Steps: f.inOrder(set)}
		if t.Title == "" {
			t.Title = n.ID
		}
		out = append(out, t)
	}
	return out
}

// checkChat adds a Chatflow's problems.
func (f *Flow) checkChat(add func(node, level, format string, a ...any)) {
	var chats []string
	for _, n := range f.Nodes {
		if n.Type == "chat" {
			chats = append(chats, n.ID)
		}
	}
	if len(f.Vars) > 0 || len(f.Secrets) > 0 {
		seen := map[string]bool{}
		for _, v := range f.Vars {
			if !varNameRE.MatchString(v.Name) {
				add("", "error", "Variable %q: use letters, digits and _ (e.g. REGION).", v.Name)
			}
			if seen[v.Name] {
				add("", "error", "Two variables or secrets are called %s.", v.Name)
			}
			seen[v.Name] = true
		}
		for _, s := range f.Secrets {
			if !varNameRE.MatchString(s.Name) {
				add("", "error", "Secret %q: use letters, digits and _ (e.g. DB_PASSWORD).", s.Name)
			}
			if seen[s.Name] {
				add("", "error", "Two variables or secrets are called %s.", s.Name)
			}
			seen[s.Name] = true
		}
	}
	if len(chats) == 0 {
		return
	}
	if len(chats) > 1 {
		add(chats[1], "error", "A Chatflow has one Chat step.")
	}
	c := f.Node(chats[0])
	up := f.ancestors(c.ID)
	if !up["start"] {
		add(c.ID, "error", "Connect the Chat step after the Inputs step (directly, or after the steps that set up the session).")
	}
	tools := f.ChatTools()
	if len(tools) == 0 {
		add(c.ID, "warning", "Connect steps after the Chat step: each path out of it is something the chat can run (a query, a fetch…). Without any it can only talk about the setup's data.")
	}
	owner := map[string]string{}
	for _, t := range tools {
		for _, id := range t.Steps {
			if up[id] {
				add(id, "error", "%s runs both before the Chat step (setup) and after it (in %q): a step is one or the other.", id, t.Title)
			}
			if prev, ok := owner[id]; ok && prev != t.Title {
				add(id, "warning", "%s is in two of the chat's tools (%q and %q): it runs with either.", id, prev, t.Title)
			}
			owner[id] = t.Title
			if n := f.Node(id); n != nil && n.Type == "chat" {
				add(id, "error", "A Chat step cannot come after the Chat step.")
			}
		}
	}
	for _, n := range f.Nodes {
		if n.Type == "start" || n.ID == c.ID || up[n.ID] {
			continue
		}
		if _, ok := owner[n.ID]; !ok {
			add(n.ID, "warning", "%s is not connected to the Chat step: it never runs in a chat.", firstNonEmptyStr(n.Title, n.ID))
		}
	}
	if len(f.Triggers) > 0 {
		add(c.ID, "warning", "A Chatflow is chatted with, not run: its triggers never start it.")
	}
}

// fedByChat reports whether one of these steps is a Chat step.
func fedByChat(f *Flow, ids []string) bool {
	for _, id := range ids {
		if n := f.Node(id); n != nil && n.Type == "chat" {
			return true
		}
	}
	return false
}
