package flow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Triggers start a flow without anyone pressing Run. A trigger belongs to
// the flow (it is in the flow file, set up in the Flow Designer on the
// Inputs step) and gives the values of the flow's inputs for the runs it
// starts. The first kind is "schedule"; others (a file change, a webhook)
// can follow with their own settings.
//
// Secret values are not in the flow file: they are kept encrypted on the
// computer that runs the trigger (core/triggers.go), as is the trigger's
// state (next run, last run, how many runs).

// TriggerKinds are the kinds of trigger.
var TriggerKinds = []string{"schedule"}

// Trigger is one way a flow starts by itself.
type Trigger struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind"` // TriggerKinds
	Name     string            `json:"name,omitempty"`
	Enabled  bool              `json:"enabled"`
	Schedule *Schedule         `json:"schedule,omitempty"`
	Inputs   map[string]string `json:"inputs,omitempty"` // values for the flow's (non-secret) inputs
	// CatchUp: a run missed while the app was closed or the computer
	// asleep runs once when it is back (otherwise the trigger waits for
	// its next time)
	CatchUp bool `json:"catchUp,omitempty"`
}

// Schedule is when a schedule trigger runs. Times are local time, written
// YYYY-MM-DDTHH:MM (dates) and HH:MM (times of day).
type Schedule struct {
	// Mode: once (At) | every (Every × Unit, from At or from when it is
	// set up) | daily (at Time) | weekly (at Time on Days)
	Mode  string   `json:"mode"`
	At    string   `json:"at,omitempty"`
	Every int      `json:"every,omitempty"`
	Unit  string   `json:"unit,omitempty"` // minutes | hours | days
	Time  string   `json:"time,omitempty"`
	Days  []string `json:"days,omitempty"` // mon tue wed thu fri sat sun
	// the end: no runs after Until; at most Count runs (0: no limit)
	Until string `json:"until,omitempty"`
	Count int    `json:"count,omitempty"`
}

// Schedule modes, units and days.
var (
	ScheduleModes = []string{"once", "every", "daily", "weekly"}
	ScheduleUnits = []string{"minutes", "hours", "days"}
	WeekDays      = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"} // time.Weekday order
)

// MinEveryMinutes is the shortest interval.
const MinEveryMinutes = 1

func parseLocal(s string, loc *time.Location) (time.Time, error) {
	for _, l := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(l, strings.TrimSpace(s), loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a date and time (YYYY-MM-DDTHH:MM)", s)
}

func parseClock(s string) (int, int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, 0, fmt.Errorf("%q is not a time of day (HH:MM)", s)
	}
	return t.Hour(), t.Minute(), nil
}

// Check reports what is wrong with a schedule ("" when it is fine).
func (s Schedule) Check() string {
	loc := time.Local
	switch s.Mode {
	case "once":
		if _, err := parseLocal(s.At, loc); err != nil {
			return "when: " + err.Error()
		}
	case "every":
		if s.Every < 1 || !slices.Contains(ScheduleUnits, s.Unit) {
			return "give how often: a number of minutes, hours or days"
		}
		if s.Unit == "minutes" && s.Every < MinEveryMinutes {
			return fmt.Sprintf("at most every %d minute", MinEveryMinutes)
		}
		if s.At != "" {
			if _, err := parseLocal(s.At, loc); err != nil {
				return "starting: " + err.Error()
			}
		}
	case "daily", "weekly":
		if _, _, err := parseClock(s.Time); err != nil {
			return "at: " + err.Error()
		}
		if s.Mode == "weekly" {
			if len(s.Days) == 0 {
				return "choose the days of the week"
			}
			for _, d := range s.Days {
				if !slices.Contains(WeekDays, d) {
					return fmt.Sprintf("%q is not a day (mon … sun)", d)
				}
			}
		}
	default:
		return "choose when it runs: once, every …, daily or weekly"
	}
	if s.Until != "" {
		if _, err := parseLocal(s.Until, loc); err != nil {
			return "until: " + err.Error()
		}
	}
	if s.Count < 0 {
		return "the number of runs cannot be negative"
	}
	return ""
}

// Next is the first run time strictly after `after`; ok is false when the
// schedule has no more runs (a time once in the past, or past Until).
// `from` anchors "every" schedules without a start (when they were set up).
func (s Schedule) Next(after, from time.Time) (time.Time, bool) {
	loc := after.Location()
	var t time.Time
	switch s.Mode {
	case "once":
		at, err := parseLocal(s.At, loc)
		if err != nil || !at.After(after) {
			return time.Time{}, false
		}
		t = at
	case "every":
		step := time.Duration(s.Every) * map[string]time.Duration{"minutes": time.Minute, "hours": time.Hour, "days": 24 * time.Hour}[s.Unit]
		if step <= 0 {
			return time.Time{}, false
		}
		start := from
		if s.At != "" {
			if at, err := parseLocal(s.At, loc); err == nil {
				start = at
			}
		}
		if start.After(after) {
			t = start
		} else {
			n := after.Sub(start)/step + 1
			t = start.Add(n * step)
		}
	case "daily", "weekly":
		h, m, err := parseClock(s.Time)
		if err != nil {
			return time.Time{}, false
		}
		y, mo, d := after.Date()
		for i := 0; i < 8; i++ {
			c := time.Date(y, mo, d+i, h, m, 0, 0, loc)
			if !c.After(after) {
				continue
			}
			if s.Mode == "weekly" && !slices.Contains(s.Days, WeekDays[c.Weekday()]) {
				continue
			}
			t = c
			break
		}
		if t.IsZero() {
			return time.Time{}, false
		}
	default:
		return time.Time{}, false
	}
	if s.Until != "" {
		if u, err := parseLocal(s.Until, loc); err == nil && t.After(u) {
			return time.Time{}, false
		}
	}
	return t, true
}

// Upcoming lists the next n run times after `after` (fewer when it ends).
func (s Schedule) Upcoming(after, from time.Time, n int) []time.Time {
	var out []time.Time
	t := after
	for len(out) < n {
		nx, ok := s.Next(t, from)
		if !ok {
			break
		}
		if s.Count > 0 && len(out) >= s.Count {
			break
		}
		out = append(out, nx)
		t = nx
	}
	return out
}

// Describe says a schedule in words.
func (s Schedule) Describe() string {
	end := ""
	if s.Until != "" {
		end += ", until " + strings.Replace(s.Until, "T", " ", 1)
	}
	if s.Count > 0 {
		end += fmt.Sprintf(", %d run%s at most", s.Count, map[bool]string{true: "", false: "s"}[s.Count == 1])
	}
	switch s.Mode {
	case "once":
		return "once, at " + strings.Replace(s.At, "T", " ", 1)
	case "every":
		u := strings.TrimSuffix(s.Unit, "s")
		every := fmt.Sprintf("every %d %ss", s.Every, u)
		if s.Every == 1 {
			every = "every " + u
		}
		if s.At != "" {
			every += ", from " + strings.Replace(s.At, "T", " ", 1)
		}
		return every + end
	case "daily":
		return "daily at " + s.Time + end
	case "weekly":
		return "every " + strings.Join(s.Days, ", ") + " at " + s.Time + end
	}
	return s.Mode
}

// Hash identifies a trigger's settings (a change starts its count again).
func (t Trigger) Hash() string {
	b, _ := json.Marshal(struct {
		S *Schedule
		I map[string]string
		C bool
	}{t.Schedule, t.Inputs, t.CatchUp})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// checkTriggers validates a flow's triggers.
func (f *Flow) checkTriggers(add func(node, level, format string, a ...any)) {
	seen := map[string]bool{}
	inputs := map[string]Input{}
	for _, in := range f.Inputs {
		inputs[in.Name] = in
	}
	for i, t := range f.Triggers {
		name := firstNonEmptyStr(t.Name, fmt.Sprintf("trigger %d", i+1))
		switch {
		case !idOK(t.ID):
			add("start", "error", "Trigger %s needs an id (letters, digits, -).", name)
		case seen[t.ID]:
			add("start", "error", "Two triggers have the id %s.", t.ID)
		}
		seen[t.ID] = true
		if !slices.Contains(TriggerKinds, t.Kind) {
			add("start", "error", "Trigger %s: unknown kind %q.", name, t.Kind)
			continue
		}
		if t.Kind == "schedule" {
			if t.Schedule == nil {
				add("start", "error", "Schedule %s: say when it runs.", name)
				continue
			}
			if p := t.Schedule.Check(); p != "" {
				add("start", "error", "Schedule %s: %s.", name, p)
			}
		}
		for k := range t.Inputs {
			in, ok := inputs[k]
			if !ok {
				add("start", "warning", "Trigger %s gives a value for %q, which is not an input of this flow.", name, k)
			} else if in.Type == "secret" {
				add("start", "error", "Trigger %s: %q is a secret; its value is saved encrypted on this computer, not in the flow.", name, k)
			}
		}
		if !t.Enabled {
			continue
		}
		for _, in := range f.Inputs {
			if in.Required && in.Type != "secret" && strings.TrimSpace(in.Default) == "" && len(in.Files) == 0 && strings.TrimSpace(t.Inputs[in.Name]) == "" {
				add("start", "warning", "Trigger %s: the required input %q has no value, so its runs will stop. Give it a value in the trigger or a default.", name, firstNonEmptyStr(in.Label, in.Name))
			}
		}
	}
}

func idOK(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}
