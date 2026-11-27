// The UI talks to core.Hub through app.go (Wails bindings) when running in
// the desktop shell, and to mockBackend.js in a plain browser.
import { createMockBackend } from "./mockBackend.js";

const METHODS = [
  "Info",
  "DetectCredentials",
  "SetAgent",
  "ClearToken",
  "OpenWorkspace",
  "Workspace",
  "SaveProfile",
  "BuildRules",
  "SaveRulesDoc",
  "ListFiles",
  "ReadFile",
  "SetDiagram",
  "PickFolder",
  "PickDiagram",
  "SaveTextFile",
  "Compile",
  "Metrics",
  "MetricsIn",
  "FlowSecretStatus",
  "CheckKeychain",
  "SaveToKeychain",
  "Workflow",
  "SaveWorkflow",
  "ResetWorkflow",
  "Compose",
  "RunStep",
  "RunPrompt",
  "Decide",
  "CancelRun",
  "Live",
  "Runs",
  "RunEvents",
  "SearchHistory",
  "FlowLessons",
  "AddFlowLesson",
  "UpdateLesson",
  "DeleteLesson",
  "ToolStats",
  "ToolArgExamples",
  "HistoryDBInfo",
  "BackupHistoryNow",
  "TriggerQueue",
  "CancelQueued",
  "Extensions",
  "SaveExtension",
  "ApproveExtension",
  "DeleteExtension",
  "GenerateExtension",
  "GenerateExtensionTrace",
  "AgentTools",
  "RenameFlow",
  "SetFlowTags",
  "PendingQuestions",
  "ChartPNG",
  "ReadToolFile",
  "AnswerQuestions",
  "ListLayout",
  "SaveListLayout",
  "RenameTool",
  "ToolFromReply",
  "GenerateScript",
  "TestExtension",
  "TestExtensionWith",
  "SaveAttachment",
  "PreviewFile",
  "RunResults",
  "RunThread",
  "RecentRuns",
  "RevealPath",
  "Flows",
  "LoadFlow",
  "NewFlow",
  "SaveFlow",
  "DeleteFlow",
  "ValidateFlow",
  "ExportFlow",
  "ImportFlow",
  "PreviewFlowStep",
  "FlowShapes",
  "ParseSchema",
  "DesignFlows",
  "SaveFlowVersion",
  "FixFlowStep",
  "TryStepFix",
  "ApplyStepFix",
  "ArchiveRuns",
  "GroupRuns",
  "RunGroups",
  "Housekeeping",
  "SaveHousekeeping",
  "ToolTags",
  "SetToolTags",
  "FlowOps",
  "RunFlow",
  "FlowRun",
  "FlowRuns",
  "DecideFlow",
  "StopFlowRun",
  "FlowCredentials",
  "SaveSecretToKeychain",
  "RemoveSecretFromKeychain",
  "KeychainAvailable",
  "Libraries",
  "SaveProjectLibrary",
  "DeleteProjectLibrary",
  "SaveSkillFile",
  "DeleteSkillFile",
  "ManagedLibraries",
  "SaveManagedLibrary",
  "DeleteManagedLibrary",
  "SaveManagedLibraryFile",
  "DeleteManagedLibraryFile",
  "SetSecret",
  "SecretStatus",
  "BuiltinTools",
  "FlowBuiltinTools",
  "SpecKitInfo",
  "SpecProjects",
  "SwitchSpecProject",
  "DownloadSpecKit",
  "InitSpecKit",
  "ExportProjectZip",
  "CallBuiltin",
  "AdminStatus",
  "CreateAdminKey",
  "UnlockAdmin",
  "SecretKeyStatus",
  "ProtectSecretKey",
  "UnlockSecretKey",
  "LockSecretKey",
  "LockAdmin",
  "AdminPolicySnippet",
  "TrustAdminKey",
  "UntrustAdminKey",
  "ManagedTools",
  "SaveManagedTool",
  "DeleteManagedTool",
  "SecretInfo",
  "AnswerSecrets",
  "CancelSecrets",
  "AnswerPermission",
  "Models",
  "CopilotExtensions",
  "LoadCopilotExtension",
  "RemoveCopilotExtension",
  "ArchiveTool",
  "PackageSettings",
  "SavePackageSettings",
  "CheckRegistries",
  "PickCAFile",
  "FlowCommands",
  "ApproveFlowCommands",
  "SetCopilotWeb",
  "ExportTools",
  "PreviewToolImport",
  "ImportTools",
  "WizardList",
  "WizardLoad",
  "WizardDelete",
  "WizardStart",
  "WizardUpdate",
  "WizardClarify",
  "WizardPlan",
  "WizardDraft",
  "WizardRevise",
  "WizardDiscard",
  "WizardSaveDraftTest",
  "WizardRunDraft",
  "WizardApply",
  "InspectTool",
  "RefineToolWithAI",
  "SaveToolMeta",
  "RunToolWith",
  "SaveToolGuide",
  "OpenRunnerWindow",
  "Chatflows",
  "ChatSessions",
  "ChatSession",
  "ChatMessages",
  "ChatData",
  "ChatCatalog",
  "ProjectSecrets",
  "SetProjectSecret",
  "DeleteProjectSecret",
  "SecretRefUses",
  "StartChatSession",
  "SendChatMessage",
  "StopChatMessage",
  "RenameChatSession",
  "DeleteChatSession",
  "AttachChatFile",
  "RefreshChatSession",
  "ArchiveChatSession",
  "ExportChatSession",
  "GenerateToolGuide",
  "RunToolExample",
  "RunToolTests",
  "FlowTriggers",
  "PreviewSchedule",
  "SaveTriggerSecrets",
  "RunTriggerNow",
];

function wailsBackend() {
  const app = window.go.main.App;
  const api = { kind: "desktop" };
  for (const m of METHODS) api[m] = (...a) => app[m](...a);
  api.on = (cb) => window.runtime.EventsOn("hub:event", cb);
  return api;
}

// The real Go backend over HTTP, for the browser preview (`SpecWright devserver`
// prints the URL to open: #devbackend=http://127.0.0.1:5174&token=…, a
// fragment, so the token never reaches a server). The address and token are
// kept for the browser tab's session.
function devConfig() {
  if (typeof window === "undefined") return null;
  try {
    const q = new URLSearchParams(window.location.hash.slice(1) || window.location.search);
    if (q.get("devbackend") && q.get("token")) {
      sessionStorage.setItem(
        "devbackend",
        JSON.stringify({ url: q.get("devbackend"), token: q.get("token") }),
      );
      // the token does not stay in the address bar
      window.history.replaceState(null, "", window.location.pathname);
    }
    return JSON.parse(sessionStorage.getItem("devbackend") || "null");
  } catch {
    return null;
  }
}

function devBackend({ url, token }) {
  const api = { kind: "dev" };
  for (const m of METHODS)
    api[m] = async (...args) => {
      const r = await fetch(url + "/api/call", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Dev-Token": token },
        body: JSON.stringify({ method: m, args }),
      });
      if (!r.ok) throw new Error(`dev server: ${r.status} ${await r.text()}`);
      const j = await r.json();
      if (j.error) throw new Error(j.error);
      return j.result;
    };
  // the event stream, read with fetch so the token goes in a header (an
  // EventSource can only put it in the URL); reconnects after a drop
  api.on = (cb) => {
    const ctl = new AbortController();
    const listen = async () => {
      while (!ctl.signal.aborted) {
        try {
          const r = await fetch(url + "/api/events", {
            headers: { "X-Dev-Token": token },
            signal: ctl.signal,
          });
          if (!r.ok || !r.body) throw new Error(`dev server events: ${r.status}`);
          const reader = r.body.pipeThrough(new TextDecoderStream()).getReader();
          let buf = "";
          for (;;) {
            const { value, done } = await reader.read();
            if (done) break;
            buf += value;
            let i;
            while ((i = buf.indexOf("\n\n")) >= 0) {
              const block = buf.slice(0, i);
              buf = buf.slice(i + 2);
              const data = block
                .split("\n")
                .filter((l) => l.startsWith("data: "))
                .map((l) => l.slice(6))
                .join("\n");
              if (data) cb(JSON.parse(data));
            }
          }
        } catch {
          if (ctl.signal.aborted) return;
        }
        await new Promise((res) => setTimeout(res, 1000));
      }
    };
    listen();
    return () => ctl.abort();
  };
  return api;
}

export const isDesktop = typeof window !== "undefined" && !!window.go?.main?.App;
const dev = !isDesktop && devConfig();
// desktop app: Wails bindings; browser preview: the dev server if given, else the mock
export const backend = isDesktop ? wailsBackend() : dev ? devBackend(dev) : createMockBackend(METHODS);
