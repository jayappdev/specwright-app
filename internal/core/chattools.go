package core

// The chat's tools (docs/design/2026-11-24-chatflows.md, step 3): each
// branch leaving the Chat step. The chat's AI is a CLI agent that may not
// reach SpecWright's MCP server (some machines do not allow MCP), so it
// asks for a tool in its reply:
//
//	```specwright-run
//	{"tool": "query", "args": {"query.sql": "SELECT …"}, "message": "…"}
//	```
//
// SpecWright runs that branch as a flow (the setup steps' results reused
// from the session, not run again), gives the result back in the same
// conversation, and the AI answers or asks for another. Branches whose
// steps only read run at once; the others ask the person first (Allow for
// this session covers the rest of the session).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"codegen/internal/builtin"
	"codegen/internal/bus"
	"codegen/internal/chat"
	"codegen/internal/extension"
	"codegen/internal/flow"
)

// maxChatRuns bounds the tools one question may run.
const maxChatRuns = 4

// chatCall is a tool the AI asked for.
type chatCall struct {
	Tool    string            `json:"tool"`
	Args    map[string]any    `json:"args"`
	Message string            `json:"message"`
	Why     string            `json:"why"`
	args    map[string]string // Args as text
}

var runBlockRE = regexp.MustCompile("(?s)```specwright-run\\s*\\n(.*?)```")

// parseChatCalls finds the tool requests in a reply, and the reply
// without them.
func parseChatCalls(reply string) ([]chatCall, string) {
	var calls []chatCall
	for _, m := range runBlockRE.FindAllStringSubmatch(reply, -1) {
		var c chatCall
		if json.Unmarshal([]byte(strings.TrimSpace(m[1])), &c) != nil || strings.TrimSpace(c.Tool) == "" {
			continue
		}
		c.args = map[string]string{}
		for k, v := range c.Args {
			if s, ok := v.(string); ok {
				c.args[k] = s
			} else if b, err := json.Marshal(v); err == nil {
				c.args[k] = string(b)
			}
		}
		calls = append(calls, c)
	}
	rest := strings.TrimSpace(runBlockRE.ReplaceAllString(reply, ""))
	// a block that is still being written (streamed text): leave it out too
	if i := strings.Index(rest, "```specwright-run"); i >= 0 {
		rest = strings.TrimSpace(rest[:i])
	}
	return calls, rest
}

// chatToolFields: the fields of a branch's tool steps the AI may fill
// (those the author left empty), as "<step>.<param>" with what the tool says.
func (h *Hub) chatToolFields(f *flow.Flow, t flow.ChatTool) map[string]string {
	out := map[string]string{}
	descs := map[string]map[string]string{}
	for _, e := range h.Callable() {
		m := map[string]string{}
		for _, p := range e.Params {
			m[p.Name] = strings.TrimSpace(p.Type + " · " + p.Desc)
		}
		descs[e.Name] = m
	}
	for _, id := range t.Steps {
		n := f.Node(id)
		if n == nil || n.Type != "tool" {
			continue
		}
		for k, v := range n.Args {
			if strings.TrimSpace(v) == "" {
				d := descs[n.Tool][k]
				if bt, ok := builtin.Find(strings.TrimPrefix(n.Tool, BuiltinPrefix)); ok && d == "" {
					if p, ok := bt.Schema["properties"].(map[string]any)[k].(map[string]any); ok {
						d, _ = p["description"].(string)
					}
				}
				out[id+"."+k] = d
			}
		}
	}
	return out
}

// chatToolsText is the part of the first prompt that lists the tools:
// the branches (each a short pipeline of steps) and the built-in ones;
// builtins lists the built-in data tools (chatData.toolsText).
func (h *Hub) chatToolsText(f *flow.Flow, builtins string) string {
	tools := f.ChatTools()
	if len(tools) == 0 && builtins == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nWHAT YOU CAN RUN (it runs in SpecWright, not on your side):\n")
	b.WriteString(builtins)
	if len(tools) > 0 && builtins != "" {
		b.WriteString("\n")
	}
	for _, t := range tools {
		var titles []string
		for _, id := range t.Steps {
			if n := f.Node(id); n != nil {
				titles = append(titles, firstNonEmpty(n.Title, n.ID))
			}
		}
		fmt.Fprintf(&b, "\n- tool %q: %s", t.ID, t.Title)
		if t.Desc != "" {
			b.WriteString(" — " + t.Desc)
		}
		if len(titles) > 1 {
			b.WriteString(" (steps: " + strings.Join(titles, " → ") + ")")
		}
		fields := h.chatToolFields(f, t)
		if len(fields) > 0 {
			keys := make([]string, 0, len(fields))
			for k := range fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			b.WriteString("\n  fields you can fill:")
			for _, k := range keys {
				fmt.Fprintf(&b, "\n    %s: %s", k, firstNonEmpty(fields[k], "text"))
			}
		}
		if !h.chatToolReadOnly(f, t) {
			b.WriteString("\n  (it changes things: the person is asked before it runs)")
		}
	}
	b.WriteString("\n\nTo run one, reply with only this block (then stop; you get its result and continue):\n```specwright-run\n{\"tool\": \"<tool id>\", \"args\": {\"<step>.<field> (or sql / query)\": \"value\"}, \"message\": \"<what to pass on, if the steps use it>\", \"why\": \"<one short reason>\"}\n```\nSeveral blocks in one reply run together. Run a tool only when the session data (and the passages sent with the question) do not already answer it; at most " + fmt.Sprint(maxChatRuns) + " pipelines and " + fmt.Sprint(maxDataRuns) + " data.query or data.search runs per question. Never put secrets or passwords in a field.\n")
	return b.String()
}

// chatToolReadOnly reports whether a branch only reads: its tools are
// read-only (built-ins that say so; project tools only when an admin signed
// them and their permissions are all reading: a tool's own label is not
// enough, an AI may have written it), and its outputs only show their
// result. Anything else is asked once per session (security review item 2).
func (h *Hub) chatToolReadOnly(f *flow.Flow, t flow.ChatTool) bool {
	perms := map[string][]string{}
	for _, e := range h.Callable() {
		if e.Managed != nil && e.Managed.Status == "verified" {
			perms[e.Name] = e.Perms
		}
	}
	for _, id := range t.Steps {
		n := f.Node(id)
		if n == nil {
			continue
		}
		switch n.Type {
		case "tool":
			if strings.HasPrefix(n.Tool, BuiltinPrefix) {
				if bt, ok := builtin.Find(strings.TrimPrefix(n.Tool, BuiltinPrefix)); !ok || !bt.ReadOnly {
					return false
				}
				continue
			}
			ps, ok := perms[n.Tool]
			if !ok || len(ps) == 0 {
				return false // unknown: ask
			}
			for _, p := range ps {
				if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(p)), "read") {
					return false
				}
			}
		case "output":
			for _, a := range n.Actions {
				if a.Type != "show" {
					return false
				}
			}
		}
	}
	return true
}

// chatToolFlow is the flow that runs one branch: the Inputs and setup
// steps (their results reused), the Chat step as the message, and the
// branch, with the AI's fields filled in where the author left them empty.
func chatToolFlow(f *flow.Flow, t flow.ChatTool, c chatCall) *flow.Flow {
	keep := map[string]bool{"start": true}
	for _, id := range f.ChatSetup() {
		keep[id] = true
	}
	inTool := map[string]bool{}
	for _, id := range t.Steps {
		keep[id], inTool[id] = true, true
	}
	chatID := f.ChatNode().ID
	keep[chatID] = true
	g := *f
	g.Name, g.Triggers, g.Nodes, g.Edges = f.Name+" · "+t.Title, nil, nil, nil
	msg := firstNonEmpty(c.Message, c.Why)
	fill := func(s string) string {
		s = strings.ReplaceAll(s, "{{chat.message}}", msg)
		return strings.ReplaceAll(s, "{{session.", "{{steps.")
	}
	for _, n := range f.Nodes {
		if !keep[n.ID] {
			continue
		}
		n.Review = false
		switch {
		case n.ID == chatID: // the Chat step gives the message to the branch
			n = flow.Node{ID: n.ID, Type: "transform", Op: "template", Title: "Message", Template: msg, X: n.X, Y: n.Y}
		case inTool[n.ID]:
			args := map[string]string{}
			for k, v := range n.Args {
				if strings.TrimSpace(v) == "" {
					v = c.args[n.ID+"."+k] // the AI fills what the author left empty
				}
				args[k] = fill(v)
			}
			n.Args = args
			n.Template, n.Prompt, n.Source, n.Instruction = fill(n.Template), fill(n.Prompt), fill(n.Source), fill(n.Instruction)
		}
		g.Nodes = append(g.Nodes, n)
	}
	for _, e := range f.Edges {
		if keep[e.From] && keep[e.To] {
			g.Edges = append(g.Edges, e)
		}
	}
	return &g
}

// runChatTool runs one branch for a message and returns its result.
func (h *Hub) runChatTool(ctx context.Context, root string, f *flow.Flow, s chat.Session, c chatCall, n int) (chat.StepRun, string) {
	var t flow.ChatTool
	for _, x := range f.ChatTools() { // by id, or by the title the AI saw
		if x.ID == c.Tool || (t.ID == "" && strings.EqualFold(x.Title, strings.TrimSpace(c.Tool))) {
			t = x
		}
	}
	c.Tool = firstNonEmpty(t.ID, c.Tool)
	run := chat.StepRun{Tool: c.Tool, Title: firstNonEmpty(t.Title, c.Tool), Args: c.Args}
	if t.ID == "" {
		run.Error = fmt.Sprintf("there is no tool %q", c.Tool)
		return run, run.Error
	}
	key := chatKey(s.Flow, s.ID)
	if !h.chatToolReadOnly(f, t) {
		input := map[string]any{"description": "The chat wants to run " + t.Title + " (" + strings.Join(t.Steps, " → ") + ")"}
		if c.Why != "" {
			input["why"] = c.Why
		}
		for k, v := range c.args {
			input[k] = v
		}
		ans := h.AskPermission(ctx, extension.PermissionRequest{Agent: "chat:" + key, Tool: t.Title, Input: input})
		if !ans.Allow {
			run.Error = firstNonEmpty(ans.Message, "The person declined to run "+t.Title+".")
			return run, run.Error
		}
	}
	st := chat.New(root)
	sess, err := st.Get(s.Flow, s.ID)
	if err != nil {
		run.Error = err.Error()
		return run, run.Error
	}
	reuse := map[string]ReusedStep{}
	for _, d := range sess.Data {
		if d.Setup && d.Step != "" {
			if content, err := st.GetData(s.Flow, s.ID, d.Name); err == nil {
				reuse[d.Step] = ReusedStep{Output: content}
			}
		}
	}
	h.chats.mu.Lock()
	env, envSec, secretInputs := h.chats.env[key], h.chats.envSec[key], h.chats.inputs[key]
	h.chats.mu.Unlock()
	if env == nil { // a session from before the app restarted: resolve its environment again
		if env, envSec, err = h.chatEnv(ctx, f); err != nil {
			run.Error = err.Error()
			return run, run.Error
		}
		h.chats.mu.Lock()
		h.chats.ready()
		h.chats.env[key], h.chats.envSec[key] = env, envSec
		h.chats.mu.Unlock()
	}
	inputs := map[string]string{}
	for k, v := range sess.Inputs {
		inputs[k] = v
	}
	o := FlowRunOptions{Inputs: inputs, Review: "none", Reuse: reuse, Env: env, EnvSecret: envSec, Secrets: secretInputs,
		Chat: &FlowRunChat{Role: "tool", Session: s.ID, Tool: t.Title}}
	for k := range secretInputs {
		o.Inputs[k] = flow.SecretMask
	}
	h.Bus.Publish(chatStepEvent(s, run, "running"))
	t0 := time.Now()
	res, err := h.startFlow(root, chatToolFlow(f, t, c), o, nil)
	if err == nil {
		run.Run = res.ID
		h.mu.RLock()
		fr := h.flowRuns[res.ID]
		h.mu.RUnlock()
		if fr != nil {
			select {
			case <-fr.done:
			case <-ctx.Done():
				_ = h.StopFlowRun(res.ID)
				<-fr.done
			}
		}
		res, err = h.FlowRun(res.ID)
	}
	run.DurationMS = time.Since(t0).Milliseconds()
	if err == nil && res.Status != "done" {
		err = fmt.Errorf("%s (%s)", firstNonEmpty(res.Error, "it did not finish"), res.Status)
	}
	if err != nil {
		run.Error = h.Store.Redactor().String(err.Error())
		h.Bus.Publish(chatStepEvent(s, run, "failed"))
		return run, run.Error
	}
	for _, o := range res.Outputs { // files the branch saved
		run.Files = append(run.Files, o.Saved...)
	}
	run.Usage = usageOf(res.Usage)
	out := chatToolResult(root, t, res)
	out = h.Store.Redactor().String(out)
	name := fmt.Sprintf("m%03d-%s.%s", n, c.Tool, contentExt(out))
	if _, err := st.PutData(s.Flow, s.ID, chat.Data{Name: name, Step: c.Tool, Title: run.Title}, out); err == nil {
		run.Data = name
	}
	h.Bus.Publish(chatStepEvent(s, run, "done"))
	return run, out
}

// chatToolResult: what a branch gave — its outputs' content, else the
// output of its last steps.
func chatToolResult(root string, t flow.ChatTool, res FlowRunState) string {
	var parts []string
	for _, o := range res.Outputs {
		if strings.TrimSpace(o.Content) != "" {
			parts = append(parts, o.Content)
		}
	}
	if len(parts) == 0 {
		last := t.Steps[len(t.Steps)-1]
		for _, s := range res.Steps {
			if s.ID != last {
				continue
			}
			c := s.Output
			if s.OutputFile != "" {
				if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(s.OutputFile))); err == nil {
					c = string(b)
				}
			}
			parts = append(parts, c)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func chatStepEvent(s chat.Session, r chat.StepRun, status string) bus.Event {
	return bus.Event{Type: "chat.step", Summary: r.Title + ": " + status,
		Data: map[string]any{"flow": s.Flow, "session": s.ID, "tool": r.Tool, "title": r.Title, "status": status, "durationMs": r.DurationMS, "error": r.Error, "data": r.Data}}
}
