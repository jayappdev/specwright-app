package core

// Pre-approving what a flow's AI steps may do without asking
// (docs/security/2026-11-26-security-review.md, items 2 and 4).
//
// An AI that runs a command, changes or runs flows and tools, sends data
// out, or opens a local or intranet address asks a person first
// (agentactions.go, outward.go). A scheduled or triggered run has nobody
// watching, so the question would wait and the step would fail. A person
// can therefore allow, per flow, the kinds of actions its AI steps may take
// unasked (Run flows › What the AI may do).
//
// Like command approvals (flowcommands.go), these are kept by the user, in
// the app's own data folder (approved-ai-actions.json), keyed by project
// folder and flow id: a project or a flow file cannot grant them itself.
// A flow's AI steps get a bridge token of their own (Bridge.NewScope), so a
// call is matched to its own run even when several run at once.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	"codegen/internal/extension"
	"codegen/internal/flow"
)

// AI action kinds a flow can pre-approve.
const (
	AIRunCommands = "commands" // run_command
	AIChangeFlows = "flows"    // save_flow, run_flow, schedule_flow, save_tool
	AISendOut     = "send"     // HTTP calls that change things, messages
	AILocalNet    = "network"  // this computer and private networks
)

var aiKinds = []string{AIRunCommands, AIChangeFlows, AISendOut, AILocalNet}

// aiKindOf: which pre-approval covers a built-in call asked about.
func aiKindOf(tool string) string {
	switch tool {
	case "run_command":
		return AIRunCommands
	case SaveFlowTool, RunFlowTool, ScheduleFlowTool, SaveToolTool:
		return AIChangeFlows
	}
	return ""
}

// FlowAIActions is what a flow's AI steps may do without asking.
type FlowAIActions struct {
	HasAI   bool     `json:"hasAI"`   // the flow has steps that run an AI
	Allowed []string `json:"allowed"` // kinds (commands, flows, send, network)
}

var aiApprovalsMu sync.Mutex

func (h *Hub) aiApprovalsPath() string {
	return filepath.Join(h.Store.Dir(), "approved-ai-actions.json")
}

// aiApprovals reads project folder → flow id → kinds.
func (h *Hub) aiApprovals() map[string]map[string][]string {
	m := map[string]map[string][]string{}
	if b, err := os.ReadFile(h.aiApprovalsPath()); err == nil {
		_ = json.Unmarshal(b, &m) // damaged: nothing pre-approved, which is safe
	}
	return m
}

func (h *Hub) flowAIAllowed(root, flowID, kind string) bool {
	if kind == "" || root == "" || flowID == "" {
		return false
	}
	aiApprovalsMu.Lock()
	defer aiApprovalsMu.Unlock()
	return slices.Contains(h.aiApprovals()[projectKey(root)][flowID], kind)
}

// flowHasAI: whether any step of f runs an AI.
func flowHasAI(f *flow.Flow) bool {
	for _, n := range f.Nodes {
		switch n.Type {
		case "ai", "improve", "decision", "context", "filter", "aggregate", "chat":
			return true
		case "transform":
			if n.Op == "ai" {
				return true
			}
		case "tool": // an AI may work out the tool's arguments
			return true
		}
	}
	return false
}

// FlowAIActions reports what a saved flow's AI steps may do unasked.
func (h *Hub) FlowAIActions(id string) (FlowAIActions, error) {
	root, err := h.need()
	if err != nil {
		return FlowAIActions{}, err
	}
	f, err := flow.Load(root, id)
	if err != nil {
		return FlowAIActions{}, err
	}
	aiApprovalsMu.Lock()
	allowed := append([]string{}, h.aiApprovals()[projectKey(root)][id]...)
	aiApprovalsMu.Unlock()
	return FlowAIActions{HasAI: flowHasAI(f), Allowed: allowed}, nil
}

// SetFlowAIActions sets what a saved flow's AI steps may do unasked (a
// person's choice in the app; agents cannot call it).
func (h *Hub) SetFlowAIActions(id string, kinds []string) (FlowAIActions, error) {
	root, err := h.need()
	if err != nil {
		return FlowAIActions{}, err
	}
	if _, err := flow.Load(root, id); err != nil {
		return FlowAIActions{}, err
	}
	var keep []string
	for _, k := range kinds {
		if !slices.Contains(aiKinds, k) {
			return FlowAIActions{}, errors.New("unknown kind of AI action: " + k)
		}
		if !slices.Contains(keep, k) {
			keep = append(keep, k)
		}
	}
	sort.Strings(keep)
	aiApprovalsMu.Lock()
	m := h.aiApprovals()
	pk := projectKey(root)
	if m[pk] == nil {
		m[pk] = map[string][]string{}
	}
	if len(keep) == 0 {
		delete(m[pk], id)
	} else {
		m[pk][id] = keep
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := h.aiApprovalsPath() + ".tmp"
	err = os.WriteFile(tmp, b, 0o600)
	if err == nil {
		err = os.Rename(tmp, h.aiApprovalsPath())
	}
	aiApprovalsMu.Unlock()
	if err != nil {
		return FlowAIActions{}, err
	}
	return h.FlowAIActions(id)
}

// callerRun: the flow run a bridge call came from (its scoped token), and
// that flow's id and project.
func (h *Hub) callerRun(ctx context.Context) (runID, flowID, root string) {
	runID = extension.ScopeOf(ctx)
	if runID == "" {
		return "", "", ""
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if fr := h.flowRuns[runID]; fr != nil && fr.flow != nil {
		return runID, fr.flow.ID, h.root
	}
	return runID, "", ""
}

// preApproved: the flow run this call came from allows this kind unasked.
func (h *Hub) preApproved(ctx context.Context, kind string) bool {
	_, flowID, root := h.callerRun(ctx)
	return h.flowAIAllowed(root, flowID, kind)
}

// askingAgent names who is asking, for the prompt and "Allow for this run".
func (h *Hub) askingAgent(ctx context.Context) string {
	if run, _, _ := h.callerRun(ctx); run != "" {
		return "flow:" + run
	}
	if a := h.activeRun(); a != nil {
		return a.Session.ID
	}
	if fr := h.onlyFlowRun(); fr != "" {
		return "flow:" + fr
	}
	return "agents"
}
