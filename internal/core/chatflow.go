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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	// Asks: the tools that ask the person before they run
	Asks      []string `json:"asks,omitempty"`
	CostLimit float64  `json:"costLimit,omitempty"`
	// Instructions: the Chat step's own guidance (Context panel), and Model
	// when the step sets one
	Instructions string `json:"instructions,omitempty"`
	Model        string `json:"model,omitempty"`
}

func chatKey(flowID, id string) string { return flowID + "/" + id }

// ready makes the maps (call with mu held).
func (c *chatState) ready() {
	if c.env == nil {
		c.env, c.envSec, c.inputs, c.answers = map[string]map[string]string{}, map[string][]string{}, map[string]map[string]string{}, map[string]context.CancelFunc{}
	}
}

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
	info.CostLimit = c.CostLimit
	info.Instructions, info.Model = strings.TrimSpace(c.Instructions), c.Model
	for _, t := range info.Tools {
		if !h.chatToolReadOnly(f, t) {
			info.Asks = append(info.Asks, t.ID)
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

// ChatCatalog is what a session's index holds: its tables (to query) and
// documents (to search). It indexes what is new first.
func (h *Hub) ChatCatalog(flowID, id string) ([]chat.Entry, error) {
	st, _, err := h.chatStore()
	if err != nil {
		return nil, err
	}
	s, err := st.Get(flowID, id)
	if err != nil {
		return nil, err
	}
	d := h.openChatData(st, s)
	defer d.close()
	if d.x == nil {
		return nil, d.err
	}
	out := []chat.Entry{}
	for _, it := range d.s.Data {
		if e, ok := d.cat[it.Name]; ok {
			out = append(out, e)
		}
	}
	return out, nil
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
	g.Name = f.Name // the run says it is the chat's setup (FlowRunOptions.Chat)
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
	_, root, err := h.chatStore()
	if err != nil {
		return chat.Session{}, err
	}
	f, err := h.chatflow(root, flowID)
	if err != nil {
		return chat.Session{}, err
	}
	sess, _, err := h.startChat(root, f, FlowRunOptions{Inputs: inputs, Review: "none"})
	return sess, err
}

// startChat starts a chat with f: its inputs checked, its environment
// resolved, a session made, and its setup started as a flow run (seen in
// Run flows) that hands over to the session when it reaches the Chat step.
func (h *Hub) startChat(root string, f *flow.Flow, o FlowRunOptions) (chat.Session, FlowRunState, error) {
	st := chat.New(root)
	if err := h.prepareRun(root, f, &o); err != nil {
		return chat.Session{}, FlowRunState{}, err
	}
	env, envSec, err := h.chatEnv(h.ctx, f)
	if err != nil {
		return chat.Session{}, FlowRunState{}, err
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
		return chat.Session{}, FlowRunState{}, err
	}
	key := chatKey(f.ID, sess.ID)
	h.chats.mu.Lock()
	h.chats.ready()
	h.chats.env[key], h.chats.envSec[key], h.chats.inputs[key] = env, envSec, o.Secrets
	h.chats.mu.Unlock()
	for _, k := range envSec {
		h.Store.Redactor().Add(env[k])
	}
	run, err := h.chatSetupRun(root, f, sess, o, env, envSec, true)
	if err != nil {
		return chat.Session{}, run, err
	}
	sess, _ = st.Get(f.ID, sess.ID)
	return sess, run, nil
}

// runChatSetupOnly runs a Chatflow's setup steps alone, without a chat:
// to try them (Run flows › Run setup only).
func (h *Hub) runChatSetupOnly(root string, f *flow.Flow, o FlowRunOptions) (FlowRunState, error) {
	if err := h.prepareRun(root, f, &o); err != nil {
		return FlowRunState{}, err
	}
	env, envSec, err := h.chatEnv(h.ctx, f)
	if err != nil {
		return FlowRunState{}, err
	}
	o.Env, o.EnvSecret = env, envSec
	o.Chat = &FlowRunChat{Role: "setup-only"}
	return h.startFlow(root, setupCopy(f), o, nil)
}

// runChatSetup runs a session's setup steps and saves what they gave.
// chatSetupRun starts a session's setup as a flow run (seen in Run flows,
// linked from the session) and finishes it in the background: what the
// steps gave becomes the session's data. first: the session's first setup,
// whose failure removes the session (the failed run shows what went
// wrong); a refresh that fails keeps the session and the data it had.
func (h *Hub) chatSetupRun(root string, f *flow.Flow, sess chat.Session, o FlowRunOptions, env map[string]string, envSec []string, first bool) (FlowRunState, error) {
	st := chat.New(root)
	o.Env, o.EnvSecret = env, envSec
	o.Chat = &FlowRunChat{Role: "setup", Session: sess.ID}
	run, err := h.startFlow(root, setupCopy(f), o, nil)
	if err != nil {
		h.chatSetupFailed(st, sess, "", err, first)
		return run, err
	}
	s, _ := st.Update(sess.Flow, sess.ID, func(x *chat.Session) { x.SetupRun, x.Status, x.Error = run.ID, "setup", "" })
	h.chatPublish(s, "Setting up")
	go h.chatSetupWait(root, sess, run.ID, first)
	return run, nil
}

// chatSetupFailed: a first setup that failed removes its session; a
// refresh that failed leaves the session as it was, saying why.
func (h *Hub) chatSetupFailed(st *chat.Store, sess chat.Session, runID string, err error, first bool) {
	msg := h.Store.Redactor().String(err.Error())
	if first {
		_ = st.Delete(sess.Flow, sess.ID)
		h.chats.mu.Lock()
		h.chats.ready()
		key := chatKey(sess.Flow, sess.ID)
		delete(h.chats.env, key)
		delete(h.chats.envSec, key)
		delete(h.chats.inputs, key)
		h.chats.mu.Unlock()
		h.Bus.Publish(bus.Event{Type: "chat.session", Summary: "Setup failed",
			Data: map[string]any{"flow": sess.Flow, "session": sess.ID, "status": "removed", "run": runID, "error": msg}})
		return
	}
	s, _ := st.Update(sess.Flow, sess.ID, func(x *chat.Session) {
		x.Status, x.Error = "ready", "The refresh did not finish: "+msg+". The session keeps the data it had."
	})
	h.chatPublish(s, "Refresh failed")
}

// chatSetupWait waits for a setup run and hands its results to the session.
func (h *Hub) chatSetupWait(root string, sess chat.Session, runID string, first bool) {
	st := chat.New(root)
	h.mu.RLock()
	fr := h.flowRuns[runID]
	h.mu.RUnlock()
	if fr != nil {
		<-fr.done
	}
	res, err := h.FlowRun(runID)
	if err == nil && res.Status != "done" {
		err = fmt.Errorf("the setup did not finish (%s): %s", res.Status, firstNonEmpty(res.Error, "see the run in Run flows"))
	}
	if err != nil {
		h.chatSetupFailed(st, sess, runID, err, first)
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
		x.Refreshed = !first && x.AgentSession != ""
		x.CostUSD += res.Usage.CostUSD
		x.Tokens += res.Usage.InputTokens + res.Usage.OutputTokens
		u := chat.Usage{}
		if p := usageOf(res.Usage); p != nil {
			u = *p
		}
		if !res.Ended.IsZero() {
			u.DurationMS = res.Ended.Sub(res.Started).Milliseconds()
		}
		x.SetupUsage.Add(u)
	})
	h.chatPublish(upd, map[bool]string{true: "Ready", false: "Refreshed"}[first])
}

// chatPrompt is the first prompt of a session's AI conversation: the
// Chatflow's instructions, the session's inputs and data, and (when the
// conversation is started again) what was said so far.
func chatPrompt(f *flow.Flow, data *chatData, s chat.Session, history []chat.Message, question, tools string) string {
	c := f.ChatNode()
	var b strings.Builder
	fmt.Fprintf(&b, "You are the assistant of the Chatflow %q in SpecWright. Answer the person's questions about the session's data below, in Markdown. Be accurate: use the data (and what you run), say when it does not answer a question, and do not make numbers up. Do not edit files or run commands yourself.\n", f.Name)
	if t := strings.TrimSpace(c.Instructions); t != "" {
		b.WriteString("\nINSTRUCTIONS:\n" + t + "\n")
	}
	if len(s.Inputs) > 0 {
		b.WriteString("\nSESSION INPUTS:\n")
		for k, v := range s.Inputs {
			fmt.Fprintf(&b, "- %s: %s\n", k, clipText(v, 500))
		}
	}
	if d := data.text(false); d != "" {
		b.WriteString("\nSESSION DATA (from the setup steps; it stays the same for the session):" + d)
	}
	b.WriteString(tools)
	b.WriteString(untrustedRule + "\n")
	b.WriteString("\nEnd every answer with one last line: `Follow-ups: <question> | <question> | <question>`: two or three short questions the person may want to ask next.\n")
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
func (h *Hub) SendChatMessage(flowID, id, text string, files []string) (chat.Message, error) {
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
	h.chats.ready()
	if h.chats.answers[key] != nil {
		h.chats.mu.Unlock()
		return chat.Message{}, errors.New("the previous question is still being answered")
	}
	ctx, cancel := context.WithCancel(h.ctx)
	h.chats.answers[key] = cancel
	h.chats.mu.Unlock()
	history, _ := st.Messages(flowID, id)
	q, err := st.Append(flowID, id, chat.Message{Role: "user", Text: text, Files: files})
	if err != nil {
		cancel()
		h.chats.mu.Lock()
		delete(h.chats.answers, key)
		h.chats.mu.Unlock()
		return chat.Message{}, err
	}
	s, _ = st.Get(flowID, id)
	h.chatPublish(s, "Question")
	go h.answerChat(ctx, cancel, root, f, s, history, text, files)
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

func (h *Hub) answerChat(ctx context.Context, cancel context.CancelFunc, root string, f *flow.Flow, s chat.Session, history []chat.Message, question string, files []string) {
	st := chat.New(root)
	data := h.openChatData(st, s)
	defer data.close()
	// what was attached, and the passages of large documents that match the question
	question += data.attachments(files) + data.passages(question)
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
	soFarLast := s.AgentSoFar // the conversation's usage so far, as last reported
	if s.AgentSession == "" {
		soFarLast = chat.Usage{}
	}
	req := agent.Request{Purpose: "chat", Step: "chat", Dir: root, Model: s.Model, Env: h.childEnv(), ReadOnly: true}
	first := chatPrompt(f, data, s, history, question, h.chatToolsText(f, data.toolsText()))
	if s.Refreshed && s.AgentSession != "" { // the setup ran again: the AI gets the new data
		question = "THE SESSION DATA WAS REFRESHED (it replaces what you had):" + data.text(true) + "\n\n" + question
	}
	if s.AgentSession != "" {
		req.SessionID, req.Resume, req.Prompt = s.AgentSession, true, question
	} else {
		req.SessionID, req.Prompt = workflow.NewUUID(), first
	}
	var text strings.Builder // this round's text, as it streams
	var mu sync.Mutex
	t0 := time.Now()
	var used chat.Usage // the answer's: every round, every tool
	model := s.Model
	meter := &chatMeter{} // this round's
	emit := func(ev agent.Event) {
		meter.event(ev)
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
			// a tool request is not shown as text (a chip shows the run)
			if _, shown := parseChatCalls(t); strings.TrimSpace(shown) != "" {
				h.Bus.Publish(bus.Event{Type: "chat.text", Summary: clipLine(h.Store.Redactor().String(shown), 120),
					Data: map[string]any{"flow": s.Flow, "session": s.ID, "text": h.Store.Redactor().String(shown)}})
			}
		}
	}
	var res agent.Result
	var steps []chat.StepRun
	var answer string
	branchRuns, dataRuns := 0, 0
	for round := 0; err == nil; round++ {
		text.Reset()
		meter = &chatMeter{}
		res, err = drv.Run(ctx, req, emit)
		own, reported := ownPart(meter.usage(res), res, soFarLast)
		used.Add(own)
		soFarLast = reported
		model = firstNonEmpty(meter.modelName(res), model)
		reply := strings.TrimSpace(res.Text)
		if reply == "" {
			reply = strings.TrimSpace(text.String())
		}
		calls, shown := parseChatCalls(reply)
		answer = strings.TrimSpace(answer + "\n\n" + shown)
		if err != nil || len(calls) == 0 || ctx.Err() != nil {
			break
		}
		// run what it asked for (the built-in data tools at once, the
		// branches as flows), and give the results back
		var results strings.Builder
		ran := 0
		for _, c := range calls {
			isData := isDataTool(c.Tool)
			if (isData && dataRuns >= maxDataRuns) || (!isData && branchRuns >= maxChatRuns) {
				continue
			}
			var run chat.StepRun
			var out string
			n := s.Messages + len(steps) + 1
			if isData {
				dataRuns++
				run, out = data.runDataTool(ctx, c, n)
			} else {
				branchRuns++
				run, out = h.runChatTool(ctx, root, f, s, c, n)
			}
			steps = append(steps, run)
			ran++
			if isData {
				used.DataRuns++
			} else {
				used.ToolRuns++
			}
			if run.Error != "" {
				used.ToolErrors++
			}
			if run.Usage != nil { // the AI its steps used
				u := *run.Usage
				u.Prompts = 0
				used.Add(u)
			}
			if run.Error != "" {
				fmt.Fprintf(&results, "\n### %s: it did not run\n%s\n", run.Title, run.Error)
			} else {
				fmt.Fprintf(&results, "\n### %s (result)\n```\n%s\n```\n", run.Title, clipKeep(out, 15000))
				if !isData {
					results.WriteString(data.tableNote(run, out, 15000))
				}
			}
		}
		if ran == 0 {
			answer = strings.TrimSpace(answer + "\n\n_(It wanted to run more, but a question may run at most " + fmt.Sprint(maxChatRuns) + " tools and " + fmt.Sprint(maxDataRuns) + " data queries or searches.)_")
			break
		}
		next := "RESULTS OF WHAT YOU RAN:" + results.String() + "\nNow answer the question from these results (or run another tool if you really need to)."
		if res.SessionID != "" { // the same conversation goes on
			req.SessionID, req.Resume, req.Prompt = res.SessionID, true, next
		} else { // the CLI cannot continue it: everything again
			req.SessionID, req.Resume, req.Prompt = workflow.NewUUID(), false, first+"\n"+next
		}
	}
	used.DurationMS = time.Since(t0).Milliseconds()
	answer, followups := splitFollowups(answer)
	m := chat.Message{Role: "assistant", Text: h.Store.Redactor().String(answer), Steps: steps, Followups: followups,
		Usage: &used, Agent: agentName, Model: model}
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
			x.Refreshed = false
			x.AgentSoFar = soFarLast
		}
		x.CostUSD += used.CostUSD
		x.Tokens += used.Tokens()
		x.Usage.Add(used)
	})
	h.Bus.Publish(bus.Event{Type: "chat.message", Summary: clipLine(m.Text, 120), Data: map[string]any{"flow": s.Flow, "session": s.ID}})
	h.chatPublish(upd, "Answered")
}

var followupRE = regexp.MustCompile(`(?im)^[ \t*_` + "`" + `]*follow-?ups?\s*:[ \t]*(.+?)[ \t*_` + "`" + `]*$`)

// splitFollowups takes the "Follow-ups: a | b | c" line off an answer.
func splitFollowups(answer string) (string, []string) {
	ms := followupRE.FindAllStringSubmatchIndex(answer, -1)
	if len(ms) == 0 {
		return answer, nil
	}
	m := ms[len(ms)-1]
	var out []string
	for _, q := range strings.Split(answer[m[2]:m[3]], "|") {
		if q = strings.Trim(strings.TrimSpace(q), "`*_\"'"); q != "" && len(out) < 3 {
			out = append(out, q)
		}
	}
	return strings.TrimSpace(answer[:m[0]] + answer[m[1]:]), out
}

// AttachChatFile keeps a file the person attached to a session: text is
// session data the AI reads (the name to pass with the message); other
// files are kept in the session folder and named by their path.
func (h *Hub) AttachChatFile(flowID, id, name, base64Content string) (chat.Data, error) {
	st, _, err := h.chatStore()
	if err != nil {
		return chat.Data{}, err
	}
	b, err := base64.StdEncoding.DecodeString(base64Content)
	if err != nil {
		return chat.Data{}, fmt.Errorf("the file could not be read: %v", err)
	}
	if len(b) > 10<<20 {
		return chat.Data{}, errors.New("attach files up to 10 MB")
	}
	safe := strings.Trim(regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(filepath.Base(name), "-"), "-.")
	if safe == "" {
		safe = "file"
	}
	path, err := st.PutFile(flowID, id, safe, b)
	if err != nil {
		return chat.Data{}, err
	}
	content := string(b)
	if !utf8.Valid(b) || strings.ContainsRune(content, 0) || len(b) > 2<<20 {
		content = fmt.Sprintf("(a %d-byte file, %s: not text; it is saved at %s)", len(b), safe, path)
	}
	return st.PutData(flowID, id, chat.Data{Name: "file-" + safe, Title: name, Step: "attachment"}, content)
}

// RefreshChatSession runs the session's setup again (new data, same
// conversation: the next message gives the AI the new data).
func (h *Hub) RefreshChatSession(flowID, id string) (chat.Session, error) {
	st, root, err := h.chatStore()
	if err != nil {
		return chat.Session{}, err
	}
	f, err := h.chatflow(root, flowID)
	if err != nil {
		return chat.Session{}, err
	}
	s, err := st.Get(flowID, id)
	if err != nil {
		return chat.Session{}, err
	}
	if s.Status == "setup" {
		return s, errors.New("the session is already setting up")
	}
	key := chatKey(flowID, id)
	h.chats.mu.Lock()
	h.chats.ready()
	env, envSec, secretInputs := h.chats.env[key], h.chats.envSec[key], h.chats.inputs[key]
	h.chats.mu.Unlock()
	if env == nil {
		if env, envSec, err = h.chatEnv(h.ctx, f); err != nil {
			return s, err
		}
		h.chats.mu.Lock()
		h.chats.env[key], h.chats.envSec[key] = env, envSec
		h.chats.mu.Unlock()
	}
	inputs := map[string]string{}
	for k, v := range s.Inputs {
		inputs[k] = v
	}
	for k := range secretInputs {
		inputs[k] = flow.SecretMask
	}
	if _, err := h.chatSetupRun(root, f, s, FlowRunOptions{Inputs: inputs, Review: "none", Secrets: secretInputs}, env, envSec, false); err != nil {
		return s, err
	}
	s, _ = st.Get(flowID, id)
	return s, nil
}

// ArchiveChatSession puts a session away (or back): it stays on disk,
// listed only when archived sessions are shown.
func (h *Hub) ArchiveChatSession(flowID, id string, archived bool) (chat.Session, error) {
	st, _, err := h.chatStore()
	if err != nil {
		return chat.Session{}, err
	}
	s, err := st.Update(flowID, id, func(s *chat.Session) { s.Archived = archived })
	h.chatPublish(s, map[bool]string{true: "Archived", false: "Restored"}[archived])
	return s, err
}

// ExportChatSession writes a session as Markdown (out/chats/<title>-<date>.md:
// the questions, answers, the steps run with their results, attachments)
// and returns its path in the project.
func (h *Hub) ExportChatSession(flowID, id string) (string, error) {
	st, root, err := h.chatStore()
	if err != nil {
		return "", err
	}
	s, err := st.Get(flowID, id)
	if err != nil {
		return "", err
	}
	ms, err := st.Messages(flowID, id)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n_%s · session %s · started %s_\n", firstNonEmpty(s.Title, "Chat"), s.FlowName, s.ID, s.Created.Format("2006-01-02 15:04"))
	if len(s.Inputs) > 0 {
		b.WriteString("\n**Inputs:** ")
		var parts []string
		for k, v := range s.Inputs {
			parts = append(parts, fmt.Sprintf("%s = %s", k, v))
		}
		sort.Strings(parts)
		b.WriteString(strings.Join(parts, " · ") + "\n")
	}
	for _, m := range ms {
		switch m.Role {
		case "user":
			fmt.Fprintf(&b, "\n---\n\n### 🧑 %s\n", m.Text)
			for _, f := range m.Files {
				fmt.Fprintf(&b, "\n📎 %s\n", strings.TrimPrefix(f, "file-"))
			}
		case "assistant":
			b.WriteString("\n")
			for _, r := range m.Steps {
				if r.Error != "" {
					fmt.Fprintf(&b, "> ▸ %s did not run: %s\n\n", r.Title, r.Error)
					continue
				}
				fmt.Fprintf(&b, "> ▸ ran **%s** (%.1f s)\n\n", r.Title, float64(r.DurationMS)/1000)
				if r.Data != "" {
					if c, err := st.GetData(flowID, id, r.Data); err == nil {
						b.WriteString(resultMarkdown(c) + "\n\n")
					}
				}
				for _, f := range r.Files {
					fmt.Fprintf(&b, "Saved: `%s`\n\n", f)
				}
			}
			b.WriteString(m.Text + "\n")
			if m.Error != "" {
				fmt.Fprintf(&b, "\n_(%s)_\n", m.Error)
			}
		}
	}
	rel := filepath.ToSlash(filepath.Join("out", "chats", slugify(firstNonEmpty(s.Title, "chat"))+"-"+time.Now().Format("20060102-150405")+".md"))
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	return rel, os.WriteFile(abs, []byte(h.Store.Redactor().String(b.String())), 0o644)
}

// resultMarkdown: a step's result for an export — rows as a table, other
// text in a block.
func resultMarkdown(c string) string {
	var rows []map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(c)), &rows) == nil && len(rows) > 0 && len(rows) <= 200 {
		var cols []string
		seen := map[string]bool{}
		for _, r := range rows {
			for k := range r {
				if !seen[k] {
					seen[k] = true
					cols = append(cols, k)
				}
			}
		}
		sort.Strings(cols)
		var b strings.Builder
		b.WriteString("| " + strings.Join(cols, " | ") + " |\n|" + strings.Repeat(" --- |", len(cols)) + "\n")
		for _, r := range rows {
			var cells []string
			for _, k := range cols {
				v := ""
				if x, ok := r[k]; ok && x != nil {
					if s, ok := x.(string); ok {
						v = s
					} else {
						bs, _ := json.Marshal(x)
						v = string(bs)
					}
				}
				cells = append(cells, strings.ReplaceAll(strings.ReplaceAll(v, "|", "\\|"), "\n", " "))
			}
			b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		}
		return b.String()
	}
	if strings.Contains(c, "data-specwright-chart") {
		return "_(a chart: see the session in SpecWright)_"
	}
	return "```\n" + clipText(c, 4000) + "\n```"
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.Trim(slugRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 50 {
		s = strings.Trim(s[:50], "-")
	}
	return firstNonEmpty(s, "chat")
}
