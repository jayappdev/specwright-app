// Time labels for run lists.

// "just now", "5 min ago", "2 h ago", "yesterday 14:05", then the date
export function ago(t, now = Date.now()) {
  const d = new Date(t), s = (now - d) / 1000;
  if (!(s >= 0)) return d.toLocaleString();
  if (s < 45) return "just now";
  if (s < 3600) return Math.round(s / 60) + " min ago";
  if (s < 6 * 3600) return Math.round(s / 3600) + " h ago";
  const day = (x) => new Date(x).toDateString(), time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  if (day(d) === day(now)) return "today " + time;
  if (day(d) === day(now - 864e5)) return "yesterday " + time;
  return d.toLocaleDateString([], { day: "numeric", month: "short" }) + " " + time;
}
