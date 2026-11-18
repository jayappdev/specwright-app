// Schedules in the browser preview: the same rules as internal/flow/trigger.go
// (next run, upcoming runs, words). Times are local, YYYY-MM-DDTHH:MM.

const DAYS = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"];
const local = (s) => {
  const m = String(s || "").match(/^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2}):(\d{2}))?/);
  return m ? new Date(+m[1], +m[2] - 1, +m[3], +(m[4] || 0), +(m[5] || 0)) : null;
};
const clock = (s) => {
  const m = String(s || "").match(/^(\d{2}):(\d{2})$/);
  return m && +m[1] < 24 && +m[2] < 60 ? [+m[1], +m[2]] : null;
};

export function checkSchedule(s) {
  if (!s) return "say when it runs";
  switch (s.mode) {
    case "once":
      if (!local(s.at)) return "when: give a date and time";
      break;
    case "every":
      if (!(s.every >= 1) || !["minutes", "hours", "days"].includes(s.unit))
        return "give how often: a number of minutes, hours or days";
      if (s.at && !local(s.at)) return "starting: give a date and time";
      break;
    case "daily":
    case "weekly":
      if (!clock(s.time)) return "at: give a time of day (HH:MM)";
      if (s.mode === "weekly" && !(s.days || []).length) return "choose the days of the week";
      break;
    default:
      return "choose when it runs: once, every …, daily or weekly";
  }
  if (s.until && !local(s.until)) return "until: give a date and time";
  return "";
}

// next: the first run strictly after `after` (null: no more runs)
export function nextRun(s, after, from) {
  let t = null;
  if (s.mode === "once") {
    const a = local(s.at);
    t = a && a > after ? a : null;
  } else if (s.mode === "every") {
    const step = s.every * { minutes: 6e4, hours: 36e5, days: 864e5 }[s.unit];
    const start = (s.at && local(s.at)) || from;
    if (!step) return null;
    t = start > after ? start : new Date(start.getTime() + (Math.floor((after - start) / step) + 1) * step);
  } else if (s.mode === "daily" || s.mode === "weekly") {
    const [h, m] = clock(s.time) || [];
    if (h === undefined) return null;
    for (let i = 0; i < 8 && !t; i++) {
      const c = new Date(after.getFullYear(), after.getMonth(), after.getDate() + i, h, m);
      if (c <= after) continue;
      if (s.mode === "weekly" && !(s.days || []).includes(DAYS[c.getDay()])) continue;
      t = c;
    }
  }
  if (t && s.until && t > local(s.until)) return null;
  return t;
}

export function upcoming(s, after, from, n, done = 0) {
  const out = [];
  let t = after;
  while (out.length < n) {
    if (s.count > 0 && done + out.length >= s.count) break;
    const nx = nextRun(s, t, from);
    if (!nx) break;
    out.push(nx);
    t = nx;
  }
  return out;
}

export function describeSchedule(s) {
  const end =
    (s.until ? ", until " + s.until.replace("T", " ") : "") +
    (s.count > 0 ? `, ${s.count} run${s.count === 1 ? "" : "s"} at most` : "");
  switch (s.mode) {
    case "once":
      return "once, at " + String(s.at || "").replace("T", " ");
    case "every":
      return `every ${s.every === 1 ? "" : s.every + " "}${s.every === 1 ? s.unit.replace(/s$/, "") : s.unit}${s.at ? ", from " + s.at.replace("T", " ") : ""}${end}`;
    case "daily":
      return "daily at " + s.time + end;
    case "weekly":
      return "every " + (s.days || []).join(", ") + " at " + s.time + end;
  }
  return s.mode || "";
}
