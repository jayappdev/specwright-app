package builtin

import "codegen/internal/flow"

// registry lists every built-in tool. Descriptions are written for the
// model: what the tool is for, when to prefer it, and what it returns.
var registry = []Tool{
	// --- read -------------------------------------------------------------
	{Name: "read_file", Title: "Read file", Group: "read", ReadOnly: true, run: readFile,
		Desc: "Read one or more text files from the workspace with line numbers (`  12 | code`). Use start_line/end_line for large files; the result says how to continue when it is cut off. Prefer this over shell `cat`.",
		Schema: obj(map[string]any{
			"path":       p("string", "File path relative to the workspace root"),
			"paths":      arr(p("string", "file path"), "Several files to read in one call (whole files)"),
			"start_line": p("integer", "First line to read (1-based, default 1)"),
			"end_line":   p("integer", "Last line to read (inclusive, default end of file; at most 2000 lines per call)"),
		})},
	{Name: "list_directory", Title: "List directory", Group: "read", ReadOnly: true, run: listDirectory,
		Desc: "List a directory of the workspace (directories first, with sizes and entry counts). depth > 1 shows a tree. Skips .git, node_modules, build output and .gitignore'd paths unless include_ignored is true.",
		Schema: obj(map[string]any{
			"path":            p("string", "Directory relative to the workspace root (default: the root)"),
			"depth":           p("integer", "Levels below the directory to show, 1-6 (default 1); folders at the last level are marked \"not expanded\""),
			"include_ignored": p("boolean", "Also show ignored and dependency folders"),
		})},
	// --- search -----------------------------------------------------------
	{Name: "file_search", Title: "Find files", Group: "search", ReadOnly: true, run: fileSearch,
		Desc: "Find files by glob pattern, e.g. `**/*.java`, `src/**/Order*.{ts,tsx}`, `pom.xml`. A pattern without `/` matches file names anywhere. Returns workspace-relative paths.",
		Schema: obj(map[string]any{
			"pattern":         p("string", "Glob: ** any folders, * within a name, ? one character, {a,b} alternatives"),
			"path":            p("string", "Only search under this directory"),
			"max_results":     p("integer", "Default 200"),
			"include_ignored": p("boolean", "Also search ignored and dependency folders"),
		}, "pattern")},
	{Name: "text_search", Title: "Search text", Group: "search", ReadOnly: true, run: textSearch,
		Desc: "Search file contents across the workspace, fast and in parallel (like ripgrep). Literal by default; set is_regex for RE2 regular expressions. Smart case: case-insensitive unless the query has capitals. Results are grouped by file as `line: text`, optionally with context lines.",
		Schema: obj(map[string]any{
			"query":           p("string", "Text or regular expression to find"),
			"is_regex":        p("boolean", "Treat query as an RE2 regular expression"),
			"case_sensitive":  p("boolean", "Force case-sensitive matching"),
			"whole_word":      p("boolean", "Match whole words only"),
			"path":            p("string", "Only search under this directory"),
			"include":         arr(p("string", "glob"), "Only files matching these globs, e.g. [\"**/*.java\"]"),
			"exclude":         arr(p("string", "glob"), "Skip files matching these globs"),
			"context_lines":   p("integer", "Lines of context before and after each match (0-5)"),
			"max_results":     p("integer", "Total matches to return (default 200)"),
			"max_per_file":    p("integer", "Matches per file (default 50)"),
			"include_ignored": p("boolean", "Also search ignored and dependency folders"),
		}, "query")},
	{Name: "find_usages", Title: "Find usages", Group: "search", ReadOnly: true, run: findUsages,
		Desc: "Find where a symbol (class, function, method, variable) is defined and referenced across the workspace. Definitions are listed first. Text-based with language-aware heuristics, so it works for any language without a compiler.",
		Schema: obj(map[string]any{
			"symbol":      p("string", "A single identifier, e.g. PlaceOrderService"),
			"path":        p("string", "Only search under this directory"),
			"include":     arr(p("string", "glob"), "Only files matching these globs"),
			"max_results": p("integer", "Default 300"),
		}, "symbol")},
	{Name: "workspace_changes", Title: "Source control changes", Group: "search", ReadOnly: true, run: workspaceChanges,
		Desc: "Show uncommitted changes in the workspace: git status (branch, staged, unstaged, untracked) and the diff against HEAD. Use it to review what you or the user changed.",
		Schema: obj(map[string]any{
			"path":         p("string", "Only changes under this path"),
			"include_diff": p("boolean", "Include the patch (default true)"),
			"max_bytes":    p("integer", "Truncate the diff after this many bytes (default 40000)"),
		})},
	// --- edit -------------------------------------------------------------
	{Name: "create_file", Title: "Create file", Group: "edit", run: createFile,
		Desc: "Create a file with the given content (parent folders are created). Fails if the file exists unless overwrite is true; to change part of an existing file use edit_file instead.",
		Schema: obj(map[string]any{
			"path":      p("string", "File path relative to the workspace root"),
			"content":   p("string", "Full file content"),
			"overwrite": p("boolean", "Replace the file if it already exists"),
		}, "path", "content")},
	{Name: "create_directory", Title: "Create directory", Group: "edit", run: createDirectory,
		Desc:   "Create a directory (and its parents) in the workspace. Succeeds if it already exists.",
		Schema: obj(map[string]any{"path": p("string", "Directory relative to the workspace root")}, "path")},
	{Name: "edit_file", Title: "Edit file", Group: "edit", run: editFile,
		Desc: "Change part of an existing file by exact text replacement: old_text must match the file exactly (including whitespace) and once, unless replace_all is true. Send several changes at once with `edits` (applied in order, all or nothing). Alternatively replace a line range with start_line/end_line + new_text. Returns a diff of the change; on a mismatch it says where similar text is.",
		Schema: obj(map[string]any{
			"path":        p("string", "File path relative to the workspace root"),
			"old_text":    p("string", "Exact text to replace (copy it from read_file, without the line-number prefix)"),
			"new_text":    p("string", "Replacement text"),
			"replace_all": p("boolean", "Replace every occurrence of old_text"),
			"start_line":  p("integer", "Line range mode: first line to replace"),
			"end_line":    p("integer", "Line range mode: last line to replace (inclusive)"),
			"edits": arr(obj(map[string]any{
				"old_text": p("string", "Exact text to replace"), "new_text": p("string", "Replacement"), "replace_all": p("boolean", "All occurrences"),
				"start_line": p("integer", "Line range mode start"), "end_line": p("integer", "Line range mode end"),
			}), "Several edits to apply in order, atomically"),
			"dry_run": p("boolean", "Show the diff without writing"),
		}, "path")},
	{Name: "move_path", Title: "Move or rename", Group: "edit", run: movePath,
		Desc: "Move or rename a file or directory inside the workspace. If `to` is an existing directory the item is moved into it.",
		Schema: obj(map[string]any{
			"from":      p("string", "Existing path"),
			"to":        p("string", "New path"),
			"overwrite": p("boolean", "Replace an existing destination"),
		}, "from", "to")},
	{Name: "delete_path", Title: "Delete", Group: "edit", run: deletePath,
		Desc: "Delete a file or directory in the workspace. Deleted items are moved to SpecWright's trash, so they can be recovered. Non-empty directories need recursive=true.",
		Schema: obj(map[string]any{
			"path":      p("string", "Path relative to the workspace root"),
			"recursive": p("boolean", "Allow deleting a non-empty directory"),
		}, "path")},
	// --- execute ----------------------------------------------------------
	{Name: "run_command", Title: "Run command", Group: "execute", run: runCommand,
		Desc: "Run a shell command in the workspace (non-interactive, no stdin; CI=1). Returns combined output (last 64 KB) and the exit code. For servers, watchers or long builds set background=true and read progress with get_command_output.",
		Schema: obj(map[string]any{
			"command":         p("string", "Shell command, e.g. `./mvnw -q test` or `npm test -- --watch=false`"),
			"cwd":             p("string", "Directory relative to the workspace root (default: the root)"),
			"timeout_seconds": p("integer", "Stop the command after this long (default 120, background 1800, max 3600)"),
			"background":      p("boolean", "Start it and return immediately with an id"),
		}, "command")},
	{Name: "get_command_output", Title: "Command output", Group: "execute", ReadOnly: true, run: getCommandOutput,
		Desc: "Read the output of a background command started by run_command, optionally waiting for it or stopping it. Without an id, lists background commands.",
		Schema: obj(map[string]any{
			"id":           p("string", "The id run_command returned, e.g. cmd-1"),
			"wait_seconds": p("integer", "Wait up to this long for it to finish (0-120)"),
			"stop":         p("boolean", "Stop the command"),
		})},
	{Name: "get_problems", Title: "Problems", Group: "execute", ReadOnly: true, run: getProblems,
		Desc:   "Build or compile the project with its configured build tool (Maven, Gradle, npm, Go, dotnet, Python) and return the compiler errors and warnings with file and line. Use after edits to check the code still compiles.",
		Schema: obj(map[string]any{})},
	// --- web --------------------------------------------------------------
	{Name: "fetch_webpage", Title: "Fetch web page", Group: "web", ReadOnly: true, run: fetchWebpage,
		Desc: "Fetch an http(s) URL and return its readable text (HTML is converted to text; JSON and plain text as is). Uses the configured corporate proxy. Good for documentation and API references. With format \"json\" the response comes back as a JSON object (url, status, ok, contentType, title, headers, and json — the parsed body — or body — its text); with format \"raw\" it is exactly the response body (e.g. an API's JSON, as is), for later steps to parse.",
		Schema: obj(map[string]any{
			"url":       p("string", "Absolute http(s) URL"),
			"max_chars": p("integer", "Characters to return (default 20000 for text; the whole response, up to 5 MB, for json and raw)"),
			"format":    map[string]any{"type": "string", "enum": []string{"text", "json", "raw"}, "description": "text (default): the URL and status, then the readable text; json: the response as a JSON object (status, headers, parsed body); raw: exactly the response body, nothing added (fails on an error status)"},
		}, "url"),
		OutputBy: map[string]map[string]*flow.Schema{"format": {"json": FetchJSONShape}}},
	// --- plan -------------------------------------------------------------
	{Name: "todo_list", Title: "Todo list", Group: "plan", ReadOnly: true, run: todoList,
		Desc: "Keep a short todo list for multi-step work: pass items to replace the list (each {title, status: pending|in_progress|done}), or call without items to read it. Mark items done as you finish them.",
		Schema: obj(map[string]any{
			"items": arr(obj(map[string]any{"title": p("string", "What to do"), "status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "done"}}}, "title"), "The whole list"),
		})},
}
