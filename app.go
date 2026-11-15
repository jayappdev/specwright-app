package main

import (
	"context"
	"os"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"codegen/internal/bus"
	"codegen/internal/core"
	"codegen/internal/extension"
	"codegen/internal/flow"
	"codegen/internal/speckit"
	"codegen/internal/store"
	"codegen/internal/workflow"
)

// App exposes core.Hub to the UI. Wails binds every exported method; the
// frontend calls them as window.go.main.App.<Method>(...) and receives hub
// events on the "hub:event" channel.
type App struct {
	ctx   context.Context
	hub   *core.Hub
	unsub func()
}

// NewApp creates the hub.
func NewApp() (*App, error) { return newApp("") }

// newApp creates the hub with its data in storeDir ("" = the app's usual folder).
func newApp(storeDir string) (*App, error) {
	// a Finder-launched app gets a minimal PATH: borrow the login shell's so
	// claude, copilot, node, go and mvn are found
	core.UseLoginShellPath()
	exe, _ := os.Executable()
	h, err := core.New(core.Options{AIShimExe: exe, StoreDir: storeDir})
	if err != nil {
		return nil, err
	}
	return &App{hub: h}, nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.hub.Clipboard = func(text string) error { return runtime.ClipboardSetText(ctx, text) }
	ch, unsub := a.hub.Bus.Subscribe(1024, nil)
	a.unsub = unsub
	go func() {
		for e := range ch {
			runtime.EventsEmit(ctx, "hub:event", e)
		}
	}()
}

func (a *App) shutdown(context.Context) {
	if a.unsub != nil {
		a.unsub()
	}
	a.hub.Close()
}

// --- app & agent ---------------------------------------------------------

func (a *App) Info() core.Info                            { return val(a.hub.Info()) }
func (a *App) DetectCredentials() []core.CredentialSource { return val(a.hub.DetectCredentials()) }
func (a *App) SetAgent(name, token, model string) error   { return a.hub.SetAgent(name, token, model) }
func (a *App) ClearToken()                                { a.hub.ClearToken() }

// --- workspace -----------------------------------------------------------

func (a *App) OpenWorkspace(path string) (core.WorkspaceState, error) {
	return ok(a.hub.OpenWorkspace(path))
}
func (a *App) Workspace() (core.WorkspaceState, error) { return ok(a.hub.Workspace()) }
func (a *App) SaveProfile(p workflow.Profile) error    { return a.hub.SaveProfile(p) }
func (a *App) BuildRules(section, mode string) (string, error) {
	return ok(a.hub.BuildRules(section, mode))
}

// SaveRulesDoc writes the bulk-edited rules as rules.md or rules.html.
func (a *App) SaveRulesDoc(format, content string) (string, error) {
	return ok(a.hub.SaveRulesDoc(format, content))
}
func (a *App) ListFiles() ([]string, error)        { return ok(a.hub.ListFiles()) }
func (a *App) ReadFile(rel string) (string, error) { return ok(a.hub.ReadFile(rel)) }
func (a *App) SetDiagram(kind, rel string) error   { return a.hub.SetDiagram(kind, rel) }
func (a *App) Compile() (core.BuildResult, error)  { return ok(a.hub.Compile()) }
func (a *App) Metrics() (core.Metrics, error)      { return ok(a.hub.Metrics()) }

// FlowSecretStatus: where each secret input of a flow gets its value (never the value).
func (a *App) FlowSecretStatus(id string) ([]core.SecretInputState, error) {
	return ok(a.hub.FlowSecretStatus(id))
}

// CheckKeychain: whether a keychain entry exists (never its value).
func (a *App) CheckKeychain(ref flow.KeychainRef) core.KeychainStatus {
	return a.hub.CheckKeychain(ref)
}

// SaveToKeychain stores a value in the system keychain.
func (a *App) SaveToKeychain(ref flow.KeychainRef, value string) error {
	return a.hub.SaveToKeychain(ref, value)
}

// MetricsIn: the metrics of the ledger entries between r.From and r.To.
func (a *App) MetricsIn(r core.MetricsRange) (core.Metrics, error) { return ok(a.hub.MetricsIn(r)) }

// PickFolder shows the native folder dialog and opens the chosen workspace.
func (a *App) PickFolder() (core.WorkspaceState, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Choose the project folder", CanCreateDirectories: true})
	if err != nil || dir == "" {
		return core.WorkspaceState{}, err
	}
	return ok(a.hub.OpenWorkspace(dir))
}

// PickDiagram lets the user choose a diagram file and copies it into the workspace.
func (a *App) PickDiagram(kind string) (string, error) {
	f, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "Choose a diagram",
		Filters: []runtime.FileFilter{{DisplayName: "Diagrams", Pattern: "*.drawio;*.svg;*.png;*.puml;*.md"}}})
	if err != nil || f == "" {
		return "", err
	}
	return ok(a.hub.ImportDiagram(kind, f))
}

// SaveTextFile asks where to save text produced by the UI (exports, CSV, JSONL).
func (a *App) SaveTextFile(name, content string) (string, error) {
	p, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{DefaultFilename: name, Title: "Save " + name})
	if err != nil || p == "" {
		return "", err
	}
	return p, os.WriteFile(p, []byte(content), 0o644)
}

// --- workflow ------------------------------------------------------------

func (a *App) Workflow() (*workflow.Workflow, error)  { return ok(a.hub.Workflow()) }
func (a *App) SaveWorkflow(w workflow.Workflow) error { return a.hub.SaveWorkflow(w) }
func (a *App) ResetWorkflow() error                   { return a.hub.ResetWorkflow() }
func (a *App) Compose(step, feedback string) (string, error) {
	return ok(a.hub.Compose(step, feedback))
}
func (a *App) RunStep(step string) (string, error) { return ok(a.hub.RunStep(step)) }

// RunPrompt runs a free-form playground prompt and returns the run id.
func (a *App) RunPrompt(prompt string, o core.PromptOptions) (string, error) {
	return ok(a.hub.RunPrompt(prompt, o))
}
func (a *App) Decide(runID, kind, feedback string) error   { return a.hub.Decide(runID, kind, feedback) }
func (a *App) CancelRun(runID string) error                { return a.hub.CancelRun(runID) }
func (a *App) Live() *core.LiveRun                         { return val(a.hub.Live()) }
func (a *App) Runs(limit int) ([]store.Run, error)         { return ok(a.hub.Runs(limit)) }
func (a *App) RunEvents(runID string) ([]bus.Event, error) { return ok(a.hub.RunEvents(runID)) }

// --- extensions ----------------------------------------------------------

func (a *App) Extensions() ([]extension.Extension, error) { return ok(a.hub.Extensions()) }
func (a *App) SaveExtension(e extension.Extension) (extension.Extension, error) {
	return ok(a.hub.SaveExtension(e))
}
func (a *App) ApproveExtension(name string) (extension.Extension, error) {
	return ok(a.hub.ApproveExtension(name))
}
func (a *App) DeleteExtension(name string) error { return a.hub.DeleteExtension(name) }
func (a *App) GenerateExtension(r core.GenerateRequest) (extension.Extension, error) {
	return ok(a.hub.GenerateExtension(r))
}

// SaveManagedLibraryFile adds or changes a file of a corporate skill and re-signs it (admin).
func (a *App) SaveManagedLibraryFile(name, file, content, oldFile string) ([]workflow.Library, error) {
	return ok(a.hub.SaveManagedLibraryFile(name, file, content, oldFile))
}

// DeleteManagedLibraryFile removes a file from a corporate skill and re-signs it (admin).
func (a *App) DeleteManagedLibraryFile(name, file string) ([]workflow.Library, error) {
	return ok(a.hub.DeleteManagedLibraryFile(name, file))
}

// SaveSkillFile creates, updates or renames a file in a project skill's folder.
func (a *App) SaveSkillFile(r core.SkillFileSave) ([]workflow.Library, error) {
	return ok(a.hub.SaveSkillFile(r))
}

// DeleteSkillFile moves a file of a project skill to the trash.
func (a *App) DeleteSkillFile(skill, file string) ([]workflow.Library, error) {
	return ok(a.hub.DeleteSkillFile(skill, file))
}

// GenerateScript has the AI write or revise a project script (not saved).
func (a *App) GenerateScript(r core.ScriptRequest) (core.ScriptDraft, error) {
	return ok(a.hub.GenerateScript(r))
}
func (a *App) TestExtension(e extension.Extension, question string) (extension.TestResult, error) {
	return ok(a.hub.TestExtension(e, question))
}

// TestExtensionWith tests a tool with files attached to the question;
// model ("" = the app's setting) picks the AI model for the test.
func (a *App) TestExtensionWith(e extension.Extension, question string, files []string, model string) (extension.TestResult, error) {
	return ok(a.hub.TestExtensionWith(e, question, files, model))
}

// CopilotExtensions lists your Copilot CLI extensions and the project's.
func (a *App) CopilotExtensions() []extension.CopilotExt { return val(a.hub.CopilotExtensions()) }

// LoadCopilotExtension copies one of your Copilot extensions into the project.
func (a *App) LoadCopilotExtension(name string, replace, install bool) (core.CopilotLoadResult, error) {
	return ok(a.hub.LoadCopilotExtension(name, replace, install))
}

// RemoveCopilotExtension archives the project's copy of an extension.
func (a *App) RemoveCopilotExtension(name string) (core.ArchiveResult, error) {
	return ok(a.hub.RemoveCopilotExtension(name))
}

// ArchiveTool deletes a project tool by moving it to .codegen/archive/<datetime>/.
func (a *App) ArchiveTool(name string) (core.ArchiveResult, error) {
	return ok(a.hub.ArchiveTool(name))
}

// Models lists the AI models each agent offers, for the model pickers.
func (a *App) Models() []core.AgentModels { return val(a.hub.Models()) }

// Libraries rescans the project's skills and scripts (corporate ones synced first).
func (a *App) Libraries() ([]workflow.Library, error) { return ok(a.hub.Libraries()) }

// SaveProjectLibrary creates or updates a project skill or script.
func (a *App) SaveProjectLibrary(r core.LibrarySave) ([]workflow.Library, error) {
	return ok(a.hub.SaveProjectLibrary(r))
}

// DeleteProjectLibrary moves a project skill or script to the trash.
func (a *App) DeleteProjectLibrary(path string) ([]workflow.Library, error) {
	return ok(a.hub.DeleteProjectLibrary(path))
}

// ManagedLibraries lists the corporate libraries (everyone can read them).
func (a *App) ManagedLibraries() []extension.ManagedLibrary { return val(a.hub.ManagedLibraries()) }

// SaveManagedLibrary signs and publishes a corporate skill (admin mode).
func (a *App) SaveManagedLibrary(name, desc, content string) ([]workflow.Library, error) {
	return ok(a.hub.SaveManagedLibrary(name, desc, content))
}

// DeleteManagedLibrary retires a corporate skill (admin mode).
func (a *App) DeleteManagedLibrary(name string) ([]workflow.Library, error) {
	return ok(a.hub.DeleteManagedLibrary(name))
}

// --- flow designer ---

// Flows lists the project's flows.
func (a *App) Flows() ([]flow.Summary, error) { return ok(a.hub.Flows()) }

// LoadFlow reads a flow.
func (a *App) LoadFlow(id string) (*flow.Flow, error) { return ok(a.hub.LoadFlow(id)) }

// NewFlow creates an empty flow.
func (a *App) NewFlow(name string) (*flow.Flow, error) { return ok(a.hub.NewFlow(name)) }

// SaveFlow stores a flow and returns its check results.
func (a *App) SaveFlow(f flow.Flow) (core.FlowState, error) { return ok(a.hub.SaveFlow(f)) }

// DeleteFlow removes a flow.
func (a *App) DeleteFlow(id string) error { return a.hub.DeleteFlow(id) }

// ValidateFlow checks a flow.
func (a *App) ValidateFlow(f flow.Flow) []flow.Issue { return val(a.hub.ValidateFlow(f)) }

// ExportFlow returns a portable flow file.
func (a *App) ExportFlow(id string) (string, error) { return ok(a.hub.ExportFlow(id)) }

// ImportFlow adds an exported flow to the project.
func (a *App) ImportFlow(data string) (core.FlowState, error) { return ok(a.hub.ImportFlow(data)) }

// PreviewFlowStep runs a merge, transform or output step on sample data.
func (a *App) PreviewFlowStep(f flow.Flow, nodeID string, sample flow.Data) core.FlowPreview {
	return val(a.hub.PreviewFlowStep(f, nodeID, sample))
}

// FlowShapes works out what data every step of a flow gets and gives.
func (a *App) FlowShapes(f flow.Flow, samples map[string]string) map[string]flow.StepShape {
	return val(a.hub.FlowShapes(f, samples))
}

// ParseSchema reads a JSON Schema, or learns one from example JSON.
func (a *App) ParseSchema(text string) (*flow.Schema, error) { return ok(flow.ParseSchema(text)) }

// DesignFlows asks the AI designer for flow options (new, or improvements of a flow).
func (a *App) DesignFlows(r core.DesignRequest) (core.DesignResult, error) {
	return ok(a.hub.DesignFlows(r))
}

// SaveFlowVersion saves a flow as a new version of another.
func (a *App) SaveFlowVersion(baseID string, f flow.Flow) (core.FlowState, error) {
	return ok(a.hub.SaveFlowVersion(baseID, f))
}

// FixFlowStep asks the AI for a changed definition of one step of a run.
func (a *App) FixFlowStep(r core.StepFixRequest) (core.StepFix, error) {
	return ok(a.hub.FixFlowStep(r))
}

// TryStepFix runs a trial of a changed step on an unsaved copy of the flow.
func (a *App) TryStepFix(f core.StepFix) (core.FlowRunState, error) { return ok(a.hub.TryStepFix(f)) }

// ApplyStepFix writes a trial's changed steps into the saved flow.
func (a *App) ApplyStepFix(runID string) (core.FlowState, error) {
	return ok(a.hub.ApplyStepFix(runID))
}

// ArchiveRuns archives (or restores) runs.
func (a *App) ArchiveRuns(ids []string, archived bool) (int, error) {
	return ok(a.hub.ArchiveRuns(ids, archived))
}

// GroupRuns puts runs in a group ("" removes them from it).
func (a *App) GroupRuns(ids []string, group string) error { return a.hub.GroupRuns(ids, group) }

// RunGroups lists the groups runs use.
func (a *App) RunGroups() []string { return val(a.hub.RunGroups()) }

// Housekeeping returns the auto-archive settings.
func (a *App) Housekeeping() core.HousekeepingSettings { return val(a.hub.Housekeeping()) }

// SaveHousekeeping stores the auto-archive settings and applies them.
func (a *App) SaveHousekeeping(s core.HousekeepingSettings) (core.HousekeepingSettings, error) {
	return ok(a.hub.SaveHousekeeping(s))
}

// ToolTags returns every tool's tags.
func (a *App) ToolTags() map[string][]string { return val(a.hub.ToolTags()) }

// SetToolTags replaces one tool's tags.
func (a *App) SetToolTags(tool string, tags []string) (map[string][]string, error) {
	return ok(a.hub.SetToolTags(tool, tags))
}

// FlowOps lists the designer's transformations, merge modes and formats.
func (a *App) FlowOps() map[string]any { return val(a.hub.FlowOps()) }

// --- flow runs ---

// FlowCommands lists a flow's command steps and whether each is approved.
func (a *App) FlowCommands(id string) ([]core.FlowCommand, error) { return ok(a.hub.FlowCommands(id)) }

// ApproveFlowCommands approves the commands of these steps for the open project.
func (a *App) ApproveFlowCommands(id string, steps []string) ([]core.FlowCommand, error) {
	return ok(a.hub.ApproveFlowCommands(id, steps))
}

// RunFlow starts a flow run.
func (a *App) RunFlow(id string, o core.FlowRunOptions) (core.FlowRunState, error) {
	return ok(a.hub.RunFlow(id, o))
}

// FlowRun returns a run's current or final state.
func (a *App) FlowRun(id string) (core.FlowRunState, error) { return ok(a.hub.FlowRun(id)) }

// FlowRuns lists recorded flow runs.
func (a *App) FlowRuns(limit int) ([]store.Run, error) { return ok(a.hub.FlowRuns(limit)) }

// DecideFlow answers a run waiting for review.
func (a *App) DecideFlow(runID string, d core.FlowDecision) error { return a.hub.DecideFlow(runID, d) }

// StopFlowRun stops a run.
func (a *App) StopFlowRun(runID string) error { return a.hub.StopFlowRun(runID) }

// FlowCredentials lists the secrets a flow's tools need.
func (a *App) FlowCredentials(id string) ([]core.FlowCredential, error) {
	return ok(a.hub.FlowCredentials(id))
}

// SaveSecretToKeychain keeps a secret in the system keychain.
func (a *App) SaveSecretToKeychain(name, value string) error {
	return a.hub.SaveSecretToKeychain(name, value)
}

// RemoveSecretFromKeychain deletes a stored secret.
func (a *App) RemoveSecretFromKeychain(name string) error {
	return a.hub.RemoveSecretFromKeychain(name)
}

// KeychainAvailable reports whether the system keychain can be used.
func (a *App) KeychainAvailable() bool { return core.KeychainAvailable() }

// --- run results and continuing conversations ---

// RunResults returns a run's results folder (written when the run ended).
func (a *App) RunResults(id string) (core.RunResults, error) { return ok(a.hub.Results(id)) }

// RunThread returns the runs id continues from, oldest first, ending with id.
func (a *App) RunThread(id string) ([]core.RunDigest, error) { return ok(a.hub.Thread(id)) }

// RecentRuns lists recent runs of every kind (to continue from).
func (a *App) RecentRuns(limit int) ([]core.RunDigest, error) { return ok(a.hub.RecentRuns(limit)) }

// RevealPath shows a project path in the file manager.
func (a *App) RevealPath(rel string) error { return a.hub.RevealPath(rel) }

// PreviewFile returns a file's content for the quick-preview panel.
func (a *App) PreviewFile(path string) (core.FilePreview, error) { return ok(a.hub.PreviewFile(path)) }

// SaveAttachment stores a pasted, dropped or picked file (base64) in the
// workspace for a test question.
func (a *App) SaveAttachment(name, dataB64 string) (core.Attachment, error) {
	return ok(a.hub.SaveAttachment(name, dataB64))
}

// --- built-in tools ----------------------------------------------------------

// FlowBuiltinTools lists the built-in tools a flow step can run (everyone:
// a flow runs them in the open project, like its other steps).
func (a *App) FlowBuiltinTools() []extension.Extension { return val(a.hub.FlowBuiltinTools()) }

// SpecKitInfo describes the open project's Spec Kit setup.
func (a *App) SpecKitInfo() speckit.Info { return val(a.hub.SpecKitInfo()) }

// SpecProjects lists the workspace's Spec Kit projects.
func (a *App) SpecProjects() ([]core.SpecProject, error) { return ok(a.hub.SpecProjects()) }

// SwitchSpecProject selects another Spec Kit project of the workspace.
func (a *App) SwitchSpecProject(project string) (core.WorkspaceState, error) {
	return ok(a.hub.SwitchSpecProject(project))
}

// DownloadSpecKit fetches Spec Kit through the configured proxy (force:
// again, for a newer release).
func (a *App) DownloadSpecKit(force bool) speckit.Info { return val(a.hub.DownloadSpecKit(force)) }

// InitSpecKit sets the project up with the downloaded Spec Kit.
func (a *App) InitSpecKit(project, integration string) (core.SpecKitSetup, error) {
	return ok(a.hub.InitSpecKit(project, integration))
}

// ExportProjectZip asks where to save the project as a zip and writes it.
// An empty path means the dialog was cancelled.
func (a *App) ExportProjectZip(withContext bool) (core.ZipResult, error) {
	p, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{DefaultFilename: a.hub.ProjectZipName(), Title: "Download the project as a zip",
		Filters: []runtime.FileFilter{{DisplayName: "Zip archive (*.zip)", Pattern: "*.zip"}}})
	if err != nil || p == "" {
		return core.ZipResult{}, err
	}
	return ok(a.hub.ExportProjectZip(p, withContext))
}

// BuiltinTools lists the built-in tools for the UI, in admin mode only.
func (a *App) BuiltinTools() []core.BuiltinTool {
	if !a.hub.BuiltinsVisible() {
		return []core.BuiltinTool{}
	}
	return val(a.hub.BuiltinTools())
}
func (a *App) CallBuiltin(name string, args map[string]any) (string, error) {
	return ok(a.hub.CallBuiltin(name, args))
}

// --- admin -----------------------------------------------------------------

func (a *App) AdminStatus() core.AdminStatus { return val(a.hub.AdminStatus()) }
func (a *App) CreateAdminKey(name, passphrase string) (core.AdminStatus, error) {
	return ok(a.hub.CreateAdminKey(name, passphrase))
}
func (a *App) UnlockAdmin(passphrase string) (core.AdminStatus, error) {
	return ok(a.hub.UnlockAdmin(passphrase))
}
func (a *App) LockAdmin()                          { a.hub.LockAdmin() }
func (a *App) AdminPolicySnippet() (string, error) { return ok(a.hub.AdminPolicySnippet()) }
func (a *App) TrustAdminKey(name, publicKey string) error {
	return a.hub.TrustAdminKey(name, publicKey)
}
func (a *App) UntrustAdminKey(id string) error     { return a.hub.UntrustAdminKey(id) }
func (a *App) ManagedTools() []extension.Extension { return val(a.hub.ManagedTools()) }
func (a *App) DeleteManagedTool(name string) error { return a.hub.DeleteManagedTool(name) }
func (a *App) SaveManagedTool(e extension.Extension) (extension.Extension, error) {
	return ok(a.hub.SaveManagedTool(e))
}

func (a *App) SetSecret(name, value string)                { a.hub.SetSecret(name, value) }
func (a *App) SecretStatus(names []string) map[string]bool { return val(a.hub.SecretStatus(names)) }

// SecretInfo says where each secret's value will come from (session, env, ask).
func (a *App) SecretInfo(secrets []extension.Secret) []core.SecretState {
	return val(a.hub.SecretInfo(secrets))
}

// AnswerPermission answers a "permission.request": decision is once, run or deny.
func (a *App) AnswerPermission(id, decision, note string) error {
	return a.hub.AnswerPermission(id, decision, note)
}

// AnswerSecrets answers a "secret.request" event; remember keeps the values for the session.
func (a *App) AnswerSecrets(id string, values map[string]string, remember bool) error {
	return a.hub.AnswerSecrets(id, values, remember)
}

// CancelSecrets declines a "secret.request"; the tool call fails.
func (a *App) CancelSecrets(id string) error { return a.hub.CancelSecrets(id) }

// --- packages & proxy ----------------------------------------------------

func (a *App) PackageSettings() (core.PackageView, error) { return ok(a.hub.PackageSettings()) }
func (a *App) SavePackageSettings(s extension.PackageSettings) error {
	return a.hub.SavePackageSettings(s)
}
func (a *App) CheckRegistries() ([]extension.RegistryCheck, error) {
	return ok(a.hub.CheckRegistries())
}

// PickCAFile lets the user choose the corporate root CA bundle (PEM).
func (a *App) PickCAFile() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "Choose the corporate CA bundle",
		Filters: []runtime.FileFilter{{DisplayName: "Certificates", Pattern: "*.pem;*.crt;*.cer"}}})
}
