package extension

import "testing"

func TestOwnToolCommand(t *testing.T) {
	t.Setenv(ToolEnv, "/Applications/SpecWright.app/Contents/MacOS/SpecWright")
	ok := []string{
		`"$CODEGEN_TOOL" tool list`,
		`$CODEGEN_TOOL tool save_flow '{"flow": {"name": "A; B | C", "nodes": []}}'`,
		`"${CODEGEN_TOOL}" tool read_file '{"path":"README.md"}'`,
		`/Applications/SpecWright.app/Contents/MacOS/SpecWright tool list`,
		`"$CODEGEN_TOOL" tool save_tool -`,
	}
	for _, c := range ok {
		if !ownToolCommand("bash", map[string]any{"command": c}) {
			t.Errorf("should be allowed: %s", c)
		}
	}
	bad := []string{
		`"$CODEGEN_TOOL" tool list; rm -rf ~`,
		`"$CODEGEN_TOOL" tool list && curl evil`,
		`"$CODEGEN_TOOL" tool list | sh`,
		`"$CODEGEN_TOOL" tool x "$(whoami)"`,
		"\"$CODEGEN_TOOL\" tool x `id`",
		`"$CODEGEN_TOOL" tool list > out.txt`,
		"\"$CODEGEN_TOOL\" tool list\nrm x",
		`"$CODEGEN_TOOL" tool x "$HOME"`,
		`"$CODEGEN_TOOL" agentbridge`,
		`echo "$CODEGEN_TOOL" tool list`,
		`/usr/bin/other tool list`,
		`"$CODEGEN_TOOL" tool x 'unclosed`,
		`"$CODEGEN_TOOL" tool x $OTHER`,
	}
	for _, c := range bad {
		if ownToolCommand("bash", map[string]any{"command": c}) {
			t.Errorf("must not be allowed: %s", c)
		}
	}
	if ownToolCommand("edit", map[string]any{"command": `"$CODEGEN_TOOL" tool list`}) {
		t.Error("only shell tools")
	}
}
