package core

// schedule_flow: how an agent schedules a flow. It lists, sets (adds or
// changes) and removes a flow's schedule triggers, the same ones the Flow
// Designer sets up on the Inputs step: the flow file is saved in place (the
// schedule is part of the flow) and the scheduler picks the change up on its
// next check. Secret values cannot be given here: the user enters them in
// the Flow Designer, where they are saved encrypted on this computer.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// ScheduleFlowTool is the built-in agents schedule flows with.
const ScheduleFlowTool = "schedule_flow"

func scheduleFlowInfo() extension.BuiltinInfo {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	return extension.BuiltinInfo{
		Name:  ScheduleFlowTool,
		Title: "Schedule a flow",
		Group: "flows",
		Description: "List, set or remove the schedules of a SpecWright flow (the triggers the Flow Designer sets up on its Inputs step). " +
			"A schedule runs the flow by itself: once at a time, every N minutes/hours/days, daily or weekly at a time of day; it can end on a date (until) or after a number of runs (count). " +
			"Times are the computer's local time: dates YYYY-MM-DDTHH:MM, times of day HH:MM. inputs gives values for the flow's non-secret inputs. " +
			"Secret inputs cannot be given here: tell the user to enter them in the Flow Designer (Inputs step, the schedule's Secrets), where they are saved encrypted. " +
			"action set with a trigger_id that exists changes only the fields you give; without one it adds a schedule. Runs started by a schedule show as \"scheduled\" in Run flows and Run History.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"flow_id":    str("The flow's id (its file is .codegen/flows/<id>.json)."),
				"action":     map[string]any{"type": "string", "enum": []string{"list", "set", "remove"}, "description": "list (the default when nothing else is given), set (add or change), remove"},
				"trigger_id": str("The schedule to change or remove (from list). Empty with set: add a new one."),
				"name":       str("A name for the schedule, e.g. Every morning."),
				"enabled":    map[string]any{"type": "boolean", "description": "On (default true for a new schedule)."},
				"schedule": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"mode":  map[string]any{"type": "string", "enum": flow.ScheduleModes},
						"at":    str("once: when (YYYY-MM-DDTHH:MM); every: when it starts (optional)"),
						"every": map[string]any{"type": "integer", "minimum": 1, "description": "every: how many units"},
						"unit":  map[string]any{"type": "string", "enum": flow.ScheduleUnits},
						"time":  str("daily/weekly: the time of day, HH:MM"),
						"days":  map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": flow.WeekDays}, "description": "weekly: the days"},
						"until": str("No runs after this (YYYY-MM-DDTHH:MM); empty: no end date"),
						"count": map[string]any{"type": "integer", "minimum": 0, "description": "At most this many runs (0: no limit)"},
					},
					"required": []string{"mode"},
				},
				"inputs":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Values for the flow's (non-secret) inputs, by input name."},
				"catch_up": map[string]any{"type": "boolean", "description": "A run missed while the app was closed or the computer asleep runs once when it is back."},
			},
			"required": []string{"flow_id"},
		},
	}
}

// agentScheduleFlow implements schedule_flow.
func (h *Hub) agentScheduleFlow(args map[string]any) (string, error) {
	root, err := h.need()
	if err != nil {
		return "", err
	}
	str := func(k string) string { s, _ := args[k].(string); return strings.TrimSpace(s) }
	id := str("flow_id")
	if id == "" {
		return "", errors.New("give flow_id: the id of the flow to schedule (its file is .codegen/flows/<id>.json)")
	}
	f, err := flow.Load(root, id)
	if err != nil {
		return "", fmt.Errorf("no flow %s: give flow_id as a flow's id (.codegen/flows/<id>.json)", id)
	}
	action := str("action")
	if action == "" {
		action = "list"
		if args["schedule"] != nil || str("trigger_id") != "" && (args["name"] != nil || args["enabled"] != nil || args["inputs"] != nil) {
			action = "set"
		}
	}
	tid := str("trigger_id")
	idx := -1
	for i, t := range f.Triggers {
		if t.ID == tid {
			idx = i
		}
	}
	switch action {
	case "list":
		return h.describeTriggers(f, "")
	case "remove":
		if idx < 0 {
			return "", fmt.Errorf("the flow %s has no schedule %q (call schedule_flow with action list to see them)", id, tid)
		}
		f.Triggers = append(f.Triggers[:idx], f.Triggers[idx+1:]...)
		if _, err := h.SaveFlow(*f); err != nil {
			return "", err
		}
		return h.describeTriggers(f, fmt.Sprintf("Removed the schedule %s.", tid))
	case "set":
	default:
		return "", fmt.Errorf("action is list, set or remove, not %q", action)
	}

	var t flow.Trigger
	if idx >= 0 {
		t = f.Triggers[idx]
	} else {
		if tid != "" && !flow.TriggerIDOK(tid) {
			return "", fmt.Errorf("trigger_id %q: use letters, digits and - (or leave it empty for a new schedule)", tid)
		}
		t = flow.Trigger{ID: tid, Kind: "schedule", Enabled: true}
	}
	if n := str("name"); n != "" {
		t.Name = n
	}
	if v, ok := args["enabled"].(bool); ok {
		t.Enabled = v
	}
	if v, ok := args["catch_up"].(bool); ok {
		t.CatchUp = v
	}
	if raw, ok := args["schedule"]; ok && raw != nil {
		var s flow.Schedule
		b, _ := json.Marshal(raw)
		if s2, ok := raw.(string); ok {
			b = []byte(jsonReply(s2))
		}
		if err := json.Unmarshal(b, &s); err != nil {
			return "", fmt.Errorf("schedule: %v (give an object: {\"mode\": \"daily\", \"time\": \"09:00\"})", err)
		}
		s.At, s.Until = strings.TrimSpace(s.At), strings.TrimSpace(s.Until)
		for i, d := range s.Days {
			s.Days[i] = strings.ToLower(strings.TrimSpace(d))
			if len(s.Days[i]) > 3 {
				s.Days[i] = s.Days[i][:3]
			}
		}
		t.Schedule = &s
	}
	if t.Schedule == nil {
		return "", errors.New(`give "schedule": when it runs, e.g. {"mode": "daily", "time": "09:00"}`)
	}
	if p := t.Schedule.Check(); p != "" {
		return "", fmt.Errorf("schedule: %s", p)
	}
	if raw, ok := args["inputs"].(map[string]any); ok {
		secret := map[string]bool{}
		known := map[string]bool{}
		for _, in := range f.Inputs {
			known[in.Name] = true
			secret[in.Name] = in.Type == "secret"
		}
		vals := map[string]string{}
		for k, v := range raw {
			switch {
			case !known[k]:
				return "", fmt.Errorf("%q is not an input of the flow %s", k, id)
			case secret[k]:
				return "", fmt.Errorf("%q is a secret: the user enters it in the Flow Designer (Inputs step, the schedule's Secrets), where it is saved encrypted; do not pass it here", k)
			}
			if v != nil {
				vals[k] = fmt.Sprint(v)
			}
		}
		if len(vals) == 0 {
			vals = nil
		}
		t.Inputs = vals
	}
	if t.ID == "" {
		base := flow.Slug(firstNonEmpty(t.Name, "schedule"))
		if len(base) > 32 || !flow.TriggerIDOK(base) {
			base = "schedule"
		}
		t.ID = base
		for i := 2; ; i++ {
			taken := false
			for _, x := range f.Triggers {
				taken = taken || x.ID == t.ID
			}
			if !taken {
				break
			}
			t.ID = fmt.Sprintf("%s-%d", base, i)
		}
	}
	if t.Name == "" {
		t.Name = "Schedule " + fmt.Sprint(len(f.Triggers)+1)
	}
	verb := "Changed"
	if idx >= 0 {
		f.Triggers[idx] = t
	} else {
		f.Triggers = append(f.Triggers, t)
		verb = "Added"
	}
	st, err := h.SaveFlow(*f)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("%s the schedule %s (%s) of %q.", verb, t.ID, t.Schedule.Describe(), f.Name)
	var probs []string
	for _, is := range st.Issues {
		if is.Node == "start" && (strings.Contains(is.Message, "Trigger") || strings.Contains(is.Message, "Schedule")) {
			probs = append(probs, is.Level+": "+is.Message)
		}
	}
	if len(probs) > 0 {
		msg += "\nPROBLEMS:\n- " + strings.Join(probs, "\n- ")
	}
	return h.describeTriggers(st.Flow, msg)
}

// describeTriggers: a flow's schedules in words, for the agent.
func (h *Hub) describeTriggers(f *flow.Flow, head string) (string, error) {
	views, err := h.FlowTriggers(f.ID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if head != "" {
		b.WriteString(head + "\n")
	}
	if len(views) == 0 {
		fmt.Fprintf(&b, "The flow %q has no schedules.", f.Name)
		return b.String(), nil
	}
	fmt.Fprintf(&b, "Schedules of %q:", f.Name)
	for _, v := range views {
		t := v.Trigger
		on := "on"
		if !t.Enabled {
			on = "off"
		}
		fmt.Fprintf(&b, "\n- %s (%s, %s): %s", t.ID, firstNonEmpty(t.Name, t.ID), on, v.Describe)
		if len(t.Inputs) > 0 {
			ins, _ := json.Marshal(t.Inputs)
			fmt.Fprintf(&b, "; inputs %s", ins)
		}
		if t.CatchUp {
			b.WriteString("; catches up missed runs")
		}
		switch {
		case v.State.Done:
			b.WriteString("; finished (no more runs)")
		case len(v.Upcoming) > 0 && t.Enabled:
			var up []string
			for _, u := range v.Upcoming {
				up = append(up, u.Local().Format("Mon 2006-01-02 15:04"))
			}
			b.WriteString("; next: " + strings.Join(up, ", "))
		}
		if v.State.Runs > 0 {
			fmt.Fprintf(&b, "; %d run(s), last %s", v.State.Runs, v.State.LastStatus)
		}
		var missing []string
		for _, s := range v.SecretsNeeded {
			if !slicesContains(v.SecretsSaved, s) {
				missing = append(missing, s)
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(&b, "; secrets not entered yet: %s (the user enters them in the Flow Designer, Inputs step)", strings.Join(missing, ", "))
		}
	}
	return b.String(), nil
}
