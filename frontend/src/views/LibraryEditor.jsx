// Create and edit Development wizard libraries in a side drawer.
//   Corporate libraries (global): admin mode only; saving signs and publishes
//   the skill for every project in the app.
//   Project skills and scripts: anyone; saved as files in the project.
import { useEffect } from "react";
import { confirmDialog, askText } from "../Dialog.jsx";
import { backend } from "../backend.js";
import { S, update, toast, errMsg } from "../store.js";
import { Icon } from "../ui.jsx";
import { RichEditor } from "../RichEditor.jsx";

const SKILL_TEMPLATE = (name) => `---
name: ${name || "my-skill"}
description: One line on when the agent should use this skill
---

# ${name || "My skill"}

## When to use
- …

## Rules
- …
`;
const SCRIPT_TEMPLATE = (file) => file.endsWith(".py") ? "#!/usr/bin/env python3\n# What this script does, in one line\n\n" : file.endsWith(".js") || file.endsWith(".mjs") ? "#!/usr/bin/env node\n// What this script does, in one line\n\n" : "#!/usr/bin/env bash\n# What this script does, in one line\nset -euo pipefail\n\n";

const ed = () => S.libEdit;
const close = () => { S.libEdit = null; update(); };

// scope: global | project; kind: skill | script; lib: the library to edit (or null for new)
export async function openLibraryEditor(scope, kind, lib, opts = {}) {
  const e = { scope, kind, isNew: !lib || !!opts.publish, publish: !!opts.publish, name: lib?.name || "", desc: "", content: "", oldPath: lib && !opts.publish ? lib.path : "", loading: !!lib, busy: false, key: Date.now() };
  if (!lib) e.content = kind === "script" ? "" : SKILL_TEMPLATE("");
  S.libEdit = e; update();
  if (!lib) return;
  try {
    if (scope === "global" && lib.managed) {
      const m = (await backend.ManagedLibraries()).find((x) => x.name === lib.name);
      e.content = m?.content ?? ""; e.desc = m?.desc || "";
    } else {
      e.content = await backend.ReadFile(lib.path);
      if (scope === "global") e.desc = lib.note || "";
    }
  } catch (err) { toast(errMsg(err)); }
  e.loading = false; e.key = Date.now(); update();
}

const setLibs = (libs) => { if (S.ws && libs) S.ws.libraries = libs; };

export async function deleteLibrary(scope, lib) {
  const managed = scope === "global";
  if (!(await confirmDialog(managed ? "It is removed from every project on this computer." : "It moves to SpecWright's trash and can be recovered from there.", { title: managed ? `Retire the corporate skill ${lib.name}?` : `Delete ${lib.name}?`, ok: managed ? "Retire" : "Delete", danger: true }))) return;
  try {
    setLibs(managed ? await backend.DeleteManagedLibrary(lib.name) : await backend.DeleteProjectLibrary(lib.path));
    toast(managed ? `${lib.name} retired.` : `${lib.name} deleted.`); update();
  } catch (err) { toast(errMsg(err)); }
}

async function save() {
  const e = ed();
  const name = e.name.trim();
  if (!name) return toast(e.kind === "script" ? "Give the script a file name, e.g. gen-dto.sh." : "Give the skill a name, e.g. orders-domain.");
  e.busy = true; update();
  try {
    const libs = e.scope === "global"
      ? await backend.SaveManagedLibrary(name, e.desc, e.content)
      : await backend.SaveProjectLibrary({ kind: e.kind, name, content: e.content, oldPath: e.oldPath });
    setLibs(libs);
    toast(e.scope === "global" ? `${name} published to every project.` : `${name} saved.`);
    close();
  } catch (err) { e.busy = false; toast(errMsg(err)); update(); }
}

export function LibraryEditor() {
  const e = ed();
  useEffect(() => {
    if (!e) return;
    const onKey = (k) => {
      if (k.key === "Escape") close();
      if ((k.metaKey || k.ctrlKey) && k.key.toLowerCase() === "s") { k.preventDefault(); save(); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [!!e]);
  if (!e) return null;
  const g = e.scope === "global", script = e.kind === "script";
  const title = (e.isNew ? (e.publish ? "Publish " : "New ") : "Edit ") + (g ? "corporate skill" : script ? "project script" : "project skill");
  const nameOk = script ? /^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$/.test(e.name.trim()) : /^[a-z0-9][a-z0-9._-]{0,59}$/.test(e.name.trim().replace(/\.md$/, ""));
  return (
    <div className="drawer-scrim" onMouseDown={(x) => x.target === x.currentTarget && close()}>
      <aside className="drawer lib-drawer" role="dialog" aria-modal="true" aria-labelledby="le-title">
        <header className="drawer-head"><h2 id="le-title">{title}</h2>
          {g ? <span className="badge badge-warn"><Icon.lock /> admin · all projects</span> : <span className="badge">this project</span>}
          <button className="link" style={{ marginLeft: "auto" }} onClick={close} aria-label="Close">Close</button></header>
        <p className="wf-desc">{g
          ? "Corporate skills are signed with your admin key and copied into every project on this computer (.codegen/skills/global/). Regular users can read them but not change them; edits to a project's copy are overwritten."
          : script ? <>Saved as an executable file in <code className="inline-code">.codegen/scripts/</code>. Select it under Project skills so the agent knows it can run it.</>
          : <>Saved as <code className="inline-code">.codegen/skills/project/&lt;name&gt;/SKILL.md</code>. Write what the agent should know or follow; Markdown formatting is kept.</>}</p>
        <div className="two-col">
          <label className="field"><span className="muted">{script ? "File name" : "Name"}</span>
            <input value={e.name} disabled={g && !e.isNew} placeholder={script ? "gen-dto.sh" : "orders-domain"} aria-invalid={!!e.name && !nameOk} style={{ fontFamily: "var(--font-mono)" }}
              onChange={(x) => { e.name = x.target.value; if (script && !e.content) e.content = SCRIPT_TEMPLATE(e.name); update(); }} />
            {!!e.name && !nameOk && <small className="err-text">{script ? "Letters, digits, '.', '-' and '_' only" : "Lowercase letters, digits, '.', '-' and '_' only (it becomes a folder name)"}</small>}</label>
          {g && <label className="field"><span className="muted">Description (shown in the wizard)</span><input value={e.desc} placeholder="House Java style for services" onChange={(x) => { e.desc = x.target.value; update(); }} /></label>}
        </div>
        {e.loading ? <div className="fp-center"><span className="spinner" /> Loading…</div>
          : script ? <label className="field" style={{ flex: 1 }}><span className="muted">Script</span>
              <textarea className="editor lib-code" spellCheck="false" value={e.content} onChange={(x) => { e.content = x.target.value; update(); }} placeholder={SCRIPT_TEMPLATE(e.name || "x.sh")} /></label>
          : <div className="field" style={{ flex: 1 }}><span className="muted">Skill (SKILL.md)</span>
              <RichEditor key={e.key} value={e.content} onChange={(md) => { e.content = md; update(); }} minHeight={320} big label="Skill text" /></div>}
        <div className="row-actions drawer-actions">
          <button className="btn primary" disabled={e.busy || e.loading || !nameOk || !e.content.trim()} onClick={save}>{e.busy ? "Saving…" : g ? (e.isNew ? "Sign and publish" : "Sign and publish new version") : "Save"}</button>
          <button className="btn secondary" onClick={close}>Cancel</button>
          <span className="muted" style={{ marginLeft: "auto", fontSize: ".85em" }}>⌘S to save · Esc to close</span>
        </div>
      </aside>
    </div>
  );
}
