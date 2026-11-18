package core

// The flow wizard: an AI designer you can follow step by step, instead of
// one call that comes back with flows or an error.
//
//	1 Requirements  what the user wants, in their words
//	2 Clarify       the AI lists the requirements it understands, flags the
//	                NOT-CLEAR ones and asks about them and about the flow's
//	                inputs, outputs, constraints and secrets; the answers go
//	                back to it until nothing is unclear (or the user moves on)
//	3 Plan          a plan document: the tools in context and why, the
//	                clarified requirements, the inputs and outputs, how each
//	                secret is handled, the steps and the risks; editable, and
//	                the AI revises it on request
//	4 Drafts        temporary flows from the plan, each checked and tried with
//	                sample data (the designer's trial); the user runs a draft
//	                for real, gives feedback for a revision, compares versions
//	5 Apply         the approved draft becomes a flow in the Flow designer
//
// A session is saved in .codegen/flow-wizard/<id>.json, so it can be left
// and taken up again. Every AI call is logged in it (phase, what was asked,
// the answer, time and usage) so the user can see what happened.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"codegen/internal/bus"
	"codegen/internal/flow"
	"codegen/internal/store"
)

// WizardDir is where wizard sessions are saved, in the workspace.
const WizardDir = ".codegen/flow-wizard"

// WizardSession is one wizard run.
type WizardSession struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	// Phase: requirements | clarify | plan | drafts | applied
	Phase        string        `json:"phase"`
	Request      string        `json:"request"`
	Model        string        `json:"model,omitempty"`
	Requirements []WizReq      `json:"requirements"`
	Questions    []WizQuestion `json:"questions"`
	Spec         WizSpec       `json:"spec"`
	Plan         string        `json:"plan,omitempty"` // Markdown, editable
	PlanInfo     *WizPlanInfo  `json:"planInfo,omitempty"`
	Drafts       []WizDraft    `json:"drafts"`
	Log          []WizLogEntry `json:"log"`
	Usage        store.Usage   `json:"usage"`
	Applied      []WizApplied  `json:"applied,omitempty"`
	Notes        []string      `json:"notes,omitempty"`
}

// WizReq is one requirement as the AI understands it.
type WizReq struct {
	ID      string `json:"id"`   // R1, R2 …
	Text    string `json:"text"` // the requirement, in plain words
	Status  string `json:"status"`
	Unclear string `json:"unclear,omitempty"` // NOT-CLEAR: what is missing
}

// Requirement statuses.
const (
	ReqClear    = "clear"
	ReqNotClear = "not-clear"
)

// WizQuestion is a question the AI asks to clarify the flow.
type WizQuestion struct {
	ID      string   `json:"id"`              // Q1, Q2 …
	Req     string   `json:"req,omitempty"`   // the requirement it clarifies
	Topic   string   `json:"topic,omitempty"` // requirement | input | output | constraint | secret
	Text    string   `json:"text"`
	Kind    string   `json:"kind,omitempty"` // text | choice | multichoice | yesno
	Options []string `json:"options,omitempty"`
	Answer  string   `json:"answer,omitempty"`
	Round   int      `json:"round,omitempty"` // the clarify round that asked it
}

// WizSpec is what the flow takes and gives, and its constraints.
type WizSpec struct {
	Inputs      []WizIO     `json:"inputs"`
	Outputs     []WizIO     `json:"outputs"`
	Constraints []string    `json:"constraints"`
	Secrets     []WizSecret `json:"secrets"`
}

// WizIO is one input or output of the flow.
type WizIO struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"` // inputs: a flow input type; outputs: md | txt | html | csv | json
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Destination string `json:"destination,omitempty"` // outputs: show, save to a path, call a tool …
}

// WizSecret is a password, token or key the flow needs.
type WizSecret struct {
	Name   string `json:"name"`
	UsedBy string `json:"usedBy,omitempty"` // the tool that needs it
	Source string `json:"source,omitempty"` // flow.SecretSources: ask | keychain | env | auto | prompt
	Env    string `json:"env,omitempty"`    // env / auto: the environment variable
	Why    string `json:"why,omitempty"`
}

// WizPlanInfo is the plan's structured part (the document is Plan).
type WizPlanInfo struct {
	Tools []WizPlanTool `json:"tools"`
	Steps []string      `json:"steps"`
	Risks []string      `json:"risks"`
}

// WizPlanTool is a tool the plan uses, and why.
type WizPlanTool struct {
	Name string `json:"name"`
	Why  string `json:"why"`
}

// WizDraft is a temporary flow: tried, run, revised, then applied or not.
type WizDraft struct {
	ID        string          `json:"id"` // d1, d2 …
	Title     string          `json:"title"`
	Summary   string          `json:"summary"`
	Why       string          `json:"why,omitempty"`
	Version   int             `json:"version"`
	Parent    string          `json:"parent,omitempty"`   // the draft it revises
	Feedback  string          `json:"feedback,omitempty"` // what the revision was asked to change
	Flow      flow.Flow       `json:"flow"`
	Issues    []flow.Issue    `json:"issues"`
	Errors    int             `json:"errors"`
	AISteps   int             `json:"aiSteps"`
	Tools     []string        `json:"tools"`
	Test      flow.DryTest    `json:"test"`
	Trial     *flow.DryResult `json:"trial,omitempty"`
	Runs      []string        `json:"runs,omitempty"` // real test runs (run ids)
	Changes   []string        `json:"changes,omitempty"`
	Recommend bool            `json:"recommended,omitempty"`
	Applied   string          `json:"applied,omitempty"` // the flow it became
	Discarded bool            `json:"discarded,omitempty"`
}

// WizLogEntry records one thing the wizard did: an AI call (with what was
// asked and answered) or a step of the user's.
type WizLogEntry struct {
	At       time.Time   `json:"at"`
	Phase    string      `json:"phase"`
	Summary  string      `json:"summary"`
	Prompt   string      `json:"prompt,omitempty"` // clipped
	Answer   string      `json:"answer,omitempty"` // clipped
	Error    string      `json:"error,omitempty"`
	Duration int64       `json:"durationMs,omitempty"`
	Usage    store.Usage `json:"usage"`
}

// WizApplied is a draft that became a flow.
type WizApplied struct {
	Draft string    `json:"draft"`
	Flow  string    `json:"flow"`
	At    time.Time `json:"at"`
}

// WizardSummary is a session in the list.
type WizardSummary struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Phase   string    `json:"phase"`
	Updated time.Time `json:"updated"`
	Drafts  int       `json:"drafts"`
	Unclear int       `json:"unclear"`
}

const wizLogText = 24 << 10 // how much of a prompt and answer the log keeps

var wizIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,80}$`)

func (h *Hub) wizPath(root, id string) (string, error) {
	if !wizIDRE.MatchString(id) {
		return "", fmt.Errorf("no wizard session %q", id)
	}
	return filepath.Join(root, filepath.FromSlash(WizardDir), id+".json"), nil
}

func (h *Hub) wizLoad(root, id string) (*WizardSession, error) {
	p, err := h.wizPath(root, id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no wizard session %q", id)
		}
		return nil, err
	}
	var s WizardSession
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("wizard session %s: %v", id, err)
	}
	s.normalize()
	return &s, nil
}

func (h *Hub) wizSave(root string, s *WizardSession) error {
	p, err := h.wizPath(root, s.ID)
	if err != nil {
		return err
	}
	s.Updated = time.Now()
	s.normalize()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (s *WizardSession) normalize() {
	if s.Requirements == nil {
		s.Requirements = []WizReq{}
	}
	if s.Questions == nil {
		s.Questions = []WizQuestion{}
	}
	if s.Drafts == nil {
		s.Drafts = []WizDraft{}
	}
	if s.Log == nil {
		s.Log = []WizLogEntry{}
	}
	if s.Spec.Inputs == nil {
		s.Spec.Inputs = []WizIO{}
	}
	if s.Spec.Outputs == nil {
		s.Spec.Outputs = []WizIO{}
	}
	if s.Spec.Constraints == nil {
		s.Spec.Constraints = []string{}
	}
	if s.Spec.Secrets == nil {
		s.Spec.Secrets = []WizSecret{}
	}
}

// unclear counts the NOT-CLEAR requirements.
func (s *WizardSession) unclear() int {
	n := 0
	for _, r := range s.Requirements {
		if r.Status == ReqNotClear {
			n++
		}
	}
	return n
}

// --- API ------------------------------------------------------------------------

// WizardList lists the project's wizard sessions, newest first.
func (h *Hub) WizardList() ([]WizardSummary, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	ents, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(WizardDir)))
	out := []WizardSummary{}
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		s, err := h.wizLoad(root, id)
		if err != nil {
			continue
		}
		n := 0
		for _, d := range s.Drafts {
			if !d.Discarded {
				n++
			}
		}
		out = append(out, WizardSummary{ID: s.ID, Title: s.Title, Phase: s.Phase, Updated: s.Updated, Drafts: n, Unclear: s.unclear()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// WizardLoad returns a session.
func (h *Hub) WizardLoad(id string) (WizardSession, error) {
	root, err := h.need()
	if err != nil {
		return WizardSession{}, err
	}
	s, err := h.wizLoad(root, id)
	if err != nil {
		return WizardSession{}, err
	}
	return *s, nil
}

// WizardDelete removes a session (its drafts go with it; applied flows stay).
func (h *Hub) WizardDelete(id string) error {
	root, err := h.need()
	if err != nil {
		return err
	}
	p, err := h.wizPath(root, id)
	if err != nil {
		return err
	}
	return h.toTrash(p)
}

// WizardStart starts a session from the user's request: the AI derives the
// requirements, flags the unclear ones and asks its first questions.
func (h *Hub) WizardStart(request, model string) (WizardSession, error) {
	root, err := h.need()
	if err != nil {
		return WizardSession{}, err
	}
	request = strings.TrimSpace(request)
	if request == "" {
		return WizardSession{}, errors.New("describe what the flow should do")
	}
	now := time.Now()
	s := &WizardSession{ID: wizardID(root, request, now), Title: clipText(request, 80), Created: now, Phase: "requirements", Request: request, Model: model}
	s.normalize()
	s.Log = append(s.Log, WizLogEntry{At: now, Phase: "requirements", Summary: "You described the flow"})
	if err := h.wizSave(root, s); err != nil {
		return WizardSession{}, err
	}
	err = h.wizUnderstand(root, s, "")
	if serr := h.wizSave(root, s); err == nil {
		err = serr
	}
	return *s, err
}

func wizardID(root, request string, now time.Time) string {
	base := flow.Slug(clipText(request, 40))
	id := now.Format("20060102-150405") + "-" + base
	if len(id) > 80 {
		id = strings.TrimRight(id[:80], "-")
	}
	return id
}

// WizardEdit saves the user's changes to the request, requirements, spec,
// answers or plan (no AI call). Fields left empty stay as they are.
type WizardEdit struct {
	Request      *string           `json:"request,omitempty"`
	Requirements []WizReq          `json:"requirements,omitempty"`
	Spec         *WizSpec          `json:"spec,omitempty"`
	Answers      map[string]string `json:"answers,omitempty"` // question id → answer
	Plan         *string           `json:"plan,omitempty"`
	Model        *string           `json:"model,omitempty"`
	Title        *string           `json:"title,omitempty"`
	Phase        string            `json:"phase,omitempty"` // move to an earlier or done phase
}

// WizardUpdate applies a WizardEdit.
func (h *Hub) WizardUpdate(id string, e WizardEdit) (WizardSession, error) {
	root, err := h.need()
	if err != nil {
		return WizardSession{}, err
	}
	s, err := h.wizLoad(root, id)
	if err != nil {
		return WizardSession{}, err
	}
	s.applyEdit(e)
	if e.Phase != "" {
		switch e.Phase {
		case "requirements", "clarify", "plan", "drafts":
			s.Phase = e.Phase
		default:
			return WizardSession{}, fmt.Errorf("unknown phase %q", e.Phase)
		}
	}
	return *s, h.wizSave(root, s)
}

func (s *WizardSession) applyEdit(e WizardEdit) {
	if e.Request != nil {
		s.Request = strings.TrimSpace(*e.Request)
	}
	if e.Title != nil && strings.TrimSpace(*e.Title) != "" {
		s.Title = strings.TrimSpace(*e.Title)
	}
	if e.Requirements != nil {
		s.Requirements = e.Requirements
	}
	if e.Spec != nil {
		s.Spec = *e.Spec
	}
	if e.Plan != nil {
		s.Plan = *e.Plan
	}
	if e.Model != nil {
		s.Model = *e.Model
	}
	for i := range s.Questions {
		if a, ok := e.Answers[s.Questions[i].ID]; ok {
			s.Questions[i].Answer = strings.TrimSpace(a)
		}
	}
}

// WizardClarify sends the answers (and any edits) back to the AI, which
// updates the requirements and asks again only about what is still unclear.
func (h *Hub) WizardClarify(id string, e WizardEdit) (WizardSession, error) {
	root, err := h.need()
	if err != nil {
		return WizardSession{}, err
	}
	s, err := h.wizLoad(root, id)
	if err != nil {
		return WizardSession{}, err
	}
	s.applyEdit(e)
	err = h.wizUnderstand(root, s, "")
	if serr := h.wizSave(root, s); err == nil {
		err = serr
	}
	return *s, err
}

// WizardPlan has the AI write (or revise, with feedback) the plan.
func (h *Hub) WizardPlan(id, feedback string) (WizardSession, error) {
	root, err := h.need()
	if err != nil {
		return WizardSession{}, err
	}
	s, err := h.wizLoad(root, id)
	if err != nil {
		return WizardSession{}, err
	}
	err = h.wizPlan(root, s, strings.TrimSpace(feedback))
	if serr := h.wizSave(root, s); err == nil {
		err = serr
	}
	return *s, err
}

// WizardDraft makes n temporary flows (2–4) from the plan (or straight from
// the request when there is no plan: "skip to drafts").
func (h *Hub) WizardDraft(id string, n int, feedback string) (WizardSession, error) {
	root, err := h.need()
	if err != nil {
		return WizardSession{}, err
	}
	s, err := h.wizLoad(root, id)
	if err != nil {
		return WizardSession{}, err
	}
	err = h.wizDrafts(root, s, n, strings.TrimSpace(feedback))
	if serr := h.wizSave(root, s); err == nil {
		err = serr
	}
	return *s, err
}

// WizardRevise has the AI revise one draft with feedback (and, when it was
// run, how that run went): the revision is a new draft, the old one stays.
func (h *Hub) WizardRevise(id, draftID, feedback string) (WizardSession, error) {
	root, err := h.need()
	if err != nil {
		return WizardSession{}, err
	}
	s, err := h.wizLoad(root, id)
	if err != nil {
		return WizardSession{}, err
	}
	err = h.wizRevise(root, s, draftID, strings.TrimSpace(feedback))
	if serr := h.wizSave(root, s); err == nil {
		err = serr
	}
	return *s, err
}

// WizardDiscard hides a draft (or brings it back).
func (h *Hub) WizardDiscard(id, draftID string, discard bool) (WizardSession, error) {
	root, err := h.need()
	if err != nil {
		return WizardSession{}, err
	}
	s, err := h.wizLoad(root, id)
	if err != nil {
		return WizardSession{}, err
	}
	d := s.draft(draftID)
	if d == nil {
		return WizardSession{}, fmt.Errorf("no draft %q", draftID)
	}
	d.Discarded = discard
	return *s, h.wizSave(root, s)
}

// WizardSaveDraftTest changes a draft's sample data and tries it again.
func (h *Hub) WizardSaveDraftTest(id, draftID string, test flow.DryTest) (WizardSession, error) {
	root, err := h.need()
	if err != nil {
		return WizardSession{}, err
	}
	s, err := h.wizLoad(root, id)
	if err != nil {
		return WizardSession{}, err
	}
	d := s.draft(draftID)
	if d == nil {
		return WizardSession{}, fmt.Errorf("no draft %q", draftID)
	}
	d.Test = test
	h.retryDraft(d)
	return *s, h.wizSave(root, s)
}

// WizardRunDraft runs a draft for real (tools and AI called), without saving
// it as a flow. Reviews are off: the wizard shows the steps as they run.
func (h *Hub) WizardRunDraft(id, draftID string, o FlowRunOptions) (FlowRunState, error) {
	root, err := h.need()
	if err != nil {
		return FlowRunState{}, err
	}
	s, err := h.wizLoad(root, id)
	if err != nil {
		return FlowRunState{}, err
	}
	d := s.draft(draftID)
	if d == nil {
		return FlowRunState{}, fmt.Errorf("no draft %q", draftID)
	}
	f := d.Flow
	f.ID = "draft-" + s.ID + "-" + d.ID
	if len(f.ID) > 60 {
		f.ID = f.ID[:60]
	}
	f.Name = d.Title + " (draft)"
	if o.Review == "" {
		o.Review = "none"
	}
	if err := h.prepareRun(root, &f, &o); err != nil {
		return FlowRunState{}, err
	}
	st, err := h.startFlow(root, &f, o, &FlowTrial{Draft: s.ID + "/" + d.ID})
	if err != nil {
		return FlowRunState{}, err
	}
	d.Runs = append(d.Runs, st.ID)
	s.Log = append(s.Log, WizLogEntry{At: time.Now(), Phase: "drafts", Summary: fmt.Sprintf("You ran %s v%d for real (run %s)", d.Title, d.Version, st.ID)})
	_ = h.wizSave(root, s)
	return st, nil
}

// WizardApply saves a draft as a flow in the Flow designer.
func (h *Hub) WizardApply(id, draftID, name string) (FlowState, error) {
	root, err := h.need()
	if err != nil {
		return FlowState{}, err
	}
	s, err := h.wizLoad(root, id)
	if err != nil {
		return FlowState{}, err
	}
	d := s.draft(draftID)
	if d == nil {
		return FlowState{}, fmt.Errorf("no draft %q", draftID)
	}
	f := d.Flow
	f.Name = firstNonEmpty(strings.TrimSpace(name), f.Name, d.Title)
	f.ID = flow.UniqueID(root, flow.Slug(f.Name))
	f.Description = firstNonEmpty(f.Description, d.Summary)
	f.Designed = "Made with the flow wizard: “" + clipText(s.Request, 200) + "”."
	if f.Context.Text == "" && s.Plan != "" {
		f.Context.Text = wizContext(s)
	}
	st, err := h.SaveFlow(f)
	if err != nil {
		return FlowState{}, err
	}
	d.Applied = st.Flow.ID
	s.Applied = append(s.Applied, WizApplied{Draft: d.ID, Flow: st.Flow.ID, At: time.Now()})
	s.Phase = "applied"
	s.Log = append(s.Log, WizLogEntry{At: time.Now(), Phase: "apply", Summary: fmt.Sprintf("You applied %s v%d as the flow %s", d.Title, d.Version, st.Flow.Name)})
	if err := h.wizSave(root, s); err != nil {
		return st, err
	}
	return st, nil
}

// wizContext is the flow's custom context from the clarified requirements
// (AI steps get it): what the user asked, as the wizard settled it.
func wizContext(s *WizardSession) string {
	var b strings.Builder
	b.WriteString("Requirements (from the flow wizard):\n")
	for _, r := range s.Requirements {
		fmt.Fprintf(&b, "- %s\n", r.Text)
	}
	for _, c := range s.Spec.Constraints {
		fmt.Fprintf(&b, "- Constraint: %s\n", c)
	}
	return strings.TrimSpace(b.String())
}

func (s *WizardSession) draft(id string) *WizDraft {
	for i := range s.Drafts {
		if s.Drafts[i].ID == id {
			return &s.Drafts[i]
		}
	}
	return nil
}

// --- the AI phases -----------------------------------------------------------------

// wizAsk runs one AI call and logs it in the session.
func (h *Hub) wizAsk(root string, s *WizardSession, phase, summary, prompt string) (string, error) {
	h.wizProgress(s, phase, summary)
	start := time.Now()
	text, u, err := h.designAgent(root, prompt, s.Model)
	e := WizLogEntry{At: start, Phase: phase, Summary: summary, Prompt: clipText2(prompt, wizLogText), Answer: clipText2(text, wizLogText),
		Duration: time.Since(start).Milliseconds(), Usage: u}
	if err != nil {
		e.Error = err.Error()
	}
	s.Log = append(s.Log, e)
	s.Usage.Add(u)
	h.recordDesignUsage(root, u, s.Model)
	return text, err
}

// clipText2 cuts s to n bytes, keeping its lines (clipText joins them).
func clipText2(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…(cut)"
}

func (h *Hub) wizProgress(s *WizardSession, phase, msg string) {
	h.Bus.Publish(bus.Event{Type: "design.progress", Summary: msg, Data: map[string]any{"wizard": s.ID, "phase": phase}})
}

func (s *WizardSession) brief() string {
	var b strings.Builder
	fmt.Fprintf(&b, "WHAT THE USER WANTS (their words):\n%s\n", s.Request)
	if len(s.Requirements) > 0 {
		b.WriteString("\nREQUIREMENTS SO FAR:\n")
		for _, r := range s.Requirements {
			fmt.Fprintf(&b, "- %s [%s] %s", r.ID, r.Status, r.Text)
			if r.Unclear != "" {
				fmt.Fprintf(&b, " (unclear: %s)", r.Unclear)
			}
			b.WriteString("\n")
		}
	}
	answered := false
	for _, q := range s.Questions {
		if q.Answer != "" {
			if !answered {
				b.WriteString("\nQUESTIONS AND THE USER'S ANSWERS:\n")
				answered = true
			}
			fmt.Fprintf(&b, "- %s (%s): %s\n  Answer: %s\n", q.ID, firstNonEmpty(q.Req, q.Topic), q.Text, q.Answer)
		}
	}
	var open []string
	for _, q := range s.Questions {
		if q.Answer == "" {
			open = append(open, fmt.Sprintf("- %s: %s", q.ID, q.Text))
		}
	}
	if len(open) > 0 {
		b.WriteString("\nQUESTIONS NOT ANSWERED YET (the user skipped them; make a reasonable assumption and say so):\n" + strings.Join(open, "\n") + "\n")
	}
	sp, _ := json.Marshal(s.Spec)
	fmt.Fprintf(&b, "\nINPUTS, OUTPUTS, CONSTRAINTS AND SECRETS SO FAR:\n%s\n", sp)
	return b.String()
}

// toolList is the tools in a short form (name: what it does), for the
// phases before the plan.
func toolList(tools []designTool) string {
	var b strings.Builder
	for _, t := range tools {
		fmt.Fprintf(&b, "- %s: %s\n", t.Name, clipText(t.Desc, 120))
	}
	if b.Len() == 0 {
		return "(none)\n"
	}
	return b.String()
}

const wizUnderstandFormat = `Reply with ONLY this JSON, no explanation and no code fences:
{"title": "<a short name for the flow>",
 "requirements": [{"id": "R1", "text": "<one requirement, plain words>", "status": "clear|not-clear", "unclear": "<when not-clear: what is missing or ambiguous>"}],
 "questions": [{"id": "Q1", "req": "<the requirement id it clarifies, if any>", "topic": "requirement|input|output|constraint|secret", "text": "<the question>", "kind": "text|choice|multichoice|yesno", "options": ["<for choice and multichoice>"]}],
 "inputs": [{"name": "<input name: letters, digits, _>", "type": "text|multiline|secret|number|boolean|date|datetime|choice|multichoice|file|files|clipboard", "description": "...", "required": true}],
 "outputs": [{"name": "...", "type": "md|txt|html|csv|json", "description": "...", "destination": "show|save to <path>|call <tool>|clipboard"}],
 "constraints": ["<limits, rules, performance, what must not happen>"],
 "secrets": [{"name": "<input name of the secret>", "usedBy": "<tool>", "why": "..."}]}`

// wizUnderstand: the AI derives the requirements from the request (and the
// answers so far) and asks about what is NOT-CLEAR.
func (h *Hub) wizUnderstand(root string, s *WizardSession, extra string) error {
	round := 1
	for _, q := range s.Questions {
		round = max(round, q.Round+1)
	}
	first := len(s.Requirements) == 0
	var b strings.Builder
	b.WriteString("You help a user design an automation flow in SpecWright (a pipeline of the project's tools, plain data steps and AI steps). Before anything is built, work out exactly what they need.\n\n")
	b.WriteString(s.brief())
	b.WriteString("\nTHE PROJECT'S TOOLS (what is possible):\n" + toolList(h.designTools()))
	if first {
		b.WriteString(`
Derive the requirements from what the user wants: one per line, specific and testable. Mark a requirement "not-clear" when something needed to build it is missing or ambiguous (which data, how much, what format, where it goes, when it should stop, what counts as success…), and say what in "unclear". Ask questions (at most 8) for every not-clear requirement and for what the flow needs to take as input, give as output, its constraints, and any passwords, tokens or API keys it needs — prefer choice / yesno questions with good options when you can guess them. Propose the inputs, outputs, constraints and secrets you can already see.
`)
	} else {
		b.WriteString(`
Update the requirements with the user's answers: keep the ids, mark a requirement "clear" once the answers settle it, reword it to include what was decided, add requirements the answers reveal. Ask NEW questions only for what is still not clear (at most 6, none already answered; an empty list when everything is clear). Update the inputs, outputs, constraints and secrets.
`)
	}
	if extra != "" {
		b.WriteString("\nALSO: " + extra + "\n")
	}
	b.WriteString("\n" + wizUnderstandFormat)
	phase := "clarify"
	summary := fmt.Sprintf("Understanding the answers (round %d)", round)
	if first {
		phase, summary = "requirements", "Deriving the requirements from your request"
	}
	text, err := h.wizAsk(root, s, phase, summary, b.String())
	if err != nil {
		return err
	}
	var got struct {
		Title        string        `json:"title"`
		Requirements []WizReq      `json:"requirements"`
		Questions    []WizQuestion `json:"questions"`
		Inputs       []WizIO       `json:"inputs"`
		Outputs      []WizIO       `json:"outputs"`
		Constraints  []string      `json:"constraints"`
		Secrets      []WizSecret   `json:"secrets"`
	}
	if err := json.Unmarshal([]byte(firstJSON(jsonReply(text))), &got); err != nil || len(got.Requirements) == 0 {
		err = fmt.Errorf("the AI did not return the requirements (%v)", firstNonEmpty(fmt.Sprint(err), "none"))
		s.Log[len(s.Log)-1].Error = err.Error()
		return err
	}
	for i := range got.Requirements {
		r := &got.Requirements[i]
		if r.ID == "" {
			r.ID = fmt.Sprintf("R%d", i+1)
		}
		if r.Status != ReqNotClear {
			r.Status, r.Unclear = ReqClear, ""
		}
	}
	s.Requirements = got.Requirements
	if got.Title != "" && first {
		s.Title = clipText(got.Title, 80)
	}
	// new questions are added; answered ones stay as the record
	seen := map[string]bool{}
	for _, q := range s.Questions {
		seen[strings.ToLower(q.Text)] = true
	}
	n := len(s.Questions)
	var kept []WizQuestion
	for _, q := range s.Questions {
		if q.Answer != "" || first {
			kept = append(kept, q)
		}
	}
	s.Questions = kept
	for _, q := range got.Questions {
		if strings.TrimSpace(q.Text) == "" || seen[strings.ToLower(q.Text)] {
			continue
		}
		n++
		q.ID = fmt.Sprintf("Q%d", n)
		q.Round = round
		switch q.Kind {
		case "choice", "multichoice", "yesno":
		default:
			q.Kind = "text"
		}
		if q.Kind == "yesno" {
			q.Options = []string{"Yes", "No"}
		}
		s.Questions = append(s.Questions, q)
	}
	if got.Inputs != nil {
		s.Spec.Inputs = got.Inputs
	}
	if got.Outputs != nil {
		s.Spec.Outputs = got.Outputs
	}
	if got.Constraints != nil {
		s.Spec.Constraints = got.Constraints
	}
	if got.Secrets != nil {
		s.Spec.Secrets = mergeSecrets(s.Spec.Secrets, got.Secrets)
	}
	s.normalize()
	s.Phase = "clarify"
	return nil
}

// mergeSecrets keeps the sources the user (or the plan) chose.
func mergeSecrets(old, got []WizSecret) []WizSecret {
	by := map[string]WizSecret{}
	for _, x := range old {
		by[x.Name] = x
	}
	for i := range got {
		if o, ok := by[got[i].Name]; ok && got[i].Source == "" {
			got[i].Source, got[i].Env = o.Source, o.Env
		}
	}
	return got
}

// designToolsText is the tools with their contracts, as the designer gives them.
func designToolsText(tools []designTool) string {
	full := designPrompt(DesignRequest{Prompt: "-"}, nil, nil, tools)
	a := strings.Index(full, "TOOLS (name:")
	z := strings.Index(full, "\nWHAT THE USER WANTS:")
	if a < 0 || z < a {
		return toolList(tools)
	}
	return full[a:z]
}

// wizPlan: the AI writes the plan document from the clarified requirements.
func (h *Hub) wizPlan(root string, s *WizardSession, feedback string) error {
	var b strings.Builder
	b.WriteString("You plan an automation flow in SpecWright (a pipeline of the project's tools, plain data steps — transform, filter, aggregate, decision, loop, merge, output — and AI steps only where nothing else fits). The requirements have been clarified with the user.\n\n")
	b.WriteString(s.brief())
	b.WriteString("\n" + designToolsText(h.designTools()))
	if s.Plan != "" {
		b.WriteString("\nTHE CURRENT PLAN:\n" + s.Plan + "\n")
	}
	if feedback != "" {
		b.WriteString("\nREVISE THE PLAN — the user asks: " + feedback + "\n")
	}
	b.WriteString(`
Write the plan the flow will be built from, as a Markdown document with these sections:
## Goal — one or two sentences.
## Requirements — every clarified requirement (by id), and any assumption you made for an unanswered question.
## Tools in context — the tools from the list above the flow will use, and for each why and with which arguments; say which steps need no tool (plain data steps) and which need AI, and why.
## Inputs — each flow input: name, type, required, example.
## Outputs — each output: format, what it contains, where it goes.
## Secrets — every password, token or API key: which tool needs it, the flow input that carries it (type secret, never sent to AI steps), and where its value comes from: the Keychain (best on a desktop), an environment variable (CI, scheduled runs), asked when the run starts, or asked when the step is reached. "None" if there are none.
## Steps — the flow's steps in order, each with what it does and the data it passes on.
## Risks and checks — what could go wrong, and how the flow checks its results.

Reply with ONLY this JSON, no explanation and no code fences:
{"plan": "<the Markdown document>",
 "tools": [{"name": "<tool name from the list>", "why": "..."}],
 "inputs": [{"name": "...", "type": "...", "description": "...", "required": true}],
 "outputs": [{"name": "...", "type": "md|txt|html|csv|json", "description": "...", "destination": "..."}],
 "secrets": [{"name": "<the secret input's name>", "usedBy": "<tool>", "source": "keychain|env|ask|prompt|auto", "env": "<variable, for env/auto>", "why": "..."}],
 "steps": ["<step 1>", "..."],
 "risks": ["..."]}`)
	summary := "Writing the plan"
	if feedback != "" {
		summary = "Revising the plan: " + clipText(feedback, 80)
	}
	text, err := h.wizAsk(root, s, "plan", summary, b.String())
	if err != nil {
		return err
	}
	var got struct {
		Plan    string        `json:"plan"`
		Tools   []WizPlanTool `json:"tools"`
		Inputs  []WizIO       `json:"inputs"`
		Outputs []WizIO       `json:"outputs"`
		Secrets []WizSecret   `json:"secrets"`
		Steps   []string      `json:"steps"`
		Risks   []string      `json:"risks"`
	}
	if err := json.Unmarshal([]byte(firstJSON(jsonReply(text))), &got); err != nil || strings.TrimSpace(got.Plan) == "" {
		err = fmt.Errorf("the AI did not return a plan (%v)", firstNonEmpty(fmt.Sprint(err), "empty"))
		s.Log[len(s.Log)-1].Error = err.Error()
		return err
	}
	// only tools that exist
	known := map[string]bool{}
	for _, t := range h.designTools() {
		known[t.Name] = true
	}
	var tools []WizPlanTool
	for _, t := range got.Tools {
		if known[t.Name] || known["builtin:"+t.Name] {
			tools = append(tools, t)
		} else {
			s.Notes = appendOnce(s.Notes, fmt.Sprintf("The plan named a tool that is not in this project: %s. The drafts use only tools that exist.", t.Name))
		}
	}
	for i := range got.Secrets {
		if !slices.Contains(flow.SecretSources, got.Secrets[i].Source) {
			got.Secrets[i].Source = "auto"
		}
	}
	s.Plan = strings.TrimSpace(got.Plan)
	s.PlanInfo = &WizPlanInfo{Tools: tools, Steps: got.Steps, Risks: got.Risks}
	if len(got.Inputs) > 0 {
		s.Spec.Inputs = got.Inputs
	}
	if len(got.Outputs) > 0 {
		s.Spec.Outputs = got.Outputs
	}
	if got.Secrets != nil {
		s.Spec.Secrets = got.Secrets
	}
	s.normalize()
	s.Phase = "plan"
	return nil
}

func appendOnce(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// draftBrief is what the designer builds the drafts from: the plan (or the
// request and requirements when there is none yet).
func (s *WizardSession) draftBrief() string {
	var b strings.Builder
	b.WriteString(s.Request + "\n")
	if len(s.Requirements) > 0 {
		b.WriteString("\nClarified requirements:\n")
		for _, r := range s.Requirements {
			fmt.Fprintf(&b, "- %s: %s\n", r.ID, r.Text)
		}
	}
	if s.Plan != "" {
		b.WriteString("\nFollow this plan (agreed with the user):\n" + s.Plan + "\n")
	}
	if len(s.Spec.Secrets) > 0 {
		b.WriteString("\nSecrets: give each as an input of type secret named as here, used only in the tool arguments that need it:\n")
		for _, x := range s.Spec.Secrets {
			fmt.Fprintf(&b, "- %s (for %s)\n", x.Name, firstNonEmpty(x.UsedBy, "a tool"))
		}
	}
	return b.String()
}

// wizDrafts asks the designer for temporary flows from the plan.
func (h *Hub) wizDrafts(root string, s *WizardSession, n int, feedback string) error {
	if n < 1 || n > 4 {
		n = 3
	}
	prompt := s.draftBrief()
	if feedback != "" {
		prompt += "\nThe user also asks: " + feedback + "\n"
	}
	summary := fmt.Sprintf("Making %d draft flow%s from the %s", n, map[bool]string{true: "", false: "s"}[n == 1], map[bool]string{true: "plan", false: "request"}[s.Plan != ""])
	h.wizProgress(s, "drafts", summary)
	start := time.Now()
	res, err := h.DesignFlows(DesignRequest{Prompt: prompt, Options: n, Min: 1, Model: s.Model})
	e := WizLogEntry{At: start, Phase: "drafts", Summary: summary, Prompt: clipText2(prompt, wizLogText), Duration: time.Since(start).Milliseconds(), Usage: res.Usage}
	raw := clipText2(strings.Join(res.Raw, "\n\n---\n\n"), wizLogText)
	if err != nil {
		e.Error, e.Answer = err.Error(), raw
		s.Log = append(s.Log, e)
		s.Usage.Add(res.Usage)
		return err
	}
	var names []string
	for _, o := range res.Options {
		d := s.addDraft(o, "", "")
		names = append(names, fmt.Sprintf("%s (%s)", d.Title, draftVerdict(d)))
	}
	e.Answer = "Drafts: " + strings.Join(names, "; ")
	if res.Repaired {
		e.Answer += "\nSome drafts did not check out at first and were fixed by the AI in a second round."
	}
	for _, n := range res.Notes {
		e.Answer += "\n" + n
	}
	e.Answer += "\n\nThe AI's answer:\n" + raw
	if len(res.Options) < n && len(res.Notes) > 0 {
		s.Notes = appendOnce(s.Notes, fmt.Sprintf("%d of the %d drafts asked for came back: %s", len(res.Options), n, strings.Join(res.Notes, " ")))
	}
	s.Log = append(s.Log, e)
	s.Usage.Add(res.Usage)
	s.Phase = "drafts"
	return nil
}

func draftVerdict(d *WizDraft) string {
	switch {
	case d.Errors > 0:
		return fmt.Sprintf("%d problem%s", d.Errors, map[bool]string{true: "", false: "s"}[d.Errors == 1])
	case d.Trial != nil && d.Trial.OK:
		return "checks out, sample run passed"
	}
	return "checks out"
}

// addDraft adds a design as a draft; secret inputs get the plan's source.
func (s *WizardSession) addDraft(o FlowOption, parent, feedback string) *WizDraft {
	n := 0
	for _, d := range s.Drafts {
		var k int
		if _, err := fmt.Sscanf(d.ID, "d%d", &k); err == nil && k > n {
			n = k
		}
	}
	d := WizDraft{ID: fmt.Sprintf("d%d", n+1), Title: o.Title, Summary: o.Summary, Why: o.Why, Version: 1, Parent: parent, Feedback: feedback,
		Flow: o.Flow, Issues: o.Issues, Errors: o.Errors, AISteps: o.AISteps, Tools: o.Tools, Test: o.Test, Trial: o.Trial, Changes: o.Changes, Recommend: o.Recommended}
	if p := s.draft(parent); p != nil {
		d.Version = p.Version + 1
		d.Title = p.Title
	}
	for i := range d.Flow.Inputs {
		in := &d.Flow.Inputs[i]
		if in.Type != "secret" {
			continue
		}
		for _, x := range s.Spec.Secrets {
			if strings.EqualFold(x.Name, in.Name) && x.Source != "" {
				in.Source = x.Source
				if x.Env != "" {
					in.Env = x.Env
				}
			}
		}
	}
	s.Drafts = append(s.Drafts, d)
	return &s.Drafts[len(s.Drafts)-1]
}

// wizRevise asks the designer for a revision of one draft.
func (h *Hub) wizRevise(root string, s *WizardSession, draftID, feedback string) error {
	d := s.draft(draftID)
	if d == nil {
		return fmt.Errorf("no draft %q", draftID)
	}
	var b strings.Builder
	b.WriteString(firstNonEmpty(feedback, "Fix its problems and make it more reliable.") + "\n")
	if len(d.Runs) > 0 {
		last := d.Runs[len(d.Runs)-1]
		if st, err := h.FlowRun(last); err == nil {
			fmt.Fprintf(&b, "\nIts last real run %s.", st.Status)
			if st.Error != "" {
				b.WriteString(" Error: " + clipText(st.Error, 400))
			}
			for _, x := range st.Steps {
				if x.Status == "failed" {
					fmt.Fprintf(&b, "\nStep %s failed: %s", firstNonEmpty(x.Title, x.ID), clipText(x.Error, 300))
				}
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\nThe flow must still meet these requirements:\n" + s.draftBrief())
	base := d.Flow
	summary := fmt.Sprintf("Revising %s v%d: %s", d.Title, d.Version, clipText(firstNonEmpty(feedback, "fix and improve"), 80))
	h.wizProgress(s, "drafts", summary)
	start := time.Now()
	res, err := h.DesignFlows(DesignRequest{Prompt: b.String(), Base: &base, Options: 1, Min: 1, Model: s.Model})
	e := WizLogEntry{At: start, Phase: "drafts", Summary: summary, Prompt: clipText2(b.String(), wizLogText), Duration: time.Since(start).Milliseconds(), Usage: res.Usage}
	s.Usage.Add(res.Usage)
	e.Answer = clipText2(strings.Join(res.Raw, "\n\n---\n\n"), wizLogText)
	if err == nil && len(res.Options) == 0 {
		err = errors.New("the AI returned no revision that could be read: " + firstNonEmpty(strings.Join(res.Notes, " "), "no options"))
	}
	if err != nil {
		e.Error = err.Error()
		s.Log = append(s.Log, e)
		return err
	}
	nd := s.addDraft(res.Options[0], d.ID, feedback)
	e.Answer = fmt.Sprintf("%s v%d: %s. Changes: %s\n\n%s", nd.Title, nd.Version, draftVerdict(nd), strings.Join(nd.Changes, "; "), e.Answer)
	for _, n := range res.Notes {
		s.Notes = appendOnce(s.Notes, n)
	}
	s.Log = append(s.Log, e)
	return nil
}

// retryDraft checks a draft again and tries it with its sample data.
func (h *Hub) retryDraft(d *WizDraft) {
	tools := map[string]flow.DryTool{}
	for _, t := range h.designTools() {
		dt := flow.DryTool{Output: t.Output, OutputBy: t.OutputBy}
		for _, p := range t.Params {
			if p.Required {
				dt.Required = append(dt.Required, p.Name)
			}
		}
		tools[t.Name] = dt
	}
	d.Issues = h.ValidateFlow(d.Flow)
	if d.Issues == nil {
		d.Issues = []flow.Issue{}
	}
	d.Errors = 0
	for _, is := range d.Issues {
		if is.Level == "error" {
			d.Errors++
		}
	}
	d.Trial = nil
	if d.Errors == 0 {
		f := d.Flow
		t := flow.DryRunFlow(&f, d.Test, tools)
		d.Trial = &t
		if !t.OK {
			d.Errors++
			d.Issues = append(d.Issues, flow.Issue{Level: "error", Message: "Trial run with sample data failed"})
		}
	}
}
