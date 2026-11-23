package store

// What the history database learns from runs (schema 2):
//
//   - lessons: what people asked for when they reviewed a step (Refine
//     feedback, "another round" feedback in an Improve step, or added by
//     hand), per project, flow and step. AI steps get them in their prompt,
//     so a mistake a reviewer pointed out is not made again.
//   - tool_calls: every call of a tool (from a flow, an agent, the test
//     form): ok or not, how long, the error, the arguments (redacted). The
//     Designer and Tools show how reliable a tool is; the test form offers
//     arguments that worked.
//   - trigger_queue: trigger runs waiting for their turn (the flow was
//     still running, or too many triggered runs at once) and retries after
//     a failure; kept across restarts.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

func migrate2(tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS lessons(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workspace TEXT NOT NULL, flow TEXT NOT NULL, step TEXT NOT NULL DEFAULT '',
			text TEXT NOT NULL, source TEXT NOT NULL DEFAULT '', run_id TEXT NOT NULL DEFAULT '',
			created INTEGER NOT NULL, used INTEGER NOT NULL DEFAULT 0, last_used INTEGER NOT NULL DEFAULT 0,
			off INTEGER NOT NULL DEFAULT 0)`,
		`CREATE INDEX IF NOT EXISTS lessons_by_flow ON lessons(workspace, flow, step)`,
		`CREATE TABLE IF NOT EXISTS tool_calls(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			at INTEGER NOT NULL, workspace TEXT NOT NULL, tool TEXT NOT NULL, source TEXT NOT NULL DEFAULT '',
			ok INTEGER NOT NULL, duration_ms INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '',
			args TEXT NOT NULL DEFAULT '', run_id TEXT NOT NULL DEFAULT '', flow TEXT NOT NULL DEFAULT '', step TEXT NOT NULL DEFAULT '')`,
		`CREATE INDEX IF NOT EXISTS tool_calls_by_tool ON tool_calls(workspace, tool, at DESC)`,
		`CREATE TABLE IF NOT EXISTS trigger_queue(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workspace TEXT NOT NULL, flow TEXT NOT NULL, trigger TEXT NOT NULL,
			why TEXT NOT NULL DEFAULT '', vars TEXT NOT NULL DEFAULT '{}', chain TEXT NOT NULL DEFAULT '[]',
			attempt INTEGER NOT NULL DEFAULT 0, not_before INTEGER NOT NULL DEFAULT 0, queued INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS trigger_queue_by_ws ON trigger_queue(workspace, queued)`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// --- lessons ------------------------------------------------------------------------

// Lesson is one thing a reviewer asked for, kept for later runs of the step.
type Lesson struct {
	ID        int64     `json:"id"`
	Workspace string    `json:"workspace"`
	Flow      string    `json:"flow"`
	Step      string    `json:"step"`
	Text      string    `json:"text"`
	Source    string    `json:"source"` // refine | improve | manual
	RunID     string    `json:"runId,omitempty"`
	Created   time.Time `json:"created"`
	Used      int       `json:"used"` // how many prompts it was in
	LastUsed  time.Time `json:"lastUsed,omitzero"`
	Off       bool      `json:"off,omitempty"` // kept, but not given to the AI
}

// ErrNoDB is returned by what needs the history database when it is not open.
var ErrNoDB = errors.New("the history database is not open")

// AddLesson keeps a lesson (redacted, at most 1,000 characters). The same
// text for the same step is not kept twice: the old one comes back on.
func (s *Store) AddLesson(l Lesson) (Lesson, error) {
	if s.db == nil {
		return Lesson{}, ErrNoDB
	}
	l.Text = clip(strings.TrimSpace(s.red.String(l.Text)), 1000)
	if l.Text == "" {
		return Lesson{}, errors.New("a lesson needs some text")
	}
	if l.Created.IsZero() {
		l.Created = time.Now()
	}
	var id int64
	if s.db.QueryRow(`SELECT id FROM lessons WHERE workspace=? AND flow=? AND step=? AND lower(text)=lower(?)`, l.Workspace, l.Flow, l.Step, l.Text).Scan(&id) == nil {
		_, err := s.db.Exec(`UPDATE lessons SET off=0, created=? WHERE id=?`, l.Created.UnixNano(), id)
		l.ID = id
		return l, err
	}
	res, err := s.db.Exec(`INSERT INTO lessons(workspace, flow, step, text, source, run_id, created, off) VALUES(?,?,?,?,?,?,?,?)`,
		l.Workspace, l.Flow, l.Step, l.Text, l.Source, l.RunID, l.Created.UnixNano(), boolInt(l.Off))
	if err != nil {
		return Lesson{}, err
	}
	l.ID, _ = res.LastInsertId()
	return l, nil
}

// Lessons lists a flow's lessons (step "" : all its steps), newest first.
func (s *Store) Lessons(workspace, flow, step string) ([]Lesson, error) {
	if s.db == nil {
		return []Lesson{}, nil
	}
	q := `SELECT id, workspace, flow, step, text, source, run_id, created, used, last_used, off FROM lessons WHERE workspace=? AND flow=?`
	args := []any{workspace, flow}
	if step != "" {
		q += ` AND step=?`
		args = append(args, step)
	}
	rows, err := s.db.Query(q+` ORDER BY created DESC, id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Lesson{}
	for rows.Next() {
		var l Lesson
		var created, last int64
		var off int
		if rows.Scan(&l.ID, &l.Workspace, &l.Flow, &l.Step, &l.Text, &l.Source, &l.RunID, &created, &l.Used, &last, &off) != nil {
			continue
		}
		l.Created, l.Off = time.Unix(0, created), off != 0
		if last > 0 {
			l.LastUsed = time.Unix(0, last)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// UpdateLesson changes a lesson's text or turns it off or on.
func (s *Store) UpdateLesson(id int64, text string, off bool) error {
	if s.db == nil {
		return ErrNoDB
	}
	text = clip(strings.TrimSpace(s.red.String(text)), 1000)
	if text == "" {
		return errors.New("a lesson needs some text")
	}
	res, err := s.db.Exec(`UPDATE lessons SET text=?, off=? WHERE id=?`, text, boolInt(off), id)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			err = errors.New("no such lesson")
		}
	}
	return err
}

// DeleteLesson forgets a lesson.
func (s *Store) DeleteLesson(id int64) error {
	if s.db == nil {
		return ErrNoDB
	}
	_, err := s.db.Exec(`DELETE FROM lessons WHERE id=?`, id)
	return err
}

// LessonsUsed counts that these lessons were given to the AI.
func (s *Store) LessonsUsed(ids []int64) {
	if s.db == nil {
		return
	}
	now := time.Now().UnixNano()
	for _, id := range ids {
		_, _ = s.db.Exec(`UPDATE lessons SET used=used+1, last_used=? WHERE id=?`, now, id)
	}
}

// --- tool calls ---------------------------------------------------------------------

// ToolCall is one call of a tool.
type ToolCall struct {
	At         time.Time      `json:"at"`
	Workspace  string         `json:"workspace"`
	Tool       string         `json:"tool"`
	Source     string         `json:"source"` // flow | agent | test
	OK         bool           `json:"ok"`
	DurationMS int64          `json:"durationMs"`
	Error      string         `json:"error,omitempty"`
	Args       map[string]any `json:"args,omitempty"`
	RunID      string         `json:"runId,omitempty"`
	Flow       string         `json:"flow,omitempty"`
	Step       string         `json:"step,omitempty"`
}

// RecordToolCall keeps a tool call (its error and arguments redacted).
func (s *Store) RecordToolCall(c ToolCall) error {
	if s.db == nil || c.Tool == "" {
		return nil
	}
	if c.At.IsZero() {
		c.At = time.Now()
	}
	args := ""
	if len(c.Args) > 0 {
		if b, err := json.Marshal(c.Args); err == nil && len(b) <= 4000 {
			args = s.red.String(string(b))
		}
	}
	_, err := s.db.Exec(`INSERT INTO tool_calls(at, workspace, tool, source, ok, duration_ms, error, args, run_id, flow, step) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		c.At.UnixNano(), c.Workspace, c.Tool, c.Source, boolInt(c.OK), c.DurationMS, clip(s.red.String(strings.TrimSpace(c.Error)), 600), args, c.RunID, c.Flow, c.Step)
	return err
}

// ToolStat is how a tool has done lately in a project.
type ToolStat struct {
	Tool       string    `json:"tool"`
	Calls      int       `json:"calls"`  // in the last RecentCalls (or fewer)
	Failed     int       `json:"failed"` // of those
	Recent     []bool    `json:"recent"` // ok or not, newest first (at most 10)
	TypicalMS  int64     `json:"typicalMs"`
	LastAt     time.Time `json:"lastAt"`
	LastOK     time.Time `json:"lastOk,omitzero"`
	LastError  string    `json:"lastError,omitempty"`
	LastFailAt time.Time `json:"lastFailAt,omitzero"`
}

// RecentCalls is how many of a tool's latest calls its numbers cover.
const RecentCalls = 10

// ToolStats is every tool's numbers in a project (tool "" : all tools).
func (s *Store) ToolStats(workspace, tool string) (map[string]ToolStat, error) {
	out := map[string]ToolStat{}
	if s.db == nil {
		return out, nil
	}
	q := `SELECT tool, at, ok, duration_ms, error FROM tool_calls WHERE workspace=?`
	args := []any{workspace}
	if tool != "" {
		q += ` AND tool=?`
		args = append(args, tool)
	}
	rows, err := s.db.Query(q+` ORDER BY at DESC LIMIT 20000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	durs := map[string][]int64{}
	for rows.Next() {
		var name, e string
		var at, ms int64
		var ok int
		if rows.Scan(&name, &at, &ok, &ms, &e) != nil {
			continue
		}
		st := out[name]
		st.Tool = name
		t := time.Unix(0, at)
		if st.LastAt.IsZero() {
			st.LastAt = t
		}
		if ok != 0 && st.LastOK.IsZero() {
			st.LastOK = t
		}
		if ok == 0 && st.LastFailAt.IsZero() {
			st.LastFailAt, st.LastError = t, e
		}
		if st.Calls < RecentCalls {
			st.Calls++
			st.Recent = append(st.Recent, ok != 0)
			if ok == 0 {
				st.Failed++
			}
		}
		if ok != 0 && len(durs[name]) < 50 {
			durs[name] = append(durs[name], ms)
		}
		out[name] = st
	}
	for name, d := range durs {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		st := out[name]
		st.TypicalMS = d[len(d)/2] // the median of its last successful calls
		out[name] = st
	}
	return out, rows.Err()
}

// ToolArgExamples are the arguments of a tool's latest successful calls
// (distinct, newest first, at most n).
func (s *Store) ToolArgExamples(workspace, tool string, n int) ([]map[string]any, error) {
	out := []map[string]any{}
	if s.db == nil {
		return out, nil
	}
	rows, err := s.db.Query(`SELECT args FROM tool_calls WHERE workspace=? AND tool=? AND ok=1 AND args<>'' ORDER BY at DESC LIMIT 200`, workspace, tool)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() && len(out) < n {
		var a string
		if rows.Scan(&a) != nil || seen[a] {
			continue
		}
		seen[a] = true
		var m map[string]any
		if json.Unmarshal([]byte(a), &m) == nil && len(m) > 0 {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

// --- the trigger queue --------------------------------------------------------------

// QueuedRun is a trigger run waiting for its turn.
type QueuedRun struct {
	ID        int64             `json:"id"`
	Workspace string            `json:"workspace"`
	Flow      string            `json:"flow"`
	Trigger   string            `json:"trigger"`
	Why       string            `json:"why"`
	Vars      map[string]string `json:"vars,omitempty"`
	Chain     []string          `json:"chain,omitempty"`
	Attempt   int               `json:"attempt,omitempty"` // a retry: which one
	NotBefore time.Time         `json:"notBefore,omitzero"`
	Queued    time.Time         `json:"queued"`
}

// Enqueue adds a run to the queue.
func (s *Store) Enqueue(q QueuedRun) (int64, error) {
	if s.db == nil {
		return 0, ErrNoDB
	}
	if q.Queued.IsZero() {
		q.Queued = time.Now()
	}
	v, _ := json.Marshal(q.Vars)
	c, _ := json.Marshal(q.Chain)
	nb := int64(0)
	if !q.NotBefore.IsZero() {
		nb = q.NotBefore.UnixNano()
	}
	res, err := s.db.Exec(`INSERT INTO trigger_queue(workspace, flow, trigger, why, vars, chain, attempt, not_before, queued) VALUES(?,?,?,?,?,?,?,?,?)`,
		q.Workspace, q.Flow, q.Trigger, q.Why, s.red.String(string(v)), string(c), q.Attempt, nb, q.Queued.UnixNano())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Queue lists a project's waiting runs, oldest first.
func (s *Store) Queue(workspace string) ([]QueuedRun, error) {
	out := []QueuedRun{}
	if s.db == nil {
		return out, nil
	}
	rows, err := s.db.Query(`SELECT id, workspace, flow, trigger, why, vars, chain, attempt, not_before, queued FROM trigger_queue WHERE workspace=? ORDER BY queued, id`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var q QueuedRun
		var v, c string
		var nb, at int64
		if rows.Scan(&q.ID, &q.Workspace, &q.Flow, &q.Trigger, &q.Why, &v, &c, &q.Attempt, &nb, &at) != nil {
			continue
		}
		_ = json.Unmarshal([]byte(v), &q.Vars)
		_ = json.Unmarshal([]byte(c), &q.Chain)
		if nb > 0 {
			q.NotBefore = time.Unix(0, nb)
		}
		q.Queued = time.Unix(0, at)
		out = append(out, q)
	}
	return out, rows.Err()
}

// Dequeue removes a run from the queue (it started, or is no longer wanted).
func (s *Store) Dequeue(id int64) error {
	if s.db == nil {
		return ErrNoDB
	}
	_, err := s.db.Exec(`DELETE FROM trigger_queue WHERE id=?`, id)
	return err
}

// PruneLessons keeps only the newest keep lessons of one source for a step
// (lessons a person typed are never pruned by callers; learned ones are).
func (s *Store) PruneLessons(workspace, flow, step, source string, keep int) error {
	if s.db == nil {
		return ErrNoDB
	}
	_, err := s.db.Exec(`DELETE FROM lessons WHERE workspace=? AND flow=? AND step=? AND source=? AND id NOT IN
		(SELECT id FROM lessons WHERE workspace=? AND flow=? AND step=? AND source=? ORDER BY created DESC, id DESC LIMIT ?)`,
		workspace, flow, step, source, workspace, flow, step, source, keep)
	return err
}

// RenameFlow moves what is kept by flow id (lessons, queued trigger runs)
// to the flow's new id. Past runs and tool calls keep the id they ran with.
func (s *Store) RenameFlow(workspace, from, to string) error {
	if s.db == nil {
		return nil
	}
	for _, q := range []string{`UPDATE lessons SET flow=? WHERE workspace=? AND flow=?`, `UPDATE trigger_queue SET flow=? WHERE workspace=? AND flow=?`} {
		if _, err := s.db.Exec(q, to, workspace, from); err != nil {
			return err
		}
	}
	return nil
}
