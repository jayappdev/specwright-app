package extension

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const greeterExt = `import { joinSession } from "@github/copilot-sdk/extension";
console.log("this must not reach the result");
await joinSession({
  tools: [
    {
      name: "greet",
      description: "Say hello to someone.",
      parameters: { type: "object", properties: { who: { type: "string", description: "Who to greet" }, loud: { type: "boolean" } }, required: ["who"] },
      handler: async ({ who, loud }) => { const s = "Hello, " + who + " from " + process.cwd().split("/").pop(); return loud ? s.toUpperCase() : s; },
    },
    {
      name: "fail_politely",
      description: "Always fails.",
      parameters: { type: "object", properties: {} },
      handler: async () => ({ textResultForLlm: "nope", resultType: "failure" }),
    },
  ],
  hooks: { onPreToolUse: async () => {} },
});
`

func TestCopilotExtensionToolsRunAsProjectTools(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "extensions", "greeter")
	write(t, filepath.Join(dir, "extension.mjs"), greeterExt)
	s := NewStore(root)

	// cloned with the project, never approved here: listed, not runnable
	l, err := s.List()
	if err != nil || len(l) != 2 || l[0].Kind != KindCopilot || l[0].Status != "needs approval" || l[0].Runnable() == nil {
		t.Fatalf("before approval: %+v %v", l, err)
	}

	x, _ := readCopilotExt(dir, "project")
	defs, hooks, err := DiscoverCopilotTools(context.Background(), root, x, nil)
	if err != nil || len(defs) != 2 || len(hooks) != 1 || hooks[0] != "onPreToolUse" {
		t.Fatalf("discover: %+v %v %v", defs, hooks, err)
	}
	tools := CopilotToolExtensions(root, x, defs, hooks, map[string]bool{"greet": true}) // "greet" is taken
	if tools[0].Name != "greeter-greet" || tools[0].Tool != "greet" || len(tools[0].Params) != 2 || tools[0].Params[0].Name != "who" || !tools[0].Params[0].Required {
		t.Fatalf("tools: %+v", tools[0])
	}
	if err := s.PutCopilotTools(".github/extensions/greeter", tools); err != nil {
		t.Fatal(err)
	}
	e, err := s.Get("greeter-greet")
	if err != nil || e.Runnable() != nil {
		t.Fatalf("approved: %+v %v", e, err)
	}

	res := Exec(context.Background(), root, e, map[string]any{"who": "Ada", "loud": true}, nil, nil, 20*time.Second)
	if !res.OK || res.Output != "HELLO, ADA FROM "+strings.ToUpper(filepath.Base(root)) {
		t.Fatalf("call: %+v", res)
	}
	if r := Exec(context.Background(), root, e, map[string]any{}, nil, nil, 20*time.Second); r.OK || !strings.Contains(r.Error, "who") {
		t.Fatalf("missing required argument must fail: %+v", r)
	}
	f, _ := s.Get("fail_politely")
	if r := Exec(context.Background(), root, f, nil, nil, nil, 20*time.Second); r.OK || !strings.HasPrefix(r.Output, "nope") {
		t.Fatalf("failure result: %+v", r)
	}

	// changing the extension withdraws the approval
	write(t, filepath.Join(dir, "extension.mjs"), greeterExt+"// changed\n")
	e, _ = s.Get("greeter-greet")
	if e.Status != "needs approval" || e.Runnable() == nil {
		t.Fatalf("after change: %+v", e)
	}
	// and removing it drops its tools
	if err := s.PutCopilotTools(".github/extensions/greeter", nil); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.List(); len(l) != 2 || l[0].Approved {
		t.Fatalf("after removal from extensions.json it is listed from source again: %+v", l)
	}
}
