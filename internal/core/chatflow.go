package core

// Chatflow sessions (docs/design/2026-11-24-chatflows.md, step 2).
//
//   - StartChatSession: the inputs (as Run flow takes them), the Chatflow's
//     variables and secrets resolved, then its setup steps (those feeding
//     the Chat step) run once, as a flow run of a copy holding only them.
//     Each setup step's result is saved as session data.
//   - SendChatMessage: the chat's AI answers in one conversation per
//     session. The first message carries the instructions and the
//     session's data; later ones only the question, continuing that
//     conversation (Claude --resume, Copilot --session-id). When it cannot
//     be continued (the agent changed), the conversation so far is replayed
//     once into a new one. The chat's AI is read-only: what it may do runs
//     as the Chatflow's own steps (step 3).
//
// Secrets and the values of secret inputs are kept in memory only, never
// in the session's files, and are redacted from everything shown.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codegen/internal/agent"
	"codegen/internal/bus"
	"codegen/internal/chat"
	"codegen/internal/extension"
	"codegen/internal/flow"
	"codegen/internal/workflow"
)

// chatState: what the app holds for live sessions (memory only).
type chatState struct {
	mu sync.Mutex
	// a session's environment (variables and secret values) and secret
	// inputs, by "<flow>/<session>"
	env     map[string]map[string]string
	envSec  map[string][]string
	inputs  map[string]map[string]string
	answers map[string]context.CancelFunc // a message being answered
}

// ChatflowInfo describes a Chatflow for the Chats screen.
type ChatflowInfo struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Desc     string          `json:"desc,omitempty"`
	Inputs   []flow.Input    `json:"inputs"`
	Starters []string        `json:"starters,omitempty"`
	Setup    []string        `json:"setup"` // setup step titles
	Tools    []flow.ChatTool `json:"tools"`
	Vars     []string        `json:"vars,omitempty"`    // names only
	Secrets  []string        `json:"secrets,omitempty"` // names only
	Issues   []string        `json:"issues,omitempty"`  // errors that keep it from chatting
}

func chatKey(flowID, id string) string { return flowID + "/" + id }

func (h *Hub) chatStore() (*chat.Store, string, error) {
	root, err := h.need()
	if err != nil {
		return nil, "", err
	}
	return chat.New(root), root, nil
}

func (h *Hub) chatflow(root, id string) (*flow.Flow, error) {
	f, err := flow.Load(root, id)
	if err != nil {
		return nil, err
	}
	if !f.IsChatflow() {
		return nil, fmt.Errorf("%s is not a Chatflow (it has no Chat step)", f.Name)
	}
	return f, nil
}

// Chatflows lists the project's Chatflows.
func (h *Hub) Chatflows() ([]ChatflowInfo, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	list, err := flow.List(root)
	if err != nil {
		return nil, err
	}
	out := []ChatflowInfo{}
	for _, s := range list {
		if !s.Chat {
			continue
		}
		f, err := flow.Load(root, s.ID)
		if err != nil {
			continue
		}
		out = append(out, h.chatflowInfo(f))
	}
	return out, nil
}

func (h *Hub) chatflowInfo(f *flow.Flow) ChatflowInfo {
	c := f.ChatNode()
	info := ChatflowInfo{ID: f.ID, Name: f.Name, Desc: f.Description, Inputs: f.Inputs, Tools: f.ChatTools()}
	if info.Inputs == nil {
		info.Inputs = []flow.Input{}
	}
	if info.Tools == nil {
		info.Tools = []flow.ChatTool{}
	}
	for _, s := range c.Starters {
		if s = strings.TrimSpace(s); s != "" {
			info.Starters = append(info.Starters, s)
		}
	}
	info.Setup = []string{}
	for _, id := range f.ChatSetup() {
		if n := f.Node(id); n != nil && n.Type != "start" {
			info.Setup = append(info.Setup, firstNonEmpty(n.Title, n.ID))
		}
	}
	for _, v := range f.Vars {
		info.Vars = append(info.Vars, v.Name)
	}
	for _, s := range f.Secrets {
		info.Secrets = append(info.Secrets, s.Name)
	}
	for _, is := range h.ValidateFlow(*f) {
		if is.Level == "error" {
			info.Issues = append(info.Issues, is.Message)
		}
	}
	return info
}

// ChatSessions lists a Chatflow's sessions ("" lists every Chatflow's).
func (h *Hub) ChatSessions(flowID string) ([]chat.Session, error) {
	st, _, err := h.chatStore()
	if err != nil {
		return nil, err
	}
	return st.List(flowID)
}

// ChatSession reads one session.
func (h *Hub) ChatSession(flowID, id string) (chat.Session, error) {
	st, _, err := h.chatStore()
	if err != nil {
		return chat.Session{}, err
	}
	return st.Get(flowID, id)
}

// ChatMessages reads a session's conversation.
func (h *Hub) ChatMessages(flowID, id string) ([]chat.Message, error) {
	st, _, err := h.chatStore()
	if err != nil {
		return nil, err
	}
	return st.Messages(flowID, id)
}

// ChatData reads one of a session's data items.
func (h *Hub) ChatData(flowID, id, name string) (string, error) {
	st, _, err := h.chatStore()
	if err != nil {
		return "", err
	}
	return st.GetData(flowID, id, name)
}

// RenameChatSession gives a session a title.
func (h *Hub) RenameChatSession(flowID, id, title string) (chat.Session, error) {
	st, _, err := h.chatStore()
	if err != nil {
		return chat.Session{}, err
	}
	title = chat.Title(title)
	if title == "" {
		return chat.Session{}, errors.New("give the session a title")
	}
	s, err := st.Update(flowID, id, func(s *chat.Session) { s.Title = title })
	h.chatPublish(s, "Renamed")
	return s, err
}

// DeleteChatSession removes a session and its files.
func (h *Hub) DeleteChatSession(flowID, id string) error {
	st, _, err := h.chatStore()
	if err != nil {
		return err
	}
	h.chats.mu.Lock()
	if cancel := h.chats.answers[chatKey(flowID, id)]; cancel != nil {
		cancel()
	}
	delete(h.chats.env, chatKey(flowID, id))
	delete(h.chats.inputs, chatKey(flowID, id))
	h.chats.mu.Unlock()
	return st.Delete(flowID, id)
}

// chatPublish tells the UI a session changed.
func (h *Hub) chatPublish(s chat.Session, summary string) {
	h.Bus.Publish(bus.Event{Type: "chat.session", Summary: firstNonEmpty(summary, s.Title),
		Data: map[string]any{"flow": s.Flow, "session": s.ID, "status": s.Status}})
}

// chatEnv resolves a Chatflow's variables (a variable in the app's
// environment wins) and secrets (session, environment, keychain, or asked
// for once).
func (h *Hub) chatEnv(ctx context.Context, f *flow.Flow) (map[string]string, []string, error) {
	env := map[string]string{}
	for _, v := range f.Vars {
		if x, ok := lookupEnv(v.Name); ok {
			env[v.Name] = x
		} else {
			env[v.Name] = v.Value
		}
	}
	if len(f.Secrets) == 0 {
		return env, nil, nil
	}
	var ss []extension.Secret
	var names []string
	for _, s := range f.Secrets {
		ss = append(ss, extension.Secret{Name: s.Name, Desc: firstNonEmpty(s.Desc, "Needed by the Chatflow "+f.Name), Source: "auto"})
		names = append(names, s.Name)
	}
	vals, err := h.ResolveSecrets(ctx, extension.Extension{Name: f.Name, Desc: "The Chatflow " + f.Name + " needs these to start a session", Secrets: ss})
	if err != nil {
		return nil, nil, fmt.Errorf("the Chatflow's secrets: %v", err)
	}
	for k, v := range vals {
		env[k] = v
	}
	return env, names, nil
}

// setupCopy is the Chatflow with only its Inputs and setup steps: what a
// session runs once (it has no Chat step, so it runs as a flow).
func setupCopy(f *flow.Flow) *flow.Flow {
	keep := map[string]bool{}
	for _, id := range f.ChatSetup() {
		keep[id] = true
	}
	keep["start"] = true
	g := *f
	g.Name = f.Name + " · chat setup"
	g.Triggers = nil
	g.Nodes = nil
	for _, n := range f.Nodes {
		if keep[n.ID] {
			g.Nodes = append(g.Nodes, n)
		}
	}
	g.Edges = nil
	for _, e := range f.Edges {
		if keep[e.From] && keep[e.To] {
			g.Edges = append(g.Edges, e)
		}
	}
	return &g
}

func randomPart() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// StartChatSession starts a session of a Chatflow with its inputs: the
// setup steps run in the background (the session's status says when it
// is ready).
func (h *Hub) StartChatSession(flowID string, inputs map[string]string) (chat.Session, error) {
	st, root, err := h.chatStore()
	if err != nil {
		return chat.Session{}, err
	}
	f, err := h.chatflow(root, flowID)
	if err != nil {
		return chat.Session{}, err
	}
	o := FlowRunOptions{Inputs: inputs, Review: "none"}
	if err := h.prepareRun(root, f, &o); err != nil {
		return chat.Session{}, err
	}
	env, envSec, err := h.chatEnv(h.ctx, f)
	if err != nil {
		return chat.Session{}, err
	}
	shown := map[string]string{} // non-secret inputs only
	for _, in := range f.Inputs {
		if in.Type != "secret" {
			shown[in.Name] = o.Inputs[in.Name]
		}
	}
	h.mu.RLock()
	agentName, model := h.agentName, h.model
	h.mu.RUnlock()
	if c := f.ChatNode(); c.Model != "" {
		model = c.Model
	}
	sess, err := st.Create(chat.Session{ID: chat.NewID(time.Now(), randomPart()), Flow: f.ID, FlowName: f.Name, Status: "setup",
		Inputs: shown, Agent: agentName, Model: model})
	if err != nil {
		return chat.Session{}, err
	}
	key := chatKey(f.ID, sess.ID)
	h.chats.mu.Lock()
	if h.chats.env == nil {
		h.chats.env, h.chats.envSec, h.chats.inputs, h.chats.answers = map[string]map[string]string{}, map[string][]string{}, map[string]map[string]string{}, map[string]context.CancelFunc{}
	}
	h.chats.env[key], h.chats.envSec[key], h.chats.inputs[key] = env, envSec, o.Secrets
	h.chats.mu.Unlock()
	for _, k := range envSec {
		h.Store.Redactor().Add(env[k])
	}
	h.chatPublish(sess, "Setting up")
	go h.runChatSetup(root, f, sess, o, env, envSec)
	return sess, nil
}

// runChatSetup runs a session's setup steps and saves what they gave.
func (h *Hub) runChatSetup(root string, f *flow.Flow, sess chat.Session, o FlowRunOptions, env map[string]string, envSec []string) {
	st := chat.New(root)
	fail := func(err error) {
		s, _ := st.Update(sess.Flow, sess.ID, func(s *chat.Session) { s.Status, s.Error = "failed", h.Store.Redactor().String(err.Error()) })
		h.chatPublish(s, "Setup failed")
	}
	g := setupCopy(f)
	if len(g.Nodes) <= 1 { // nothing to set up
		s, _ := st.Update(sess.Flow, sess.ID, func(s *chat.Session) { s.Status, s.SetupAt = "ready", time.Now() })
		h.chatPublish(s, "Ready")
		return
	}
	o.Env, o.EnvSecret = env, envSec
	run, err := h.startFlow(root, g, o, nil)
	if err != nil {
		fail(err)
		return
	}
	h.mu.RLock()
	fr := h.flowRuns[run.ID]
	h.mu.RUnlock()
	if fr != nil {
		<-fr.done
	}
	res, err := h.FlowRun(run.ID)
	if err != nil {
		fail(err)
		return
	}
	if res.Status != "done" {
		fail(fmt.Errorf("the setup did not finish (%s): %s", res.Status, res.Error))
		return
	}
	for _, s := range res.Steps {
		if s.ID == "start" || s.Status != "done" {
			continue
		}
		content := s.Output
		if s.OutputFile != "" {
			if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(s.OutputFile))); err == nil {
				content = string(b)
			}
		}
		content = h.Store.Redactor().String(content)
		name := "setup-" + s.ID + "." + contentExt(content)
		_, _ = st.PutData(sess.Flow, sess.ID, chat.Data{Name: name, Step: s.ID, Title: firstNonEmpty(s.Title, s.ID), Setup: true}, content)
	}
	upd, _ := st.Update(sess.Flow, sess.ID, func(x *chat.Session) {
		x.Status, x.SetupAt, x.Error = "ready", time.Now(), ""
		x.CostUSD += res.Usage.CostUSD
		x.Tokens += res.Usage.InputTokens + res.Usage.OutputTokens
	})
	h.chatPublish(upd, "Ready")
}

// chatDataText is the session's data for the AI's first message.
func chatDataText(st *chat.Store, s chat.Session) string {
	if len(s.Data) == 0 {
		return ""
	}
	var b strings.Builder
	total := 0
	for _, d := range s.Data {
		c, err := st.GetData(s.Flow, s.ID, d.Name)
		if err != nil {
			continue
		}
		c = clipText(c, 20000)
		if total+len(c) > 80000 {
			fmt.Fprintf(&b, "\n### %s\n(left out: the data is too large to send; ask for a summary step)\n", firstNonEmpty(d.Title, d.Name))
			continue
		}
		total += len(c)
		fmt.Fprintf(&b, "\n### %s (%s)\n```\n%s\n```\n", firstNonEmpty(d.Title, d.Name), d.Name, c)
	}
	return b.String()
}

// chatPrompt is the first prompt of a session's AI conversation: the
// Chatflow's instructions, the session's inputs and data, and (when the
// conversation is started again) what was said so far.
func chatPrompt(f *flow.Flow, st *chat.Store, s chat.Session, history []chat.Message, question string) string {
	c := f.ChatNode()
	var b strings.Builder
	fmt.Fprintf(&b, "You are the assistant of the Chatflow %q in SpecWright. Answer the person's questions about the session's data below, in Markdown. Be accurate: use the data, say when it does not answer a question, and do not make numbers up. Do not edit files or run commands.\n", f.Name)
	if t := strings.TrimSpace(c.Instructions); t != "" {
		b.WriteString("\nINSTRUCTIONS:\n" + t + "\n")
	}
	if len(s.Inputs) > 0 {
		b.WriteString("\nSESSION INPUTS:\n")
		for k, v := range s.Inputs {
			fmt.Fprintf(&b, "- %s: %s\n", k, clipText(v, 500))
		}
	}
	if d := chatDataText(st, s); d != "" {
		b.WriteString("\nSESSION DATA (from the setup steps; it stays the same for the session):" + d)
	}
	if len(history) > 0 {
		b.WriteString("\nTHE CONVERSATION SO FAR:\n")
		for _, m := range history {
			if m.Role == "user" || m.Role == "assistant" {
				fmt.Fprintf(&b, "\n%s: %s\n", map[string]string{"user": "Person", "assistant": "You"}[m.Role], clipText(m.Text, 4000))
			}
		}
	}
	b.WriteString("\nQUESTION:\n" + question + "\n")
	return b.String()
}

// SendChatMessage asks the session's AI a question; the answer streams
// as "chat.text" events and is saved when it ends ("chat.message").
func (h *Hub) SendChatMessage(flowID, id, text string) (chat.Message, error) {
	st, root, err := h.chatStore()
	if err != nil {
		return chat.Message{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return chat.Message{}, errors.New("type a question")
	}
	f, err := h.chatflow(root, flowID)
	if err != nil {
		return chat.Message{}, err
	}
	s, err := st.Get(flowID, id)
	if err != nil {
		return chat.Message{}, err
	}
	switch s.Status {
	case "setup":
		return chat.Message{}, errors.New("the session is still setting up")
	case "failed":
		return chat.Message{}, fmt.Errorf("the session's setup failed: %s", s.Error)
	}
	if lim := f.ChatNode().CostLimit; lim > 0 && s.CostUSD >= lim {
		return chat.Message{}, fmt.Errorf("the session reached its cost limit ($%.2f): start a new session", lim)
	}
	key := chatKey(flowID, id)
	h.chats.mu.Lock()
	if h.chats.answers == nil {
		h.chats.answers = map[string]context.CancelFunc{}
	}
	if h.chats.answers[key] != nil {
		h.chats.mu.Unlock()
		return chat.Message{}, errors.New("the previous question is still being answered")
	}
	ctx, cancel := context.WithCancel(h.ctx)
	h.chats.answers[key] = cancel
	h.chats.mu.Unlock()
	history, _ := st.Messages(flowID, id)
	q, err := st.Append(flowID, id, chat.Message{Role: "user", Text: text})
	if err != nil {
		cancel()
		h.chats.mu.Lock()
		delete(h.chats.answers, key)
		h.chats.mu.Unlock()
		return chat.Message{}, err
	}
	s, _ = st.Get(flowID, id)
	h.chatPublish(s, "Question")
	go h.answerChat(ctx, cancel, root, f, s, history, text)
	return q, nil
}

// StopChatMessage stops the answer being written.
func (h *Hub) StopChatMessage(flowID, id string) error {
	h.chats.mu.Lock()
	cancel := h.chats.answers[chatKey(flowID, id)]
	h.chats.mu.Unlock()
	if cancel == nil {
		return errors.New("nothing is being answered")
	}
	cancel()
	return nil
}

func (h *Hub) answerChat(ctx context.Context, cancel context.CancelFunc, root string, f *flow.Flow, s chat.Session, history []chat.Message, question string) {
	st := chat.New(root)
	key := chatKey(s.Flow, s.ID)
	defer func() {
		cancel()
		h.chats.mu.Lock()
		delete(h.chats.answers, key)
		h.chats.mu.Unlock()
	}()
	h.mu.RLock()
	agentName := h.agentName
	h.mu.RUnlock()
	drv, err := h.Agents.Get(agentName)
	if err == nil && s.Agent != "" && s.Agent != agentName {
		s.AgentSession = "" // another agent cannot continue that conversation
	}
	req := agent.Request{Purpose: "chat", Step: "chat", Dir: root, Model: s.Model, Env: h.childEnv(), ReadOnly: true}
	if s.AgentSession != "" {
		req.SessionID, req.Resume, req.Prompt = s.AgentSession, true, question
	} else {
		req.SessionID, req.Prompt = workflow.NewUUID(), chatPrompt(f, st, s, history, question)
	}
	var text strings.Builder
	var mu sync.Mutex
	tokens := 0
	emit := func(ev agent.Event) {
		switch ev.Type {
		case "model.text":
			t, _ := ev.Data["text"].(string)
			if t == "" {
				t = ev.Summary
			}
			mu.Lock()
			if text.Len() > 0 {
				text.WriteString("\n\n")
			}
			text.WriteString(t)
			mu.Unlock()
			h.Bus.Publish(bus.Event{Type: "chat.text", Summary: clipLine(h.Store.Redactor().String(t), 120),
				Data: map[string]any{"flow": s.Flow, "session": s.ID, "text": h.Store.Redactor().String(t)}})
		case "model.request":
			tokens += toInt(ev.Data["input_tokens"])
		case "model.response":
			tokens += toInt(ev.Data["output_tokens"])
		}
	}
	var res agent.Result
	if err == nil {
		res, err = drv.Run(ctx, req, emit)
	}
	answer := strings.TrimSpace(res.Text)
	if answer == "" {
		answer = strings.TrimSpace(text.String())
	}
	m := chat.Message{Role: "assistant", Text: h.Store.Redactor().String(answer)}
	switch {
	case ctx.Err() != nil:
		m.Error = "Stopped."
	case err != nil:
		m.Error = h.Store.Redactor().String(err.Error())
	case answer == "":
		m.Error = "The AI gave no answer."
	}
	_, _ = st.Append(s.Flow, s.ID, m)
	upd, _ := st.Update(s.Flow, s.ID, func(x *chat.Session) {
		if res.SessionID != "" && err == nil {
			x.AgentSession, x.Agent = res.SessionID, agentName
		}
		x.CostUSD += res.CostUSD
		x.Tokens += tokens
	})
	h.Bus.Publish(bus.Event{Type: "chat.message", Summary: clipLine(m.Text, 120), Data: map[string]any{"flow": s.Flow, "session": s.ID}})
	h.chatPublish(upd, "Answered")
}
