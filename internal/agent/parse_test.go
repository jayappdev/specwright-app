package agent

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestClaudeParser(t *testing.T) {
	var evs []Event
	p := newClaudeParser(func(e Event) { evs = append(evs, e) })
	for _, l := range []string{
		`{"type":"system","subtype":"init","model":"claude-sonnet-5-5"}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Reading the spec."}],"usage":{"input_tokens":10,"cache_read_input_tokens":2000,"output_tokens":40}}}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"spec.md"}}],"usage":{"input_tokens":10,"cache_read_input_tokens":2000,"output_tokens":40}}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"# Spec"}]}]}}`,
		`{"type":"result","subtype":"success","result":"done","total_cost_usd":0.0123,"num_turns":2}`,
	} {
		p.line(l)
	}
	count := map[string]int{}
	for _, e := range evs {
		count[e.Type]++
	}
	if count["model.request"] != 1 || count["model.response"] != 1 {
		t.Fatalf("usage must be counted once per message id: %v", count)
	}
	if count["tool.call"] != 1 || count["tool.result"] != 1 || count["model.text"] != 1 || count["run.usage"] != 1 {
		t.Fatalf("counts %v", count)
	}
	if evs[0].Data["input_tokens"] != 2010 || p.result().CostUSD != 0.0123 || p.result().Model != "claude-sonnet-5-5" {
		t.Fatalf("bad usage %v %+v", evs[0].Data, p.result())
	}
}

func TestCopilotParser(t *testing.T) {
	var evs []Event
	p := &copilotParser{emit: func(e Event) { evs = append(evs, e) }}
	for _, l := range []string{
		"I'll read the spec first.",
		"",
		"✓ Read specs/001/spec.md",
		"✗ shell npm test",
		"Done: wrote the plan.",
		"Total usage est:       2 Premium requests",
		"Usage by model:",
		"    claude-sonnet-4.5    12.3k input, 456 output, 10.1k cache read",
	} {
		p.line(l)
	}
	res := p.finish()
	if res.PremiumRequests != 2 || res.Model != "claude-sonnet-4.5" || p.in != 12300 || p.cached != 10100 {
		t.Fatalf("%+v in=%d cached=%d", res, p.in, p.cached)
	}
	fails := 0
	for _, e := range evs {
		if e.Type == "tool.result" && e.Data["ok"] == false {
			fails++
		}
	}
	if fails != 1 {
		t.Fatalf("want one failed tool, events %v", evs)
	}
}

// Event shapes captured from GitHub Copilot CLI 1.0.107 (--output-format json).
func TestCopilotJSONParser(t *testing.T) {
	var evs []Event
	p := &copilotJSON{emit: func(e Event) { evs = append(evs, e) }, names: map[string]string{}, started: map[string]time.Time{}}
	for _, l := range []string{
		`{"type":"model.call_start","data":{"turnId":"0","model":"claude-sonnet-5.5"},"ephemeral":true}`,
		`{"type":"assistant.message","data":{"content":"","model":"claude-sonnet-5.5","toolRequests":[{"toolCallId":"t1","name":"agentbridge-read_file","arguments":{"path":"src/app/Main.java"},"type":"function"}]}}`,
		`{"type":"tool.execution_start","data":{"toolCallId":"t1","toolName":"agentbridge-read_file"}}`,
		`{"type":"tool.execution_complete","data":{"toolCallId":"t1","success":true,"result":{"content":"== src/app/Main.java (lines 1-6 of 6)"}}}`,
		`{"type":"assistant.message","data":{"content":"done","toolRequests":[]}}`,
		`{"type":"result","exitCode":0,"usage":{"premiumRequests":1}}`,
	} {
		p.line(l)
	}
	dir := t.TempDir()
	uf := dir + "/u.json"
	_ = os.WriteFile(uf, []byte(`{"totalPremiumRequestCost":1,"totalNanoAiu":7132170000,"modelMetrics":{"claude-sonnet-5.5":{"requests":{"count":2},"usage":{"inputTokens":54155,"outputTokens":79,"cacheReadTokens":27022}}}}`), 0o644)
	p.usage(uf)
	byType := map[string][]Event{}
	for _, e := range evs {
		byType[e.Type] = append(byType[e.Type], e)
	}
	if c := byType["tool.call"]; len(c) != 1 || c[0].Data["tool"] != "mcp__agentbridge__read_file" {
		t.Fatalf("tool call: %+v", c)
	}
	if r := byType["tool.result"]; len(r) != 1 || r[0].Data["ok"] != true || !strings.Contains(r[0].Data["output"].(string), "Main.java") {
		t.Fatalf("tool result: %+v", r)
	}
	if p.res.Text != "done" || p.res.PremiumRequests != 1 || p.res.Model != "claude-sonnet-5.5" {
		t.Fatalf("%+v", p.res)
	}
	u := byType["run.usage"][0].Data
	if u["input_tokens"] != 54155 || u["output_tokens"] != 79 || u["cached_tokens"] != 27022 || u["premium_requests"] != 1 {
		t.Fatalf("usage %+v", u)
	}
}
