// Flow designer: decisions, filters and aggregates — paths, condition groups,
// row inputs, groupings — and connection (edge) settings.
import { MATCHES, ROW_FORMATS, changed, fl, nodeOf, removeSelected } from "../Flows.jsx";
import { Field, Preview } from "./Inspector.jsx";
import { track } from "./Fields.jsx";

// --- decisions, filters, aggregates ---------------------------------------------------

const newRule = () => ({ field: "", op: "eq", value: "" });
export const newCond = () => ({ match: "all", rules: [newRule()] });
export const condCount = (c) => (c?.rules || []).reduce((k, r) => k + (r.group ? condCount(r.group) : 1), 0);
// the next path of a decision that leads nowhere yet
export function nextBranch(a) {
  const used = new Set(
    fl()
      .flow.edges.filter((x) => x.from === a.id)
      .map((x) => x.branch),
  );
  return (
    [...(a.branches || []).map((x) => x.id), "else"].find((id) => !used.has(id)) ||
    a.branches?.[0]?.id ||
    "else"
  );
}
export function branchName(n, id) {
  return id === "else" ? "Otherwise" : (n?.branches || []).find((b) => b.id === id)?.label || id;
}

// A connection: which path of a Decision it is on.
export function EdgeInspector({ e }) {
  const a = nodeOf(e.from),
    b = nodeOf(e.to);
  if (!a || !b) return null;
  const set = (v) => {
    e.branch = v;
    changed();
  };
  return (
    <>
      <div className="fl-insp-head">
        <div>
          <h3>
            {a.title || a.id} → {b.title || b.id}
          </h3>
          <small className="muted">Connection</small>
        </div>
        <button className="link" style={{ marginLeft: "auto" }} onClick={removeSelected}>
          Delete
        </button>
      </div>
      {a.type === "decision" ? (
        <>
          <Field
            label="Path"
            hint="The steps after this connection run only when the decision takes this path."
          >
            <select value={e.branch || ""} onChange={(ev) => set(ev.target.value)}>
              <option value="">Always (not on a path)</option>
              {(a.branches || []).map((x) => (
                <option key={x.id} value={x.id}>
                  {x.label || x.id}
                </option>
              ))}
              <option value="else">Otherwise (no path matched)</option>
            </select>
          </Field>
        </>
      ) : (
        <p className="muted fl-help">
          {b.title || b.id} gets the output of {a.title || a.id}. Press Delete to remove the connection.
        </p>
      )}
    </>
  );
}

// A condition: rules joined by all / any / none; a rule can be a group of its own.
function CondEditor({ cond, onChange, depth = 0, what = "the data", listId }) {
  const ops = fl().ops?.condOps || [];
  const c = cond || newCond();
  const put = () => onChange({ ...c });
  const setRule = (i, k, v) => {
    c.rules[i] = { ...c.rules[i], [k]: v };
    put();
  };
  return (
    <div className={"fl-cond" + (depth ? " nested" : "")}>
      <div className="fl-cond-head">
        <select
          aria-label="Match"
          value={c.match || "all"}
          onChange={(e) => {
            c.match = e.target.value;
            put();
          }}
        >
          {MATCHES.map(([v, l]) => (
            <option key={v} value={v}>
              {l}
            </option>
          ))}
        </select>
        <span className="muted">{depth ? "these:" : "these conditions:"}</span>
        {depth > 0 && (
          <button className="link" onClick={() => onChange(null)}>
            Remove group
          </button>
        )}
      </div>
      {(c.rules || []).map((r, i) =>
        r.group ? (
          <CondEditor
            key={i}
            cond={r.group}
            depth={depth + 1}
            what={what}
            listId={listId}
            onChange={(g) => {
              if (g) c.rules[i] = { group: g };
              else c.rules.splice(i, 1);
              put();
            }}
          />
        ) : (
          <div key={i} className="fl-rule">
            <input
              className="fl-rule-field"
              aria-label="Field"
              list={listId}
              data-path="1"
              placeholder="field, e.g. customer.tier (empty: the whole value)"
              value={r.field || ""}
              style={{ fontFamily: "var(--font-mono)" }}
              {...track}
              onChange={(e) => setRule(i, "field", e.target.value)}
            />
            <button
              className="link"
              aria-label="Remove condition"
              title="Remove"
              onClick={() => {
                c.rules.splice(i, 1);
                put();
              }}
            >
              ✕
            </button>
            <select
              aria-label="Comparison"
              value={r.op || "eq"}
              onChange={(e) => setRule(i, "op", e.target.value)}
            >
              {ops.map((o) => (
                <option key={o.id} value={o.id}>
                  {o.name}
                </option>
              ))}
            </select>
            {!ops.find((o) => o.id === r.op)?.unary && (
              <input
                className="fl-rule-value"
                aria-label="Value"
                placeholder={
                  r.op === "between"
                    ? "low, high"
                    : r.op === "in" || r.op === "not_in"
                      ? "a, b, c"
                      : "value or {{…}}"
                }
                value={r.value || ""}
                {...track}
                onChange={(e) => setRule(i, "value", e.target.value)}
              />
            )}
            <div className="fl-rule-opts">
              <label className="check">
                <input
                  type="checkbox"
                  checked={!!r.ignoreCase}
                  onChange={(e) => setRule(i, "ignoreCase", e.target.checked)}
                />{" "}
                ignore case
              </label>
              {/\[\*\]|\./.test(r.field || "") && (
                <label
                  className="check"
                  title="When the field is a list (items[*].qty), every element must match, not just one"
                >
                  <input
                    type="checkbox"
                    checked={!!r.every}
                    onChange={(e) => setRule(i, "every", e.target.checked)}
                  />{" "}
                  every element of a list
                </label>
              )}
            </div>
          </div>
        ),
      )}
      <div className="row-actions">
        <button
          className="btn secondary small"
          onClick={() => {
            (c.rules ||= []).push(newRule());
            put();
          }}
        >
          + Condition
        </button>
        {depth < 4 && (
          <button
            className="btn secondary small"
            onClick={() => {
              (c.rules ||= []).push({ group: { match: "any", rules: [newRule()] } });
              put();
            }}
          >
            + Group
          </button>
        )}
      </div>
      {depth === 0 && (
        <p className="muted fl-help">
          A field is a path into {what}:{" "}
          <code className="inline-code" data-nofile>
            status
          </code>
          ,{" "}
          <code className="inline-code" data-nofile>
            customer.address.city
          </code>
          ,{" "}
          <code className="inline-code" data-nofile>
            items[0].sku
          </code>
          ,{" "}
          <code className="inline-code" data-nofile>
            items[*].qty
          </code>{" "}
          (any element; tick “every element” for all). Leave it empty for the whole value. Values can use
          variables, and{" "}
          <code className="inline-code" data-nofile>
            {"{{this.<field>}}"}
          </code>{" "}
          compares two fields. Numbers compare as numbers, dates (2026-11-12) as text.
        </p>
      )}
    </div>
  );
}

function RowsInput({ n, set }) {
  return (
    <>
      <Field label="Rows" hint="JSON comes out as a JSON array; one row per line comes out the same way.">
        <select value={n.split || "auto"} onChange={(e) => set("split", e.target.value)}>
          {ROW_FORMATS.map(([v, l]) => (
            <option key={v} value={v}>
              {l}
            </option>
          ))}
        </select>
      </Field>
      <details>
        <summary className="muted">Input (default: the output of the step before)</summary>
        <Field label="Use instead">
          <input
            value={n.source || ""}
            placeholder="{{steps.…}}"
            {...track}
            onChange={(e) => set("source", e.target.value)}
          />
        </Field>
      </details>
    </>
  );
}

const listText = (a) => (a || []).join(", ");
const textList = (s) =>
  String(s || "")
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);

export function DecisionInspector({ n, set }) {
  const f = fl(),
    flow = f.flow;
  const targets = (id) =>
    flow.edges.filter((e) => e.from === n.id && e.branch === id).map((e) => nodeOf(e.to)?.title || e.to);
  const put = () => set("branches", [...n.branches]);
  const nextId = () => {
    let k = (n.branches || []).length + 1;
    while ((n.branches || []).some((b) => b.id === "path" + k)) k++;
    return "path" + k;
  };
  return (
    <>
      <p className="muted fl-help">
        Checks the data from the step before and sends the flow down a path: the steps connected on that path
        run, the others are skipped. The data passes on unchanged. Paths are checked in order;{" "}
        <b>Otherwise</b> runs when none matches.
      </p>
      <Field label="When several paths match">
        <select value={n.mode || "first"} onChange={(e) => set("mode", e.target.value)}>
          <option value="first">Take the first one</option>
          <option value="all">Take every one that matches</option>
        </select>
      </Field>
      {(n.branches || []).map((b, i) => (
        <div key={b.id} className="fl-branch">
          <div className="fl-branch-head">
            <span className="fl-branch-n">{i + 1}</span>
            <input
              aria-label="Path name"
              value={b.label || ""}
              placeholder={b.id}
              onChange={(e) => {
                n.branches[i] = { ...b, label: e.target.value };
                put();
              }}
            />
            <button
              className="link"
              disabled={i === 0}
              title="Check earlier"
              onClick={() => {
                const x = n.branches.splice(i, 1)[0];
                n.branches.splice(i - 1, 0, x);
                put();
              }}
            >
              ↑
            </button>
            <button
              className="link"
              aria-label="Remove path"
              title="Remove path"
              onClick={() => {
                n.branches.splice(i, 1);
                flow.edges.forEach((e) => {
                  if (e.from === n.id && e.branch === b.id) e.branch = "";
                });
                put();
              }}
            >
              ✕
            </button>
          </div>
          <CondEditor
            cond={b.when}
            what="the data"
            listId={"paths-" + n.id}
            onChange={(c) => {
              n.branches[i] = { ...b, when: c || newCond() };
              put();
            }}
          />
          <small className="muted">
            Leads to:{" "}
            {targets(b.id).join(", ") ||
              "nothing yet: connect a step from this decision and choose this path on the connection"}
          </small>
        </div>
      ))}
      <button
        className="btn secondary"
        onClick={() => {
          const id = nextId();
          (n.branches ||= []).push({ id, label: "Path " + id.replace("path", ""), when: newCond() });
          put();
        }}
      >
        + Path
      </button>
      <div className="fl-branch">
        <div className="fl-branch-head">
          <span className="fl-branch-n">∗</span>
          <b>Otherwise</b>
        </div>
        <small className="muted">
          Leads to: {targets("else").join(", ") || "nothing (the flow ends here when no path matches)"}
        </small>
      </div>
      <details>
        <summary className="muted">Input (default: the output of the step before)</summary>
        <Field label="Use instead">
          <input
            value={n.source || ""}
            placeholder="{{steps.…}}"
            {...track}
            onChange={(e) => set("source", e.target.value)}
          />
        </Field>
      </details>
      <Preview node={n} />
    </>
  );
}

export function FilterInspector({ n, set }) {
  return (
    <>
      <Field label="Rows that match">
        <div className="seg">
          {[
            ["keep", "Keep"],
            ["drop", "Drop"],
          ].map(([v, l]) => (
            <button
              key={v}
              type="button"
              aria-pressed={(n.mode || "keep") === v}
              onClick={() => set("mode", v)}
            >
              {l}
            </button>
          ))}
        </div>
      </Field>
      <CondEditor
        cond={n.cond}
        what="each row"
        listId={"paths-" + n.id}
        onChange={(c) => set("cond", c || newCond())}
      />
      <div className="fl-row">
        <Field label="Sort by" hint="Fields; -total sorts high to low">
          <input
            list={"paths-" + n.id}
            data-path="list"
            {...track}
            value={listText(n.orderBy)}
            placeholder="e.g. -total, id"
            onChange={(e) => set("orderBy", textList(e.target.value))}
          />
        </Field>
        <Field label="At most" hint="rows (empty: all)">
          <input
            type="number"
            min="0"
            value={n.max || ""}
            onChange={(e) => set("max", +e.target.value || 0)}
          />
        </Field>
      </div>
      <label className="check">
        <input type="checkbox" checked={!!n.unique} onChange={(e) => set("unique", e.target.checked)} /> Drop
        repeated rows
      </label>
      <RowsInput n={n} set={set} />
      <Preview node={n} />
    </>
  );
}

export function AggregateInspector({ n, set }) {
  const fns = fl().ops?.aggFns || [],
    win = n.mode === "window";
  const ms = n.measures || [];
  const put = () => set("measures", [...ms]);
  return (
    <>
      <Field label="Result">
        <div className="seg">
          {[
            ["group", "One row per group"],
            ["window", "Every row, with its group's values"],
          ].map(([v, l]) => (
            <button
              key={v}
              type="button"
              aria-pressed={(n.mode || "group") === v}
              onClick={() => set("mode", v)}
            >
              {l}
            </button>
          ))}
        </div>
      </Field>
      <p className="muted fl-help">
        {win
          ? "Like SQL's OVER (PARTITION BY … ORDER BY …): every row is kept, with values over its group added: totals, ranks, running sums, the previous or next row's value."
          : "Like SQL's GROUP BY: one row per distinct combination of the group fields, with the values calculated over its rows. With no group fields, one row of totals."}
      </p>
      <Field label={win ? "Partition by" : "Group by"} hint="Fields, comma-separated: region, customer.tier">
        <input
          list={"paths-" + n.id}
          data-path="list"
          {...track}
          value={listText(n.groupBy)}
          placeholder="e.g. region, customer.tier"
          onChange={(e) => set("groupBy", textList(e.target.value))}
        />
      </Field>
      <div className="fl-measures">
        <div className="muted">Values to calculate</div>
        {ms.map((m, i) => {
          const fn = fns.find((x) => x.id === m.fn) || {};
          return (
            <div key={i} className="fl-measure">
              <select
                aria-label="Function"
                value={m.fn}
                onChange={(e) => {
                  ms[i] = { ...m, fn: e.target.value };
                  put();
                }}
              >
                <optgroup label="Over the group">
                  {fns
                    .filter((x) => !x.window)
                    .map((x) => (
                      <option key={x.id} value={x.id}>
                        {x.name}
                      </option>
                    ))}
                </optgroup>
                {win && (
                  <optgroup label="Window (per row)">
                    {fns
                      .filter((x) => x.window)
                      .map((x) => (
                        <option key={x.id} value={x.id}>
                          {x.name}
                        </option>
                      ))}
                  </optgroup>
                )}
              </select>
              {!fn.noField || m.fn === "count" ? (
                <input
                  aria-label="Field"
                  list={"paths-" + n.id}
                  data-path="1"
                  {...track}
                  placeholder={m.fn === "count" ? "field (empty: rows)" : "field, e.g. total"}
                  value={m.field || ""}
                  style={{ fontFamily: "var(--font-mono)" }}
                  onChange={(e) => {
                    ms[i] = { ...m, field: e.target.value };
                    put();
                  }}
                />
              ) : (
                <span />
              )}
              <input
                aria-label="Name"
                placeholder="name in the result"
                value={m.as || ""}
                onChange={(e) => {
                  ms[i] = { ...m, as: e.target.value };
                  put();
                }}
              />
              <button
                className="link"
                aria-label="Remove value"
                onClick={() => {
                  ms.splice(i, 1);
                  put();
                }}
              >
                ✕
              </button>
              {fn.arg && (
                <input
                  className="fl-measure-arg"
                  aria-label={fn.arg}
                  placeholder={fn.arg}
                  value={m.arg || ""}
                  onChange={(e) => {
                    ms[i] = { ...m, arg: e.target.value };
                    put();
                  }}
                />
              )}
            </div>
          );
        })}
        <button
          className="btn secondary small"
          onClick={() => {
            ms.push({ fn: "sum", field: "", as: "" });
            put();
          }}
        >
          + Value
        </button>
      </div>
      <div className="fl-row">
        <Field
          label={win ? "Order within the group" : "Sort the result by"}
          hint="Fields; -total for high to low"
        >
          <input
            value={listText(n.orderBy)}
            placeholder="e.g. -total"
            onChange={(e) => set("orderBy", textList(e.target.value))}
          />
        </Field>
        <Field label="At most" hint="rows (empty: all)">
          <input
            type="number"
            min="0"
            value={n.max || ""}
            onChange={(e) => set("max", +e.target.value || 0)}
          />
        </Field>
      </div>
      <p className="muted fl-help">
        Fields are paths into each row (
        <code className="inline-code" data-nofile>
          customer.tier
        </code>
        ); a list path like{" "}
        <code className="inline-code" data-nofile>
          items[*].qty
        </code>{" "}
        counts every element, so you can sum nested values.
      </p>
      <RowsInput n={n} set={set} />
      <Preview node={n} />
    </>
  );
}
