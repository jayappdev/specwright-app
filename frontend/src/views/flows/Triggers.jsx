// Flow Designer › Inputs › Triggers: ways the flow starts by itself. The
// first kind is a schedule: once, every N minutes / hours / days, daily or
// weekly, until a time or for a number of runs, with the values of the
// flow's inputs. Secret values are saved encrypted on this computer (never
// in the flow file). The trigger's state (next and last run) comes from the
// backend, which runs the schedules while the project is open, and picks
// them up again when the app starts.
import { useEffect, useState } from "react";
import { backend } from "../../backend.js";
import { toast, errMsg } from "../../store.js";
import { Icon } from "../../ui.jsx";
import { DateTimeField } from "../../DateTimeField.jsx";
import { fl, changed } from "../Flows.jsx";

const MODES = [
  ["once", "Once"],
  ["every", "Every…"],
  ["daily", "Daily"],
  ["weekly", "Weekly"],
];
const DAYS = [
  ["mon", "Mon"],
  ["tue", "Tue"],
  ["wed", "Wed"],
  ["thu", "Thu"],
  ["fri", "Fri"],
  ["sat", "Sat"],
  ["sun", "Sun"],
];
const when = (t) =>
  t
    ? new Date(t).toLocaleString([], {
        weekday: "short",
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      })
    : "";

export function TriggersEditor() {
  const f = fl(),
    flow = f.flow;
  const [views, setViews] = useState([]);
  const load = () =>
    backend
      .FlowTriggers(flow.id)
      .then((v) => setViews(v || []))
      .catch(() => {});
  useEffect(() => {
    load();
    const t = setInterval(load, 20000);
    return () => clearInterval(t);
  }, [flow.id, flow.rev]);
  const list = (flow.triggers ||= []);
  const add = () => {
    const ids = new Set(list.map((t) => t.id));
    let n = list.length + 1;
    while (ids.has("schedule-" + n)) n++;
    list.push({
      id: "schedule-" + n,
      kind: "schedule",
      name: "Schedule " + n,
      enabled: true,
      schedule: { mode: "daily", time: "09:00" },
      inputs: {},
    });
    changed();
  };
  return (
    <div className="fl-triggers">
      <h4>
        Triggers{" "}
        <span className="muted" style={{ fontWeight: 400, textTransform: "none" }}>
          · start this flow by itself
        </span>
      </h4>
      {!list.length && (
        <p className="muted fl-help">
          None: the flow runs when you press Run. Add a schedule to run it at a time, or every so often.
          Scheduled runs start while this project is open in SpecWright; after the app was closed or the
          computer asleep, they pick up again.
        </p>
      )}
      {list.map((t, i) => (
        <TriggerCard
          key={t.id}
          t={t}
          v={views.find((x) => x.trigger.id === t.id)}
          remove={() => {
            list.splice(i, 1);
            changed();
          }}
          reload={load}
        />
      ))}
      <button className="btn secondary" onClick={add}>
        <Icon.plus /> Add a schedule
      </button>
    </div>
  );
}

function TriggerCard({ t, v, remove, reload }) {
  const f = fl(),
    flow = f.flow,
    s = (t.schedule ||= { mode: "daily", time: "09:00" });
  const [preview, setPreview] = useState({ times: [], error: "" });
  const [secretVals, setSecretVals] = useState({});
  const [busy, setBusy] = useState(false);
  const set = (k, val) => {
    s[k] = val;
    changed();
  };
  const key = JSON.stringify(s);
  useEffect(() => {
    const h = setTimeout(
      () =>
        backend
          .PreviewSchedule(s)
          .then((times) => setPreview({ times: times || [], error: "" }))
          .catch((e) => setPreview({ times: [], error: errMsg(e) })),
      250,
    );
    return () => clearTimeout(h);
  }, [key]);
  const end = s.count > 0 ? "count" : s.until ? "until" : "never";
  const inputs = (flow.inputs || []).filter((i) => i.type !== "secret");
  const needed =
    v?.secretsNeeded || (flow.inputs || []).filter((i) => i.type === "secret").map((i) => i.name);
  const saved = new Set(v?.secretsSaved || []);
  const st = v?.state || {};
  return (
    <div className={"fl-trigger" + (t.enabled ? "" : " off")}>
      <div className="fl-trigger-head">
        <Icon.history />
        <input
          className="fl-trigger-name"
          value={t.name || ""}
          aria-label="Name of the schedule"
          onChange={(e) => {
            t.name = e.target.value;
            changed();
          }}
        />
        <label className="check">
          <input
            type="checkbox"
            checked={!!t.enabled}
            onChange={(e) => {
              t.enabled = e.target.checked;
              changed();
            }}
          />{" "}
          On
        </label>
        <button
          className="link danger-text"
          aria-label={`Delete the schedule ${t.name || t.id}`}
          onClick={remove}
        >
          Delete
        </button>
      </div>
      <div className="seg" role="group" aria-label="When it runs">
        {MODES.map(([m, l]) => (
          <button key={m} type="button" aria-pressed={s.mode === m} onClick={() => set("mode", m)}>
            {l}
          </button>
        ))}
      </div>
      {s.mode === "once" && (
        <label className="field">
          <span className="muted">At</span>
          <DateTimeField value={s.at || ""} label="Runs at" onChange={(x) => set("at", x)} />
        </label>
      )}
      {s.mode === "every" && (
        <div className="fl-row">
          <label className="field">
            <span className="muted">Every</span>
            <span className="fl-trigger-every">
              <input
                type="number"
                min={1}
                value={s.every || ""}
                aria-label="Every how many"
                onChange={(e) => set("every", Math.max(1, +e.target.value || 1))}
              />
              <select
                value={s.unit || "hours"}
                aria-label="Unit"
                onChange={(e) => set("unit", e.target.value)}
              >
                <option value="minutes">minutes</option>
                <option value="hours">hours</option>
                <option value="days">days</option>
              </select>
            </span>
          </label>
          <label className="field">
            <span className="muted">Starting (empty: from now)</span>
            <DateTimeField value={s.at || ""} label="Starting" onChange={(x) => set("at", x)} />
          </label>
        </div>
      )}
      {(s.mode === "daily" || s.mode === "weekly") && (
        <label className="field">
          <span className="muted">At</span>
          <input
            type="time"
            value={s.time || ""}
            aria-label="Time of day"
            onChange={(e) => set("time", e.target.value)}
          />
        </label>
      )}
      {s.mode === "weekly" && (
        <div className="chips" role="group" aria-label="Days of the week">
          {DAYS.map(([d, l]) => {
            const on = (s.days || []).includes(d);
            return (
              <button
                key={d}
                type="button"
                className="chip"
                aria-pressed={on}
                onClick={() => set("days", on ? s.days.filter((x) => x !== d) : [...(s.days || []), d])}
              >
                {l}
              </button>
            );
          })}
        </div>
      )}
      {s.mode !== "once" && (
        <div className="field">
          <span className="muted">Ends</span>
          <div className="seg" role="group" aria-label="When it ends">
            {[
              ["never", "Never"],
              ["until", "On a date"],
              ["count", "After a number of runs"],
            ].map(([k, l]) => (
              <button
                key={k}
                type="button"
                aria-pressed={end === k}
                onClick={() => {
                  s.until = k === "until" ? s.until || "" : "";
                  s.count = k === "count" ? s.count || 10 : 0;
                  if (k === "until" && !s.until) {
                    const d = new Date(Date.now() + 7 * 864e5);
                    s.until = d.toISOString().slice(0, 10) + "T23:59";
                  }
                  changed();
                }}
              >
                {l}
              </button>
            ))}
          </div>
          {end === "until" && (
            <DateTimeField value={s.until || ""} label="Until" onChange={(x) => set("until", x)} />
          )}
          {end === "count" && (
            <span className="fl-trigger-every">
              <input
                type="number"
                min={1}
                value={s.count || ""}
                aria-label="How many runs"
                onChange={(e) => set("count", Math.max(1, +e.target.value || 1))}
              />{" "}
              runs
            </span>
          )}
        </div>
      )}
      <label className="check">
        <input
          type="checkbox"
          checked={!!t.catchUp}
          onChange={(e) => {
            t.catchUp = e.target.checked;
            changed();
          }}
        />{" "}
        If a run was missed (the app closed, the computer asleep), run it once when back
      </label>
      {preview.error ? (
        <div className="banner error">{preview.error}</div>
      ) : (
        <div className="muted fl-help">
          Next: {preview.times.length ? preview.times.slice(0, 3).map(when).join(" · ") : "no more runs"}
        </div>
      )}
      {inputs.length > 0 && (
        <details open={inputs.some((i) => i.required && !i.default && !(t.inputs || {})[i.name])}>
          <summary className="muted">Inputs for its runs</summary>
          {inputs.map((i) => (
            <label key={i.name} className="field">
              <span className="muted">
                {i.label || i.name}
                {i.required ? " *" : ""}
              </span>
              <input
                value={(t.inputs || {})[i.name] || ""}
                placeholder={i.default ? `default: ${i.default}` : ""}
                onChange={(e) => {
                  (t.inputs ||= {})[i.name] = e.target.value;
                  if (!e.target.value) delete t.inputs[i.name];
                  changed();
                }}
              />
            </label>
          ))}
        </details>
      )}
      {needed.length > 0 && (
        <details open={needed.some((n) => !saved.has(n))}>
          <summary className="muted">
            Secrets for its runs ({needed.filter((n) => saved.has(n)).length} of {needed.length} saved)
          </summary>
          <p className="muted fl-help">
            Saved encrypted on this computer (
            {v?.keyIn === "file" ? "the key in a private file" : "the key in the Keychain"}
            ), never in the flow file. Leave one empty to use the usual source (Keychain, environment).
          </p>
          {needed.map((n) => (
            <label key={n} className="field">
              <span className="muted">
                <code className="inline-code" data-nofile>
                  {n}
                </code>{" "}
                {saved.has(n) && <span className="badge badge-ok">saved</span>}
              </span>
              <span className="fl-trigger-every">
                <input
                  type="password"
                  autoComplete="off"
                  value={secretVals[n] || ""}
                  placeholder={saved.has(n) ? "•••••• (type to replace)" : ""}
                  aria-label={n}
                  onChange={(e) => setSecretVals({ ...secretVals, [n]: e.target.value })}
                />
                {saved.has(n) && (
                  <button
                    className="link"
                    onClick={async () => {
                      await backend
                        .SaveTriggerSecrets(flow.id, t.id, { [n]: "" })
                        .catch((e) => toast(errMsg(e), "error"));
                      reload();
                    }}
                  >
                    Forget
                  </button>
                )}
              </span>
            </label>
          ))}
          <button
            className="btn secondary"
            disabled={!Object.values(secretVals).some(Boolean)}
            onClick={async () => {
              try {
                await backend.SaveTriggerSecrets(
                  flow.id,
                  t.id,
                  Object.fromEntries(Object.entries(secretVals).filter(([, x]) => x)),
                );
                setSecretVals({});
                toast("Saved, encrypted.");
                reload();
              } catch (e) {
                toast(errMsg(e), "error");
              }
            }}
          >
            <Icon.lock /> Save the secrets
          </button>
        </details>
      )}
      <div className="fl-trigger-state muted">
        {st.runs ? `${st.runs} run${st.runs === 1 ? "" : "s"}` : "No runs yet"}
        {st.lastRun ? ` · last ${when(st.lastRun)} (${st.lastStatus || "…"})` : ""}
        {st.missed ? ` · ${st.missed} missed` : ""}
        {st.done ? " · finished" : st.nextRun && t.enabled ? ` · next ${when(st.nextRun)}` : ""}
        {st.lastError && <div className="ai-trial-err">{st.lastError}</div>}
      </div>
      <div className="row-actions">
        <button
          className="btn secondary"
          disabled={busy || f.dirty}
          title={f.dirty ? "Saving the flow first…" : "Start a run now, as the schedule would"}
          onClick={async () => {
            setBusy(true);
            try {
              const r = await backend.RunTriggerNow(flow.id, t.id);
              toast(`Started a scheduled run (${r.id.slice(0, 15)}…). See it in Run flows.`);
              reload();
            } catch (e) {
              toast(errMsg(e), "error");
            }
            setBusy(false);
          }}
        >
          <Icon.run /> Run now
        </button>
      </div>
    </div>
  );
}

// the chip on the Inputs step's card when a schedule is on
export const hasSchedule = (flow) => (flow?.triggers || []).some((t) => t.enabled && t.kind === "schedule");
