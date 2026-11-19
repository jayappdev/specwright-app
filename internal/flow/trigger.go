package flow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Triggers start a flow without anyone pressing Run. A trigger belongs to
// the flow (it is in the flow file, set up in the Flow Designer on the
// Inputs step) and gives the values of the flow's inputs for the runs it
// starts. The first kind is "schedule"; others (a file change, a webhook)
// can follow with their own settings. Kinds:
//
//   - schedule: at a time, or every so often (Schedule);
//   - flow: when another flow's run finishes (After), to chain flows;
//   - files: when files in the project change (Files).
//
// A trigger's input values can use what started the run:
// {{trigger.flow}}, {{trigger.run}}, {{trigger.status}}, {{trigger.output}}
// and {{trigger.error}} (flow), {{trigger.files}} and {{trigger.file}}
// (files), and {{trigger.name}} and {{trigger.kind}} (any).
//
// Secret values are not in the flow file: they are kept encrypted on the
// computer that runs the trigger (core/triggers.go), as is the trigger's
// state (next run, last run, how many runs).

// TriggerKinds are the kinds of trigger.
var TriggerKinds = []string{"schedule", "flow", "files"}

// TriggerVars are the {{trigger.*}} values each kind gives its runs.
var TriggerVars = map[string][]string{
	"schedule": {"name", "kind"},
	"flow":     {"name", "kind", "flow", "run", "status", "output", "error"},
	"files":    {"name", "kind", "files", "file"},
}

// AfterFlow: a "flow" trigger starts when a run of Flow ends as On says.
type AfterFlow struct {
	Flow string   `json:"flow"`         // the flow's id
	On   []string `json:"on,omitempty"` // done | failed | stopped (default done)
}

// AfterOutcomes are how a run can end, for AfterFlow.On.
var AfterOutcomes = []string{"done", "failed", "stopped"}

// Outcomes: the ends it starts on (default: done).
func (a AfterFlow) Outcomes() []string {
	if len(a.On) == 0 {
		return []string{"done"}
	}
	return a.On
}

// Matches says whether a run that ended with status starts it ("interrupted"
// counts as stopped).
func (a AfterFlow) Matches(status string) bool {
	if status == "interrupted" {
		status = "stopped"
	}
	return slices.Contains(a.Outcomes(), status)
}

// FileWatch: a "files" trigger starts when files matching Paths (and none
// of Ignore) change as Ops says, once the changes have been quiet for Wait
// seconds (a burst of saves starts one run). Patterns are relative to the
// project, or absolute (/Users/me/Downloads/in/**, ~/Downloads/in): a
// folder outside the project is then watched too. * is any name part, **
// any folders, a pattern without / matches a file name in any folder
// (*.java), and a pattern without * is that file, or everything in that
// folder.
type FileWatch struct {
	Paths  []string `json:"paths"`
	Ignore []string `json:"ignore,omitempty"`
	Ops    []string `json:"ops,omitempty"`  // created | modified | deleted (default all)
	Wait   int      `json:"wait,omitempty"` // seconds of quiet before it runs (default DefaultFileWait)
	// WhileClosed: changes made while the app was closed count too, when
	// it opens the project again (the last scan is kept for this)
	WhileClosed bool `json:"whileClosed,omitempty"`
}

// FileOps are the kinds of file change; DefaultFileWait the default quiet.
var FileOps = []string{"created", "modified", "deleted"}

const DefaultFileWait = 5

// Quiet is how long the changes must be quiet before a run.
func (w FileWatch) Quiet() time.Duration {
	if w.Wait <= 0 {
		return DefaultFileWait * time.Second
	}
	return time.Duration(w.Wait) * time.Second
}

// Matches says whether a change counts: rel is its path relative to the
// project ("" for a file outside it), abs its absolute path. SpecWright's
// own folder (.codegen: flows, run results) never counts, so a flow's
// results do not start it again.
func (w FileWatch) Matches(rel, abs, op string) bool {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	abs = filepath.ToSlash(abs)
	if rel != "" && (rel == ".codegen" || strings.HasPrefix(rel, ".codegen/")) {
		return false
	}
	if len(w.Ops) > 0 && !slices.Contains(w.Ops, op) {
		return false
	}
	match := func(g string) bool {
		g = strings.TrimSpace(g)
		switch {
		case IsAbsPattern(g):
			return abs != "" && GlobMatch(ExpandPattern(g), abs)
		case !strings.Contains(g, "/"): // a name, anywhere
			return (rel != "" && GlobMatch(g, rel)) || (abs != "" && GlobMatch(g, abs))
		default:
			return rel != "" && GlobMatch(g, rel)
		}
	}
	hit := false
	for _, g := range w.Paths {
		hit = hit || match(g)
	}
	if !hit {
		return false
	}
	for _, g := range w.Ignore {
		if match(g) {
			return false
		}
	}
	return true
}

// IsAbsPattern: a pattern from the top of the disk (/…) or the home folder (~/…).
func IsAbsPattern(g string) bool {
	g = strings.TrimSpace(g)
	return filepath.IsAbs(g) || strings.HasPrefix(g, "/") || g == "~" || strings.HasPrefix(g, "~/")
}

// ExpandPattern puts the home folder for a leading ~.
func ExpandPattern(g string) string {
	g = strings.TrimSpace(g)
	if g == "~" || strings.HasPrefix(g, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			g = home + g[1:]
		}
	}
	return filepath.ToSlash(g)
}

// GlobBase is the part of a pattern before its first wildcard: the folder
// (or file) to watch for it.
func GlobBase(g string) string {
	g = strings.TrimRight(ExpandPattern(g), "/")
	parts := strings.Split(g, "/")
	for i, p := range parts {
		if strings.ContainsAny(p, "*?{[") {
			parts = parts[:i]
			break
		}
	}
	b := strings.Join(parts, "/")
	if b == "" && strings.HasPrefix(g, "/") {
		b = "/"
	}
	return b
}

// GlobMatch matches a slash path against a pattern (see FileWatch).
func GlobMatch(pattern, p string) bool {
	pattern = strings.TrimPrefix(strings.TrimSpace(filepath.ToSlash(pattern)), "./")
	if pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/") { // a folder: everything in it
		pattern += "**"
	}
	if !strings.Contains(pattern, "/") {
		pattern = "**/" + pattern
	}
	re, err := globRE(pattern)
	if err == nil && re.MatchString(p) {
		return true
	}
	// no wildcards: that file, or everything in that folder
	if !strings.ContainsAny(pattern, "*?{[") {
		re, err = globRE(strings.TrimRight(pattern, "/") + "/**")
		return err == nil && re.MatchString(p)
	}
	return false
}

func globRE(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case strings.HasPrefix(pattern[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '{':
			j := strings.IndexByte(pattern[i:], '}')
			if j < 0 {
				return nil, fmt.Errorf("%q: a { without }", pattern)
			}
			var alts []string
			for _, a := range strings.Split(pattern[i+1:i+j], ",") {
				alts = append(alts, regexp.QuoteMeta(a))
			}
			b.WriteString("(?:" + strings.Join(alts, "|") + ")")
			i += j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// Trigger is one way a flow starts by itself.
type Trigger struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind"` // TriggerKinds
	Name     string            `json:"name,omitempty"`
	Enabled  bool              `json:"enabled"`
	Schedule *Schedule         `json:"schedule,omitempty"` // kind schedule
	After    *AfterFlow        `json:"after,omitempty"`    // kind flow
	Files    *FileWatch        `json:"files,omitempty"`    // kind files
	Inputs   map[string]string `json:"inputs,omitempty"`   // values for the flow's (non-secret) inputs
	// CatchUp: a run missed while the app was closed or the computer
	// asleep runs once when it is back (otherwise the trigger waits for
	// its next time)
	CatchUp bool `json:"catchUp,omitempty"`
	// IfBusy: when a run is due while this flow is still running (or too
	// many triggered runs are going): "wait" for its turn (the default) or
	// "skip" this time
	IfBusy string `json:"ifBusy,omitempty"`
	// Retries: a run of this trigger that fails is tried again this many
	// times (0–3), RetryWait minutes apart (default DefaultRetryWait)
	Retries   int `json:"retries,omitempty"`
	RetryWait int `json:"retryWait,omitempty"`
}

// Busy choices, the most retries, and the default wait between them.
const (
	MaxRetries       = 3
	DefaultRetryWait = 5
)

// Skips says the trigger skips a run that is due while it cannot start.
func (t Trigger) Skips() bool { return t.IfBusy == "skip" }

// RetryAfter is the wait before a retry.
func (t Trigger) RetryAfter() time.Duration {
	if t.RetryWait <= 0 {
		return DefaultRetryWait * time.Minute
	}
	return time.Duration(t.RetryWait) * time.Minute
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
		A *AfterFlow `json:",omitempty"`
		F *FileWatch `json:",omitempty"`
	}{t.Schedule, t.Inputs, t.CatchUp, t.After, t.Files})
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
		if t.Kind == "flow" {
			switch {
			case t.After == nil || strings.TrimSpace(t.After.Flow) == "":
				add("start", "error", "Trigger %s: choose the flow whose runs start this one.", name)
			case t.After.Flow == f.ID:
				add("start", "error", "Trigger %s: a flow cannot start after itself (it would run forever).", name)
			}
			if t.After != nil {
				for _, o := range t.After.On {
					if !slices.Contains(AfterOutcomes, o) {
						add("start", "error", "Trigger %s: %q is not how a run ends (done, failed, stopped).", name, o)
					}
				}
			}
		}
		if t.Kind == "files" {
			if t.Files == nil || len(t.Files.Paths) == 0 {
				add("start", "error", "Trigger %s: say which files to watch (e.g. src/**/*.java).", name)
			} else {
				for _, g := range append(append([]string{}, t.Files.Paths...), t.Files.Ignore...) {
					if _, err := globRE(g); err != nil || strings.TrimSpace(g) == "" {
						add("start", "error", "Trigger %s: %q is not a file pattern.", name, g)
					}
				}
				for _, o := range t.Files.Ops {
					if !slices.Contains(FileOps, o) {
						add("start", "error", "Trigger %s: %q is not a file change (created, modified, deleted).", name, o)
					}
				}
				if t.Files.Wait < 0 || t.Files.Wait > 3600 {
					add("start", "error", "Trigger %s: wait 0 to 3600 seconds.", name)
				}
			}
		}
		if t.IfBusy != "" && t.IfBusy != "wait" && t.IfBusy != "skip" {
			add("start", "error", "Trigger %s: when the flow is busy it waits or skips, not %q.", name, t.IfBusy)
		}
		if t.Retries < 0 || t.Retries > MaxRetries {
			add("start", "error", "Trigger %s: retries 0 to %d.", name, MaxRetries)
		}
		if t.RetryWait < 0 || t.RetryWait > 1440 {
			add("start", "error", "Trigger %s: wait 0 to 1440 minutes between retries.", name)
		}
		for k, v := range t.Inputs {
			for _, m := range triggerVarRE.FindAllStringSubmatch(v, -1) {
				if !slices.Contains(TriggerVars[t.Kind], m[1]) {
					add("start", "warning", "Trigger %s: {{trigger.%s}} in %q is empty for this kind of trigger (it has %s).", name, m[1], k, strings.Join(TriggerVars[t.Kind], ", "))
				}
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

// TriggerIDOK says whether s can be a trigger's id.
func TriggerIDOK(s string) bool { return idOK(s) }

var triggerVarRE = regexp.MustCompile(`\{\{\s*trigger\.([a-z]+)\s*\}\}`)

// FillTriggerVars puts what started a run into a trigger's input values
// ({{trigger.x}}); a value the run does not have becomes empty.
func FillTriggerVars(s string, vars map[string]string) string {
	return triggerVarRE.ReplaceAllStringFunc(s, func(m string) string {
		return vars[triggerVarRE.FindStringSubmatch(m)[1]]
	})
}

// Describe says a trigger in words; flowName names a flow id (for
// "after" triggers).
func (t Trigger) Describe(flowName func(id string) string) string {
	switch t.Kind {
	case "schedule":
		if t.Schedule != nil {
			return t.Schedule.Describe()
		}
	case "flow":
		if t.After == nil || strings.TrimSpace(t.After.Flow) == "" {
			return "after another flow (none chosen yet)"
		}
		if t.After != nil {
			n := t.After.Flow
			if flowName != nil {
				n = flowName(n)
			}
			ends := map[string]string{"done": "completes", "failed": "fails", "stopped": "is stopped"}
			var w []string
			for _, o := range t.After.Outcomes() {
				w = append(w, firstNonEmptyStr(ends[o], o))
			}
			return fmt.Sprintf("after %s %s", n, strings.Join(w, " or "))
		}
	case "files":
		if t.Files != nil {
			s := "when " + strings.Join(t.Files.Paths, ", ")
			if len(t.Files.Ops) > 0 && len(t.Files.Ops) < len(FileOps) {
				s += " is " + strings.Join(t.Files.Ops, " or ")
			} else {
				s += " changes"
			}
			if len(t.Files.Ignore) > 0 {
				s += " (not " + strings.Join(t.Files.Ignore, ", ") + ")"
			}
			return s
		}
	}
	return t.Kind
}
