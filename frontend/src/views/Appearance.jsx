import { backend } from "../backend.js";
import { ModelPicker } from "../ModelPicker.jsx";
import { S, update } from "../store.js";
import { resolveTheme, applyAppearance, ALL_THEMES, ALL_SKINS } from "../theme.js";
import { FAMILIES, ANDROID_SEEDS, UI_FONTS, CODE_FONTS } from "../palettes.js";
import { agentLabel } from "./Setup.jsx";
import { HousekeepingCard } from "../RunHousekeeping.jsx";
import { PackagesCard } from "./Packages.jsx";
import { RunSettings } from "./Workflow.jsx";

// Settings: one place for the app's settings, in tabs. The same settings stay
// where they were used before too (Run settings on the workflow, Packages &
// proxy in the wizard); they edit the same values.
const TABS = [["appearance", "Appearance"], ["agent", "Agent and model"], ["runs", "Runs"], ["network", "Network and packages"]];
export function openSettings(tab) { S.settingsTab = tab; S.view = "appearance"; update(); }

export function Appearance() {
  const a = S.appearance, cur = resolveTheme(a.theme);
  const set = (k, v) => { a[k] = v; applyAppearance(a); update(); };
  const Swatch = ({ t }) => { const u = t.ui; return (
    <button className={"theme-card" + (a.theme === t.id ? " active" : "")} aria-pressed={a.theme === t.id} onClick={() => set("theme", t.id)}>
      <span className="theme-preview" style={{ background: u["--bg"], borderColor: u["--border"] }} aria-hidden="true"><span style={{ background: u["--activity-bg"] }} /><span className="tp-body"><i style={{ background: u["--syn-keyword"] }} /><i style={{ background: u["--syn-string"] }} /><i style={{ background: u["--syn-function"] }} /><i style={{ background: u["--btn-bg"] }} /></span></span>
      <span className="theme-name">{t.name.replace(/^(GitHub|Copilot) /, "")}</span></button>); };
  const fam = S.themeFamily || cur.family || "github";
  const list = ALL_THEMES.filter((t) => t.family === fam);
  const custom = /^m3:/.test(a.theme) ? a.theme.split(":") : null;
  const seed = custom ? custom[1] : S.m3seed || "#6750a4", seedDark = custom ? custom[2] === "dark" : S.m3dark ?? true;
  const famSkin = ALL_SKINS.find((s) => s.family === cur.family);
  const setSeed = (hex, dark) => { S.m3seed = hex; S.m3dark = dark; set("theme", `m3:${hex.toLowerCase()}:${dark ? "dark" : "light"}`); };
  const cli = S.info?.agents?.find((x) => x.name === S.setup.provider);
  const tab = TABS.some(([id]) => id === S.settingsTab) ? S.settingsTab : "appearance";
  return (
    <section className="view"><header className="view-head"><h1>Settings</h1></header>
      <div className="tabs settings-tabs" role="tablist" aria-label="Settings">{TABS.map(([id, l]) => <button key={id} id={"st-" + id} role="tab" className="tab" aria-selected={tab === id} aria-controls="st-panel"
        tabIndex={tab === id ? 0 : -1} onClick={() => { S.settingsTab = id; update(); }}
        onKeyDown={(e) => { const k = TABS.findIndex(([x]) => x === tab), d = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0; if (d) { e.preventDefault(); const n = TABS[(k + d + TABS.length) % TABS.length][0]; S.settingsTab = n; update(); setTimeout(() => document.getElementById("st-" + n)?.focus(), 0); } }}>{l}</button>)}</div>
      <div id="st-panel" role="tabpanel" aria-labelledby={"st-" + tab} className="settings-panel">
      {tab === "appearance" && <>
      <p className="wf-desc">Themes change color; skins change layout and type. Pick a family: GitHub, Copilot or Android (Material 3).</p>
      <div className="card"><h3>Theme</h3><label className="check"><input type="radio" name="theme" checked={a.theme === "system"} onChange={() => set("theme", "system")} /> Match system (now {cur.name})</label>
        <div className="tabs" role="tablist" aria-label="Theme family" style={{ marginTop: 10 }}>{FAMILIES.map((f) => <button key={f.id} role="tab" className="tab" aria-selected={fam === f.id} onClick={() => { S.themeFamily = f.id; update(); }}>{f.name}</button>)}</div>
        <p className="muted" style={{ margin: "10px 0 0" }}>{FAMILIES.find((f) => f.id === fam)?.note}</p>
        <h4>Dark</h4><div className="theme-grid">{list.filter((t) => t.type === "dark").map((t) => <Swatch key={t.id} t={t} />)}</div>
        <h4>Light</h4><div className="theme-grid">{list.filter((t) => t.type === "light").map((t) => <Swatch key={t.id} t={t} />)}</div>
        {fam === "android" && <>
          <h4>Your own seed color</h4>
          <div className="m3-seed">
            <input type="color" value={seed} aria-label="Seed color" onChange={(e) => setSeed(e.target.value, seedDark)} />
            <div className="chips">{ANDROID_SEEDS.map((s) => <button key={s.key} className="chip m3-chip" title={s.name} aria-label={s.name} style={{ background: s.seed }} onClick={() => setSeed(s.seed, seedDark)} />)}</div>
            <div className="seg" role="group" aria-label="Brightness"><button className={"btn " + (seedDark ? "primary" : "secondary")} onClick={() => setSeed(seed, true)}>Dark</button><button className={"btn " + (!seedDark ? "primary" : "secondary")} onClick={() => setSeed(seed, false)}>Light</button></div>
            {custom && <span className="badge badge-ok">in use</span>}
          </div>
          <p className="muted" style={{ marginTop: 6 }}>Material You builds the whole scheme (buttons, containers, surfaces, outlines and code colors) from the one color you pick.</p>
        </>}</div>
      {famSkin && a.skin !== famSkin.id && <div className="banner style-hint"><span>You're using a {FAMILIES.find((f) => f.id === cur.family)?.name} theme. Want the matching style too ({famSkin.name}: fonts, shapes, buttons)?</span>
        <button className="btn primary" onClick={() => { a.font = ""; a.codeFont = ""; set("skin", famSkin.id); }}>Use {famSkin.name} style</button></div>}
      <div className="card"><h3>Style</h3><p className="muted" style={{ margin: "0 0 8px" }}>Fonts, shapes and spacing. Colors come from the theme, so any style works with any theme.</p>
        <div className="skin-grid">{ALL_SKINS.map((s) => <label key={s.id} className={"skin-card skin-sample" + (a.skin === s.id ? " active" : "")} data-sample={s.id}><input type="radio" name="skin" checked={a.skin === s.id} onChange={() => set("skin", s.id)} /><b>{s.name}</b><span className="muted">{s.note}</span><span className="sample-row" aria-hidden="true"><span className="sample-btn">Run</span><span className="sample-chip">chip</span></span></label>)}</div></div>
      <div className="card"><h3>Fonts</h3>
        <div className="two-col">
          <label className="field"><span className="muted">Interface font</span><select value={a.font || ""} onChange={(e) => set("font", e.target.value)}>{UI_FONTS.map((f) => <option key={f.id} value={f.id}>{f.name}</option>)}</select></label>
          <label className="field"><span className="muted">Code font</span><select value={a.codeFont || ""} onChange={(e) => set("codeFont", e.target.value)}>{CODE_FONTS.map((f) => <option key={f.id} value={f.id}>{f.name}</option>)}</select></label>
        </div>
        <div className="font-preview"><span>SpecWright turns specs into working code — 0123456789</span><code>func (o Order) Total() float64 {"{"} return o.Price {"}"}</code></div>
        <div className="field inline size-row" style={{ marginTop: 10 }}><label htmlFor="ap-size">Text size {a.fontSize}px</label>
          <span className="size-slider"><input id="ap-size" type="range" min="11" max="18" step="1" list="ap-size-ticks" value={a.fontSize} onChange={(e) => set("fontSize", +e.target.value)} />
            <datalist id="ap-size-ticks">{[11, 12, 13, 14, 15, 16, 17, 18].map((v) => <option key={v} value={v} label={String(v)} />)}</datalist>
            <span className="size-ticks" aria-hidden="true">{[11, 12, 13, 14, 15, 16, 17, 18].map((v) => <span key={v} className={v === a.fontSize ? "on" : ""}>{v}</span>)}</span></span>
          {a.fontSize !== 13 && <button className="link" onClick={() => set("fontSize", 13)}>Reset to 13px</button>}</div>
        <p className="muted" style={{ marginTop: 6 }}>Roboto, Roboto Mono, Mona Sans, Inter and JetBrains Mono ship with the app (SIL Open Font License), so they work offline.</p></div>
      </>}
      {tab === "runs" && <>
        <HousekeepingCard />
        <div className="card"><h3>Spec workflow runs</h3>
          {S.wf ? <><p className="muted" style={{ margin: "0 0 6px" }}>Budgets for {S.ws?.speckit?.project ? <b>{S.ws.speckit.project}</b> : "this project"}'s workflow. Retries and the approval gate are for the step selected on Run spec workflow.</p><RunSettings wf={S.wf} open /></>
            : <p className="muted">Open a project to set its workflow budgets, retries and approval gates.</p>}</div>
      </>}
      {tab === "network" && (S.ws ? <PackagesCard /> : <div className="card"><h3>Packages & proxy</h3><p className="muted">Open a project first: package and proxy settings are saved in its <code className="inline-code">.codegen/packages.json</code>.</p></div>)}
      {tab === "agent" && <div className="card"><h3>Agent and model</h3>
        <p>Agent: <b>{agentLabel()}</b>{S.setup.source ? ` · ${S.setup.source}` : ""}{cli?.version ? ` · ${cli.version}` : ""}</p>
        <div className="field inline"><span>App-wide model</span><ModelPicker value={S.setup.model || ""} inherit="" label="Model" onChange={(v) => { S.setup.model = v; update(); backend.SetAgent(S.setup.provider === "skipped" ? "mock" : S.setup.provider, "", v).catch(() => {}); }} /></div>
        <p className="muted" style={{ marginTop: 4 }}>Used wherever a screen leaves its own model on Default: workflow steps, the Playground, Ask AI, tool tests and flows.</p>
        <p className="muted" style={{ marginTop: 8 }}>The app never stores agent credentials. The CLIs use their own sign-in; a pasted token lives in memory until the app quits. Run history is kept in <code className="inline-code">{S.info?.storeDir}</code>.</p>
        <div className="row-actions"><button className="btn secondary" onClick={() => { backend.ClearToken(); S.setup = { ...S.setup, done: false }; update(); }}>Change agent or credentials</button></div></div>}
      </div>
    </section>
  );
}
