package flow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02T15:04", s, time.Local)
	return t
}

func TestScheduleNext(t *testing.T) {
	now := at("2026-11-18T10:30") // a Wednesday
	from := at("2026-11-18T10:00")
	cases := []struct {
		s    Schedule
		want string // "" = no more runs
	}{
		{Schedule{Mode: "once", At: "2026-11-18T12:00"}, "2026-11-18T12:00"},
		{Schedule{Mode: "once", At: "2026-11-18T09:00"}, ""},
		{Schedule{Mode: "every", Every: 15, Unit: "minutes"}, "2026-11-18T10:45"}, // from when it was set up
		{Schedule{Mode: "every", Every: 2, Unit: "hours", At: "2026-11-18T07:00"}, "2026-11-18T11:00"},
		{Schedule{Mode: "every", Every: 1, Unit: "days", At: "2026-11-20T08:00"}, "2026-11-20T08:00"}, // starts later
		{Schedule{Mode: "daily", Time: "09:00"}, "2026-11-19T09:00"},
		{Schedule{Mode: "daily", Time: "11:00"}, "2026-11-18T11:00"},
		{Schedule{Mode: "weekly", Time: "09:00", Days: []string{"mon", "fri"}}, "2026-11-20T09:00"},
		{Schedule{Mode: "daily", Time: "09:00", Until: "2026-11-19T08:00"}, ""}, // ended
	}
	for _, c := range cases {
		got, ok := c.s.Next(now, from)
		g := ""
		if ok {
			g = got.Format("2006-01-02T15:04")
		}
		if g != c.want {
			t.Errorf("%s: %q, want %q", c.s.Describe(), g, c.want)
		}
	}
	up := Schedule{Mode: "every", Every: 1, Unit: "hours", Count: 3}.Upcoming(now, from, 10)
	if len(up) != 3 || up[2].Format("15:04") != "13:00" {
		t.Fatalf("%v", up)
	}
}

func TestTriggerChecks(t *testing.T) {
	f := New("x", "x")
	f.Inputs = []Input{{Name: "project", Type: "text", Required: true}, {Name: "token", Type: "secret"}}
	f.Triggers = []Trigger{
		{ID: "nightly", Kind: "schedule", Enabled: true, Schedule: &Schedule{Mode: "daily", Time: "25:00"}, Inputs: map[string]string{"token": "x", "other": "y"}},
		{ID: "nightly", Kind: "webhook"},
	}
	var msgs []string
	for _, is := range Validate(f, nil) {
		msgs = append(msgs, is.Level+": "+is.Message)
	}
	all := strings.Join(msgs, "\n")
	for _, want := range []string{"not a time of day", "is a secret", "not an input of this flow", "required input", "Two triggers", "unknown kind"} {
		if !strings.Contains(all, want) {
			t.Errorf("want %q in:\n%s", want, all)
		}
	}
}

func TestGlobMatch(t *testing.T) {
	for _, c := range []struct {
		g, p string
		ok   bool
	}{
		{"*.java", "src/main/A.java", true},
		{"*.java", "A.java", true},
		{"src/**/*.java", "src/A.java", true},
		{"src/**/*.java", "src/a/b/A.java", true},
		{"src/**/*.java", "lib/A.java", false},
		{"src/*.java", "src/a/A.java", false},
		{"docs/", "docs/x/y.md", true},
		{"api/openapi.{yaml,yml}", "api/openapi.yml", true},
		{"a?.md", "ab.md", true},
		{"a?.md", "abc.md", false},
	} {
		if GlobMatch(c.g, c.p) != c.ok {
			t.Errorf("GlobMatch(%q, %q) != %v", c.g, c.p, c.ok)
		}
	}
	w := FileWatch{Paths: []string{"**"}, Ignore: []string{"*.log"}, Ops: []string{"modified"}}
	if w.Matches(".codegen/runs/x.md", "/p/.codegen/runs/x.md", "modified") || w.Matches("a.log", "/p/a.log", "modified") || w.Matches("a.md", "/p/a.md", "created") || !w.Matches("a.md", "/p/a.md", "modified") {
		t.Fatal("FileWatch.Matches")
	}
	// absolute patterns: a folder outside the project, ~ for the home folder
	home, _ := os.UserHomeDir()
	ext := FileWatch{Paths: []string{"/Users/me/Downloads/testtrigger/**", "~/inbox", "/data/report.csv"}, Ignore: []string{"*.tmp"}}
	for _, c := range []struct {
		rel, abs string
		ok       bool
	}{
		{"", "/Users/me/Downloads/testtrigger/a.txt", true},
		{"", "/Users/me/Downloads/testtrigger/sub/b.txt", true},
		{"", "/Users/me/Downloads/testtrigger/x.tmp", false},
		{"", "/Users/me/Downloads/other.txt", false},
		{"", filepath.ToSlash(filepath.Join(home, "inbox", "n.md")), true},
		{"", "/data/report.csv", true},
		{"report.csv", "/p/report.csv", false}, // a relative path does not match an absolute pattern
	} {
		if ext.Matches(c.rel, c.abs, "created") != c.ok {
			t.Errorf("Matches(%q, %q) != %v", c.rel, c.abs, c.ok)
		}
	}
	if GlobBase("/Users/me/Downloads/testtrigger/**") != "/Users/me/Downloads/testtrigger" || GlobBase("/a/b/*.csv") != "/a/b" || GlobBase("/data/report.csv") != "/data/report.csv" || GlobBase("/**") != "/" {
		t.Fatal("GlobBase")
	}
	if got := FillTriggerVars("{{trigger.flow}} {{ trigger.files }}{{trigger.nope}}", map[string]string{"flow": "a", "files": "x"}); got != "a x" {
		t.Fatal(got)
	}
	if !(AfterFlow{}).Matches("done") || (AfterFlow{}).Matches("failed") || !(AfterFlow{On: []string{"stopped"}}).Matches("interrupted") {
		t.Fatal("AfterFlow.Matches")
	}
}

func TestFlowAndFilesTriggerChecks(t *testing.T) {
	f := New("a", "A")
	f.Triggers = []Trigger{
		{ID: "self", Kind: "flow", Enabled: true, After: &AfterFlow{Flow: "a"}},
		{ID: "none", Kind: "flow", Enabled: true},
		{ID: "bad-on", Kind: "flow", Enabled: true, After: &AfterFlow{Flow: "b", On: []string{"maybe"}}},
		{ID: "no-paths", Kind: "files", Enabled: true, Files: &FileWatch{}},
		{ID: "bad-glob", Kind: "files", Enabled: true, Files: &FileWatch{Paths: []string{"src/{a"}, Ops: []string{"renamed"}}},
		{ID: "var", Kind: "files", Enabled: true, Files: &FileWatch{Paths: []string{"*.md"}}, Inputs: map[string]string{"x": "{{trigger.output}}"}},
	}
	var msgs []string
	for _, is := range Validate(f, nil) {
		msgs = append(msgs, is.Level+": "+is.Message)
	}
	all := strings.Join(msgs, "\n")
	for _, want := range []string{"cannot start after itself", "choose the flow", `"maybe" is not how a run ends`, "say which files", `"src/{a" is not a file pattern`, `"renamed" is not a file change`, "{{trigger.output}}"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
}
