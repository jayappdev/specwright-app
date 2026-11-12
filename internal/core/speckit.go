package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"codegen/internal/bus"
	"codegen/internal/speckit"
	"codegen/internal/workflow"
)

// Spec Kit setup. SpecWright carries Spec Kit's project template
// (internal/speckit) and sets a project up with it — no `specify` CLI or
// network needed — then keeps the project ready for the Spec workflow:
//
//  1. the template: .specify/ (constitution, scripts, templates) and the
//     agent's speckit-* skills (.claude/skills or .github/skills);
//  2. the project context: the selected corporate and project skills,
//     rules, tools and diagrams, in a managed block of CLAUDE.md or
//     .github/copilot-instructions.md, refreshed before every step;
//  3. each step runs its own Spec Kit command (/speckit-<step>) with a
//     note to follow that command's skill file and stay within its scope.

// SpecKitSetup is what InitSpecKit did.
type SpecKitSetup struct {
	speckit.Result
	Context []string     `json:"context"` // agent instruction files written
	Info    speckit.Info `json:"info"`
}

// SpecKitInfo describes the open project's Spec Kit setup and the
// download on this computer.
func (h *Hub) SpecKitInfo() speckit.Info {
	var in speckit.Info
	if h.Root() != "" {
		in = speckit.Status(h.specDir())
		in.Project = h.specRel()
	}
	in.Available = h.skCache().Latest()
	h.skMu.Lock()
	in.Downloading, in.Progress, in.Error = h.skRunning, h.skProgress, h.skErr
	h.skMu.Unlock()
	return in
}

// --- downloading Spec Kit ---------------------------------------------------------------

// SpecWright does not ship Spec Kit: it is downloaded on this computer,
// through the configured proxy, the first time it is needed.

func (h *Hub) skCache() speckit.Cache {
	return speckit.Cache{Dir: filepath.Join(h.Store.Dir(), "speckit")}
}

// skNet is how the download reaches the internet: the workspace's proxy,
// CA bundle and package index (or the machine's when no workspace is open).
func (h *Hub) skNet() speckit.Net {
	ps := h.packageSettings()
	eff := ps.Effective(detectedPackages())
	env := eff.Env("") // no workspace caches: Spec Kit's CLI is not a project dependency
	if eff.CAFile != "" {
		env = append(env, "UV_NATIVE_TLS=1")
	}
	return speckit.Net{Env: env, Client: eff.HTTPClient(20 * time.Second)}
}

// DownloadSpecKit fetches the latest Spec Kit release in the background
// (force: even when a version is already downloaded). Progress shows in
// SpecKitInfo.
func (h *Hub) DownloadSpecKit(force bool) speckit.Info {
	if force || h.skCache().Latest() == "" {
		h.startSpecKitDownload()
	}
	return h.SpecKitInfo()
}

// startSpecKitDownload runs one download at a time; done is closed when it
// ends.
func (h *Hub) startSpecKitDownload() chan struct{} {
	h.skMu.Lock()
	defer h.skMu.Unlock()
	if h.skRunning {
		return h.skDone
	}
	h.skRunning, h.skErr, h.skProgress = true, "", "Looking up the latest Spec Kit release…"
	h.skDone = make(chan struct{})
	done := h.skDone
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(h.ctx, 15*time.Minute)
		defer cancel()
		n := h.skNet()
		v := speckit.LatestRelease(ctx, n)
		log := func(l string) {
			h.skMu.Lock()
			h.skProgress = l
			h.skMu.Unlock()
			h.Bus.Publish(bus.Event{Type: "speckit.download", Summary: l})
		}
		err := h.skCache().Download(ctx, v, n, log)
		h.skMu.Lock()
		h.skRunning = false
		if err != nil {
			h.skErr = h.Store.Redactor().String(err.Error())
			h.skProgress = ""
		}
		h.skMu.Unlock()
		if err != nil {
			h.Bus.Publish(bus.Event{Type: "speckit.download", Summary: "Spec Kit download failed", Data: map[string]any{"error": h.skErr}})
		}
	}()
	return done
}

// ensureSpecKit returns the downloaded version, downloading it now if
// needed.
func (h *Hub) ensureSpecKit() (string, error) {
	if v := h.skCache().Latest(); v != "" {
		return v, nil
	}
	<-h.startSpecKitDownload()
	if v := h.skCache().Latest(); v != "" {
		return v, nil
	}
	h.skMu.Lock()
	msg := h.skErr
	h.skMu.Unlock()
	return "", fmt.Errorf("Spec Kit could not be downloaded: %s (check the proxy under Packages & proxy)", firstNonEmpty(msg, "unknown error"))
}

// specRel is the Spec Kit project folder, relative to the workspace ("" =
// the workspace itself).
func (h *Hub) specRel() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.profile == nil {
		return ""
	}
	return h.profile.SpecProject
}

// specDir is the Spec Kit project folder (absolute).
func (h *Hub) specDir() string {
	root := h.Root()
	if rel := h.specRel(); rel != "" && root != "" {
		return filepath.Join(root, filepath.FromSlash(rel))
	}
	return root
}

// rebase makes a workspace-relative path relative to the project folder,
// where the agent runs ("../.codegen/…" for a project in a subfolder).
func rebase(project, p string) string {
	if project == "" || p == "" || filepath.IsAbs(p) {
		return p
	}
	r, err := filepath.Rel(filepath.FromSlash(project), filepath.FromSlash(p))
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}

// rebased returns the libraries and profile with their paths relative to
// the project folder.
func rebased(project string, libs []workflow.Library, p *workflow.Profile) ([]workflow.Library, *workflow.Profile) {
	if project == "" || p == nil {
		return libs, p
	}
	out := make([]workflow.Library, len(libs))
	for i, l := range libs {
		l.Path, l.Dir = rebase(project, l.Path), rebase(project, l.Dir)
		out[i] = l
	}
	cp := *p
	cp.Diagrams = map[string]string{}
	for k, v := range p.Diagrams {
		cp.Diagrams[k] = rebase(project, v)
	}
	return out, &cp
}

// ProjectNameRE is a valid Spec Kit project folder name.
var ProjectNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// InitSpecKit creates the Spec Kit project folder project in the workspace
// (like `specify init <project>`; "" = the folder chosen before, or the
// workspace itself for projects set up that way), installs the bundled
// template for integration (claude | copilot; "" = the agent SpecWright
// runs) and writes the project context for the agent.
func (h *Hub) InitSpecKit(project, integration string) (SpecKitSetup, error) {
	root, err := h.need()
	if err != nil {
		return SpecKitSetup{}, err
	}
	project = strings.Trim(strings.TrimSpace(filepath.ToSlash(project)), "/")
	if project == "" {
		project = h.specRel()
	}
	if project != "" {
		if !ProjectNameRE.MatchString(project) || project == ".codegen" || project == ".specify" || strings.HasPrefix(project, ".") {
			return SpecKitSetup{}, fmt.Errorf("%q is not a good project folder name: use letters, digits, - . _ (e.g. orders-service)", project)
		}
		if err := os.MkdirAll(filepath.Join(root, project), 0o755); err != nil {
			return SpecKitSetup{}, err
		}
		// a new project gets its own workflow; the one being left keeps its progress
		if err := h.selectProject(project); err != nil {
			return SpecKitSetup{}, err
		}
	}
	dir := h.specDir()
	if integration == "" {
		h.mu.RLock()
		integration = h.agentName
		h.mu.RUnlock()
		if !slices.Contains(speckit.Integrations, integration) {
			integration = "claude"
		}
	}
	v, err := h.ensureSpecKit()
	if err != nil {
		return SpecKitSetup{}, err
	}
	res, err := speckit.Init(dir, h.skCache().Source(v, integration), integration)
	if err != nil {
		return SpecKitSetup{}, err
	}
	h.prepareLibraries() // corporate libraries synced into .codegen/skills/global
	files, err := h.writeAgentContext()
	out := SpecKitSetup{Result: res, Context: files, Info: h.SpecKitInfo()}
	if project != "" { // paths as the workspace sees them
		for _, l := range []*[]string{&out.Created, &out.Updated, &out.Kept, &out.Context} {
			for i := range *l {
				(*l)[i] = project + "/" + (*l)[i]
			}
		}
	}
	return out, err
}

const (
	ctxBegin = "<!-- SPECWRIGHT:BEGIN — managed by SpecWright (Choose input); edits inside this block are replaced -->"
	ctxEnd   = "<!-- SPECWRIGHT:END -->"
)

// agentFiles are the instruction files of each integration.
var agentFiles = map[string]string{"claude": "CLAUDE.md", "copilot": ".github/copilot-instructions.md"}

// writeAgentContext refreshes the SpecWright block in the instruction file
// of every installed integration. It does nothing for a project without
// Spec Kit.
func (h *Hub) writeAgentContext() ([]string, error) {
	root, dir, project := h.Root(), h.specDir(), h.specRel()
	if root == "" {
		return nil, nil
	}
	info := speckit.Status(dir)
	if !info.Initialized {
		return nil, nil
	}
	h.mu.RLock()
	wf, prof := h.wf, h.profile
	var doc string
	if wf != nil && prof != nil {
		libs, p := rebased(project, h.libsFor(root, prof), prof)
		doc = workflow.ContextDoc(wf, p, libs, h.toolRefs())
		if project != "" {
			doc += "\nSpecWright's skills, rules and inputs live in the workspace one level up (`../.codegen/`); this project folder is `" + project + "/`.\n"
		}
	}
	h.mu.RUnlock()
	if doc == "" {
		return nil, nil
	}
	integs := info.Integrations
	if len(integs) == 0 && info.Integration != "" {
		integs = []string{info.Integration}
	}
	var written []string
	var errs []error
	for _, in := range integs {
		rel, ok := agentFiles[in]
		if !ok {
			continue
		}
		changed, err := upsertBlock(filepath.Join(dir, filepath.FromSlash(rel)), doc)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if changed {
			written = append(written, rel) // relative to the project folder
		}
	}
	return written, errors.Join(errs...)
}

// upsertBlock puts body between the SpecWright markers of file, keeping
// everything else; it reports whether the file changed.
func upsertBlock(file, body string) (bool, error) {
	block := ctxBegin + "\n" + strings.TrimSpace(body) + "\n" + ctxEnd + "\n"
	cur, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	s := string(cur)
	var next string
	if i := strings.Index(s, ctxBegin); i >= 0 {
		if j := strings.Index(s[i:], ctxEnd); j >= 0 {
			end := i + j + len(ctxEnd)
			if end < len(s) && s[end] == '\n' {
				end++
			}
			next = s[:i] + block + s[end:]
		}
	}
	if next == "" {
		switch {
		case s == "":
			next = block
		case strings.HasSuffix(s, "\n"):
			next = s + "\n" + block
		default:
			next = s + "\n\n" + block
		}
	}
	if next == s {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(file, []byte(next), 0o644)
}

// stepScope is what each Spec Kit step may produce, so a step does its
// own job only.
var stepScope = map[string]string{
	"constitution": "Only create or update `.specify/memory/constitution.md` (with its Sync Impact Report). Do not create specs, plans, tasks or code; list anything else as Next Actions.",
	"specify":      "Create this feature's spec only: the feature folder under `specs/` (made by `.specify/scripts/bash/create-new-feature.sh`), its `spec.md` and requirements checklist. No plan, tasks or code.",
	"clarify":      "Only update the current feature's `spec.md` (its Clarifications and the sections the answers touch). This run is not interactive: use the answers given above, and record any question still open in the spec instead of waiting for a reply.",
	"plan":         "Only write the plan documents in the current feature folder: `plan.md`, `research.md`, `data-model.md`, `contracts/` and `quickstart.md`. No tasks or code.",
	"tasks":        "Only write `tasks.md` in the current feature folder.",
	"analyze":      "Read-only: report findings; do not change any file.",
	"checklist":    "Only write the checklist file under the current feature's `checklists/`.",
	"implement":    "Implement the tasks in `tasks.md` in order, marking each one done ([X]) when it is finished. Stop at the first failing build or test and report it.",
}

// specKitStep fills in how step i of the Spec workflow runs in this
// project: the installed command and its guide. root is the project folder
// (the agent's working directory).
func specKitStep(root, integration, project string, s *workflow.Step) {
	if !strings.HasPrefix(s.Cmd, "/speckit") {
		return
	}
	st := speckit.StepFor(root, integration, s.ID)
	s.Cmd = st.Invoke
	var g []string
	if st.Skill != "" {
		g = append(g, "This is the Spec Kit command `"+st.Invoke+"`: read `"+st.Skill+"` and follow it exactly, with the text after the command above as its input ($ARGUMENTS).")
	}
	if sc := stepScope[s.ID]; sc != "" {
		g = append(g, sc)
	}
	if project != "" {
		g = append(g, "Your working directory is the project folder `"+project+"/` (where `.specify/` is): keep every spec, plan and source file inside it. SpecWright's skills and inputs are one level up, in `../.codegen/`.")
	}
	g = append(g, "You run without a person at the keyboard: where the command would ask and wait, use the input above or make a reasonable assumption and say so.")
	s.Guide = strings.Join(g, "\n")
}
