package agent

// Copilot hosted through the Copilot SDK (interactive runs).
//
// `copilot -p` runs with nobody at the keyboard: a tool call that needs
// approval (a shell command, an edit outside the project) is denied unless
// SpecWright's permissionRequest hook answers, and where managed settings
// block plugin hooks, or require a person's answer, the CLI answers
// "user not available" itself.
//
// For a run a person is watching (the Playground), SpecWright instead hosts
// the session: it starts the user's own Copilot CLI as an SDK runtime and
// owns the session, so every permission question and every question to
// the user comes to SpecWright over the session connection (no MCP, no
// hooks). The app asks the person; their answer is attributed to them
// (human_response from the owning connection), which managed policy
// accepts. When the runtime cannot be started (an older CLI), the run falls
// back to -p and says why.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	copilot "github.com/microsoft/copilot-sdk/go/v2"
	"github.com/microsoft/copilot-sdk/go/v2/rpc"
)

// errSDKUnavailable: the SDK runtime could not be started; use -p.
var errSDKUnavailable = errors.New("the Copilot SDK runtime is not available")

// useSDK says whether this run is hosted through the SDK.
func useSDK(req Request) bool {
	return req.Interactive && req.AskPermission != nil && os.Getenv("SPECWRIGHT_COPILOT_SDK") != "0"
}

// sdkTool turns an SDK permission request into the tool name and input the
// app's permission dialog shows (the same shape the -p hook sends).
func sdkTool(r rpc.PermissionRequest) (string, map[string]any) {
	in := map[string]any{"permission": string(r.Kind())}
	switch x := deref(r).(type) {
	case rpc.PermissionRequestShell:
		in["command"] = x.FullCommandText
		if x.Intention != "" {
			in["description"] = x.Intention
		}
		if x.Warning != nil && *x.Warning != "" {
			in["warning"] = *x.Warning
		}
		return "shell", in
	case rpc.PermissionRequestWrite:
		in["path"] = x.FileName
		if x.Intention != "" {
			in["description"] = x.Intention
		}
		if x.Diff != "" {
			in["diff"] = x.Diff
		}
		return "write", in
	case rpc.PermissionRequestRead:
		in["path"] = x.Path
		if x.Intention != "" {
			in["description"] = x.Intention
		}
		return "read", in
	case rpc.PermissionRequestURL:
		in["url"] = x.URL
		if x.Intention != "" {
			in["description"] = x.Intention
		}
		return "url", in
	case rpc.PermissionRequestMCP:
		if m, ok := x.Args.(map[string]any); ok {
			for k, v := range m {
				in[k] = v
			}
		}
		return x.ServerName + "/" + x.ToolName, in
	case rpc.PermissionRequestCustomTool:
		if m, ok := x.Args.(map[string]any); ok {
			for k, v := range m {
				in[k] = v
			}
		}
		if x.ToolDescription != "" {
			in["description"] = x.ToolDescription
		}
		return x.ToolName, in
	}
	// anything else: what the request says about itself
	if b, err := json.Marshal(r); err == nil {
		var m map[string]any
		if json.Unmarshal(b, &m) == nil {
			for k, v := range m {
				in[k] = v
			}
		}
	}
	return string(r.Kind()), in
}

// deref: the request variants arrive as values or pointers.
func deref(r rpc.PermissionRequest) any {
	switch x := r.(type) {
	case *rpc.PermissionRequestShell:
		return *x
	case *rpc.PermissionRequestWrite:
		return *x
	case *rpc.PermissionRequestRead:
		return *x
	case *rpc.PermissionRequestURL:
		return *x
	case *rpc.PermissionRequestMCP:
		return *x
	case *rpc.PermissionRequestCustomTool:
		return *x
	}
	return r
}

// person: the context of a decision a person made in the app.
func person() *rpc.PermissionDecisionContext {
	interactive := copilot.PermissionResponseCapabilityInteractive
	return &rpc.PermissionDecisionContext{Source: copilot.PermissionDecisionSourceHumanResponse, ResponseCapability: &interactive,
		Surface: copilot.PermissionDecisionSurfaceSDK, Outcome: copilot.PermissionDecisionOutcomePromptedUser}
}

// sdkPermission answers Copilot's permission questions: SpecWright's own
// tools and edits inside the run's folders are allowed (unless managed
// policy wants a person); everything else is asked in the app.
func sdkPermission(ctx context.Context, req Request, agentID string, emit func(Event)) copilot.PermissionHandlerFunc {
	return func(r copilot.PermissionRequest, _ copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
		tool, input := sdkTool(r)
		if !r.RequiresManagedApproval() && req.AutoAllow != nil && req.AutoAllow(tool, input) {
			return &rpc.PermissionDecisionApproveOnce{}, nil
		}
		allow, msg := req.AskPermission(ctx, agentID, tool, input)
		if allow {
			yes := true
			return copilot.NewAttributedPermissionResult(&rpc.PermissionDecisionApproveOnce{ApprovedInteractively: &yes}, person()), nil
		}
		emit(Event{Type: "agent.log", Summary: "Denied: " + tool, Data: map[string]any{"line": msg}})
		return copilot.NewAttributedPermissionResult(&rpc.PermissionDecisionReject{Feedback: &msg}, person()), nil
	}
}

// sdkUserInput puts Copilot's questions to the person in the app.
func sdkUserInput(ctx context.Context, req Request) copilot.AttributedUserInputHandler {
	return func(q copilot.UserInputRequest, _ copilot.UserInputInvocation) (copilot.AttributedUserInputResponse, error) {
		if req.AskUser == nil {
			return copilot.AttributedUserInputResponse{}, errors.New("nobody can answer questions in this run")
		}
		free := q.AllowFreeform == nil || *q.AllowFreeform
		answer, freeform, err := req.AskUser(ctx, q.Question, q.Choices, free)
		if err != nil {
			return copilot.AttributedUserInputResponse{}, err
		}
		return copilot.AttributedUserInputResponse{UserInputResponse: copilot.UserInputResponse{Answer: answer, WasFreeform: freeform},
			DecisionContext: person()}, nil
	}
}

// mcpServers reads the run's MCP config (SpecWright's agentbridge) for the
// session; where MCP is not allowed, the CLI ignores them.
func mcpServers(path string) map[string]copilot.MCPServerConfig {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(b, &cfg) != nil || len(cfg.MCPServers) == 0 {
		return nil
	}
	out := map[string]copilot.MCPServerConfig{}
	for name, s := range cfg.MCPServers {
		out[name] = copilot.MCPStdioServerConfig{Command: s.Command, Args: s.Args, Env: s.Env, Tools: []string{"*"}}
	}
	return out
}

// runSDK runs req as an SDK-hosted session. errSDKUnavailable means the
// runtime could not be started and nothing ran.
func (c *Copilot) runSDK(ctx context.Context, bin string, req Request, emit func(Event)) (res Result, err error) {
	// the SDK is young: a panic in it ends this attempt, not the app
	started := false
	defer func() {
		if r := recover(); r != nil {
			if started {
				err = fmt.Errorf("the Copilot SDK failed: %v", r)
			} else {
				err = fmt.Errorf("%w: %v", errSDKUnavailable, r)
			}
		}
	}()
	base, dropped := baseEnv()
	noteDropped(emit, dropped)
	env := append(append([]string{}, base...), c.env(req)...)
	client := copilot.NewClient(&copilot.ClientOptions{Mode: copilot.ModeCodingAgent, Runtime: copilot.SpawnedStdioConnection{Path: bin, Env: env, WorkingDirectory: req.Dir}})
	if err := client.Start(ctx); err != nil {
		return Result{}, fmt.Errorf("%w: %v", errSDKUnavailable, err)
	}
	defer func() { _ = client.Stop() }()
	agentID := fmt.Sprintf("copilot-sdk-%d", time.Now().UnixNano())
	yes := true
	p := &copilotJSON{emit: emit, names: map[string]string{}, started: map[string]time.Time{}, model: modelOr(req.Model, "copilot/default")}
	opts := &copilot.CreateSessionOptions{ClientName: "SpecWright", Model: req.Model, WorkingDirectory: req.Dir,
		Filesystem:  copilot.CreateSessionFilesystemOptions{AdditionalDirectories: req.AddDirs},
		Permissions: copilot.SessionPermissionOptions{OnRequest: sdkPermission(ctx, req, agentID, emit)},
		Interaction: copilot.SessionInteractionOptions{OnAttributedUserInputRequest: sdkUserInput(ctx, req)},
		MCP:         copilot.SessionMCPOptions{Servers: mcpServers(req.MCPConfig)},
		// Copilot extensions: the project's, and SpecWright's own (its tools
		// where MCP is not allowed), as -p loads them
		Extensions: copilot.SessionExtensionOptions{Enabled: &yes},
	}
	if hasSelfExt() {
		opts.Experiments.EnableExperimentalMode = &yes
	}
	session, err := client.CreateSession(ctx, opts)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", errSDKUnavailable, err)
	}
	defer func() { _ = session.Disconnect() }()
	started = true
	emit(Event{Type: "agent.log", Summary: "Copilot is hosted by SpecWright: its questions come to you in the app", Data: map[string]any{"line": "copilot sdk session"}})
	emit(Event{Type: "model.request", Summary: "Request → Copilot", Data: map[string]any{"input_tokens": 0, "model": p.model}})
	// the session's events are the ones -p prints with --output-format json
	off := session.On(func(ev copilot.SessionEvent) {
		if b, err := json.Marshal(ev); err == nil {
			p.line(string(b))
		}
	})
	defer off()
	_, sendErr := session.SendAndWait(ctx, copilot.MessageOptions{Prompt: req.Prompt})
	if m, merr := session.RPC.Usage.GetMetrics(context.WithoutCancel(ctx)); merr == nil && m != nil {
		if f, ferr := os.CreateTemp("", "specwright-copilot-usage-*.json"); ferr == nil {
			b, _ := json.Marshal(m)
			_, _ = f.Write(b)
			f.Close()
			p.usage(f.Name())
			os.Remove(f.Name())
		}
	}
	if sendErr != nil && ctx.Err() == nil && strings.TrimSpace(p.res.Text) == "" {
		return p.res, sendErr
	}
	return p.res, ctx.Err()
}
