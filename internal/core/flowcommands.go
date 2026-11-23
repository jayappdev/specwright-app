package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"codegen/internal/flow"
)

// Flow command approval.
//
// A flow's "Run a command" steps (transform op "command") run a shell command
// in the project. Flows can be imported, pasted or written by the AI, so a run
// refuses to start while any of its commands is not approved for the project,
// the same way agents cannot call a project tool before it is approved.
//
// Approvals are kept by the user, not by the project: in the app's own data
// folder (approved-commands.json), keyed by project folder and by a hash of
// the command text as written in the flow (before {{…}} placeholders are
// filled in). A project folder therefore cannot ship its own approvals, and
// changing a command needs a new approval.

// FlowCommand is one command step of a flow, with whether it may run.
type FlowCommand struct {
	Step     string `json:"step"`
	Title    string `json:"title"`
	Command  string `json:"command"`
	Approved bool   `json:"approved"`
	// Kind: "" a shell command; "sends" a built-in call that sends data out
	// of the computer (outward.go)
	Kind string `json:"kind,omitempty"`
}

// ErrCommandsNotApproved starts the error of a run refused for unapproved commands.
var ErrCommandsNotApproved = errors.New("this flow runs commands that are not approved yet")

var approvalsMu sync.Mutex

func commandHash(cmd string) string {
	s := sha256.Sum256([]byte(strings.TrimSpace(cmd)))
	return hex.EncodeToString(s[:])
}

func (h *Hub) approvalsPath() string { return filepath.Join(h.Store.Dir(), "approved-commands.json") }

// approvals reads project folder → approved command hashes.
func (h *Hub) approvals() map[string][]string {
	m := map[string][]string{}
	if b, err := os.ReadFile(h.approvalsPath()); err == nil {
		_ = json.Unmarshal(b, &m) // a damaged file means nothing is approved: safe
	}
	return m
}

func projectKey(root string) string {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return filepath.Clean(root)
}

// approveCommandTexts approves these commands (as written in the flow) for the project.
func (h *Hub) approveCommandTexts(root string, cmds []string) error {
	approvalsMu.Lock()
	defer approvalsMu.Unlock()
	m := h.approvals()
	k := projectKey(root)
	have := map[string]bool{}
	for _, x := range m[k] {
		have[x] = true
	}
	for _, c := range cmds {
		if strings.TrimSpace(c) != "" {
			have[commandHash(c)] = true
		}
	}
	list := make([]string, 0, len(have))
	for x := range have {
		list = append(list, x)
	}
	sort.Strings(list)
	m[k] = list
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := h.approvalsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, h.approvalsPath())
}

// flowCommands lists the command steps of f with their approval for the project.
func (h *Hub) flowCommands(root string, f *flow.Flow) []FlowCommand {
	approvalsMu.Lock()
	ok := map[string]bool{}
	for _, x := range h.approvals()[projectKey(root)] {
		ok[x] = true
	}
	approvalsMu.Unlock()
	var out []FlowCommand
	for _, n := range f.Nodes {
		if n.Type != "transform" || n.Op != "command" || strings.TrimSpace(n.Expr) == "" {
			continue
		}
		out = append(out, FlowCommand{Step: n.ID, Title: firstNonEmpty(n.Title, n.ID), Command: n.Expr, Approved: ok[commandHash(n.Expr)]})
	}
	for _, e := range outwardEntries(f) { // built-ins that send data out (outward.go)
		e.Approved = ok[commandHash(e.Command)]
		out = append(out, e)
	}
	return out
}

// checkFlowCommands refuses a flow that has commands not approved for the project.
func (h *Hub) checkFlowCommands(root string, f *flow.Flow) error {
	var missing []string
	for _, c := range h.flowCommands(root, f) {
		if !c.Approved {
			missing = append(missing, fmt.Sprintf("%s ($ %s)", c.Title, c.Command))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s. Review and approve them in Run flows first", ErrCommandsNotApproved, strings.Join(missing, "; "))
}

// FlowCommands lists the command steps of a saved flow and whether each is approved.
func (h *Hub) FlowCommands(id string) ([]FlowCommand, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	f, err := flow.Load(root, id)
	if err != nil {
		return nil, err
	}
	return h.flowCommands(root, f), nil
}

// ApproveFlowCommands approves the commands of these steps of a saved flow,
// as they are written now, for the open project.
func (h *Hub) ApproveFlowCommands(id string, steps []string) ([]FlowCommand, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	f, err := flow.Load(root, id)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, s := range steps {
		want[s] = true
	}
	var cmds []string
	for _, c := range h.flowCommands(root, f) {
		if want[c.Step] {
			cmds = append(cmds, c.Command)
		}
	}
	if len(cmds) == 0 {
		return nil, errors.New("no such command step in this flow")
	}
	if err := h.approveCommandTexts(root, cmds); err != nil {
		return nil, err
	}
	return h.flowCommands(root, f), nil
}
