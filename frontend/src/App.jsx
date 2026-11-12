import { useEffect } from "react";
import { backend } from "./backend.js";
import { S, update, useApp, onHubEvent, openWorkspace, refreshAdmin, totals, isCopilot, fmtK, currentStep } from "./store.js";
import { applyAppearance, onSystemThemeChange, resolveTheme } from "./theme.js";
import { Icon, Pill } from "./ui.jsx";
import { Logo } from "./Logo.jsx";
import { SecretPrompt } from "./SecretPrompt.jsx";
import { PermissionPrompt } from "./PermissionPrompt.jsx";
import { RulesEditors } from "./views/RulesEditors.jsx";
import { Setup, agentLabel } from "./views/Setup.jsx";
import { Wizard } from "./views/Wizard.jsx";
import { Tools } from "./views/Tools.jsx";
import { Inputs } from "./views/Inputs.jsx";
import { Workflow } from "./views/Workflow.jsx";
import { Files } from "./views/Files.jsx";
import { Metrics } from "./views/Metrics.jsx";
import { History } from "./views/History.jsx";
import { Playground } from "./views/Playground.jsx";
import { Admin } from "./views/Admin.jsx";
import { Appearance } from "./views/Appearance.jsx";
import { Flows } from "./views/Flows.jsx";
import { FlowRun } from "./views/FlowRun.jsx";
import { FilePreview } from "./FilePreview.jsx";
import { LibraryEditor } from "./views/LibraryEditor.jsx";
import { DialogHost } from "./Dialog.jsx";
import { RunPicker } from "./views/Playground.jsx";
import { installFileLinks } from "./FileLinks.jsx";

// The sidebar, in groups: building from a spec, tools, utilities, admin.
const GROUPS = [
  { id: "build", label: "Build", title: "Build from a spec", views: [
    { id: "wizard", label: "Development wizard", icon: Icon.wizard, C: Wizard },
    { id: "inputs", label: "Choose input", icon: Icon.inputs, C: Inputs },
    { id: "workflow", label: "Run spec workflow", icon: Icon.workflow, C: Workflow },
  ] },
  { id: "tools", label: "Tools", title: "Tools, playground and flows", views: [
    { id: "tools", label: "Tools", icon: Icon.tool, C: Tools },
    { id: "playground", label: "Prompt playground", icon: Icon.playground, C: Playground },
    { id: "flows", label: "Flow designer", icon: Icon.flow, C: Flows },
    { id: "flowrun", label: "Run flows", icon: Icon.run, C: FlowRun },
  ] },
  { id: "utils", label: "Utils", title: "Utilities", views: [
    { id: "files", label: "Project files", icon: Icon.files, C: Files },
    { id: "history", label: "Run history and replay", icon: Icon.history, C: History },
    { id: "metrics", label: "Metrics", icon: Icon.metrics, C: Metrics },
    { id: "appearance", label: "Appearance", icon: Icon.appearance, C: Appearance },
  ] },
  { id: "admin", label: "Admin", title: "Administration", bottom: true, views: [
    { id: "admin", label: "Admin", icon: Icon.shield, C: Admin },
  ] },
];
const VIEWS = GROUPS.flatMap((g) => g.views);

let booted = false;
async function boot() {
  if (booted) return; booted = true;
  backend.on(onHubEvent);
  S.info = await backend.Info().catch(() => null);
  refreshAdmin();
  if (S.setup.done && S.setup.provider && S.setup.provider !== "skipped") {
    // the chosen agent is a UI preference; any token was session-only
    await backend.SetAgent(S.setup.provider, "", S.setup.model || "").catch(() => {});
  }
  update();
  const ws = S.lastWorkspace || (backend.kind === "mock" ? S.info?.workspace : "");
  if (ws) await openWorkspace(ws);
}

export function App() {
  useApp();
  useEffect(() => { installFileLinks(); boot(); return onSystemThemeChange(() => { applyAppearance(S.appearance); update(); }); }, []);
  useEffect(() => applyAppearance(S.appearance), [S.appearance.theme, S.appearance.skin, S.appearance.fontSize, S.appearance.font, S.appearance.codeFont]);

  if (!S.setup.done) return <div className="app"><main className="main main-full"><Setup /></main></div>;
  const view = VIEWS.find((v) => v.id === S.view) || VIEWS[0];
  const wf = S.wf, statuses = wf?.steps.map((s) => s.status) || [];
  const gate = statuses.includes("paused");
  const runState = statuses.includes("running") ? "running" : gate ? "paused" : statuses.length && statuses.every((s) => s === "done") ? "done" : "idle";
  const cur = wf ? currentStep(wf) : 0, allDone = statuses.length && statuses.every((s) => s === "done");
  const t = totals();
  return (
    <>
      <div className="app">
        <nav className="activity" aria-label="Views">
          <button className="brand-mark" title="SpecWright · go to the workflow" aria-label="SpecWright home" onClick={() => { S.view = "workflow"; update(); }}><Logo size={42} /></button>
          {GROUPS.map((g) => (
          <div key={g.id} className={"activity-group" + (g.bottom ? " activity-bottom" : "")} role="group" aria-label={g.title}>
            <span className="activity-label" title={g.title}>{g.label}</span>
            {g.views.map((v) => (
            <button key={v.id} className={"activity-btn" + (S.view === v.id ? " active" : "")} title={`${v.label} · ${g.title}`} aria-label={v.label} aria-current={S.view === v.id ? "page" : undefined} onClick={() => { S.view = v.id; update(); }}>
              <v.icon />{v.id === "workflow" && gate && <span className="activity-dot" aria-label="needs approval" />}</button>))}
          </div>))}</nav>
        <main className="main" data-view={S.view}><view.C /></main>
        <footer className="statusbar">
          <span className="status-item status-brand"><Logo size={18} /><b>SpecWright</b></span>
          <span className="status-item"><Pill s={runState} /></span>
          {wf && <span className="status-item">{wf.name} · {allDone ? "complete" : `step ${cur + 1}/${wf.steps.length} ${wf.steps[cur].title}`}</span>}
          {gate && <span className="status-item status-warn">approval needed</span>}
          {t.in > 0 && <><span className="status-item" title="Tokens used in this workflow">{fmtK(t.in + t.out)} tokens</span><span className="status-item">{isCopilot() ? t.premium + " premium req" : "$" + t.usd.toFixed(3)}</span><span className="status-item">{t.tools} tools</span></>}
          <span className="status-spacer" />
          <span className="status-item">{agentLabel()}{S.setup.source ? " · " + S.setup.source : ""}</span>
          {S.ws && <span className="status-item">{S.ws.root.split("/").slice(-2).join("/")}</span>}
          <span className="status-item">{resolveTheme(S.appearance.theme).name}</span>
          <span className="status-item">{backend.kind === "desktop" ? "v" + (S.info?.version || "") : "browser preview · mock backend"}</span>
        </footer>
      </div>
      <RulesEditors />
      <SecretPrompt />
      <PermissionPrompt />
      <FilePreview />
      <LibraryEditor />
      <RunPicker />
      <DialogHost />
      <div className="toast" aria-live="polite">{S.toast && <div className="banner">{S.toast}</div>}</div>
    </>
  );
}
