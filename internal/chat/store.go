// Package chat keeps Chatflow chat sessions in the project
// (docs/design/2026-11-24-chatflows.md):
//
//	.codegen/chats/<chatflow id>/<session id>/
//	    session.json     the session: inputs (never secrets), status, the AI conversation id, usage
//	    messages.jsonl   the conversation, one message per line
//	    data/            what the session's steps gave (setup and messages), one file each
//	    files/           files the person attached
//
// The folder is git-ignored. Secrets are never written here: the caller
// passes only non-secret inputs, and step output has secret values
// redacted before it is saved.
package chat

import (
	"bufio"
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
)

// Dir is where sessions are kept, relative to the project.
const Dir = ".codegen/chats"

var (
	flowRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,59}$`)
	sessionRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[a-z0-9]{6}$`)
	dataRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
)

// Session is one chat with a Chatflow.
type Session struct {
	ID       string    `json:"id"`
	Flow     string    `json:"flow"`
	FlowName string    `json:"flowName"`
	Title    string    `json:"title"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	// Status: new | setup (its setup steps are running) | ready | failed
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	// Inputs are the session's non-secret inputs
	Inputs  map[string]string `json:"inputs,omitempty"`
	SetupAt time.Time         `json:"setupAt,omitzero"`
	// the AI: agent, model, and its conversation (continued each message)
	Agent        string `json:"agent,omitempty"`
	Model        string `json:"model,omitempty"`
	AgentSession string `json:"agentSession,omitempty"`
	// usage so far (CostUSD and Tokens: all of it, setup included)
	CostUSD float64 `json:"costUsd,omitempty"`
	Tokens  int     `json:"tokens,omitempty"`
	// Usage: the questions' (the chat's AI, and its tools); SetupUsage:
	// the setup steps' (every time they ran)
	Usage      Usage `json:"usage,omitzero"`
	SetupUsage Usage `json:"setupUsage,omitzero"`
	// AgentSoFar: what the AI last reported as its conversation's usage so
	// far (Copilot reports a resumed session's totals), to count each
	// answer's own part
	AgentSoFar Usage  `json:"agentSoFar,omitzero"`
	Messages   int    `json:"messages"`
	Data       []Data `json:"data,omitempty"`
	Archived   bool   `json:"archived,omitempty"`
	// Refreshed: the setup ran again; the next message tells the AI
	Refreshed bool `json:"refreshed,omitempty"`
}

// Data is one thing the session keeps: a setup step's result, or what a
// message's step gave.
type Data struct {
	Name  string    `json:"name"`           // file name in data/
	Step  string    `json:"step,omitempty"` // the step that gave it
	Title string    `json:"title,omitempty"`
	Setup bool      `json:"setup,omitempty"` // from the setup steps
	Bytes int       `json:"bytes"`
	At    time.Time `json:"at"`
}

// Message is one turn of the conversation.
type Message struct {
	ID   string    `json:"id"`
	Role string    `json:"role"` // user | assistant | system
	Text string    `json:"text"`
	At   time.Time `json:"at"`
	// the steps the assistant ran for it, and files the person attached
	Steps []StepRun `json:"steps,omitempty"`
	Files []string  `json:"files,omitempty"` // attachments (data names)
	Error string    `json:"error,omitempty"`
	// Followups: questions the assistant suggests next
	Followups []string `json:"followups,omitempty"`
	// what answering it took (an answer's), with which agent and model
	Usage *Usage `json:"usage,omitempty"`
	Agent string `json:"agent,omitempty"`
	Model string `json:"model,omitempty"`
}

// Usage is what an answer (or a session, or a tool) took.
type Usage struct {
	Prompts         int     `json:"prompts,omitempty"`    // prompts sent to the chat's AI (one per round)
	ModelCalls      int     `json:"modelCalls,omitempty"` // model calls those made
	PremiumRequests int     `json:"premiumRequests,omitempty"`
	InputTokens     int     `json:"inputTokens,omitempty"`
	OutputTokens    int     `json:"outputTokens,omitempty"`
	CachedTokens    int     `json:"cachedTokens,omitempty"` // of the input tokens, read from the cache
	CostUSD         float64 `json:"costUsd,omitempty"`
	ToolRuns        int     `json:"toolRuns,omitempty"`   // the Chatflow's tools (its branches) run
	DataRuns        int     `json:"dataRuns,omitempty"`   // data.query / data.search runs
	ToolErrors      int     `json:"toolErrors,omitempty"` // of those, the ones that did not run
	AgentTools      int     `json:"agentTools,omitempty"` // tools the AI used on its own side
	DurationMS      int64   `json:"durationMs,omitempty"`
}

// Add adds o to u.
func (u *Usage) Add(o Usage) {
	u.Prompts += o.Prompts
	u.ModelCalls += o.ModelCalls
	u.PremiumRequests += o.PremiumRequests
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.CachedTokens += o.CachedTokens
	u.CostUSD += o.CostUSD
	u.ToolRuns += o.ToolRuns
	u.DataRuns += o.DataRuns
	u.ToolErrors += o.ToolErrors
	u.AgentTools += o.AgentTools
	u.DurationMS += o.DurationMS
}

// Tokens is the input and output tokens.
func (u Usage) Tokens() int { return u.InputTokens + u.OutputTokens }

// StepRun is one of the chat's tools run for a message.
type StepRun struct {
	Tool       string         `json:"tool"` // the branch's first step
	Title      string         `json:"title"`
	Args       map[string]any `json:"args,omitempty"`
	Data       string         `json:"data,omitempty"` // its result, in data/
	DurationMS int64          `json:"durationMs"`
	Error      string         `json:"error,omitempty"`
	// Files the step saved in the project (workspace paths)
	Files []string `json:"files,omitempty"`
	// Usage: the AI its steps used (a branch with AI steps)
	Usage *Usage `json:"usage,omitempty"`
}

// Store reads and writes a project's chat sessions.
type Store struct {
	root string
	mu   sync.Mutex
}

// New returns the store of the project at root.
func New(root string) *Store { return &Store{root: root} }

func (s *Store) dir(flow, id string) (string, error) {
	if !flowRE.MatchString(flow) {
		return "", fmt.Errorf("no chatflow %q", flow)
	}
	if id != "" && !sessionRE.MatchString(id) {
		return "", fmt.Errorf("no session %q", id)
	}
	return filepath.Join(s.root, filepath.FromSlash(Dir), flow, id), nil
}

// ensureIgnored keeps the chats out of version control.
func (s *Store) ensureIgnored() {
	base := filepath.Join(s.root, filepath.FromSlash(Dir))
	_ = os.MkdirAll(base, 0o755)
	p := filepath.Join(base, ".gitignore")
	if _, err := os.Stat(p); err != nil {
		_ = os.WriteFile(p, []byte("# chat sessions (SpecWright): local, not shared\n*\n"), 0o644)
	}
}

// NewID makes a session id: when it started, and a random part.
func NewID(t time.Time, random string) string {
	r := strings.ToLower(random)
	r = regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString(r, "")
	for len(r) < 6 {
		r += "0"
	}
	return t.Format("20060102-150405") + "-" + r[:6]
}

// Create starts a session record.
func (s *Store) Create(sess Session) (Session, error) {
	if sess.ID == "" {
		return Session{}, errors.New("a session needs an id")
	}
	d, err := s.dir(sess.Flow, sess.ID)
	if err != nil {
		return Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureIgnored()
	if _, err := os.Stat(d); err == nil {
		return Session{}, fmt.Errorf("session %s exists", sess.ID)
	}
	for _, sub := range []string{"data", "files"} {
		if err := os.MkdirAll(filepath.Join(d, sub), 0o755); err != nil {
			return Session{}, err
		}
	}
	now := time.Now()
	if sess.Created.IsZero() {
		sess.Created = now
	}
	sess.Updated = now
	if sess.Status == "" {
		sess.Status = "new"
	}
	return sess, s.write(d, sess)
}

func (s *Store) write(d string, sess Session) error {
	b, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(d, ".session.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(d, "session.json"))
}

// Get reads a session.
func (s *Store) Get(flow, id string) (Session, error) {
	d, err := s.dir(flow, id)
	if err != nil {
		return Session{}, err
	}
	b, err := os.ReadFile(filepath.Join(d, "session.json"))
	if err != nil {
		return Session{}, fmt.Errorf("no session %s", id)
	}
	var sess Session
	return sess, json.Unmarshal(b, &sess)
}

// Update changes a session with f and saves it.
func (s *Store) Update(flow, id string, f func(*Session)) (Session, error) {
	d, err := s.dir(flow, id)
	if err != nil {
		return Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.Get(flow, id)
	if err != nil {
		return Session{}, err
	}
	f(&sess)
	sess.Updated = time.Now()
	return sess, s.write(d, sess)
}

// List returns a chatflow's sessions, newest first ("" lists every
// chatflow's).
func (s *Store) List(flow string) ([]Session, error) {
	base := filepath.Join(s.root, filepath.FromSlash(Dir))
	flows := []string{flow}
	if flow == "" {
		flows = nil
		ents, _ := os.ReadDir(base)
		for _, e := range ents {
			if e.IsDir() && flowRE.MatchString(e.Name()) {
				flows = append(flows, e.Name())
			}
		}
	}
	out := []Session{}
	for _, f := range flows {
		d, err := s.dir(f, "")
		if err != nil {
			return nil, err
		}
		ents, err := os.ReadDir(d)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range ents {
			if !e.IsDir() || !sessionRE.MatchString(e.Name()) {
				continue
			}
			if sess, err := s.Get(f, e.Name()); err == nil {
				out = append(out, sess)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// Append adds a message to the conversation (and counts it).
func (s *Store) Append(flow, id string, m Message) (Message, error) {
	d, err := s.dir(flow, id)
	if err != nil {
		return Message{}, err
	}
	if m.At.IsZero() {
		m.At = time.Now()
	}
	if m.ID == "" {
		m.ID = m.At.Format("150405.000000")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return Message{}, err
	}
	s.mu.Lock()
	f, err := os.OpenFile(filepath.Join(d, "messages.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		_, err = f.Write(append(b, '\n'))
		f.Close()
	}
	s.mu.Unlock()
	if err != nil {
		return Message{}, err
	}
	_, err = s.Update(flow, id, func(x *Session) {
		x.Messages++
		if x.Title == "" && m.Role == "user" { // named after the first question
			x.Title = Title(m.Text)
		}
	})
	return m, err
}

// Title is a session title made from a question.
func Title(q string) string {
	q = strings.Join(strings.Fields(q), " ")
	if r := []rune(q); len(r) > 60 {
		q = strings.TrimSpace(string(r[:59])) + "…"
	}
	return q
}

// Messages reads the conversation.
func (s *Store) Messages(flow, id string) ([]Message, error) {
	d, err := s.dir(flow, id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(d, "messages.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return []Message{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := []Message{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 32<<20)
	for sc.Scan() {
		var m Message
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			out = append(out, m)
		}
	}
	return out, sc.Err()
}

// PutData saves what a step gave in the session (replacing an earlier
// item of that name) and records it.
func (s *Store) PutData(flow, id string, item Data, content string) (Data, error) {
	d, err := s.dir(flow, id)
	if err != nil {
		return Data{}, err
	}
	if !dataRE.MatchString(item.Name) {
		return Data{}, fmt.Errorf("data name %q", item.Name)
	}
	if err := os.WriteFile(filepath.Join(d, "data", item.Name), []byte(content), 0o644); err != nil {
		return Data{}, err
	}
	item.Bytes, item.At = len(content), time.Now()
	_, err = s.Update(flow, id, func(x *Session) {
		for i := range x.Data {
			if x.Data[i].Name == item.Name {
				x.Data[i] = item
				return
			}
		}
		x.Data = append(x.Data, item)
	})
	return item, err
}

// PutFile keeps a file the person attached (files/<name>) and returns
// its path relative to the project.
func (s *Store) PutFile(flow, id, name string, content []byte) (string, error) {
	d, err := s.dir(flow, id)
	if err != nil {
		return "", err
	}
	if !dataRE.MatchString(name) {
		return "", fmt.Errorf("file name %q", name)
	}
	if err := os.MkdirAll(filepath.Join(d, "files"), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(d, "files", name), content, 0o644); err != nil {
		return "", err
	}
	return s.Path(flow, id) + "/files/" + name, nil
}

// GetData reads a data item's content.
func (s *Store) GetData(flow, id, name string) (string, error) {
	d, err := s.dir(flow, id)
	if err != nil {
		return "", err
	}
	if !dataRE.MatchString(name) {
		return "", fmt.Errorf("data name %q", name)
	}
	b, err := os.ReadFile(filepath.Join(d, "data", name))
	return string(b), err
}

// Path is a session's folder, relative to the project (for links).
func (s *Store) Path(flow, id string) string { return Dir + "/" + flow + "/" + id }

// Delete removes a session and everything in it.
func (s *Store) Delete(flow, id string) error {
	d, err := s.dir(flow, id)
	if err != nil {
		return err
	}
	if id == "" {
		return errors.New("name the session")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(d)
}
