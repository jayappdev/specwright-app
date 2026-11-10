package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func box(t *testing.T) (*Box, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "my project (v2) & O'Brien")
	files := map[string]string{
		"src/main/java/com/acme/Order.java":             "package com.acme;\n\npublic final class Order {\n    // TODO validate lines\n    public Order place() { return this; }\n}\n",
		"src/main/java/com/acme/PlaceOrderService.java": "package com.acme;\n\nclass PlaceOrderService {\n    Order run(Order o) { return o.place(); }\n}\n",
		"web/app.ts":                 "export function placeOrder(id: string) {\n  return fetch(`/orders/${id}`)\n}\n",
		"README.md":                  "# Orders\n\nTODO: write docs\n",
		"node_modules/x/index.js":    "TODO should not be found\n",
		"generated/out.txt":          "TODO ignored by gitignore\n",
		".gitignore":                 "generated/\n*.log\n",
		"debug.log":                  "TODO in a log\n",
		"bin.dat":                    "\x00\x01TODO\x00",
		"docs/it's \"here\"/a b.txt": "hello TODO world\n",
	}
	for p, c := range files {
		abs := filepath.Join(root, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(abs), 0o755)
		_ = os.WriteFile(abs, []byte(c), 0o644)
	}
	b := New(Config{Trash: filepath.Join(t.TempDir(), "trash")})
	b.SetRoot(root)
	return b, root
}

func call(t *testing.T, b *Box, name string, a map[string]any) string {
	t.Helper()
	out, err := b.Call(context.Background(), name, a)
	if err != nil {
		t.Fatalf("%s(%v): %v", name, a, err)
	}
	return out
}

func callErr(t *testing.T, b *Box, name string, a map[string]any) error {
	t.Helper()
	_, err := b.Call(context.Background(), name, a)
	if err == nil {
		t.Fatalf("%s(%v) should fail", name, a)
	}
	return err
}

func TestReadFile(t *testing.T) {
	b, _ := box(t)
	out := call(t, b, "read_file", map[string]any{"path": "src/main/java/com/acme/Order.java", "start_line": 3.0, "end_line": 4.0})
	if !strings.Contains(out, "(lines 3-4 of 6)") || !strings.Contains(out, "3 | public final class Order {") || strings.Contains(out, "package") {
		t.Fatal(out)
	}
	if !strings.Contains(out, "start_line=5 to continue") {
		t.Fatalf("must say how to continue: %s", out)
	}
	out = call(t, b, "read_file", map[string]any{"paths": []any{"README.md", "web/app.ts", "bin.dat", "missing.txt"}})
	for _, want := range []string{"== README.md", "== web/app.ts", "binary file", "missing.txt: does not exist"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
	callErr(t, b, "read_file", map[string]any{"path": "../../etc/passwd"})
	callErr(t, b, "read_file", map[string]any{"path": "/etc/passwd"})
}

func TestSymlinkEscapeRefused(t *testing.T) {
	b, root := box(t)
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s3cret"), 0o644)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := callErr(t, b, "read_file", map[string]any{"path": "link/secret.txt"}); !strings.Contains(err.Error(), "outside") {
		t.Fatal(err)
	}
	callErr(t, b, "create_file", map[string]any{"path": "link/new.txt", "content": "x"})
}

func TestListAndFind(t *testing.T) {
	b, _ := box(t)
	out := call(t, b, "list_directory", map[string]any{"depth": 2.0})
	if !strings.Contains(out, "src/") || strings.Contains(out, "node_modules") || strings.Contains(out, "generated") || strings.Contains(out, "debug.log") {
		t.Fatal(out)
	}
	// depth counts levels below the listed directory: 3 → src/, src/main/, src/main/java/
	out = call(t, b, "list_directory", map[string]any{"depth": 3.0})
	if !strings.Contains(out, "      java/  (1 entry, not expanded") || strings.Contains(out, "        com/") {
		t.Fatalf("depth 3 must show exactly three levels:\n%s", out)
	}
	out = call(t, b, "file_search", map[string]any{"pattern": "**/*.java"})
	if !strings.Contains(out, "2 files") || !strings.Contains(out, "src/main/java/com/acme/Order.java") {
		t.Fatal(out)
	}
	out = call(t, b, "file_search", map[string]any{"pattern": "*.{ts,md}"})
	if !strings.Contains(out, "web/app.ts") || !strings.Contains(out, "README.md") {
		t.Fatal(out)
	}
	out = call(t, b, "file_search", map[string]any{"pattern": "a b.txt"})
	if !strings.Contains(out, `docs/it's "here"/a b.txt`) {
		t.Fatal(out)
	}
}

func TestTextSearch(t *testing.T) {
	b, _ := box(t)
	out := call(t, b, "text_search", map[string]any{"query": "todo"})
	for _, want := range []string{"Order.java", "README.md", "a b.txt", "4: "} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	for _, bad := range []string{"node_modules", "generated", "debug.log", "bin.dat"} {
		if strings.Contains(out, bad) {
			t.Fatalf("must skip %s: %s", bad, out)
		}
	}
	out = call(t, b, "text_search", map[string]any{"query": "TODO", "include": []any{"**/*.java"}, "context_lines": 1.0})
	if !strings.Contains(out, "1 match in 1 file") || !strings.Contains(out, "3- public final class Order {") {
		t.Fatal(out)
	}
	out = call(t, b, "text_search", map[string]any{"query": `place\w*\(`, "is_regex": true})
	if !strings.Contains(out, "app.ts") || !strings.Contains(out, "Order.java") {
		t.Fatal(out)
	}
	callErr(t, b, "text_search", map[string]any{"query": "(", "is_regex": true})
	// catastrophic-backtracking pattern is linear in RE2
	_ = os.WriteFile(filepath.Join(b.Root(), "big.txt"), []byte(strings.Repeat("a", 50000)+"!"), 0o644)
	call(t, b, "text_search", map[string]any{"query": "(a+)+$", "is_regex": true})
}

func TestFindUsages(t *testing.T) {
	b, _ := box(t)
	out := call(t, b, "find_usages", map[string]any{"symbol": "Order"})
	def := strings.Index(out, "Definitions")
	ref := strings.Index(out, "References")
	if def < 0 || ref < def || !strings.Contains(out[def:ref], "Order.java:3") || !strings.Contains(out[ref:], "PlaceOrderService.java:4") {
		t.Fatal(out)
	}
	callErr(t, b, "find_usages", map[string]any{"symbol": "a b"})
}

func TestEditFile(t *testing.T) {
	b, root := box(t)
	p := "src/main/java/com/acme/Order.java"
	out := call(t, b, "edit_file", map[string]any{"path": p, "old_text": "    // TODO validate lines\n", "new_text": "    // lines are validated in the constructor\n"})
	if !strings.Contains(out, "changed at line 4") || !strings.Contains(out, "+ ") {
		t.Fatal(out)
	}
	// atomic: second edit fails, first must not be written
	before, _ := os.ReadFile(filepath.Join(root, p))
	err := callErr(t, b, "edit_file", map[string]any{"path": p, "edits": []any{
		map[string]any{"old_text": "final class", "new_text": "class"},
		map[string]any{"old_text": "does not exist", "new_text": "x"},
	}})
	if !strings.Contains(err.Error(), "edit 2: old_text was not found") || !strings.Contains(err.Error(), "no changes were written") {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(root, p))
	if string(before) != string(after) {
		t.Fatal("failed edits must not write anything")
	}
	if err := callErr(t, b, "edit_file", map[string]any{"path": p, "old_text": "Order", "new_text": "Ord"}); !strings.Contains(err.Error(), "appears") {
		t.Fatal(err)
	}
	out = call(t, b, "edit_file", map[string]any{"path": p, "old_text": "Order", "new_text": "PurchaseOrder", "replace_all": true})
	if !strings.Contains(out, "replaced 2 occurrences") {
		t.Fatal(out)
	}
	out = call(t, b, "edit_file", map[string]any{"path": "README.md", "start_line": 3.0, "end_line": 3.0, "new_text": "Docs live in docs/."})
	if got, _ := os.ReadFile(filepath.Join(root, "README.md")); string(got) != "# Orders\n\nDocs live in docs/.\n" {
		t.Fatalf("%q %s", got, out)
	}
	// whitespace mismatch gets a helpful hint
	err = callErr(t, b, "edit_file", map[string]any{"path": p, "old_text": "public PurchaseOrder place() {  return this; }", "new_text": "x"})
	if !strings.Contains(err.Error(), "appears (ignoring indentation)") && !strings.Contains(err.Error(), "copy the text exactly") {
		t.Fatal(err)
	}
}

func TestCreateMoveDelete(t *testing.T) {
	b, root := box(t)
	call(t, b, "create_file", map[string]any{"path": "new/dir/Hello.java", "content": "class Hello {}\n"})
	callErr(t, b, "create_file", map[string]any{"path": "new/dir/Hello.java", "content": "x"})
	call(t, b, "create_file", map[string]any{"path": "new/dir/Hello.java", "content": "class Hello2 {}\n", "overwrite": true})
	call(t, b, "create_directory", map[string]any{"path": "empty dir"})
	call(t, b, "move_path", map[string]any{"from": "new/dir/Hello.java", "to": "empty dir"})
	if _, err := os.Stat(filepath.Join(root, "empty dir", "Hello.java")); err != nil {
		t.Fatal(err)
	}
	callErr(t, b, "delete_path", map[string]any{"path": "empty dir"}) // not empty
	out := call(t, b, "delete_path", map[string]any{"path": "empty dir", "recursive": true})
	if !strings.Contains(out, "trash") {
		t.Fatal(out)
	}
	if _, err := os.Stat(filepath.Join(root, "empty dir")); !os.IsNotExist(err) {
		t.Fatal("should be gone")
	}
	callErr(t, b, "delete_path", map[string]any{"path": "."})
}

func TestRunCommand(t *testing.T) {
	b, _ := box(t)
	out := call(t, b, "run_command", map[string]any{"command": "echo hi && echo oops 1>&2 && exit 3"})
	if !strings.Contains(out, "hi") || !strings.Contains(out, "oops") || !strings.Contains(out, "exit code 3") {
		t.Fatal(out)
	}
	out = call(t, b, "run_command", map[string]any{"command": "sleep 5", "timeout_seconds": 1.0})
	if !strings.Contains(out, "timed out") {
		t.Fatal(out)
	}
	out = call(t, b, "run_command", map[string]any{"command": "echo start; sleep 30", "background": true})
	if !strings.Contains(out, "cmd-1") || !strings.Contains(out, "start") {
		t.Fatal(out)
	}
	out = call(t, b, "get_command_output", map[string]any{"id": "cmd-1", "stop": true})
	if strings.Contains(out, "still running") {
		t.Fatal(out)
	}
	out = call(t, b, "run_command", map[string]any{"command": "pwd", "cwd": "src"})
	if !strings.Contains(out, filepath.Join("my project (v2) & O'Brien", "src")) {
		t.Fatal(out)
	}
}

func TestTodosAndHTML(t *testing.T) {
	b, _ := box(t)
	call(t, b, "todo_list", map[string]any{"items": []any{map[string]any{"title": "read spec", "status": "done"}, "write tests"}})
	out := call(t, b, "todo_list", nil)
	if !strings.Contains(out, "1 of 2 done") || !strings.Contains(out, "[x] read spec") || !strings.Contains(out, "[ ] write tests") {
		t.Fatal(out)
	}
	title, text := htmlToText("<html><head><title>Docs &amp; API</title><style>x{}</style></head><body><h1>Hello</h1><script>alert(1)</script><p>World &lt;3</p></body></html>")
	if title != "Docs & API" || !strings.Contains(text, "Hello") || !strings.Contains(text, "World <3") || strings.Contains(text, "alert") {
		t.Fatalf("%q %q", title, text)
	}
}

func TestEveryToolHasSchema(t *testing.T) {
	seen := map[string]bool{}
	for _, tl := range Tools() {
		if seen[tl.Name] || tl.run == nil || tl.Desc == "" || tl.Schema["type"] != "object" {
			t.Fatalf("bad tool %+v", tl.Name)
		}
		seen[tl.Name] = true
	}
	if len(seen) < 15 {
		t.Fatal(fmt.Sprint(len(seen)))
	}
}

func BenchmarkTextSearch(b *testing.B) {
	root := b.TempDir()
	for i := range 2000 {
		_ = os.MkdirAll(filepath.Join(root, fmt.Sprintf("pkg%d", i%40)), 0o755)
		_ = os.WriteFile(filepath.Join(root, fmt.Sprintf("pkg%d", i%40), fmt.Sprintf("f%d.go", i)), []byte(strings.Repeat("func helper() { return }\n", 200)+"// NEEDLE here\n"), 0o644)
	}
	bx := New(Config{})
	bx.SetRoot(root)
	b.ResetTimer()
	for range b.N {
		if _, err := bx.Call(context.Background(), "text_search", map[string]any{"query": "NEEDLE", "max_results": 5000.0}); err != nil {
			b.Fatal(err)
		}
	}
}
