// DateTimeField: a date and a time, as two fields, giving one value
// "YYYY-MM-DDTHH:MM" (local time). The browser's datetime-local picker is
// not used: in WebKit (the desktop app) it shows a picked date while its
// value stays "" until the time is filled in too, so a form saw an empty
// field that looked filled. Here a date alone is a value (00:00 until a time
// is chosen), and what is typed in one part is never lost while the other is
// empty.
import { useEffect, useState } from "react";

const split = (v) => {
  const s = String(v || "");
  const m = s.match(/^(\d{4}-\d{2}-\d{2})(?:[T ](\d{2}:\d{2})(?::(\d{2}))?)?/);
  return m ? { date: m[1], time: m[2] || "", sec: m[3] || "" } : { date: "", time: "", sec: "" };
};
const join = (date, time, sec) => (date ? `${date}T${time || "00:00"}${sec && time ? ":" + sec : ""}` : "");

export function DateTimeField({ value, onChange, label = "Date and time", id, min, max, className = "" }) {
  const [parts, setParts] = useState(() => split(value));
  // a value set from outside (a default, Now, Clear): show it
  useEffect(() => {
    if ((value || "") !== join(parts.date, parts.time, parts.sec)) setParts(split(value));
  }, [value]);
  const set = (p) => {
    const next = { ...parts, ...p };
    setParts(next);
    onChange(join(next.date, next.time, next.sec));
  };
  return (
    <span className={"dt-field " + className}>
      <input
        id={id}
        type="date"
        value={parts.date}
        min={min ? split(min).date : undefined}
        max={max ? split(max).date : undefined}
        aria-label={label + " (date)"}
        onChange={(e) => set({ date: e.target.value })}
      />
      <input
        type="time"
        value={parts.time}
        aria-label={label + " (time)"}
        title={parts.date && !parts.time ? "00:00 until you choose a time" : undefined}
        placeholder="00:00"
        onChange={(e) => set({ time: e.target.value, sec: "" })}
      />
    </span>
  );
}
