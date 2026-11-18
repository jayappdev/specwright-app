// Flow Designer › Inputs › Triggers: ways the flow starts by itself.
//   - a schedule: once, every N minutes / hours / days, daily or weekly,
//     until a time or for a number of runs;
//   - after another flow: when its run completes (fails, is stopped);
//   - when files change: files matching patterns are created, modified or
//     deleted (one run per burst of changes).
// Each gives the values of the flow's inputs, which can use what started
// the run ({{trigger.files}}, {{trigger.output}}, …). Secret values are saved encrypted on this computer (never
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
  const add = (kind) => {
    const ids = new Set(list.map((t) => t.id));
    const base = { schedule: "schedule", flow: "after-flow", files: "files" }[kind];
    let n = 1;
    while (ids.has(base + "-" + n)) n++;
    const t = { id: base + "-" + n, kind, enabled: true, inputs: {} };
    if (kind === "schedule")
      Object.assign(t, { name: "Schedule " + n, schedule: { mode: "daily", time: "09:00" } });
    if (kind === "flow") Object.assign(t, { name: "After another flow", after: { flow: "", on: ["done"] } });
    if (kind === "files")
      Object.assign(t, { name: "When files change", files: { paths: ["src/**"], ops: [], wait: 5 } });
    list.push(t);
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
          None: the flow runs when you press Run. A trigger runs it by itself: at a time or every so often,
          after another flow, or when files change. Triggers work while this project is open in SpecWright;
          schedules pick up again after the app was closed or the computer asleep.
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
      <div className="row-actions">
        <button className="btn secondary" onClick={() => add("schedule")}>
          <Icon.plus /> Add a schedule
        </button>
        <button className="btn secondary" onClick={() => add("flow")}>
          <Icon.plus /> After another flow
        </button>
        <button className="btn secondary" onClick={() => add("files")}>
          <Icon.plus /> When files change
        </button>
      </div>
    </div>
  );
}

function TriggerCard({ t, v, remove, reload }) {
  const f = fl(),
    flow = f.flow;
  const [secretVals, setSecretVals] = useState({});
  const [busy, setBusy] = useState(false);
  const vars = TRIGGER_VARS[t.kind] || [];
  const inputs = (flow.inputs || []).filter((i) => i.type !== "secret");
  const needed =
    v?.secretsNeeded || (flow.inputs || []).filter((i) => i.type === "secret").map((i) => i.name);
  const saved = new Set(v?.secretsSaved || []);
  const st = v?.state || {};
  const what = KIND_WORD[t.kind] || "trigger";
  const KindIcon = { schedule: Icon.history, flow: Icon.flow, files: Icon.files }[t.kind] || Icon.history;
  return (
    <div className={"fl-trigger" + (t.enabled ? "" : " off")}>
      <div className="fl-trigger-head">
        <KindIcon />
        <input
          className="fl-trigger-name"
          value={t.name || ""}
          aria-label={`Name of the ${what}`}
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
          aria-label={`Delete the ${what} ${t.name || t.id}`}
          onClick={remove}
        >
          Delete
        </button>
      </div>
      {t.kind === "schedule" && <ScheduleFields t={t} />}
      {t.kind === "flow" && <AfterFields t={t} />}
      {t.kind === "files" && <FilesFields t={t} />}
      {inputs.length > 0 && (
        <details open={inputs.some((i) => i.required && !i.default && !(t.inputs || {})[i.name])}>
          <summary className="muted">Inputs for its runs</summary>
          {vars.length > 1 && (
            <p className="muted fl-help">
              A value can use what started the run:{" "}
              {vars.map((x, k) => (
                <span key={x}>
                  {k ? ", " : ""}
                  <code className="inline-code" data-nofile>{`{{trigger.${x}}}`}</code>
                </span>
              ))}
              .
            </p>
          )}
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
        {t.kind !== "schedule" && v?.describe ? `Starts ${v.describe} · ` : ""}
        {st.runs ? `${st.runs} run${st.runs === 1 ? "" : "s"}` : "No runs yet"}
        {st.lastRun ? ` · last ${when(st.lastRun)} (${st.lastStatus || "…"})` : ""}
        {st.missed ? ` · ${st.missed} missed` : ""}
        {st.done ? " · finished" : st.nextRun && t.enabled ? ` · next ${when(st.nextRun)}` : ""}
        {st.watching?.length > 0 && <div>Watching {st.watching.join(", ")} (outside the project)</div>}
        {st.watchError && <div className="ai-trial-err">Not watched: {st.watchError}</div>}
        {st.lastError && <div className="ai-trial-err">{st.lastError}</div>}
      </div>
      <div className="row-actions">
        <button
          className="btn secondary"
          disabled={busy || f.dirty}
          title={f.dirty ? "Saving the flow first…" : `Start a run now, as the ${what} would`}
          onClick={async () => {
            setBusy(true);
            try {
              const r = await backend.RunTriggerNow(flow.id, t.id);
              toast(
                `Started a run (${r.id.slice(0, 15)}…), tagged “${TAG[t.kind] || t.kind}”. See it in Run flows.`,
              );
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

// a schedule: when it runs, when it ends, and its next run times
function ScheduleFields({ t }) {
  const s = (t.schedule ||= { mode: "daily", time: "09:00" });
  const [preview, setPreview] = useState({ times: [], error: "" });
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
  return (
    <>
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
    </>
  );
}

const OUTCOMES = [
  ["done", "completes"],
  ["failed", "fails"],
  ["stopped", "is stopped"],
];

// after another flow: which flow, and how its run ends
function AfterFields({ t }) {
  const a = (t.after ||= { flow: "", on: ["done"] }),
    self = fl().flow.id;
  const [flows, setFlows] = useState([]);
  useEffect(() => {
    backend
      .Flows()
      .then((l) => setFlows((l || []).filter((x) => x.id !== self)))
      .catch(() => {});
  }, [self]);
  const on = a.on?.length ? a.on : ["done"];
  return (
    <>
      <label className="field">
        <span className="muted">Starts after a run of</span>
        <select
          value={a.flow || ""}
          aria-label="The flow it starts after"
          onChange={(e) => {
            a.flow = e.target.value;
            changed();
          }}
        >
          <option value="">Choose a flow…</option>
          {flows.map((x) => (
            <option key={x.id} value={x.id}>
              {x.name}
            </option>
          ))}
          {a.flow && !flows.some((x) => x.id === a.flow) && (
            <option value={a.flow}>{a.flow} (not found)</option>
          )}
        </select>
      </label>
      <div className="field">
        <span className="muted">When that run</span>
        <div className="chips" role="group" aria-label="How that run ends">
          {OUTCOMES.map(([k, l]) => {
            const pressed = on.includes(k);
            return (
              <button
                key={k}
                type="button"
                className="chip"
                aria-pressed={pressed}
                onClick={() => {
                  const next = pressed ? on.filter((x) => x !== k) : [...on, k];
                  a.on = next.length ? next : ["done"];
                  changed();
                }}
              >
                {l}
              </button>
            );
          })}
        </div>
      </div>
      <p className="muted fl-help">
        Chains stop by themselves: a flow does not start again inside its own chain (A → B → A), and a chain
        runs at most 5 flows. Trial runs do not start it.
      </p>
    </>
  );
}

const OPS = [
  ["created", "created"],
  ["modified", "modified"],
  ["deleted", "deleted"],
];
const lines = (x) =>
  String(x || "")
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);

// when files change: which files, which changes, how long to wait
function FilesFields({ t }) {
  const w = (t.files ||= { paths: [], ops: [], wait: 5 });
  // the text as typed (a line being written is not dropped)
  const [paths, setPaths] = useState((w.paths || []).join("\n"));
  const [ignore, setIgnore] = useState((w.ignore || []).join("\n"));
  const ops = w.ops?.length ? w.ops : OPS.map(([k]) => k);
  return (
    <>
      <label className="field">
        <span className="muted">Files to watch (one pattern a line)</span>
        <textarea
          rows={2}
          value={paths}
          placeholder={"src/**/*.java\napi/openapi.yaml\n~/Downloads/inbox/**"}
          aria-label="Files to watch"
          onChange={(e) => {
            setPaths(e.target.value);
            w.paths = lines(e.target.value);
            changed();
          }}
        />
      </label>
      <label className="field">
        <span className="muted">But not (optional)</span>
        <textarea
          rows={1}
          value={ignore}
          placeholder="*.log"
          aria-label="Files to leave out"
          onChange={(e) => {
            setIgnore(e.target.value);
            w.ignore = lines(e.target.value);
            if (!w.ignore.length) delete w.ignore;
            changed();
          }}
        />
      </label>
      <p className="muted fl-help">
        <code className="inline-code" data-nofile>
          *
        </code>{" "}
        is any name,{" "}
        <code className="inline-code" data-nofile>
          **
        </code>{" "}
        any folders; a pattern without / matches a file name in any folder. Patterns are relative to the
        project, or a full path (
        <code className="inline-code" data-nofile>
          /Users/me/Downloads/in/**
        </code>
        ,{" "}
        <code className="inline-code" data-nofile>
          ~/Downloads/in
        </code>
        ) to watch a folder outside it. SpecWright&apos;s .codegen folder never counts, nor changes made while
        this flow runs.
      </p>
      <div className="field">
        <span className="muted">When a file is</span>
        <div className="chips" role="group" aria-label="Which changes count">
          {OPS.map(([k, l]) => {
            const pressed = ops.includes(k);
            return (
              <button
                key={k}
                type="button"
                className="chip"
                aria-pressed={pressed}
                onClick={() => {
                  const next = pressed ? ops.filter((x) => x !== k) : [...ops, k];
                  w.ops = next.length === OPS.length || !next.length ? [] : next;
                  changed();
                }}
              >
                {l}
              </button>
            );
          })}
        </div>
      </div>
      <label className="field">
        <span className="muted">Wait for quiet (seconds), so a burst of saves runs it once</span>
        <input
          type="number"
          min={0}
          max={3600}
          value={w.wait ?? 5}
          aria-label="Seconds of quiet before it runs"
          onChange={(e) => {
            w.wait = Math.max(0, Math.min(3600, +e.target.value || 0));
            changed();
          }}
        />
      </label>
    </>
  );
}

const KIND_WORD = { schedule: "schedule", flow: "after-flow trigger", files: "file trigger" };

// the {{trigger.x}} values each kind gives its runs (flow/trigger.go TriggerVars)
const TRIGGER_VARS = {
  schedule: ["name"],
  flow: ["flow", "run", "status", "output", "error", "name"],
  files: ["files", "file", "name"],
};

// TAG: the tag of a run a trigger started (Run flows, Run History)
export const TAG = { schedule: "scheduled", flow: "after flow", files: "file change" };

// the chip on the Inputs step's card: what starts the flow by itself
export function triggerChip(flow) {
  const on = [...new Set((flow?.triggers || []).filter((t) => t.enabled).map((t) => t.kind))];
  if (!on.length) return "";
  return on.length > 1 ? "triggers" : TAG[on[0]] || on[0];
}
