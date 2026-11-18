package flow

import (
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
		{Schedule{Mode: "every", Every: 15, Unit: "minutes"}, "2026-11-18T10:45"},     // from when it was set up
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
