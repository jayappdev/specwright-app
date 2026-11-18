// Rules editing for the development tool wizard:
//   incremental → a side panel that adds rules one at a time (or edits one)
//   bulk        → one document with every rule, edited as rich text or
//                 Markdown and saved as .md or as rich text (.html)
import { useEffect, useState } from "react";
import { confirmDialog } from "../Dialog.jsx";
import { backend } from "../backend.js";
import { S, update, toast, errMsg, saveProfile } from "../store.js";
import { RichEditor } from "../RichEditor.jsx";
import { rulesToMd, mdToRules, rulesToHtmlDoc, nextRuleId } from "../richtext.js";
import { Icon } from "../ui.jsx";

const ui = () => (S.rulesUi ||= { drawer: null, bulk: null });
const profile = () => S.ws.profile;

export function openRuleDrawer(index) {
  const rules = profile().rules;
  const r = index != null ? rules[index] : null;
  ui().drawer = r
    ? { mode: "edit", index, id: r.id, text: r.text, added: [] }
    : { mode: "add", id: nextRuleId(rules), text: "", added: [] };
  update();
}

export function openBulkEditor() {
  const md = rulesToMd(profile().rules);
  ui().bulk = { md, original: md };
  update();
}

function close(key) {
  ui()[key] = null;
  update();
}

function commitRules(rules) {
  profile().rules = rules;
  update();
  saveProfile();
}

function RuleDrawer() {
  const d = ui().drawer;
  useEffect(() => {
    const onKey = (e) => e.key === "Escape" && close("drawer");
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  const rules = profile().rules;
  const idTaken = rules.some((r, k) => r.id === d.id.trim() && k !== d.index);
  const save = (again) => {
    const id = d.id.trim(),
      text = d.text.trim();
    if (!id) return toast("Give the rule an id, e.g. R-020.");
    if (idTaken) return toast(`${id} is already used by another rule.`);
    if (!text) return toast("Write the rule first.");
    if (d.mode === "edit") {
      const next = rules.slice();
      next[d.index] = { id, text };
      commitRules(next);
      toast(`${id} updated.`);
      return close("drawer");
    }
    const next = [...rules, { id, text }];
    commitRules(next);
    d.added.push(id);
    if (again) {
      d.id = nextRuleId(next);
      d.text = "";
      d.key = (d.key || 0) + 1;
      update();
    } else {
      toast(`Added ${d.added.join(", ")}.`);
      close("drawer");
    }
  };
  return (
    <div className="drawer-scrim" onMouseDown={(e) => e.target === e.currentTarget && close("drawer")}>
      <aside className="drawer" role="dialog" aria-modal="true" aria-labelledby="rd-title">
        <header className="drawer-head">
          <h2 id="rd-title">{d.mode === "edit" ? "Edit rule" : "Add rules"}</h2>
          <span className="badge">{d.mode === "edit" ? "incremental · one rule" : "incremental"}</span>
          <button
            className="link"
            style={{ marginLeft: "auto" }}
            onClick={() => close("drawer")}
            aria-label="Close"
          >
            Close
          </button>
        </header>
        <p className="wf-desc">
          {d.mode === "edit"
            ? "Change this rule. Other rules are untouched."
            : "Each rule you add is appended; existing rules stay as they are."}{" "}
          Formatting is kept as Markdown, which the agent reads.
        </p>
        {d.added.length > 0 && (
          <div className="banner" style={{ borderColor: "var(--ok)" }}>
            <Icon.check /> Added {d.added.join(", ")}
          </div>
        )}
        <label className="field">
          <span className="muted">Rule id</span>
          <input
            value={d.id}
            onChange={(e) => {
              d.id = e.target.value;
              update();
            }}
            style={{ fontFamily: "var(--font-mono)", width: 160 }}
            aria-invalid={idTaken}
          />
          {idTaken && <small className="err-text">Already used</small>}
        </label>
        <div className="field" style={{ marginTop: 8 }}>
          <span className="muted">Rule</span>
          <RichEditor
            key={d.key || 0}
            value={d.text}
            onChange={(md) => {
              d.text = md;
              update();
            }}
            autoFocus
            minHeight={220}
            placeholder="e.g. Monetary values use BigDecimal, never double. Round half-even to 2 decimals."
          />
        </div>
        <div className="row-actions drawer-actions">
          {d.mode === "add" && (
            <button className="btn secondary" onClick={() => save(true)}>
              Add and write another
            </button>
          )}
          <button className="btn primary" onClick={() => save(false)}>
            {d.mode === "edit" ? "Save rule" : "Add rule"}
          </button>
          <button className="btn secondary" onClick={() => close("drawer")}>
            Cancel
          </button>
        </div>
      </aside>
    </div>
  );
}

function BulkEditor() {
  const b = ui().bulk;
  const [busy, setBusy] = useState(false);
  const parsed = mdToRules(b.md);
  const before = profile().rules;
  const ids = parsed.map((r) => r.id),
    dup = ids.find((x, i) => ids.indexOf(x) !== i);
  const added = parsed.filter((r) => !before.some((o) => o.id === r.id)).length;
  const removed = before.filter((o) => !parsed.some((r) => r.id === o.id)).length;
  const changed = parsed.filter((r) =>
    before.some((o) => o.id === r.id && o.text.trim() !== r.text.trim()),
  ).length;
  const dirty = b.md !== b.original;
  const cancel = async () => {
    if (
      !dirty ||
      (await confirmDialog("Your edits to the rules will be lost.", {
        title: "Discard your changes?",
        ok: "Discard",
        danger: true,
      }))
    )
      close("bulk");
  };
  const save = async (format) => {
    if (dup) return toast(`Two rules use the id ${dup}.`);
    if (!parsed.length) return toast("No rules found. Start each rule with a “## R-001” heading.");
    setBusy(true);
    try {
      commitRules(parsed);
      await backend.SaveProfile(profile());
      const doc = format === "html" ? rulesToHtmlDoc(parsed) : rulesToMd(parsed);
      const path = await backend.SaveRulesDoc(format, doc);
      toast(`Saved ${parsed.length} rules to ${path}.`);
      close("bulk");
    } catch (e) {
      toast(errMsg(e), "error");
    }
    setBusy(false);
  };
  return (
    <div className="modal-scrim" role="presentation" onKeyDown={(e) => e.key === "Escape" && cancel()}>
      <div className="card modal bulk-modal" role="dialog" aria-modal="true" aria-labelledby="be-title">
        <div className="card-head">
          <h3 id="be-title">Edit all rules</h3>
          <span className="badge">bulk</span>
          <span className="muted">
            {parsed.length} rules{dirty ? ` · ${added} added · ${changed} changed · ${removed} removed` : ""}
          </span>
        </div>
        <p className="wf-desc">
          Every rule starts with a heading holding its id (<code className="inline-code">## R-001</code>);
          everything under it, up to the next heading of that level, is the rule. Saving replaces all rules.
        </p>
        {dup && <div className="banner error">Two rules use the id {dup}.</div>}
        <RichEditor
          value={b.md}
          onChange={(md) => {
            b.md = md;
            update();
          }}
          minHeight={360}
          big
          label="All rules"
        />
        <div className="row-actions">
          <button className="btn primary" disabled={busy || !!dup} onClick={() => save("md")}>
            Save as Markdown
          </button>
          <button className="btn secondary" disabled={busy || !!dup} onClick={() => save("html")}>
            Save as rich text
          </button>
          <button className="btn secondary" onClick={cancel}>
            Cancel
          </button>
          <span className="muted" style={{ marginLeft: "auto" }}>
            Writes <code className="inline-code">.codegen/rules/rules.md</code> or{" "}
            <code className="inline-code">rules.html</code>; the profile always keeps the rules as Markdown.
          </span>
        </div>
      </div>
    </div>
  );
}

export function RulesEditors() {
  if (!S.ws) return null;
  const u = ui();
  return (
    <>
      {u.drawer && <RuleDrawer />}
      {u.bulk && <BulkEditor />}
    </>
  );
}
