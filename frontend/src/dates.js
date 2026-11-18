// Date and date-time flow inputs (the same rules as internal/flow/dates.go):
// a date is YYYY-MM-DD, a date-time YYYY-MM-DDTHH:MM in local time (seconds
// kept when not zero). Relative values — today, today-7d, now, now-2h,
// now+30m — are worked out when the run starts.

const pad = (n) => String(n).padStart(2, "0");
export const fmtDate = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
export const fmtDateTime = (d) =>
  `${fmtDate(d)}T${pad(d.getHours())}:${pad(d.getMinutes())}` +
  (d.getSeconds() ? `:${pad(d.getSeconds())}` : "");

const REL = /^(today|now)\s*(?:([+-])\s*(\d{1,5})\s*([mhdw])?)?$/;

// resolveDate returns the value in its standard form ("" stays ""), or
// throws when it is not a date (or date and time).
export function resolveDate(type, s, now = new Date()) {
  const t = String(s ?? "").trim();
  if (!t) return "";
  const m = t.toLowerCase().match(REL);
  if (m) {
    const d = new Date(now);
    d.setSeconds(0, 0); // to the minute, as a picker shows it
    if (m[1] === "today") d.setHours(0, 0, 0, 0);
    if (m[2]) {
      const n = (m[2] === "-" ? -1 : 1) * Number(m[3]),
        unit = m[4] || "d";
      if (m[1] === "today" && (unit === "m" || unit === "h"))
        throw new Error(`"${t}": use days or weeks with today (or now-${m[3]}${unit})`);
      if (unit === "m") d.setMinutes(d.getMinutes() + n);
      else if (unit === "h") d.setHours(d.getHours() + n);
      else d.setDate(d.getDate() + (unit === "w" ? 7 * n : n));
    }
    return type === "date" ? fmtDate(d) : fmtDateTime(d);
  }
  const p = t.match(/^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2}):(\d{2})(?::(\d{2}))?)?(Z|[+-]\d{2}:?\d{2})?$/);
  if (p) {
    const [y, mo, da, h = "0", mi = "0", se = "0"] = p.slice(1, 7).map((x) => x ?? undefined);
    let d = new Date(+y, +mo - 1, +da, +h, +mi, +se);
    const ok =
      d.getFullYear() === +y &&
      d.getMonth() === +mo - 1 &&
      d.getDate() === +da &&
      d.getHours() === +h &&
      d.getMinutes() === +mi;
    if (ok) {
      if (p[7] && type !== "date") d = new Date(t.replace(" ", "T")); // a zone: as local time
      return type === "date" ? fmtDate(d) : fmtDateTime(d);
    }
  }
  throw new Error(
    type === "date"
      ? `"${t.slice(0, 40)}" is not a date (YYYY-MM-DD, or today, today-7d)`
      : `"${t.slice(0, 40)}" is not a date and time (YYYY-MM-DDTHH:MM, or now, now-2h, today)`,
  );
}

// A default as the run form shows it: relative values worked out now; a
// value that is not a date is left for the run to report.
export function dateDefault(type, s) {
  try {
    return resolveDate(type, s);
  } catch {
    return s || "";
  }
}
