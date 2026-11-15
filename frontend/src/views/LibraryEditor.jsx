// Create and edit Development wizard libraries in a side drawer.
//   Corporate libraries (global): admin mode only; saving signs and publishes
//   the skill for every project in the app.
//   Project skills and scripts: anyone; saved as files in the project.
import { useEffect } from "react";
import { confirmDialog } from "../Dialog.jsx";
import { backend } from "../backend.js";
import { S, update, toast, errMsg } from "../store.js";
import { Icon } from "../ui.jsx";
import { RichEditor } from "../RichEditor.jsx";
import { ModelPicker } from "../ModelPicker.jsx";

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

// A project skill's folder (it may still be a single <name>.md file).
export const projectSkills = () => (S.ws?.libraries || []).filter((l) => l.scope === "project" && l.kind === "skill");
export const skillDirOf = (name, scope = "project") => (S.ws?.libraries || []).find((l) => l.scope === scope && l.kind === "skill" && l.name === name)?.dir || `.codegen/skills/${scope}/${name}`;
// A skill file or doc is Markdown unless its name says it is code or data;
// a name without an extension gets ".md".
const CODE_EXT = /\.(json|ya?ml|xml|csv|tsv|sh|bash|zsh|py|m?js|cjs|ts|java|kt|go|cs|sql|properties|toml|ini|env|txt|html?|css|gradle)$/i;
const asDocName = (n) => { n = n.trim(); return !n || n.endsWith("/") || /\.[^/]+$/.test(n.split("/").pop()) ? n : n + ".md"; };
const isMdDoc = (n) => !CODE_EXT.test(n.trim());
const DOC_TEMPLATE = (file) => asDocName(file).endsWith(".md") ? "# " + (file.split("/").pop().replace(/\.md$/, "").replace(/[-_]/g, " ") || "Notes") + "\n\n" : "";
const segOk = (p) => !!p && p !== "SKILL.md" && p.split("/").length <= 6 && p.split("/").every((x) => /^[A-Za-z0-9_][A-Za-z0-9._-]{0,79}$/.test(x));

// scope: global | project; kind: skill | script | doc; lib: the library to edit (or null for new).
// opts.skill and opts.file: a file in a project skill's folder (scripts/<file> for a script,
// any path such as docs/api.md for a doc). A new script goes into a skill's scripts/ folder.
export async function openLibraryEditor(scope, kind, lib, opts = {}) {
  const inSkill = opts.skill !== undefined || kind === "doc" || (kind === "script" && !lib);
  const skill = inSkill ? (opts.skill ?? projectSkills()[0]?.name ?? "") : "";
  const file = opts.file || "";
  const e = { scope, kind, skill, chooseSkill: scope === "project" && kind === "script" && !lib && !file && opts.skill === undefined,
    isNew: (!lib && !file) || !!opts.publish, publish: !!opts.publish,
    name: file ? (kind === "script" ? file.replace(/^scripts\//, "") : file) : lib?.name || (kind === "doc" ? "docs/" : ""),
    desc: "", content: "", oldPath: file || (lib && !opts.publish ? lib.path : ""), loading: !!lib || !!file, busy: false, key: Date.now(),
    ai: { open: kind === "script" && !lib && !file, prompt: "", lang: "", model: "", busy: false, summary: "", undo: null } };
  if (!lib && !file) e.content = kind === "skill" ? SKILL_TEMPLATE("") : "";
  S.libEdit = e; update();
  if (!lib && !file) return;
  try {
    if (file) {
      // a corporate skill's file comes from the signed catalog, not the project's copy
      e.content = scope === "global" ? (await backend.ManagedLibraries()).find((x) => x.name === skill)?.files?.[file] ?? "" : await backend.ReadFile(skillDirOf(skill) + "/" + file);
      e.loading = false; e.key = Date.now(); update(); return;
    }
    if (scope === "global" && lib.managed) {
      const m = (await backend.ManagedLibraries()).find((x) => x.name === lib.name);
      e.content = m?.content ?? ""; e.desc = m?.desc || "";
    } else {
      e.content = await backend.ReadFile(lib.path);
      if (scope === "global") e.desc = lib.note || "";
    }
  } catch (err) { toast(errMsg(err), "error"); }
  e.loading = false; e.key = Date.now(); update();
}

const setLibs = (libs) => { if (S.ws && libs) S.ws.libraries = libs; };

export async function deleteSkillFile(skill, file, scope = "project") {
  const g = scope === "global";
  if (!(await confirmDialog(g ? "The corporate skill is signed and published again without it, and it is removed from every project. Update any link to it in SKILL.md." : "It moves to SpecWright's trash and can be recovered from there. Update any link to it in SKILL.md.", { title: `Delete ${file} from ${skill}?`, ok: g ? "Delete and publish" : "Delete", danger: true }))) return;
  try { setLibs(g ? await backend.DeleteManagedLibraryFile(skill, file) : await backend.DeleteSkillFile(skill, file)); toast(g ? `${file} removed from ${skill} and published.` : `${file} deleted.`); update(); } catch (err) { toast(errMsg(err), "error"); }
}

export async function deleteLibrary(scope, lib) {
  const managed = scope === "global";
  if (!(await confirmDialog(managed ? "It is removed from every project on this computer." : "It moves to SpecWright's trash and can be recovered from there.", { title: managed ? `Retire the corporate skill ${lib.name}?` : `Delete ${lib.name}?`, ok: managed ? "Retire" : "Delete", danger: true }))) return;
  try {
    setLibs(managed ? await backend.DeleteManagedLibrary(lib.name) : await backend.DeleteProjectLibrary(lib.path));
    toast(managed ? `${lib.name} retired.` : `${lib.name} deleted.`); update();
  } catch (err) { toast(errMsg(err), "error"); }
}

async function save() {
  const e = ed();
  const name = e.kind === "doc" ? asDocName(e.name) : e.name.trim();
  if (!name) return toast(e.kind === "script" ? "Give the script a file name, e.g. gen-dto.sh." : e.kind === "doc" ? "Give the file a path, e.g. docs/api.md." : "Give the skill a name, e.g. orders-domain.");
  e.busy = true; update();
  try {
    const fpath = e.kind === "script" ? "scripts/" + name : name;
    const libs = e.scope === "global" && (e.kind === "doc" || e.kind === "script")
      ? await backend.SaveManagedLibraryFile(e.skill, fpath, e.content, e.oldPath)
      : e.scope === "project" && (e.kind === "doc" || (e.kind === "script" && e.skill))
      ? await backend.SaveSkillFile({ skill: e.skill, path: e.kind === "script" ? "scripts/" + name : name, content: e.content, oldPath: e.oldPath })
      : e.scope === "global"
      ? await backend.SaveManagedLibrary(name, e.desc, e.content)
      : await backend.SaveProjectLibrary({ kind: e.kind, name, content: e.content, oldPath: e.oldPath });
    setLibs(libs);
    toast(e.scope === "global" ? (e.kind === "skill" ? `${name} published to every project.` : `${e.skill} published again with ${fpath}.`) : `${name} saved.`);
    close();
  } catch (err) { e.busy = false; toast(errMsg(err), "error"); update(); }
}

// The AI writes the script (or changes it) from a plain description. The
// result goes into the editor; nothing is saved until Save.
async function writeWithAI() {
  const e = ed(), a = e.ai;
  if (!a.prompt.trim()) return toast(e.content.trim() ? "Say what to change in the script." : "Describe what the script should do.");
  a.busy = true; a.summary = ""; update();
  try {
    const d = await backend.GenerateScript({ description: a.prompt, language: a.lang, fileName: e.name.trim(), current: e.content.trim() ? e.content : "", model: a.model });
    if (ed() !== e) return;
    a.undo = { name: e.name, content: e.content };
    e.content = d.content; if (!e.name.trim() || e.name.trim() !== d.fileName && !e.oldPath) e.name = d.fileName;
    a.summary = d.summary || ""; a.prompt = "";
  } catch (err) { toast(errMsg(err), "error"); }
  a.busy = false; update();
}

function ScriptAI() {
  const e = ed(), a = e.ai, has = !!e.content.trim();
  if (!a.open) return <button className="btn secondary le-ai-open" onClick={() => { a.open = true; update(); }}><Icon.spark /> {has ? "Change it with AI" : "Write it with AI"}</button>;
  return (
    <div className="card le-ai">
      <div className="card-head"><Icon.spark /><h3>{has ? "Change it with AI" : "Write it with AI"}</h3>
        <span className="muted" style={{ fontWeight: 400 }}>{has ? "say what to change; the AI edits the script below" : "not sure how to write it? describe what it should do"}</span>
        <button className="link" style={{ marginLeft: "auto" }} onClick={() => { a.open = false; update(); }}>Hide</button></div>
      <textarea rows={3} value={a.prompt} disabled={a.busy} autoFocus
        placeholder={has ? "e.g. Also accept a folder as the first argument, and print the total at the end" : "e.g. Find every TODO and FIXME under src/, and print them grouped by file with line numbers"}
        onChange={(x) => { a.prompt = x.target.value; update(); }} onKeyDown={(x) => { if (x.key === "Enter" && (x.metaKey || x.ctrlKey)) { x.preventDefault(); writeWithAI(); } }} />
      <div className="row-actions le-ai-row">
        <button className="btn primary" disabled={a.busy || !a.prompt.trim()} onClick={writeWithAI}>{a.busy ? <><span className="spinner" /> Writing…</> : has ? "Change script" : "Write script"}</button>
        {!has && <label className="le-ai-lang"><span className="muted">Language</span>
          <select value={a.lang} disabled={a.busy} onChange={(x) => { a.lang = x.target.value; update(); }}>
            <option value="">Let the AI choose</option><option value="bash">Bash</option><option value="python">Python</option><option value="node">Node.js</option></select></label>}
        <ModelPicker compact value={a.model} disabled={a.busy} onChange={(v) => { a.model = v; update(); }} label="Model" />
        {a.undo && !a.busy && <button className="link" onClick={() => { e.name = a.undo.name; e.content = a.undo.content; a.undo = null; a.summary = ""; update(); }}>Undo</button>}
      </div>
      {a.summary && <p className="le-ai-sum"><Icon.check /><span>{a.summary} <span className="muted">Review it below, then Save.</span></span></p>}
    </div>
  );
}

export function LibraryEditor() {
  const e = ed();
  useEffect(() => {
    if (!e) return;
    const onKey = (k) => {
      if (k.key === "Escape" && !ed()?.ai?.busy) close();
      if ((k.metaKey || k.ctrlKey) && k.key.toLowerCase() === "s") { k.preventDefault(); save(); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [!!e]);
  if (!e) return null;
  const g = e.scope === "global", script = e.kind === "script", doc = e.kind === "doc";
  const title = (e.isNew ? (e.publish ? "Publish " : "New ") : "Edit ") + (g ? (script ? "corporate skill script" : doc ? "corporate skill file" : "corporate skill") : script ? (e.skill ? "skill script" : "shared script") : doc ? "skill file" : "project skill");
  const nameOk = script ? /^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$/.test(e.name.trim()) : doc ? segOk(asDocName(e.name)) : /^[a-z0-9][a-z0-9._-]{0,59}$/.test(e.name.trim().replace(/\.md$/, ""));
  const gfile = g && (script || doc);
  const dir = e.skill ? skillDirOf(e.skill, e.scope) : "", skills = projectSkills(), md = doc && isMdDoc(e.name), docName = asDocName(e.name);
  return (
    <div className="drawer-scrim" onMouseDown={(x) => x.target === x.currentTarget && close()}>
      <aside className="drawer lib-drawer" role="dialog" aria-modal="true" aria-labelledby="le-title">
        <header className="drawer-head"><h2 id="le-title">{title}</h2>
          {g ? <span className="badge badge-warn"><Icon.lock /> admin · all projects</span> : <span className="badge">this project</span>}
          <button className="link" style={{ marginLeft: "auto" }} onClick={close} aria-label="Close">Close</button></header>
        <p className="wf-desc">{gfile
          ? <>Part of the corporate skill <b>{e.skill}</b>: saving signs and publishes the skill again as a new version, and every project gets the file at <code className="inline-code">{dir}/{script ? "scripts/" + (e.name.trim() || "<file>") : docName || "<path>"}</code>{script ? " (executable)" : ""}. Link to it from SKILL.md with a relative path, e.g. <code className="inline-code">{script ? "scripts/" + (e.name.trim() || "check.sh") : "[notes](" + (docName || "docs/rules.md") + ")"}</code>.</>
          : g
          ? "Corporate skills are signed with your admin key and copied into every project on this computer (.codegen/skills/global/). Regular users can read them but not change them; edits to a project's copy are overwritten. Docs in sub-folders and scripts/ can be added to a corporate skill from the wizard (+ Script, + File or doc); they are signed and copied with it."
          : script && e.skill ? <>Saved as an executable file in <code className="inline-code">{dir}/scripts/{e.name.trim() || "<file>"}</code>. When the skill is selected, the agent is told the script is there; mention in SKILL.md when to run it, e.g. <code className="inline-code">scripts/{e.name.trim() || "gen.sh"}</code>.</>
          : script ? <>Saved as an executable file in <code className="inline-code">.codegen/scripts/</code>, shared by the project (the old place for scripts). Select it under Project skills so the agent knows it can run it.</>
          : doc ? <>Saved as <code className="inline-code">{dir}/{docName || "<path>"}</code>. Sub-folders are created for you, and a name without an extension is saved as Markdown (<code className="inline-code">.md</code>). Link to it from SKILL.md with a relative path, e.g. <code className="inline-code">[API notes]({docName || "docs/api.md"})</code>; the agent is told which files the skill has.</>
          : <>Saved as <code className="inline-code">.codegen/skills/project/&lt;name&gt;/SKILL.md</code>. Write what the agent should know or follow; Markdown formatting is kept. A skill can have more files in its folder (docs, references, <code className="inline-code">scripts/</code>): add them from the wizard and link to them with relative paths such as <code className="inline-code">[API notes](docs/api.md)</code>.</>}</p>
        <div className="two-col">
          {(e.chooseSkill || ((script || doc) && e.skill)) && <label className="field"><span className="muted">Skill</span>
            {e.chooseSkill ? <select value={e.skill} onChange={(x) => { e.skill = x.target.value; update(); }}>
                {skills.map((l) => <option key={l.id} value={l.name}>{l.name}  ·  {(l.dir || ".codegen/skills/project/" + l.name)}/scripts/</option>)}
                <option value="">No skill · shared .codegen/scripts/</option></select>
              : <input value={e.skill} disabled style={{ fontFamily: "var(--font-mono)" }} />}
            {e.chooseSkill && !skills.length && <small className="muted">This project has no skills of its own yet, so the script goes to the shared folder. To keep scripts inside a skill (<code className="inline-code">&lt;skill&gt;/scripts/</code>), create one with <b>New skill</b> first.</small>}</label>}
          <label className="field"><span className="muted">{script ? "File name" : doc ? "Path inside the skill folder" : "Name"}</span>
            <input value={e.name} disabled={g && !gfile && !e.isNew} placeholder={script ? "gen-dto.sh" : doc ? "docs/api.md" : "orders-domain"} aria-invalid={!!e.name && !nameOk} style={{ fontFamily: "var(--font-mono)" }}
              onChange={(x) => { e.name = x.target.value; if (script && !e.content) e.content = SCRIPT_TEMPLATE(e.name); update(); }}
              onBlur={() => { if (doc && !e.content.trim() && nameOk) { e.content = DOC_TEMPLATE(e.name.trim()); e.key = Date.now(); update(); } }} />
            {!!e.name && !nameOk && !(doc && e.name.endsWith("/")) && <small className="err-text">{script ? "Letters, digits, '.', '-' and '_' only" : doc ? "Folders and a file name, e.g. docs/api.md: letters, digits, '.', '-' and '_' (not SKILL.md)" : "Lowercase letters, digits, '.', '-' and '_' only (it becomes a folder name)"}</small>}</label>
          {g && <label className="field"><span className="muted">Description (shown in the wizard)</span><input value={e.desc} placeholder="House Java style for services" onChange={(x) => { e.desc = x.target.value; update(); }} /></label>}
        </div>
        {script && !e.loading && <ScriptAI />}
        {e.loading ? <div className="fp-center"><span className="spinner" /> Loading…</div>
          : script ? <label className="field" style={{ flex: 1 }}><span className="muted">Script</span>
              <textarea className="editor lib-code" spellCheck="false" value={e.content} readOnly={e.ai.busy} onChange={(x) => { e.content = x.target.value; update(); }} placeholder={SCRIPT_TEMPLATE(e.name || "x.sh") + "# …or use Write it with AI above"} /></label>
          : doc && !md ? <label className="field" style={{ flex: 1 }}><span className="muted">File</span>
              <textarea className="editor lib-code" spellCheck="false" value={e.content} onChange={(x) => { e.content = x.target.value; update(); }} placeholder="Text, JSON, YAML, a template…" /></label>
          : doc ? <div className="field" style={{ flex: 1 }}><span className="muted">Document (Markdown)</span>
              <RichEditor key={e.key} value={e.content} onChange={(m) => { e.content = m; update(); }} minHeight={320} big label="Document text" /></div>
          : <div className="field" style={{ flex: 1 }}><span className="muted">Skill (SKILL.md)</span>
              <RichEditor key={e.key} value={e.content} onChange={(md) => { e.content = md; update(); }} minHeight={320} big label="Skill text" /></div>}
        <div className="row-actions drawer-actions">
          <button className="btn primary" disabled={e.busy || e.loading || !nameOk || !e.content.trim()} onClick={save}>{e.busy ? "Saving…" : g ? (e.isNew && !gfile ? "Sign and publish" : "Sign and publish new version") : "Save"}</button>
          <button className="btn secondary" onClick={close}>Cancel</button>
          <span className="muted" style={{ marginLeft: "auto", fontSize: ".85em" }}>⌘S to save · Esc to close</span>
        </div>
      </aside>
    </div>
  );
}
