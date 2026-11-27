package flow

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// Every value reaches the command as one literal argument, wherever its
// placeholder sits (outside quotes, in '…', in "…").
func TestRenderShellQuotesValues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell")
	}
	hostile := []string{
		"plain",
		"x; touch /tmp/sw-pwned-1",
		"$(touch /tmp/sw-pwned-2)",
		"`touch /tmp/sw-pwned-3`",
		"a'b\"c",
		"' ; touch /tmp/sw-pwned-4 ; '",
		"\" ; touch /tmp/sw-pwned-5 ; \"",
		"$HOME ${PATH} \\ back\\slash",
		"line1\nline2 | cat",
		"",
	}
	for _, tpl := range []string{
		`printf '[%s]' {{inputs.v}}`,
		`printf '[%s]' '{{inputs.v}}'`,
		`printf '[%s]' "{{inputs.v}}"`,
		`printf '[%s]' pre{{inputs.v}}post`,
	} {
		for _, v := range hostile {
			cmd, err := RenderShell(tpl, Data{Inputs: map[string]string{"v": v}}, false)
			if err != nil {
				t.Fatalf("%q: %v", tpl, err)
			}
			out, err := exec.Command("/bin/sh", "-c", cmd).Output()
			if err != nil {
				t.Fatalf("%q with %q → %q: %v", tpl, v, cmd, err)
			}
			want := "[" + v + "]"
			if strings.Contains(tpl, "pre{{") {
				want = "[pre" + v + "post]"
			}
			if string(out) != want {
				t.Errorf("%q with %q: ran %q, printed %q, want %q", tpl, v, cmd, out, want)
			}
		}
	}
}

func TestRenderShellWindowsRefusesUnsafe(t *testing.T) {
	d := Data{Inputs: map[string]string{"ok": "report-2026.md", "bad": "x & del *"}}
	if got, err := RenderShell(`type {{inputs.ok}}`, d, true); err != nil || got != "type report-2026.md" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := RenderShell(`type {{inputs.bad}}`, d, true); err == nil || !strings.Contains(err.Error(), "&") {
		t.Fatalf("an unsafe value was taken: %v", err)
	}
}
