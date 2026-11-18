// Package store keeps run history (one JSONL file of events per run), the
// usage ledger, and replays recorded runs. Everything written is redacted
// first: token-shaped strings and known secret values never reach disk.
package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"codegen/internal/bus"
)

// Run is the summary row of one recorded run.
type Run struct {
	ID        string    `json:"id"`
	Workspace string    `json:"workspace"`
	Workflow  string    `json:"workflow"`
	Step      string    `json:"step"`
	Agent     string    `json:"agent"`
	Model     string    `json:"model,omitempty"`
	State     string    `json:"state"`
	Reason    string    `json:"reason,omitempty"`
	Started   time.Time `json:"started"`
	Ended     time.Time `json:"ended,omitzero"`
	Events    int       `json:"events"`
	Usage     Usage     `json:"usage"`
	// Kind is workflow, playground or flow; Title and Ask describe the run;
	// Parents are the runs it continues from; Results is the workspace
	// folder its results were written to.
	Kind    string   `json:"kind,omitempty"`
	Title   string   `json:"title,omitempty"`
	Ask     string   `json:"ask,omitempty"`
	Parents []string `json:"parents,omitempty"`
	Results string   `json:"results,omitempty"`
	// Archived runs are hidden from the Playground and History lists until
	// restored (by hand, or automatically after some days); Group is a
	// label the user gives runs to keep them together.
	// Trigger: what started the run when no one pressed Run ("scheduled"),
	// and the trigger's name
	Trigger     string    `json:"trigger,omitempty"`
	TriggerName string    `json:"triggerName,omitempty"`
	Archived    bool      `json:"archived,omitempty"`
	ArchivedAt  time.Time `json:"archivedAt,omitzero"`
	Group       string    `json:"group,omitempty"`
}

// GetRun reads one run's summary row.
func (s *Store) GetRun(id string) (Run, error) {
	b, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return Run{}, err
	}
	var r Run
	err = json.Unmarshal(b, &r)
	return r, err
}

// Usage is what one run consumed.
type Usage struct {
	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	CachedTokens    int     `json:"cached_tokens"`
	PremiumRequests int     `json:"premium_requests"`
	ModelCalls      int     `json:"model_calls"`
	CostUSD         float64 `json:"cost_usd"`
	ToolCalls       int     `json:"tool_calls"`
	ToolErrors      int     `json:"tool_errors"`
	FilesCreated    int     `json:"files_created"`
	FilesModified   int     `json:"files_modified"`
	DurationMS      int64   `json:"duration_ms"`
}

// Add accumulates o into u.
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.CachedTokens += o.CachedTokens
	u.PremiumRequests += o.PremiumRequests
	u.ModelCalls += o.ModelCalls
	u.CostUSD += o.CostUSD
	u.ToolCalls += o.ToolCalls
	u.ToolErrors += o.ToolErrors
	u.FilesCreated += o.FilesCreated
	u.FilesModified += o.FilesModified
	u.DurationMS += o.DurationMS
}

// LedgerEntry is one line of usage.jsonl.
type LedgerEntry struct {
	At        time.Time `json:"at"`
	RunID     string    `json:"run_id"`
	Workspace string    `json:"workspace"`
	Workflow  string    `json:"workflow"`
	Step      string    `json:"step"`
	Agent     string    `json:"agent"`
	Model     string    `json:"model,omitempty"`
	State     string    `json:"state"`
	// MinutesSaved is the manual work a successful flow run replaced.
	MinutesSaved int `json:"minutes_saved,omitempty"`
	Usage
}

// Store is rooted at a directory (default: the user config dir).
type Store struct {
	dir string
	mu  sync.Mutex
	red *Redactor

	errMu      sync.Mutex
	onWriteErr func(op string, err error)
}

// SetOnWriteError registers f to be told about every failed write of run
// history (SaveRun, Append, Ledger). Callers treat history as best effort and
// carry on after a failure; this hook makes sure the failure is not silent.
func (s *Store) SetOnWriteError(f func(op string, err error)) {
	s.errMu.Lock()
	s.onWriteErr = f
	s.errMu.Unlock()
}

func (s *Store) reportWrite(op string, err error) error {
	if err == nil {
		return nil
	}
	s.errMu.Lock()
	f := s.onWriteErr
	s.errMu.Unlock()
	if f != nil {
		f(op, err)
	}
	return err
}

// Open creates (if needed) and opens a store at dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "runs"), 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir, red: NewRedactor()}, nil
}

// DefaultDir is <user config dir>/codegen.
func DefaultDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "codegen")
}

// Dir returns the store root.
func (s *Store) Dir() string { return s.dir }

// Redactor returns the redactor applied to everything written.
func (s *Store) Redactor() *Redactor { return s.red }

func (s *Store) runPath(id string) string  { return filepath.Join(s.dir, "runs", safe(id)+".jsonl") }
func (s *Store) metaPath(id string) string { return filepath.Join(s.dir, "runs", safe(id)+".json") }

func safe(id string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return '_'
	}, id)
}

// SaveRun writes the run summary.
func (s *Store) SaveRun(r Run) error { return s.reportWrite("save run "+r.ID, s.saveRun(r)) }

func (s *Store) saveRun(r Run) error {
	// fields written by others (the results writer) survive a later save
	// that does not know them
	if old, err := s.GetRun(r.ID); err == nil {
		if r.Results == "" {
			r.Results = old.Results
		}
		if r.Kind == "" {
			r.Kind = old.Kind
		}
		if r.Title == "" {
			r.Title = old.Title
		}
		if r.Ask == "" {
			r.Ask = old.Ask
		}
		if len(r.Parents) == 0 {
			r.Parents = old.Parents
		}
		// the user's archive flag and group belong to the run, not to the save
		if !r.Archived && old.Archived {
			r.Archived, r.ArchivedAt = true, old.ArchivedAt
		}
		if r.Group == "" {
			r.Group = old.Group
		}
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.metaPath(r.ID), b)
}

// SetRunMeta archives or restores a run (archived != nil) and sets its
// group (group != nil; "" removes it).
func (s *Store) SetRunMeta(id string, archived *bool, group *string) error {
	r, err := s.GetRun(id)
	if err != nil {
		return err
	}
	if archived != nil {
		r.Archived = *archived
		r.ArchivedAt = time.Time{}
		if r.Archived {
			r.ArchivedAt = time.Now().UTC()
		}
	}
	if group != nil {
		r.Group = strings.TrimSpace(*group)
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.metaPath(r.ID), b)
}

// Append records one event of a run.
func (s *Store) Append(e bus.Event) error {
	return s.reportWrite("record an event of run "+e.RunID, s.appendEvent(e))
}

func (s *Store) appendEvent(e bus.Event) error {
	if e.RunID == "" {
		return nil
	}
	s.red.Event(&e)
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.runPath(e.RunID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// Events reads every event recorded for a run.
func (s *Store) Events(runID string) ([]bus.Event, error) {
	f, err := os.Open(s.runPath(runID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []bus.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var e bus.Event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// Runs lists recorded runs, newest first, optionally for one workspace.
func (s *Store) Runs(workspace string, limit int) ([]Run, error) {
	ents, err := os.ReadDir(filepath.Join(s.dir, "runs"))
	if err != nil {
		return nil, err
	}
	var out []Run
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, "runs", e.Name()))
		if err != nil {
			continue
		}
		var r Run
		if json.Unmarshal(b, &r) != nil {
			continue
		}
		if workspace != "" && r.Workspace != workspace {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// MarkInterrupted flags runs that were still live when the app last quit.
func (s *Store) MarkInterrupted() int {
	runs, _ := s.Runs("", 0)
	n := 0
	for _, r := range runs {
		if r.State == "running" || r.State == "paused" || r.State == "idle" {
			r.State, r.Reason, r.Ended = "interrupted", "the app closed during the run", time.Now()
			if s.SaveRun(r) == nil {
				n++
			}
		}
	}
	return n
}

// Ledger appends a usage entry.
func (s *Store) Ledger(e LedgerEntry) error {
	return s.reportWrite("record usage of run "+e.RunID, s.ledger(e))
}

func (s *Store) ledger(e LedgerEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(s.dir, "usage.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// LedgerEntries reads the whole usage ledger.
func (s *Store) LedgerEntries() ([]LedgerEntry, error) {
	f, err := os.Open(filepath.Join(s.dir, "usage.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []LedgerEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e LedgerEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// Replay re-emits a recorded run with its original pacing divided by speed
// (speed <= 0 means as fast as possible).
func (s *Store) Replay(ctx context.Context, runID string, speed float64, emit func(bus.Event)) error {
	evs, err := s.Events(runID)
	if err != nil {
		return err
	}
	for i, e := range evs {
		if i > 0 && speed > 0 {
			d := time.Duration(float64(e.TS.Sub(evs[i-1].TS)) / speed)
			if d > 2*time.Second {
				d = 2 * time.Second
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
			}
		}
		emit(e)
	}
	return nil
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Redactor removes token-shaped strings and registered secret values.
type Redactor struct {
	mu      sync.RWMutex
	secrets map[string]struct{}
}

var tokenRE = regexp.MustCompile(`(sk-ant-[A-Za-z0-9_\-]{8,}|github_pat_[A-Za-z0-9_]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|xox[abpr]-[A-Za-z0-9\-]{10,}|AKIA[0-9A-Z]{16}|(?i:bearer)\s+[A-Za-z0-9._\-]{16,})`)

// NewRedactor returns a redactor with only the built-in patterns.
func NewRedactor() *Redactor { return &Redactor{secrets: map[string]struct{}{}} }

// Add registers secret values (values shorter than 4 chars are ignored).
func (r *Redactor) Add(vals ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range vals {
		if len(v) >= 4 {
			r.secrets[v] = struct{}{}
		}
	}
}

// String redacts one string.
func (r *Redactor) String(s string) string {
	s = tokenRE.ReplaceAllString(s, "[REDACTED]")
	r.mu.RLock()
	defer r.mu.RUnlock()
	for v := range r.secrets {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, "[REDACTED]")
		}
	}
	return s
}

// Event redacts the summary and every string in Data, recursively.
func (r *Redactor) Event(e *bus.Event) {
	e.Summary = r.String(e.Summary)
	if e.Data != nil {
		nd := make(map[string]any, len(e.Data))
		for k, v := range e.Data {
			nd[k] = r.value(v)
		}
		e.Data = nd
	}
}

func (r *Redactor) value(v any) any {
	switch t := v.(type) {
	case string:
		return r.String(t)
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			m[k] = r.value(x)
		}
		return m
	case []any:
		a := make([]any, len(t))
		for i, x := range t {
			a[i] = r.value(x)
		}
		return a
	case []string:
		a := make([]string, len(t))
		for i, x := range t {
			a[i] = r.String(x)
		}
		return a
	}
	return v
}
