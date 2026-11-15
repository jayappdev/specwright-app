import { useEffect, useSyncExternalStore } from "react";
import { backend } from "./backend.js";
import {
  S,
  update,
  useApp,
  onHubEvent,
  openWorkspace,
  refreshAdmin,
  totals,
  isCopilot,
  fmtK,
  currentStep,
  hideToast,
  toastHold,
  toastRelease,
} from "./store.js";
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
import { FlowRun, showFlowRun } from "./views/FlowRun.jsx";
import { FlowGuide } from "./views/FlowGuide.jsx";
import { FilePreview } from "./FilePreview.jsx";
import { LibraryEditor } from "./views/LibraryEditor.jsx";
import { DialogHost } from "./Dialog.jsx";
import { RunPicker } from "./views/Playground.jsx";
import { installFileLinks } from "./FileLinks.jsx";
import { installModalFocus, installTooltips } from "./a11y.js";
import { CommandPalette, useCommandPalette, openPalette } from "./CommandPalette.jsx";
import { Notices } from "./Notices.jsx";
import { Pane } from "./Pane.jsx";
import { openSettings } from "./views/Appearance.jsx";

// The sidebar, in groups: building from a spec, tools, utilities, admin.
const GROUPS = [
  {
    id: "build",
    label: "Build",
    title: "Build from a spec",
    views: [
      { id: "wizard", label: "Development wizard", icon: Icon.wizard, C: Wizard },
      { id: "inputs", label: "Choose input", icon: Icon.inputs, C: Inputs },
      { id: "workflow", label: "Run spec workflow", icon: Icon.workflow, C: Workflow },
    ],
  },
  {
    id: "tools",
    label: "Tools",
    title: "Tools, playground and flows",
    views: [
      { id: "tools", label: "Tools", icon: Icon.tool, C: Tools },
      { id: "playground", label: "Prompt playground", icon: Icon.playground, C: Playground },
      { id: "flows", label: "Flow designer", icon: Icon.flow, C: Flows },
      { id: "flowrun", label: "Run flows", icon: Icon.run, C: FlowRun },
      { id: "flowguide", label: "Flow step guide", icon: Icon.book, C: FlowGuide, sub: true }, // under the Flow designer; labelled only when the sidebar is open
    ],
  },
  {
    id: "utils",
    label: "Utils",
    title: "Utilities",
    views: [
      { id: "files", label: "Project files", icon: Icon.files, C: Files },
      { id: "history", label: "Run history and replay", icon: Icon.history, C: History },
      { id: "metrics", label: "Metrics", icon: Icon.metrics, C: Metrics },
      { id: "appearance", label: "Settings", icon: Icon.appearance, C: Appearance },
    ],
  },
  {
    id: "admin",
    label: "Admin",
    title: "Administration",
    bottom: true,
    views: [{ id: "admin", label: "Admin", icon: Icon.shield, C: Admin }],
  },
];
const VIEWS = GROUPS.flatMap((g) => g.views);
const NAV = GROUPS.flatMap((g) => g.views.map((v) => ({ id: v.id, label: v.label, group: g.label })));

// the sidebar shows labels when it is open: by choice, or automatically on wide windows
const WIDE = "(min-width: 1280px)";
const wide = () => window.matchMedia?.(WIDE).matches ?? true;
const onWide = (f) => {
  const m = window.matchMedia?.(WIDE);
  m?.addEventListener("change", f);
  return () => m?.removeEventListener("change", f);
};
const go = (view) => {
  S.view = view;
  update();
};
const toggleRail = () => {
  S.rail = S.railOpen ? "closed" : "open";
  update();
};

let booted = false;
async function boot() {
  if (booted) return;
  booted = true;
  backend.on(onHubEvent);
  S.info = await backend.Info().catch(() => null);
  refreshAdmin();
  if (S.setup.done && S.setup.provider && S.setup.provider !== "skipped") {
    // the chosen agent is a UI preference; any token was session-only
    await backend.SetAgent(S.setup.provider, "", S.setup.model || "").catch(() => {});
  }
  update();
  // the dev server's open folder wins (the remembered one may be the mock's)
  const ws =
    (backend.kind === "dev" && S.info?.workspace) ||
    S.lastWorkspace ||
    (backend.kind === "mock" ? S.info?.workspace : "");
  if (ws) await openWorkspace(ws);
}

export function App() {
  useApp();
  const isWide = useSyncExternalStore(onWide, wide);
  S.railOpen = S.rail ? S.rail === "open" : isWide;
  useCommandPalette(NAV, toggleRail);
  useEffect(() => {
    installFileLinks();
    installModalFocus();
    installTooltips();
    boot();
    return onSystemThemeChange(() => {
      applyAppearance(S.appearance);
      update();
    });
  }, []);
  useEffect(
    () => applyAppearance(S.appearance),
    [S.appearance.theme, S.appearance.skin, S.appearance.fontSize, S.appearance.font, S.appearance.codeFont],
  );

  if (!S.setup.done)
    return (
      <div className="app">
        <main className="main main-full">
          <Setup />
        </main>
      </div>
    );
  const view = VIEWS.find((v) => v.id === S.view) || VIEWS[0];
  const wf = S.wf,
    statuses = wf?.steps.map((s) => s.status) || [];
  const gate = statuses.includes("paused");
  const wfState = statuses.includes("running")
    ? "running"
    : gate
      ? "paused"
      : statuses.length && statuses.every((s) => s === "done")
        ? "done"
        : "idle";
  // flow runs count too: one waiting for review must show from every view
  const flowRuns = Object.values(S.flowRuns || {}),
    flowWait = flowRuns.filter((r) => r?.status === "review"),
    flowBusy = flowRuns.filter((r) => r?.status === "running");
  const runState =
    gate || flowWait.length ? "paused" : wfState === "running" || flowBusy.length ? "running" : wfState;
  const cur = wf ? currentStep(wf) : 0,
    allDone = statuses.length && statuses.every((s) => s === "done");
  const t = totals();
  return (
    <>
      <div className={"app" + (S.railOpen ? " rail-open" : "")}>
        <nav className="activity" aria-label="Views">
          <button
            className="brand-mark"
            title="SpecWright · go to the workflow"
            aria-label="SpecWright home"
            onClick={() => {
              S.view = "workflow";
              update();
            }}
          >
            <Logo size={42} />
          </button>
          {GROUPS.map((g) => (
            <div
              key={g.id}
              className={"activity-group" + (g.bottom ? " activity-bottom" : "")}
              role="group"
              aria-label={g.title}
            >
              <span className="activity-label" title={g.title}>
                {g.label}
              </span>
              {g.views
                .filter((v) => !v.sub || S.railOpen || S.view === v.id)
                .map((v) => {
                  const k = NAV.findIndex((x) => x.id === v.id);
                  return (
                    <button
                      key={v.id}
                      className={
                        "activity-btn" + (S.view === v.id ? " active" : "") + (v.sub ? " activity-sub" : "")
                      }
                      title={`${v.label} · ${g.title}${k < 9 ? ` (⌘${k + 1})` : ""}`}
                      aria-label={v.label}
                      aria-current={S.view === v.id ? "page" : undefined}
                      onClick={() => {
                        S.view = v.id;
                        update();
                      }}
                    >
                      <v.icon />
                      <span className="activity-text">{v.label}</span>
                      {v.id === "workflow" && wf && !allDone && (
                        <span className="activity-meta">
                          {cur + 1}/{wf.steps.length}
                        </span>
                      )}
                      {v.id === "inputs" && S.ws?.speckit?.initialized && (
                        <span className="activity-meta" aria-label="Spec Kit set up">
                          ✓
                        </span>
                      )}
                      {v.id === "workflow" && gate && (
                        <span className="activity-dot" aria-label="needs approval" />
                      )}
                      {v.id === "flowrun" && flowWait.length > 0 && (
                        <span
                          className="activity-dot"
                          aria-label={`${flowWait.length} flow run${flowWait.length === 1 ? "" : "s"} waiting for you`}
                        />
                      )}
                    </button>
                  );
                })}
            </div>
          ))}
          <div className="activity-tools">
            <button
              className="activity-btn"
              title="Search views and flows (⌘K / Ctrl+K)"
              aria-label="Search views and flows"
              onClick={openPalette}
            >
              <Icon.search />
              <span className="activity-text">
                Search… <kbd>⌘K</kbd>
              </span>
            </button>
            <button
              className="activity-btn"
              title={S.railOpen ? "Hide the labels" : "Show the labels"}
              aria-label={S.railOpen ? "Hide sidebar labels" : "Show sidebar labels"}
              aria-expanded={S.railOpen}
              onClick={toggleRail}
            >
              <span className="rail-chev" aria-hidden="true">
                {S.railOpen ? "«" : "»"}
              </span>
              <span className="activity-text">Hide labels</span>
            </button>
          </div>
        </nav>
        {S.railOpen && (
          <Pane
            k="rail"
            v="--rail-w"
            pane=":scope > .activity"
            min={160}
            max={360}
            label="Resize the sidebar"
            className="rail-handle"
          />
        )}
        <main className="main" data-view={S.view}>
          <view.C />
        </main>
        <footer className="statusbar">
          <span className="status-item status-brand">
            <Logo size={18} />
            <b>SpecWright</b>
          </span>
          <span className="status-item">
            <Pill s={runState} />
          </span>
          {wf && (
            <span className="status-item">
              {wf.name} · {allDone ? "complete" : `step ${cur + 1}/${wf.steps.length} ${wf.steps[cur].title}`}
            </span>
          )}
          {gate && (
            <button
              className="status-item status-warn status-btn"
              title="Go to the step that needs your approval"
              onClick={() => {
                S.sel = cur;
                S.view = "workflow";
                update();
              }}
            >
              approval needed
            </button>
          )}
          {flowWait.length > 0 && (
            <button
              className="status-item status-warn status-btn"
              title="Open the flow run that is waiting for your review"
              onClick={() => showFlowRun(flowWait[0].id)}
            >
              {flowWait.length === 1
                ? `flow waiting for you${flowWait[0].flowName ? ": " + flowWait[0].flowName : ""}`
                : `${flowWait.length} flows waiting for you`}
            </button>
          )}
          {!flowWait.length && flowBusy.length > 0 && (
            <button
              className="status-item status-btn"
              title="Open the running flow"
              onClick={() => showFlowRun(flowBusy[0].id)}
            >
              {flowBusy.length === 1
                ? `flow running${flowBusy[0].flowName ? ": " + flowBusy[0].flowName : ""}`
                : `${flowBusy.length} flows running`}
            </button>
          )}
          {t.in > 0 && (
            <button
              className="status-item status-btn"
              title="Used by this workflow · open Metrics"
              onClick={() => go("metrics")}
            >
              {fmtK(t.in + t.out)} tokens ·{" "}
              {isCopilot() ? t.premium + " premium req" : "$" + t.usd.toFixed(3)} · {t.tools} tools
            </button>
          )}
          <span className="status-spacer" />
          <Notices />
          <button
            className="status-item status-btn"
            title="Agent and model · open Settings"
            onClick={() => openSettings("agent")}
          >
            {agentLabel()}
            {S.setup.source ? " · " + S.setup.source : ""}
          </button>
          {S.ws && (
            <button
              className="status-item status-btn"
              title={S.ws.root + " · open Choose input"}
              onClick={() => go("inputs")}
            >
              {S.ws.root.split("/").slice(-2).join("/")}
            </button>
          )}
          {S.admin?.hasKey && (
            <button
              className="status-item status-btn"
              title={
                S.admin.unlocked ? "Admin mode is unlocked · open Admin" : "Admin mode is locked · open Admin"
              }
              onClick={() => go("admin")}
            >
              <Icon.lock /> {S.admin.unlocked ? "admin unlocked" : "admin"}
            </button>
          )}
          <button
            className="status-item status-btn"
            title="Theme · open Settings"
            onClick={() => openSettings("appearance")}
          >
            {resolveTheme(S.appearance.theme).name}
          </button>
          <span className="status-item">
            {backend.kind === "desktop"
              ? "v" + (S.info?.version || "")
              : backend.kind === "dev"
                ? "browser preview · real backend"
                : "browser preview · mock backend"}
          </span>
        </footer>
      </div>
      <RulesEditors />
      <SecretPrompt />
      <PermissionPrompt />
      <FilePreview />
      <LibraryEditor />
      <RunPicker />
      <DialogHost />
      <CommandPalette />
      <div className="toast" aria-live="polite" onMouseEnter={toastHold} onMouseLeave={toastRelease}>
        {S.toast && S.toastKind !== "error" && <div className="banner">{S.toast}</div>}
      </div>
      <div className="toast" role="alert" onMouseEnter={toastHold} onMouseLeave={toastRelease}>
        {S.toast && S.toastKind === "error" && (
          <div className="banner error toast-error">
            <Icon.warn />
            <span>{S.toast}</span>
            <button className="icon-btn" aria-label="Close message" title="Close" onClick={hideToast}>
              <Icon.x />
            </button>
          </div>
        )}
      </div>
    </>
  );
}
