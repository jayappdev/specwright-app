// Package loop detects an agent that is stuck repeating itself: the same
// tool call with the same arguments, or the same reply, over and over.
package loop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Config tunes the detector. Zero values pick the defaults.
type Config struct {
	MaxRepeat int // identical consecutive actions before tripping (default 3)
	Window    int // actions remembered (default 12)
	MaxInWin  int // identical actions anywhere in the window (default 5)
}

// Detector is fed every tool call and reply of one run. Not safe for
// concurrent use; the runner owns one per session.
type Detector struct {
	cfg    Config
	recent []string
	labels map[string]string
	streak int
}

// New returns a detector with cfg (defaults filled in).
func New(cfg Config) *Detector {
	if cfg.MaxRepeat <= 0 {
		cfg.MaxRepeat = 3
	}
	if cfg.Window <= 0 {
		cfg.Window = 12
	}
	if cfg.MaxInWin <= 0 {
		cfg.MaxInWin = 5
	}
	return &Detector{cfg: cfg, labels: map[string]string{}}
}

// Verdict explains a detected loop.
type Verdict struct {
	Action string `json:"action"`
	Count  int    `json:"count"`
	Reason string `json:"reason"`
}

// ToolCall records a tool call. It returns a verdict when a loop is found.
func (d *Detector) ToolCall(name string, args any) *Verdict {
	b, _ := json.Marshal(args)
	return d.observe("tool:"+name+":"+string(b), fmt.Sprintf("%s %s", name, trim(string(b), 80)))
}

// Reply records a model reply text.
func (d *Detector) Reply(text string) *Verdict {
	if len(text) < 12 { // short acknowledgements repeat legitimately
		return nil
	}
	return d.observe("say:"+text, "reply “"+trim(text, 60)+"”")
}

func (d *Detector) observe(key, label string) *Verdict {
	h := sha256.Sum256([]byte(key))
	k := hex.EncodeToString(h[:8])
	d.labels[k] = label
	if n := len(d.recent); n > 0 && d.recent[n-1] == k {
		d.streak++
	} else {
		d.streak = 1
	}
	d.recent = append(d.recent, k)
	if len(d.recent) > d.cfg.Window {
		d.recent = d.recent[len(d.recent)-d.cfg.Window:]
	}
	if d.streak >= d.cfg.MaxRepeat {
		return &Verdict{Action: label, Count: d.streak, Reason: fmt.Sprintf("same action %d times in a row", d.streak)}
	}
	c := 0
	for _, r := range d.recent {
		if r == k {
			c++
		}
	}
	if c >= d.cfg.MaxInWin {
		return &Verdict{Action: label, Count: c, Reason: fmt.Sprintf("same action %d times in the last %d", c, len(d.recent))}
	}
	return nil
}

// Reset forgets history (after the user resumes past a loop gate).
func (d *Detector) Reset() { d.recent = nil; d.streak = 0 }

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
