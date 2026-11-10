// The UI talks to core.Hub through app.go (Wails bindings) when running in
// the desktop shell, and to mockBackend.js in a plain browser.
import { createMockBackend } from "./mockBackend.js";

const METHODS = [
  "Info", "DetectCredentials", "SetAgent", "ClearToken",
  "OpenWorkspace", "Workspace", "SaveProfile", "BuildRules", "SaveRulesDoc", "ListFiles", "ReadFile", "SetDiagram",
  "PickFolder", "PickDiagram", "SaveTextFile", "Compile", "Metrics",
  "Workflow", "SaveWorkflow", "ResetWorkflow", "Compose", "RunStep", "RunPrompt", "Decide", "CancelRun", "Live", "Runs", "RunEvents",
  "Extensions", "SaveExtension", "ApproveExtension", "DeleteExtension", "GenerateExtension", "TestExtension", "TestExtensionWith", "SaveAttachment", "PreviewFile", "RunResults", "RunThread", "RecentRuns", "RevealPath", "Flows", "LoadFlow", "NewFlow", "SaveFlow", "DeleteFlow", "ValidateFlow", "ExportFlow", "ImportFlow", "PreviewFlowStep", "FlowOps", "RunFlow", "FlowRun", "FlowRuns", "DecideFlow", "StopFlowRun", "FlowCredentials", "SaveSecretToKeychain", "RemoveSecretFromKeychain", "KeychainAvailable", "Libraries", "SaveProjectLibrary", "DeleteProjectLibrary", "ManagedLibraries", "SaveManagedLibrary", "DeleteManagedLibrary", "SetSecret", "SecretStatus", "BuiltinTools", "CallBuiltin", "AdminStatus", "CreateAdminKey", "UnlockAdmin", "LockAdmin", "AdminPolicySnippet", "TrustAdminKey", "UntrustAdminKey", "ManagedTools", "SaveManagedTool", "DeleteManagedTool", "SecretInfo", "AnswerSecrets", "CancelSecrets",
  "PackageSettings", "SavePackageSettings", "CheckRegistries", "PickCAFile",
];

function wailsBackend() {
  const app = window.go.main.App;
  const api = { kind: "desktop" };
  for (const m of METHODS) api[m] = (...a) => app[m](...a);
  api.on = (cb) => window.runtime.EventsOn("hub:event", cb);
  return api;
}

export const isDesktop = typeof window !== "undefined" && !!window.go?.main?.App;
export const backend = isDesktop ? wailsBackend() : createMockBackend(METHODS);
