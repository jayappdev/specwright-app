package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	copilot "github.com/microsoft/copilot-sdk/go/v2"
	"github.com/microsoft/copilot-sdk/go/v2/rpc"
)

func TestSDKToolShapes(t *testing.T) {
	w := "risky"
	tool, in := sdkTool(rpc.PermissionRequestShell{FullCommandText: "npm test", Intention: "run the tests", Warning: &w})
	if tool != "shell" || in["command"] != "npm test" || in["description"] != "run the tests" || in["warning"] != "risky" {
		t.Fatalf("%s %v", tool, in)
	}
	tool, in = sdkTool(&rpc.PermissionRequestWrite{FileName: "/p/a.go", Diff: "+x"})
	if tool != "write" || in["path"] != "/p/a.go" || in["diff"] != "+x" {
		t.Fatalf("%s %v", tool, in)
	}
	tool, in = sdkTool(rpc.PermissionRequestMCP{ServerName: "agentbridge", ToolName: "read_file", Args: map[string]any{"path": "x"}})
	if tool != "agentbridge/read_file" || in["path"] != "x" {
		t.Fatalf("%s %v", tool, in)
	}
	tool, _ = sdkTool(rpc.PermissionRequestCustomTool{ToolName: "specwright_save_flow"})
	if tool != "specwright_save_flow" {
		t.Fatal(tool)
	}
}

func TestSDKPermissionAsksAndAttributes(t *testing.T) {
	var asked []string
	req := Request{
		AskPermission: func(_ context.Context, agentID, tool string, in map[string]any) (bool, string) {
			asked = append(asked, tool)
			return in["command"] != "rm -rf /", "The user denied permission to use shell."
		},
		AutoAllow: func(tool string, _ map[string]any) bool { return tool == "agentbridge/read_file" },
	}
	h := sdkPermission(context.Background(), req, "copilot-sdk-1", func(Event) {})
	// SpecWright's own tool: allowed without a question
	d, _ := h(rpc.PermissionRequestMCP{ServerName: "agentbridge", ToolName: "read_file"}, copilot.PermissionInvocation{})
	if _, ok := d.(*rpc.PermissionDecisionApproveOnce); !ok || len(asked) != 0 {
		t.Fatalf("%T %v", d, asked)
	}
	// managed policy wants a person: asked even for an auto-allowed tool
	yes := true
	d, _ = h(rpc.PermissionRequestMCP{ServerName: "agentbridge", ToolName: "read_file", ManagedApprovalRequired: &yes}, copilot.PermissionInvocation{})
	a, ok := d.(*copilot.AttributedPermissionResult)
	if !ok || len(asked) != 1 || a.DecisionContext.Source != copilot.PermissionDecisionSourceHumanResponse {
		t.Fatalf("%T %+v %v", d, d, asked)
	}
	// a command: asked; the person's yes is recorded as theirs
	d, _ = h(rpc.PermissionRequestShell{FullCommandText: "npm test"}, copilot.PermissionInvocation{})
	a, ok = d.(*copilot.AttributedPermissionResult)
	if !ok || a.DecisionContext.Outcome != copilot.PermissionDecisionOutcomePromptedUser {
		t.Fatalf("%T", d)
	}
	if once, ok := a.PermissionDecision.(*rpc.PermissionDecisionApproveOnce); !ok || once.ApprovedInteractively == nil || !*once.ApprovedInteractively {
		t.Fatalf("%T", a.PermissionDecision)
	}
	// and their no, with the reason for the model
	d, _ = h(rpc.PermissionRequestShell{FullCommandText: "rm -rf /"}, copilot.PermissionInvocation{})
	a, _ = d.(*copilot.AttributedPermissionResult)
	if rej, ok := a.PermissionDecision.(*rpc.PermissionDecisionReject); !ok || rej.Feedback == nil || *rej.Feedback == "" {
		t.Fatalf("%T", a.PermissionDecision)
	}
}

func TestSDKUserInputAndMCP(t *testing.T) {
	req := Request{AskUser: func(_ context.Context, q string, choices []string, free bool) (string, bool, error) {
		return "staging", false, nil
	}}
	r, err := sdkUserInput(context.Background(), req)(copilot.UserInputRequest{Question: "Where?", Choices: []string{"staging", "prod"}}, copilot.UserInputInvocation{})
	if err != nil || r.Answer != "staging" || r.DecisionContext == nil {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := sdkUserInput(context.Background(), Request{})(copilot.UserInputRequest{Question: "?"}, copilot.UserInputInvocation{}); err == nil {
		t.Fatal("nobody to ask: an error, not a made-up answer")
	}
	p := filepath.Join(t.TempDir(), "mcp.json")
	_ = os.WriteFile(p, []byte(`{"mcpServers":{"agentbridge":{"command":"/app","args":["mcp"],"env":{"CODEGEN_BRIDGE_URL":"http://127.0.0.1:1"}}}}`), 0o600)
	m := mcpServers(p)
	if s, ok := m["agentbridge"].(copilot.MCPStdioServerConfig); !ok || s.Command != "/app" || s.Env["CODEGEN_BRIDGE_URL"] == "" {
		t.Fatalf("%+v", m)
	}
}

// a runtime that cannot start: nothing ran, and the run goes on as copilot -p
func TestSDKUnavailableFallsBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := NewCopilot().runSDK(ctx, filepath.Join(t.TempDir(), "no-copilot"), Request{Dir: t.TempDir(), Prompt: "x", Interactive: true}, func(Event) {})
	if !errors.Is(err, errSDKUnavailable) {
		t.Fatalf("%v", err)
	}
	if useSDK(Request{Interactive: true}) || !useSDK(Request{Interactive: true, AskPermission: func(context.Context, string, string, map[string]any) (bool, string) { return false, "" }}) {
		t.Fatal("only interactive runs that can ask use the SDK")
	}
}

// with a real Copilot CLI (no prompt is sent: no premium requests)
func TestSDKHandshakeLive(t *testing.T) {
	if os.Getenv("SPECWRIGHT_COPILOT_LIVE") == "" {
		t.Skip("set SPECWRIGHT_COPILOT_LIVE=1 to start the installed Copilot CLI through the SDK")
	}
	bin, err := NewCopilot().bin()
	if err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	base, _ := baseEnv()
	client := copilot.NewClient(&copilot.ClientOptions{Mode: copilot.ModeCodingAgent, Runtime: copilot.SpawnedStdioConnection{Path: bin, Env: base, WorkingDirectory: t.TempDir()}})
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	s, err := client.CreateSession(ctx, &copilot.CreateSessionOptions{ClientName: "SpecWright", WorkingDirectory: t.TempDir(),
		Permissions: copilot.SessionPermissionOptions{OnRequest: sdkPermission(ctx, Request{AskPermission: func(context.Context, string, string, map[string]any) (bool, string) { return false, "no" }}, "t", func(Event) {})}})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Disconnect()
}
