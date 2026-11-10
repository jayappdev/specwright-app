// Package bus is the in-process event bus. Every run, watcher and gate
// publishes normalized Events here; the UI, the store's recorder and the
// headless runner subscribe.
package bus

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// Event is the normalized event shape shared by every agent driver.
// Type is dotted: run.*, prompt.*, model.*, tool.*, fs.*, gate.*, loop.*,
// budget.*, ext.*, error.
type Event struct {
	Seq     int            `json:"seq"`
	TS      time.Time      `json:"ts"`
	RunID   string         `json:"run_id,omitempty"`
	Step    string         `json:"step,omitempty"`
	Type    string         `json:"type"`
	Summary string         `json:"summary,omitempty"`
	Data    map[string]any `json:"-"`
}

// Kind returns the prefix of the event type ("model" for "model.request").
func (e Event) Kind() string {
	k, _, _ := strings.Cut(e.Type, ".")
	return k
}

// MarshalJSON flattens Data into the top-level object so JSONL lines read
// like {"seq":3,"type":"model.request","input_tokens":2400,...}.
func (e Event) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, len(e.Data)+6)
	for k, v := range e.Data {
		m[k] = v
	}
	m["seq"] = e.Seq
	m["ts"] = e.TS.UTC().Format(time.RFC3339Nano)
	m["type"] = e.Type
	if e.RunID != "" {
		m["run_id"] = e.RunID
	}
	if e.Step != "" {
		m["step"] = e.Step
	}
	if e.Summary != "" {
		m["summary"] = e.Summary
	}
	return json.Marshal(m)
}

// UnmarshalJSON reverses MarshalJSON.
func (e *Event) UnmarshalJSON(b []byte) error {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	take := func(k string) any { v := m[k]; delete(m, k); return v }
	if v, ok := take("seq").(float64); ok {
		e.Seq = int(v)
	}
	if v, ok := take("ts").(string); ok {
		e.TS, _ = time.Parse(time.RFC3339Nano, v)
	}
	e.Type, _ = take("type").(string)
	e.RunID, _ = take("run_id").(string)
	e.Step, _ = take("step").(string)
	e.Summary, _ = take("summary").(string)
	if len(m) > 0 {
		e.Data = m
	}
	return nil
}

// Int reads a numeric field from Data.
func (e Event) Int(k string) int {
	switch v := e.Data[k].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

// Float reads a float field from Data.
func (e Event) Float(k string) float64 {
	switch v := e.Data[k].(type) {
	case int:
		return float64(v)
	case float64:
		return v
	}
	return 0
}

// Str reads a string field from Data.
func (e Event) Str(k string) string { s, _ := e.Data[k].(string); return s }

// Bus fans events out to subscribers. Publishing never blocks: a subscriber
// whose buffer is full misses events (and is told so through Dropped).
type Bus struct {
	mu     sync.Mutex
	subs   map[int]*sub
	next   int
	filter func(*Event)
}

type sub struct {
	ch      chan Event
	match   func(Event) bool
	dropped int
}

// New returns an empty bus.
func New() *Bus { return &Bus{subs: map[int]*sub{}} }

// SetFilter installs a hook that runs on every event before fan-out, used
// for secret redaction.
func (b *Bus) SetFilter(f func(*Event)) { b.mu.Lock(); b.filter = f; b.mu.Unlock() }

// Publish stamps the event time if unset, filters it, and delivers it.
func (b *Bus) Publish(e Event) Event {
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.filter != nil {
		b.filter(&e)
	}
	for _, s := range b.subs {
		if s.match != nil && !s.match(e) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			s.dropped++
		}
	}
	return e
}

// Subscribe returns a channel of events matching match (nil = all) and a
// cancel func that closes it.
func (b *Bus) Subscribe(buf int, match func(Event) bool) (<-chan Event, func()) {
	if buf <= 0 {
		buf = 256
	}
	b.mu.Lock()
	id := b.next
	b.next++
	s := &sub{ch: make(chan Event, buf), match: match}
	b.subs[id] = s
	b.mu.Unlock()
	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			b.mu.Unlock()
			close(s.ch)
		})
	}
}

// ForRun matches events of one run.
func ForRun(runID string) func(Event) bool {
	return func(e Event) bool { return e.RunID == runID }
}
