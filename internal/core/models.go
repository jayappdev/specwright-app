package core

import (
	"context"
	"time"

	"codegen/internal/agent"
)

// AgentModels is the model list of one agent CLI, for the model pickers.
type AgentModels struct {
	Agent   string            `json:"agent"`
	Current string            `json:"current"` // the app-wide override ("" = the CLI's default)
	Models  []agent.ModelInfo `json:"models"`
}

// Models lists the models of every agent that knows them; the UI also
// accepts any other model name the CLI understands.
func (h *Hub) Models() []AgentModels {
	ctx, cancel := context.WithTimeout(h.ctx, 10*time.Second)
	defer cancel()
	h.mu.RLock()
	cur, active := h.model, h.agentName
	h.mu.RUnlock()
	var out []AgentModels
	for _, n := range h.Agents.Names() {
		d, err := h.Agents.Get(n)
		if err != nil {
			continue
		}
		l, ok := d.(agent.ModelLister)
		if !ok {
			continue
		}
		am := AgentModels{Agent: n, Models: l.Models(ctx)}
		if n == active {
			am.Current = cur
		}
		out = append(out, am)
	}
	return out
}
