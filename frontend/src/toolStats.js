// Tool reliability (core/toolstats.go): how each tool has done in its
// latest calls, shared by the tool tiles, the Flow Designer and the test
// form. Loaded at most every 30 seconds.
import { backend } from "./backend.js";
import { S, update } from "./store.js";

let loadedAt = 0,
  loading = null;

export function loadToolStats(force) {
  if (!S.ws) return Promise.resolve();
  if (!force && (loading || Date.now() - loadedAt < 30000)) return loading || Promise.resolve();
  loading = backend
    .ToolStats()
    .then((m) => {
      S.toolStats = m || {};
      loadedAt = Date.now();
      update();
    })
    .catch(() => {})
    .finally(() => {
      loading = null;
    });
  return loading;
}

const secs = (ms) => (ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(ms < 10000 ? 1 : 0)} s`);

// reliability: a short line about a tool's latest calls ("" when it has
// none). level is "bad" (30% or more failed), "warn" (some failed) or "ok".
export function reliability(name) {
  const s = S.toolStats?.[name];
  if (!s?.calls) return null;
  const level = s.failed * 10 >= s.calls * 3 ? "bad" : s.failed ? "warn" : "ok";
  const text = s.failed
    ? `failed ${s.failed} of the last ${s.calls}`
    : `${s.calls === 1 ? "its last run" : `last ${s.calls} runs`} ok`;
  return { level, text: text + (s.typicalMs ? ` · usually ${secs(s.typicalMs)}` : ""), stat: s };
}
