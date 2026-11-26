import { ErrorBoundary } from "./ErrorBoundary.jsx";
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
import { go as navGo, canBack, canForward, peek } from "./navigation.js";
import { register } from "./shortcuts.js";
import { SecretPrompt } from "./SecretPrompt.jsx";
import { QuestionPrompt, loadPendingQuestions } from "./QuestionPrompt.jsx";
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
import { FlowRun, showFlowRun, openFlowRunner } from "./views/FlowRun.jsx";
import { Chats } from "./views/Chats.jsx";
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
      { id: "chats", label: "Chats", icon: Icon.chat, C: Chats },
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
  loadPendingQuestions(); // questions asked before this window opened
  S.info = await backend.Info().catch(() => null);
  // a runner window runs one flow (the desktop app's runner mode; in the
  // browser preview, ?runner=<flow id>)
  S.runner = S.info?.runner || new URLSearchParams(location.search).get("runner") || "";
  if (S.runner) {
    if (S.info?.workspace || S.lastWorkspace) await openWorkspace(S.info?.workspace || S.lastWorkspace);
    // a Chatflow's window chats; another flow's runs
    const chats = (await backend.Chatflows().catch(() => [])) || [];
    S.runnerChat = chats.some((f) => f.id === S.runner);
    if (!S.runnerChat) openFlowRunner(S.runner);
    update();
    return;
  }
  refreshAdmin();
  if (S.setup.done && S.setup.provider && S.setup.provider !== "skipped") {
    // the chosen agent is a UI preference; any token was session-only
    await backend.SetAgent(S.setup.provider, "", S.setup.model || "").catch(() => {});
    if (S.setup.copilotWeb) await backend.SetCopilotWeb(S.setup.copilotWeb).catch(() => {});
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
  // Back / Forward (navigation.js): ⌘[ ⌘], Alt+← Alt+→, and the mouse's side buttons
  useEffect(() => {
    const off = [
      register({
        id: "nav-back",
        keys: ["mod+[", "alt+arrowleft"],
        help: [["mod+[", "Back"]],
        run: () => void navGo(-1),
      }),
      register({
        id: "nav-forward",
        keys: ["mod+]", "alt+arrowright"],
        help: [["mod+]", "Forward"]],
        run: () => void navGo(1),
      }),
    ];
    const mouse = (e) => {
      if (e.button === 3 || e.button === 4) {
        e.preventDefault();
        navGo(e.button === 3 ? -1 : 1);
      }
    };
    window.addEventListener("mouseup", mouse);
    return () => {
      off.forEach((f) => f());
      window.removeEventListener("mouseup", mouse);
    };
  }, []);
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

  if (S.runner) {
    // a runner window: that flow's Run screen (a Chatflow: its chats), nothing else
    S.view = S.runnerChat ? "chats" : "flowrun";
    return (
      <div className="app runner-mode">
        <main className="main main-full" data-view={S.view}>
          <ErrorBoundary name={S.runnerChat ? "Chats" : "Run flows"} resetKey="runner">
            {S.runnerChat ? <Chats /> : <FlowRun />}
          </ErrorBoundary>
        </main>
        <ErrorBoundary floating name="The prompts">
          <SecretPrompt />
          <QuestionPrompt />
          <PermissionPrompt />
          <FilePreview />
          <DialogHost />
        </ErrorBoundary>
        <div className="toast" aria-live="polite">
          {S.toast && S.toastKind !== "error" && <div className="banner">{S.toast}</div>}
        </div>
        <div className="toast" role="alert">
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
      </div>
    );
  }
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
          <NavArrows />
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
                          <Icon.check />
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
          <ErrorBoundary name={view.label || "This view"} resetKey={S.view}>
            <view.C />
          </ErrorBoundary>
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
      <ErrorBoundary floating name="The rules editor">
        <RulesEditors />
      </ErrorBoundary>
      <ErrorBoundary floating name="The secret prompt">
        <SecretPrompt />
        <QuestionPrompt />
      </ErrorBoundary>
      <ErrorBoundary floating name="The permission prompt">
        <PermissionPrompt />
      </ErrorBoundary>
      <ErrorBoundary
        floating
        name="The file preview"
        resetKey={S.preview?.path || ""}
        onClose={() => {
          S.preview = null;
          update();
        }}
      >
        <FilePreview />
      </ErrorBoundary>
      <ErrorBoundary floating name="The library editor">
        <LibraryEditor />
      </ErrorBoundary>
      <ErrorBoundary floating name="The run picker">
        <RunPicker />
      </ErrorBoundary>
      <ErrorBoundary floating name="The dialog">
        <DialogHost />
      </ErrorBoundary>
      <ErrorBoundary floating name="The command palette">
        <CommandPalette />
      </ErrorBoundary>
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

// Back and Forward, under the logo: their tooltips name where they lead
const placeName = (e) => {
  if (!e) return "";
  const v = VIEWS.find((x) => x.id === e.view);
  const key = String(e.key || "").replace(/^(run|flow):/, "");
  return (v?.label || e.view) + (key ? " · " + (key.length > 28 ? key.slice(0, 27) + "…" : key) : "");
};
function NavArrows() {
  const mac = typeof navigator !== "undefined" && /Mac/.test(navigator.platform || navigator.userAgent);
  const b = peek(-1),
    f = peek(1);
  return (
    <div className="nav-arrows" role="group" aria-label="Back and forward">
      <button
        className="icon-btn"
        disabled={!canBack()}
        aria-label={b ? "Back to " + placeName(b) : "Back"}
        title={b ? `Back to ${placeName(b)} (${mac ? "⌘[" : "Alt+←"})` : "Back"}
        onClick={() => navGo(-1)}
      >
        <Icon.back />
      </button>
      <button
        className="icon-btn"
        disabled={!canForward()}
        aria-label={f ? "Forward to " + placeName(f) : "Forward"}
        title={f ? `Forward to ${placeName(f)} (${mac ? "⌘]" : "Alt+→"})` : "Forward"}
        onClick={() => navGo(1)}
      >
        <Icon.forward />
      </button>
    </div>
  );
}
