# Code review: organization and improvements (2026-11-15)

Analysis only: no code was changed for this report. It follows up [2026-11-10-maintainability.md](2026-11-10-maintainability.md) (referred to below as "the 11-10 review"), re-measures the code, says which of its items are still open, and adds what has appeared since, including debt from the UI work of 15 Nov.

Priorities:
- **Critical:** risk of lost work, lost data or a security problem. Do first.
- **High:** slows every change down or will cause bugs or slowness soon.
- **Low:** worth doing when touching the area.

## Measurements now vs 11-10

| | 11-10 | Now | Trend |
|---|---|---|---|
| Go lines (incl. tests) | ~19,000 | 35,045 (6,460 test) | grew 84% |
| `core.Hub` methods / fields / lock sites / files | 155 / 39 / 70+ / 15 | **236 / 42 / 105 / 28** | worse |
| `core/flowrun.go` | 1,456 lines | **2,040 lines** | worse |
| Go functions over 80 lines | 16 | ~48 (largest: `flow.Validate` ~350, `TestExtensionWith` ~235, `execStep` ~195) | worse |
| Ignored errors (`_ = …`, non-test) | 85 | 102 | worse |
| `regexp.MustCompile` inside functions | 21+ | 22 | same |
| `go vet` / `gofmt` | not run | clean / clean | good |
| Go coverage | core 70 · agent 26 · workflow 0 | core 74 · flow 78 · loop 83 · session 82 · builtin 73 · aishim 71 · speckit 67 · watch 64 · extension 57 · store 57 · agent 31 · app layer 18 · workflow 8 · bus, `cmd/headless` 0 | mixed |
| Frontend JS/JSX | 480 KB | **948 KB, 11,781 lines** | doubled |
| Lines over 200 characters | 450+ | **979** (max 9,189, in `themes.generated.js`) | worse |
| `mockBackend.js` | 115 KB | **212 KB** | worse |
| `views/Flows.jsx` | 49 KB | **130 KB, 1,441 lines, 72 functions** | worse |
| `styles.css` | 85 KB, 953 lines | **129 KB, 1,521 lines** | worse |
| Inline `style={{…}}` | 178 | 203 | worse |
| `update()` call sites | 214 | 340 | worse |
| Global `window`/`document` listeners | — | 25 | new |
| Frontend tests, lint, formatter, CI, Makefile | none | none | same |
| `backend.js` method list vs `app.go` | in sync | 123 / 123, in sync | good |

**Fixed since 11-10:** git repository and `.gitignore` (P1); the keychain write now passes the secret on stdin, not the command line (`internal/core/keychain.go:80`); `go vet` and `gofmt` are clean.

---

## Critical

| # | Item | Effort |
|---|---|---|
| C1 | A lot of finished work is not committed | 30 min |
| C2 | Imported flows can run shell commands without approval | ½–1 day |
| C3 | Run history and metrics can be lost without any sign | ½ day |

### C1. A lot of finished work is not committed
- **Evidence:** 32 changed or new files in the working tree (five rounds of UI work, `docs/ui-review/`, three new modules: `a11y.js`, `Pane.jsx`, `CommandPalette.jsx`, `Notices.jsx`) on branch `feature/copy-paste-flow-steps`, which already has PR #5 open for different work. Each `npm run build` also deletes the tracked `frontend/dist/.gitkeep` (`frontend/build.mjs:9` removes the whole `dist` folder).
- **Why it matters:** one bad checkout or reset loses days of work; mixing the UI work into PR #5 makes that PR hard to review.
- **Fix:**
  - Commit the UI work on its own branch (for example `feature/ui-review`) and open a separate PR; keep `docs/ui-review/screenshots` (5.8 MB) out or in Git LFS.
  - Make `build.mjs` empty `dist` but keep `.gitkeep` (or recreate it), so a build never shows as a change.

### C2. Imported flows can run shell commands without approval
- **Status:** P7 in the 11-10 review, still open (no approval code for commands in `internal/core`).
- **Evidence:** the transform op `command`, review "Run a command" saved into a flow, and output actions run commands; `.flow.json` files can be imported and pasted (copy/paste of steps across flows was added this week, which makes moving steps between flows even easier). Project tools need approval before agents can run them; flow commands do not.
- **Why it matters:** opening someone else's flow file and clicking Run executes whatever it contains.
- **Fix:**
  - Approve each command per project by hash, like tools; imported or pasted steps start unapproved.
  - List every command on import or paste and before the first run, with Approve / Remove.
  - Test: an imported flow with a command does not run it until approved.

### C3. Run history and metrics can be lost without any sign
- **Evidence:** 102 ignored errors; the costly ones are `_ = h.Store.SaveRun(...)` (`flowrun.go:328`, `:545`, `results.go:457`), `_ = h.Store.Append(e)` (`flowrun.go:522`), `_ = h.Store.Ledger(...)` (`flowrun.go:551`), and results files (`results.go:328–335`).
- **Why it matters:** on a full or read-only disk, runs vanish from History and Metrics (hours-saved numbers) and nobody knows.
- **Fix:**
  - Return or log these errors and publish a `store.error` event; the UI can show it in the new message list (status bar bell).
  - Add a test with a store that fails writes.
  - Leave the intentional best-effort ones (`os.Remove` of temp files) but mark them with a comment.

---

## High

| # | Item | Effort |
|---|---|---|
| H1 | The browser mock is a second backend | 1–2 days |
| H2 | `Hub` keeps growing | 3–5 days, step by step |
| H3 | Flow executor in one 2,040-line file | 1–2 days |
| H4 | Very large frontend files | 2–3 days |
| H5 | Every change re-renders everything and writes preferences | ½–1 day |
| H6 | No lint, formatter, frontend tests or CI | 1 day to set up |
| H7 | Keyboard shortcuts are spread over 25 global listeners | 1 day |
| H8 | App state has no declared shape | 1 day |
| H9 | One 129 KB stylesheet with patches at the end | 1–2 days |

### H1. The browser mock is a second backend
- **Status:** P2 in the 11-10 review, still open, and the mock has nearly doubled (115 → 212 KB, 1,953 lines).
- **Evidence:** `mockBackend.js` reimplements validation, transforms, the flow runner, results writing, secrets, Spec Kit setup and more. Example: this week's "Run spec workflow" refused to start in the mock only after Spec Kit setup, as the Go code does, but other checks differ.
- **Why it matters:** every feature is written twice; UI checks in the mock can pass while the desktop app fails, or the reverse.
- **Fix:** serve the real Go backend to the browser in development (a small dev-only HTTP + server-sent-events bridge over the `App` methods, which `backend.js` already lists), and cut the mock down to fixtures for offline screenshots.

### H2. `Hub` keeps growing
- **Status:** P3 in the 11-10 review: 155 → 236 methods, 15 → 28 files, 70+ → 105 lock sites.
- **Why it matters:** one type and one lock for workspace, workflow, flows, flow runs, secrets, admin, libraries, packages, metrics and results; every feature edits it and lock-order mistakes get likelier.
- **Fix:** move areas out behind small interfaces, one per PR, tests green after each: Secrets, Admin, Libraries, Flows (designer), FlowRuns, Results, Metrics. `Hub` keeps the workspace and wiring.

### H3. Flow executor in one 2,040-line file
- **Status:** P4 in the 11-10 review: 1,456 → 2,040 lines. Long functions: `execStep` ~195, `runNodes` ~163, `runLoop` ~143.
- **Fix:** an `internal/flowrun` package depending on interfaces (`ToolRunner`, `AgentRunner`, `SecretResolver`, `ResultsSink`, `Publisher`), split into `run.go`, `nodes.go`, `loop.go`, `step_*.go` per step type, `review.go`; unit tests with fakes.

### H4. Very large frontend files
- **Evidence:** `views/Flows.jsx` 130 KB / 1,441 lines / 72 functions (canvas, palette, inspector, every step editor, AI designer, undo, keyboard); `FlowRun.jsx` 47 KB; `Tools.jsx` 45 KB; `Workflow.jsx` 33 KB; 979 lines over 200 characters.
- **Why it matters:** hard to review (a one-word change rewrites a 600-character line), easy to break in scripted edits, slow to load into an editor or a model's context.
- **Fix:**
  - Run Prettier once (print width ~110; exclude `*.generated.js`).
  - Split by folder: `flows/{Canvas,Palette,Inspector,StepEditors/*,AIDesigner,history,keyboard}.jsx`, `flowrun/{Setup,Options,Review,Steps,Outputs,Side}.jsx`, `tools/{Describe,Review,Test,Save}.jsx`, `workflow/{Lifecycle,StepEditor,RunPanel}.jsx`.

### H5. Every change re-renders everything and writes preferences
- **Status:** P6 in the 11-10 review, still open; the load has grown.
- **Evidence:**
  - `update()` (340 call sites, `store.js:47`) re-renders the whole tree and calls `savePrefs()`, which serializes and writes `localStorage` each time, including for every streamed agent event and every keystroke. Prefs now also hold pane widths.
  - `S.events` is never trimmed: every run's events stay in memory for the session.
  - New this week: `a11y.js` watches the whole document with a `MutationObserver` (`subtree: true`), so every render also runs a `querySelectorAll`; tooltips add global `mouseover`/`focusin` listeners.
- **Fix:**
  - Batch `update()` into one render per animation frame.
  - Save prefs only when a persisted field changes (or debounce 300 ms).
  - Keep events only for runs on screen plus the last few; reload from the backend on demand.
  - Replace the `MutationObserver` with explicit open/close calls from a shared `Modal` component.

### H6. No lint, formatter, frontend tests or CI
- **Status:** open since 11-10.
- **Evidence:** no ESLint, Prettier, `golangci-lint` config, Makefile/Taskfile, or `.github/workflows`. `package.json` has no `test` or `lint` script. Coverage gaps: `internal/workflow` 8% (the Spec workflow runner), `internal/agent` 31% (CLI output parsers), app layer 18%, `internal/bus` and `cmd/headless` 0%.
- **Why it matters:** this week's UI work was checked by hand in the browser; nothing will catch a regression in the next change. Example caught by hand only: the 11 px minimum broke the lifecycle diagram's labels.
- **Fix:**
  - Makefile: `test`, `test-race`, `lint`, `fmt`, `build`, `dev`.
  - ESLint with `react-hooks` (views add hooks next to early returns, e.g. `useReplayKeys` in `History.jsx` only works because it sits before any `return`) and Prettier.
  - Vitest + Testing Library for pure modules first: `filerefs.js`, `richtext.js`, the flow graph helpers, `optionsSummary`, `ago`, the undo history.
  - GitHub Actions running all of it on push; recorded-output tests for the Claude and Copilot parsers; tests for `internal/workflow`.

### H7. Keyboard shortcuts are spread over 25 global listeners
- **Evidence:** `window`/`document` key handlers in `Flows.jsx` (⌘S, ⌘Z, arrows, C, F, Delete, Esc, ⌘\, ⌘A/C/V), `CommandPalette.jsx` (⌘K, ⌘1–9), `History.jsx` (Space, ←/→), `a11y.js` (Tab, Esc), `Dialog.jsx`, the prompts, `ui.jsx` `usePopover`. Each one re-implements "am I typing in a field?" and "is a dialog open?".
- **Why it matters:** conflicts are already possible (Esc closes a popover and also clears the canvas selection unless the capture order is right; ⌘1–9 vs browser tab switching on Windows); new shortcuts get harder to add safely.
- **Fix:** one `shortcuts.js` registry: scope (global, view, dialog), a shared "typing" check, priority by scope, and a generated shortcut list for the help sheet and the command palette.

### H8. App state has no declared shape
- **Evidence:** 67 `S.*` keys used, 27 declared in `store.js`; 10 slices created on first use (`S.fl`, `S.frun`, `S.pg`, `S.fa`, `S.cx`, `S.sk`, `S.tf`, `S.hk`, `S.rulesUi`, `S.metricsRange`); this week added `S.panes`, `S.rail`, `S.railOpen`, `S.notices`, `S.palette`, `S.settingsTab`, `S.wfChanges`, `S.cmdFlows`. `S.panes` also holds non-width flags (`"wiz:all"`, `"wf:lc:c"`, `"flows:pal:c"`).
- **Fix:** declare every slice with its shape and default in `store.js` (or per feature `flows/store.js`), JSDoc types, and separate `prefs` (persisted) from session state; split `S.panes` into `layout.widths` and `layout.collapsed`.

### H9. One 129 KB stylesheet with patches at the end
- **Evidence:** 1,521 lines; this week added about ten sections at the end that override earlier rules, plus specificity workarounds (`html:root .app.rail-open` to beat the Material skin, 5 `!important`), and a `<style>` tag generated at runtime in `Wizard.jsx`. 203 inline `style={{…}}`.
- **Why it matters:** the sidebar bug fixed today (Material's `.app` width winning over the new rule) is this problem; more will follow.
- **Fix:** split into `base.css`, `layout.css`, one file per view, and `skins/*.css`; use CSS cascade layers (`@layer base, components, views, skins`) so skin rules have a defined order instead of specificity tricks; move repeated inline styles to classes.

---

## Low

| # | Item | Effort |
|---|---|---|
| L1 | Regexes compiled on every call | 1 hour |
| L2 | Long Go functions | ongoing |
| L3 | Label maps defined several times | 1 hour |
| L4 | Views import each other in circles | ½ day |
| L5 | Small debt from the 15 Nov UI work | ½ day |
| L6 | Generated method list | 2 hours |
| L7 | Run storage does not scale | 1 day |
| L8 | Mixed names | ongoing |
| L9 | Stray files and a React warning | 1 hour |

### L1. Regexes compiled on every call
- **Evidence:** 22 inside functions, e.g. `results.go:43`, `flowdesign.go:428` and `:626`, `extension/packages.go:323` and `:325`.
- **Fix:** move the constant ones to package-level `var`s; keep the dynamic ones (`flowrun.go:1829`, `flowdesign.go:469` build from a step id) but cache them per id if they are hot.

### L2. Long Go functions
- **Evidence:** ~48 over 80 lines; `flow.Validate` ~350 (one function for every node type), `TestExtensionWith` ~235, `stepFixPrompt` ~155, `WriteRunResults` ~152, `cmd/headless` `run` ~150, `MetricsIn` ~135.
- **Fix:** split `Validate` per node type (table of validators), `WriteRunResults` per file kind; do the rest when touching them.

### L3. Label maps defined several times
- **Evidence:** `STATUS_LABEL` in `ui.jsx:44` and `FlowRun.jsx:42`; `KIND` in `History.jsx:13` and `Playground.jsx:40`; plus `PG_LABEL`, `REVIEW_LABEL`, the mock's labels.
- **Fix:** one `labels.js`.

### L4. Views import each other in circles
- **Evidence:** `Flows.jsx` ↔ `FlowRun.jsx`, `Flows.jsx` ↔ `FlowGuide.jsx`; `History.jsx`, `Playground.jsx`, `FlowRun.jsx` import `RunPanel` from `Workflow.jsx`; `History.jsx` imports `ago` from `FlowRun.jsx`; `Appearance.jsx` imports `RunSettings` from `Workflow.jsx`.
- **Why it matters:** works today because nothing runs at import time, but a top-level use will break with an undefined import, and splitting files gets harder.
- **Fix:** move shared pieces out of views: `components/RunPanel.jsx`, `components/RunSettings.jsx`, `lib/time.js` (`ago`), `nav.js` (`openFlowRunner`, `openFlowInDesigner`, `openFlowGuide`, `openSettings`).

### L5. Small debt from the 15 Nov UI work
- `Wizard.jsx` injects a `<style>` tag to hide the other sections; render only the current section instead.
- `Workflow.jsx` has the "Start next iteration" confirm-and-reset code twice; make it one function.
- `PaneHandle` lives in `ui.jsx` and its store wrapper in `Pane.jsx`; keep both in one `components/Pane.jsx`.
- `Composer` in `Playground.jsx` takes five props and reaches into module state; give it a small API or move it with its state.
- Tooltips move `title` into `data-tip` while shown; if React changes `title` at that moment the old text comes back. Better: a `Tooltip` component or a `data-tip` attribute set by components instead of `title`.
- `Notices` and the toast both store messages; one `messages` slice would serve both.

### L6. Generated method list
- **Evidence:** `frontend/src/backend.js` lists the 123 `App` methods by hand; in sync today.
- **Fix:** generate it from `app.go` (as `scripts/gen-builtins` does for built-ins), or use the bindings Wails generates.

### L7. Run storage does not scale
- **Evidence:** `Store.Runs()` (`internal/store/store.go:253`) reads every run file in the folder on each call; housekeeping archives runs in the UI but results folders under `.codegen/runs/` have no age-based cleanup.
- **Fix:** an index file (id, time, kind, state, usage) updated on save; extend housekeeping to delete results folders of archived runs after N days.

### L8. Mixed names
- **Status:** open since 11-10: module and folders `codegen` vs product SpecWright; `workflow` vs `flow`; UI "Tools" vs code `Extension`; this week the view id `appearance` now shows as "Settings".
- **Fix:** pick one vocabulary, rename gradually in code, migrate on-disk names with a fallback read of the old ones.

### L9. Stray files and a React warning
- 12 MB `headless` binary and `.DS_Store` in the repo root (both ignored, still on disk).
- Console warning "Each child in a list should have a unique key" from `RunPanel` in `Workflow.jsx`: activity rows use `e.seq` as the key; the likely cause is the same event arriving from both the store and the live stream (not confirmed). De-duplicate by `seq` when merging, or key by `run + seq + type`.

---

## Suggested order

1. **Now:** C1 (commit on its own branch, fix `.gitkeep`), C3 (stop ignoring store errors), C2 (command approval).
2. **Next week:** H6 tooling (Prettier, ESLint, Makefile, CI) so every later step is checked; H5 performance; L9.
3. **Then:** H4 split frontend files and H9 split CSS (easier after Prettier); L3, L4, L5 along the way; H7 shortcut registry; H8 state shape.
4. **Backend:** H3 `internal/flowrun`, then H2 one `Hub` area per PR; L1, L2 when touching code.
5. **When convenient:** H1 real backend in the browser (biggest single saving once done), L6, L7, L8.
