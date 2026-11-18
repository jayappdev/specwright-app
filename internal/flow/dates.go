package flow

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Date inputs. A "date" input's value is YYYY-MM-DD; a "datetime" input's
// is YYYY-MM-DDTHH:MM (local time, as a date-time picker gives it; seconds
// are kept when they are not zero). Both also take a relative value, handy
// as a default for scheduled runs, worked out when the run starts:
//
//	today, today-7d, today+1d           (date: days; datetime: midnight)
//	now, now-2h, now+30m, now-1d        (datetime: minutes, hours, days)
//
// The value given to the steps is always the absolute one.

const (
	DateLayout     = "2006-01-02"
	DateTimeLayout = "2006-01-02T15:04"
)

var relDateRE = regexp.MustCompile(`^(today|now)\s*(?:([+-])\s*(\d{1,5})\s*([mhdw])?)?$`)

// DateInput checks a date or datetime input's value and returns it in its
// standard form ("" stays ""). now is when the run starts.
func DateInput(typ, s string, now time.Time) (string, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return "", nil
	}
	if m := relDateRE.FindStringSubmatch(strings.ToLower(t)); m != nil {
		base := now.Truncate(time.Minute) // to the minute, as a picker shows it
		if m[1] == "today" {
			y, mo, d := now.Date()
			base = time.Date(y, mo, d, 0, 0, 0, 0, now.Location())
		}
		if m[2] != "" {
			n, _ := strconv.Atoi(m[3])
			if m[2] == "-" {
				n = -n
			}
			unit := m[4]
			if unit == "" {
				unit = "d"
			}
			if m[1] == "today" && (unit == "m" || unit == "h") {
				return "", fmt.Errorf("%q: use days or weeks with today (or now-%s%s)", t, m[3], unit)
			}
			switch unit {
			case "m":
				base = base.Add(time.Duration(n) * time.Minute)
			case "h":
				base = base.Add(time.Duration(n) * time.Hour)
			case "d":
				base = base.AddDate(0, 0, n)
			case "w":
				base = base.AddDate(0, 0, 7*n)
			}
		}
		if typ == "date" {
			return base.Format(DateLayout), nil
		}
		return dateTimeString(base), nil
	}
	if typ == "date" {
		for _, l := range []string{DateLayout, DateTimeLayout, "2006-01-02T15:04:05", time.RFC3339} {
			if d, err := time.ParseInLocation(l, t, now.Location()); err == nil {
				return d.Format(DateLayout), nil
			}
		}
		return "", fmt.Errorf("%q is not a date (YYYY-MM-DD, or today, today-7d)", clip(t, 40))
	}
	if d, err := time.Parse(time.RFC3339, t); err == nil { // with a zone: as local time
		return dateTimeString(d.In(now.Location())), nil
	}
	for _, l := range []string{DateTimeLayout, "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02 15:04:05", DateLayout} {
		if d, err := time.ParseInLocation(l, t, now.Location()); err == nil {
			return dateTimeString(d), nil
		}
	}
	return "", fmt.Errorf("%q is not a date and time (YYYY-MM-DDTHH:MM, or now, now-2h, today)", clip(t, 40))
}

func dateTimeString(t time.Time) string {
	if t.Second() != 0 {
		return t.Format("2006-01-02T15:04:05")
	}
	return t.Format(DateTimeLayout)
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
