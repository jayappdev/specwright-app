package aishim_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codegen/internal/aishim"
	"codegen/internal/extension"
)

// The test binary doubles as the shim: the launchers start it as
// "<test binary> aishim <cli> …".
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "aishim" {
		os.Exit(aishim.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

const fakeClaude = `#!/bin/sh
case " $* " in
  *" --output-format json "*) echo '{"type":"result","is_error":false,"result":"claude says hi","total_cost_usd":0.0123,"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":50,"cache_creation_input_tokens":0},"modelUsage":{"claude-sonnet-5-5":{}}}' ;;
  *) echo "plain mode: usage not collected" ;;
esac
`

const fakeCopilot = `#!/bin/sh
if [ "$1" = "--help" ]; then echo "  --usage-output-file <path>  write usage"; exit 0; fi
uf=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--usage-output-file" ]; then uf="$2"; shift; fi
  shift
done
[ -n "$uf" ] && echo '{"totalPremiumRequestCost":1,"totalNanoAiu":2000000000,"modelMetrics":{"gpt-5":{"usage":{"inputTokens":300,"outputTokens":40,"cacheReadTokens":0}}}}' > "$uf"
echo "copilot says hi"
echo "Total usage est: 1 premium request" >&2
`

func TestToolAIUsageIsCounted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fake, shim, root := t.TempDir(), t.TempDir(), t.TempDir()
	_ = os.WriteFile(filepath.Join(fake, "claude"), []byte(fakeClaude), 0o755)
	_ = os.WriteFile(filepath.Join(fake, "copilot"), []byte(fakeCopilot), 0o755)
	if err := aishim.Install(shim, exe); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	_ = os.WriteFile(filepath.Join(root, "tool.sh"), []byte("claude -p 'draw it'\ncopilot -p 'draw it' -s\nclaude --version\n"), 0o755)

	e := extension.Extension{Name: "uses-ai", Command: "sh tool.sh"}
	res := extension.ExecInput(context.Background(), root, e, nil, nil, []string{aishim.EnvDir + "=" + shim}, 30*time.Second, nil)
	if res.ExitCode != 0 || res.Error != "" {
		t.Fatalf("%+v", res)
	}
	// the tool sees exactly what the real CLIs print
	if !strings.HasPrefix(res.Output, "claude says hi\ncopilot says hi") || strings.Contains(res.Output, "total_cost_usd") {
		t.Fatalf("output: %q", res.Output)
	}
	if len(res.AI) != 2 {
		t.Fatalf("records: %+v", res.AI)
	}
	c, p := res.AI[0], res.AI[1]
	if c.CLI != "claude" || c.InputTokens != 150 || c.OutputTokens != 20 || c.CachedTokens != 50 || c.CostUSD != 0.0123 || c.Model != "claude-sonnet-5-5" || !c.OK {
		t.Fatalf("claude: %+v", c)
	}
	if p.CLI != "copilot" || p.PremiumRequests != 1 || p.InputTokens != 300 || p.OutputTokens != 40 || p.AIUnits != 2 || p.Model != "gpt-5" {
		t.Fatalf("copilot: %+v", p)
	}

	// without the launchers nothing changes and nothing is counted
	res = extension.ExecInput(context.Background(), root, e, nil, nil, nil, 30*time.Second, nil)
	if len(res.AI) != 0 || !strings.Contains(res.Output, "plain mode") {
		t.Fatalf("%+v", res)
	}
}
