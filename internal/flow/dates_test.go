package flow

import (
	"strings"
	"testing"
	"time"
)

func TestDateInput(t *testing.T) {
	now := time.Date(2026, 11, 17, 14, 35, 42, 0, time.Local) // "now" drops the seconds
	for _, c := range []struct{ typ, in, want string }{
		{"date", "", ""},
		{"date", "2026-03-04", "2026-03-04"},
		{"date", "2026-03-04T10:00", "2026-03-04"},
		{"date", "today", "2026-11-17"},
		{"date", "today-7d", "2026-11-10"},
		{"date", "Today + 2", "2026-11-19"},
		{"date", "today-1w", "2026-11-10"},
		{"datetime", "2026-03-04T09:05", "2026-03-04T09:05"},
		{"datetime", "2026-03-04 09:05:30", "2026-03-04T09:05:30"},
		{"datetime", "2026-03-04", "2026-03-04T00:00"},
		{"datetime", "now", "2026-11-17T14:35"},
		{"datetime", "now-2h", "2026-11-17T12:35"},
		{"datetime", "now+30m", "2026-11-17T15:05"},
		{"datetime", "today", "2026-11-17T00:00"},
		{"datetime", "today-1d", "2026-11-16T00:00"},
	} {
		got, err := DateInput(c.typ, c.in, now)
		if err != nil || got != c.want {
			t.Errorf("%s %q: %q %v, want %q", c.typ, c.in, got, err, c.want)
		}
	}
	for _, c := range []struct{ typ, in string }{
		{"date", "next tuesday"}, {"date", "2026-13-01"}, {"datetime", "2026-03-04T25:00"}, {"date", "today-2h"},
	} {
		if v, err := DateInput(c.typ, c.in, now); err == nil {
			t.Errorf("%s %q: want an error, got %q", c.typ, c.in, v)
		}
	}
}

func TestDateInputDefaultChecked(t *testing.T) {
	f := New("d", "d")
	f.Inputs = []Input{{Name: "since", Label: "Since", Type: "date", Default: "last week"}, {Name: "at", Label: "At", Type: "datetime", Default: "now-1h"}}
	var msgs []string
	for _, is := range Validate(f, nil) {
		msgs = append(msgs, is.Message)
	}
	all := strings.Join(msgs, "\n")
	if !strings.Contains(all, `Input "since": the default value "last week" is not a date`) || strings.Contains(all, `"at"`) {
		t.Fatal(all)
	}
}
