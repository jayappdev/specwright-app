# Code review: structure and maintainability (2026-11-10)

A whole-codebase review of SpecWright, recorded as a backlog to work through. Tick items off as they land; add a short note with the commit or PR.

**Verdict:** the backend is in decent shape and the frontend needs restructuring. The internal Go packages are well separated and tested, but most new features have landed in one large Go type and a few very large React files. No git repository, linting or CI means changes aren't tracked or checked automatically.

## Measurements at the time of review

| | Value |
|---|---|
| Go | ~19,000 lines in 14 packages, one direct dependency (Wails) |
| Frontend JS/JSX | ~6,100 lines, but 480 KB: 450+ lines over 200 characters (up to 907) |
| CSS | `styles.css`, 953 lines, 85 KB, one file |
| Browser mock | `mockBackend.js`, 115 KB |
| `core.Hub` | 155 methods, 39 fields, one `sync.RWMutex`, 70+ lock sites, 15 files |
| Largest files | `core/flowrun.go` 1,456 · `core/hub.go` 834 · `flow/flow.go` 821 · `flow/transform.go` 766 · `views/Flows.jsx` 49 KB |
| Go functions over 80 lines | 16 (largest: `TestExtensionWith` 230, `flow.Validate` 174, `execStep` 153, `WriteRunResults` 150) |
| Ignored errors (`_ = …`) | 85 |
| Test coverage | flow 78% · loop 83% · session 82% · core 70% · builtin 68% · watch 64% · store 61% · extension 52% · agent 26% · workflow, app layer (`app.go`), `cmd/headless` 0% · frontend: no tests |
| Tooling | no git repository, no linter or formatter config, no CI, no Makefile |

## What is good (keep it)

- Clear lower layers: `agent`, `bus`, `session`, `loop`, `store`, `watch`, `builtin`, `flow`, `extension` each do one job, and none import `core`.
- One outside dependency (Wails); JSON, signing, diffing and CSV use the standard library.
- Real tests with good coverage of the core and end-to-end tests for runs, flows, secrets, admin signing and results folders.
- Security built in: secret redaction, no-shell tool execution, workspace sandboxing, signed admin tools, token-protected loopback bridge.

---

## Top priorities

| # | Problem | Effort |
|---|---|---|
| P1 | No version control | 10 min |
| P2 | Browser mock duplicates backend logic | 1–2 days |
| P3 | `Hub` does everything | 3–5 days, incremental |
| P4 | Flow executor is one 1,456-line file | 1–2 days |
| P5 | Frontend files with very long lines | 1 day |
| P6 | Frontend re-renders everything and saves on every change | ½ day |
| P7 | Imported flows can run commands without a warning | ½ day |

### P1. No version control
- **Evidence:** the folder is not a git repository. A stray 12 MB `headless` binary (from a test build) and `.DS_Store` sit in the repo root.
- **Why it matters:** no history, no undo, no review, no branches; every other change is riskier without it.
- **Fix:**
  - [x] `git init`, first commit, pushed to github.com/jayappdev/specwright-app (`5e710b8`).
  - [x] Extend `.gitignore`: binaries, `.DS_Store`, `frontend/node_modules`, `frontend/.dev`, `frontend/dist` (built; a `.gitkeep` keeps `go:embed` compiling), `build/bin`, `design/`, `.claude/`.
  - [ ] Delete the stray `headless` binary (ignored, so it is not committed).

### P2. The browser mock duplicates backend logic
- **Evidence:** `frontend/src/mockBackend.js` reimplements flow validation, transforms, format conversion, the flow executor, the results writer, conversation context, secrets resolution and library scanning (`validateFlow`, `transform`, `convert`, `render`, `jpath`, `startFlowRun`, `writeResults`, `contextFor`, `resolveSecrets`, …).
- **Why it matters:** every feature is written twice and the copies already differ (the mock's validation misses checks the Go code has). The preview can pass while the desktop app is broken; the Create-a-flow `prompt()` bug was exactly this.
- **Fix:**
  - [ ] Run the browser preview against the real Go backend: use `wails dev`'s browser mode, or a small dev-only HTTP/JSON-RPC bridge over the `App` methods plus an event stream for `hub:event`.
  - [ ] Shrink the mock to fixtures (sample workspace, runs, flows) for offline UI work, or delete it.

### P3. `Hub` does everything
- **Evidence:** `internal/core.Hub` has 155 methods and 39 fields guarded by one lock used at 70+ places, across 15 files: workspace, profile, workflow runs, playground, extensions, secrets, keychain, admin, managed tools, libraries, flows, flow runs, results, metrics, attachments, preview, built-ins, packages.
- **Why it matters:** unrelated features share state and a lock. It's hard to test one part alone, lock-ordering mistakes are easy, and every feature touches the same type.
- **Fix:** split into services behind small interfaces, one area at a time, keeping tests green after each:
  - [ ] `SecretsService` (session values, environment, keychain, asking, redaction registration)
  - [ ] `AdminService` (key, unlock, trust, managed tool catalog)
  - [ ] `Libraries` (corporate catalog + sync, project skills/scripts)
  - [ ] `Flows` (designer CRUD, import/export, validation, preview)
  - [ ] `Results` (run results folders, digests, threads, continuation)
  - [ ] `Hub` keeps the workspace and wiring; the app layer (`app.go`) stays a thin binding layer.

### P4. The flow executor is one 1,456-line file
- **Evidence:** `internal/core/flowrun.go` holds the run API, scheduling, items/repeat, step execution, reviews and iteration, tool calls, agent calls, shell commands, input/output files and keychain-free credential plumbing. Functions: `execStep` 153 lines, `execFlow` 97, `reviewStep` 96, `flowTool` 93, `flowAgent` 85.
- **Fix:**
  - [ ] Extract `internal/flowrun`, depending on small interfaces (`ToolRunner`, `AgentRunner`, `SecretResolver`, `ResultsSink`, `Publisher`) instead of `*core.Hub`.
  - [ ] Split by concern: `run.go` (API, lifecycle, items/repeat), `step.go` (per-type execution), `review.go`, `tools.go`, `agent.go`, `files.go`.
  - [ ] Unit-test steps with fake runners (no full app).

### P5. Frontend files with very long lines
- **Evidence:** 450+ lines over 200 characters (up to 907); `Flows.jsx` 49 KB in 623 lines, `FlowRun.jsx` 31 KB, `Tools.jsx` 33 KB, `Workflow.jsx` 29 KB. Much of this came from scripted edits.
- **Why it matters:** hard to read, review or diff; a one-word change rewrites a 600-character line.
- **Fix:**
  - [ ] Add Prettier (print width ~110) and run it once over `frontend/src`.
  - [ ] Split large views into folders (see Frontend details).

### P6. Frontend re-renders everything and saves on every change
- **Evidence:** `update()` (214 call sites) bumps a global version, re-renders the whole tree, and calls `savePrefs()`, which writes `localStorage`, on every call, including each streamed agent event and each keystroke.
- **Why it matters:** the UI will get sluggish on long runs and big flows.
- **Fix:**
  - [ ] Coalesce `update()` into one render per animation frame.
  - [ ] Save preferences only when the persisted fields change (or debounce them).
  - [ ] Drop `S.events` for runs that aren't on screen (memory grows for the whole session).

### P7. Imported flows can run commands without a warning
- **Evidence:** the transform op `command` (and review "Run a command" saved into the flow) runs any shell command; flows can be imported from a `.flow.json`. Project tools need approval before agents can run them; flow commands don't.
- **Why it matters:** someone else's flow file can carry `rm -rf …` or worse and it runs when you click Run.
- **Fix:**
  - [ ] Treat command steps like tools: approve them (hash of the command) per project; imported flows start with their commands unapproved.
  - [ ] Show all commands on import and before the first run.

---

## Backend details

- [ ] **Ignored errors that hide data loss.** `_ = Store.SaveRun(...)`, `Store.Ledger(...)`, `Store.Append(...)` in flow runs (`flowrun.go`), results writes, `workflow.Save` in `hub.go`. If the disk is full, history and metrics silently drop runs. Log them, or publish a `*.error` event as the results writer does.
- [ ] **Regexes compiled on every call.** 21 in `flow/transform.go` (the Markdown converter compiles several per line), plus `builtin/search.go:292`. Move to package-level variables.
- [ ] **Unbounded growth.**
  - Run results folders (`.codegen/runs/`) and store run files are never cleaned up: add a retention setting (e.g. keep 90 days or N runs).
  - `Store.Runs()` reads every run file on each call: add an index once runs number in the thousands.
- [ ] **Hand-written mirrors between Go and JS.**
  - Content-type detection (`contentExt` / `contentFormat`) and file-reference detection exist in both.
  - The 89 app methods are listed again by hand in `frontend/src/backend.js`; currently in sync, but a forgotten entry fails silently. Generate the list (or use the bindings Wails generates).
- [ ] **Long functions to break up:** `TestExtensionWith` (230), `flow.Validate` (174, split per node type), `Mock.Run` (156), `WriteRunResults` (150, one writer per file kind), `Metrics` (120), `cmd/headless` `run` (119), `handleMCP` (114).
- [ ] **Keychain write passes the secret on the command line** (`security add-generic-password … -w <value>`, macOS), briefly visible in the process list. Use a Keychain API via cgo-free tooling or an interactive/stdin form if one becomes available; at least document it.
- [ ] **Naming.**
  - **Old name:** module `codegen`, store folder `codegen`, `.codegen/` and the `codegen.prefs.v1` storage key, while the product is SpecWright.
  - **Two workflows:** `workflow` (the Spec workflow) vs `flow` (designed flows).
  - **Tools vs extensions:** UI "Tools" vs code `Extension` / `extensions.json`.
  - **Plan:** pick one vocabulary, rename in code gradually, and keep on-disk names (or migrate them) so existing projects keep working.

## Frontend details

- [ ] **Declare all state in one place.** Slices created on the fly: `S.fl`, `S.frun`, `S.pg`, `S.fa`, `S.rulesUi`, and also `S.preview`, `S.dialog`, `S.libEdit`, `S.pgPicker`, `S.recentRuns`, `S.bt`, `S.tb`. Declare them in `store.js` with their shape; move feature actions into feature modules (e.g. `flows/store.js`, `flowrun/store.js`).
- [ ] **Split large views into folders:**
  - [ ] `views/Flows.jsx` → `flows/Canvas.jsx`, `Palette.jsx`, `Inspector.jsx`, `InputsEditor.jsx`, `ArgsEditor.jsx`, `Preview.jsx`, `graph.js`
  - [ ] `views/FlowRun.jsx` → `flowrun/Setup.jsx`, `Review.jsx`, `Steps.jsx`, `Outputs.jsx`, `Sidebar.jsx`
  - [ ] `views/Tools.jsx` (stages: describe, review, test, save)
  - [ ] `views/Workflow.jsx` (lifecycle diagram, step editor, `RunPanel`, `ActivityRow`)
- [ ] **Styles.** `styles.css` is one 85 KB file with skin overrides throughout; 178 inline `style={{…}}` uses. Split CSS by feature (or CSS modules) and turn repeated inline styles into classes.
- [ ] **Duplicated label maps.** `STATUS_LABEL` (`ui.jsx`, `FlowRun.jsx`), `KIND` (`History.jsx`, `Playground.jsx`), `PG_LABEL`, the mock's `kindLabel`. Define once in a `labels.js`.
- [ ] **No checks.** Add ESLint (react-hooks rules would catch several patterns used here) and Prettier; consider JSDoc types or a gradual move to TypeScript for `store.js`, `backend.js` and the flow model.
- [ ] **No tests.** Add Vitest + Testing Library: the flow designer (connect, auto-wire, rename), the content viewer (format detection, Preview/Raw), `filerefs.js` (the detector already has an ad-hoc test), `richtext.js` (Markdown ⇄ HTML).

## Testing and tooling

- [ ] `internal/workflow` (Spec workflow runner) has no tests of its own; it's only covered through `core`.
- [ ] `internal/agent` is at 26%: add recorded output fixtures for the Claude stream-json and Copilot JSON/text parsers (versions change their formats).
- [ ] The app layer (`app.go`) and the command-line runner (`cmd/headless`) are untested.
- [ ] Add a `Makefile` or `Taskfile`: `test`, `test-race`, `lint` (golangci-lint, ESLint), `fmt`, `build`, `dev`.
- [ ] Add CI running those on every push.
- [ ] Check in the real-CLI smoke tests (the temporary `cmd/flowtest`, `cmd/attachtest` programs written during development) behind a build tag (e.g. `//go:build e2e`), so the Claude/Copilot checks are repeatable.

---

## Suggested order

1. **This week**
   - [x] P1: `git init`, `.gitignore` (stray binary still to delete locally)
   - [ ] P5: Prettier + ESLint, one formatting pass
   - [ ] Makefile + CI
   - [ ] P7: approval for flow commands from imported flows
2. **Next**
   - [ ] P2: browser preview against the real Go backend; cut the mock down to fixtures
   - [ ] P6: batch re-renders, save prefs only on change, evict old events
3. **Then**
   - [ ] P4: extract `internal/flowrun` behind interfaces
   - [ ] P3: split `Hub` into services, one area at a time
4. **Ongoing**
   - [ ] Split large React views and the CSS by feature
   - [ ] Unify naming
   - [ ] Retention for run results
   - [ ] Fix ignored errors and hoist regexes
