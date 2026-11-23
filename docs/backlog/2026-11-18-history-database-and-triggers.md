# Backlog: history database and triggers (2026-11-18)

Ideas to come back to, collected while adding triggers and the SQLite history database on `feature/ui-improvements`. Nothing here is built yet unless its row says so. Each item has an id (D… database, T… triggers, F… flows) so it can be referred to and marked done here.

## What is already done (for context)

| Area | State | Commit |
|---|---|---|
| Schedules as triggers (once, every, daily, weekly; until / count; catch-up; encrypted secrets; restart after the app closes) | Done | `45e86af` |
| `schedule_flow` agent tool; Refresh and 30 s auto-refresh in Run History and Run flows | Done | `fe62cbb`, `d9c93cf` |
| Triggers: after another flow, when files change | Done | `2153077` |
| File triggers: absolute paths and folders outside the project | Done | `5c8e731` |
| History database (SQLite, `modernc.org/sqlite` v1.62.0): runs, usage, trigger state, full-text search; History search inside runs; `history_search` agent tool | Done | `561b060` |
| D1 lessons from reviews, D2 tool reliability, D3 trigger run queue and retries | Done (see their rows) | `d82ff47` |

Priorities, as in the code reviews:
- **High:** fixes a gap people hit, or makes the AI and the designer better with every run.
- **Medium:** clear value, no hurry.
- **Low:** when touching the area.

## History database (SQLite)

| Id | Priority | Idea | Notes |
|---|---|---|---|
| D1 | **Done** | **Lessons from reviews.** Refine feedback (not saved into the flow) and Improve "another round" feedback become lessons for the step; AI and Improve steps get the newest 8 that are on in their prompt; the step's Lessons in the Flow Designer edit, turn off, delete and add them. Also learned (kept small): a reviewer's small hand edit (removed / added lines; rewrites skipped) and the Improve judge's first "not good enough" per run; each step keeps the newest 3 of each learned kind, and the prompt section is at most 8 lessons and about 1,500 characters, people's first. | Keep every review rejection and its reason, "refine" feedback, edit-then-approve, and Improve-step verdicts, per project / flow / step. Add the most relevant ones to prompts so the AI does not repeat a mistake. | New `lessons` table (workspace, flow, step, text, source run, times used) in the FTS index. Show and edit them in the Flow Designer. |
| D2 | **Done** | **Tool reliability.** Calls from flows, agents (the bridge) and the test form are recorded; tiles and the Designer's tool step show "failed N of the last 10 · usually X s"; the test form's "Arguments that worked"; the AI designer gets a note for tools that fail 30% or more. Not yet: built-ins called by agents (read_file, …) are not recorded. | Record every tool call: duration, ok or failed, error, arguments shape. Show "failed 4 of the last 10 runs", "usually 3 s" in the Designer and on Tools; suggest arguments that worked in the typed test form; let the AI designer prefer reliable tools. | New `tool_calls` table, filled from `builtin.result` / tool events. |
| D3 | **Done** | **A queue for trigger runs.** A run due while the flow is busy (or while 2 triggered runs go) waits, at most 3 per trigger, unless the trigger says "skip that time"; runs start in order when the flow is free and the queue survives restarts; failed runs retry 0–3 times, N minutes apart. Run flows lists the waiting runs under their flow (retry time, or "when the flow is free") with Cancel. | Today a trigger whose previous run is still going skips that time. Queue it instead, and keep the queue across restarts; limit how many triggered runs go at once; retry with a wait. | New `trigger_queue` table; the scheduler takes from it. Pairs with T-settings "limits". |
| D4 | Medium | **Items already seen**, for a "something new appears" trigger (T3): the ids of Jira issues / PRs already handled. | New `trigger_seen` table (trigger key, item id, first seen). |
| D5 | **Done** | **File changes while the app was closed.** A files trigger can "also count changes made while SpecWright was closed": the last scan of the folders such triggers watch (the project, folders outside it) is kept in the database and compared when the project opens; the differences start a run. Only those folders' scans are kept. | |
| D6 | Medium | **A record of admin and approval actions:** tool approvals, approved commands (`approved-commands.json`), policy changes, "allow for this run" answers. Append-only, searchable. | For audits. Keep admin keys out of it (see "stays as files"). |
| D7 | Medium | **Metrics over time.** Daily totals per flow, tool and model (tokens, cost, premium requests, minutes saved); budgets and premium-request limits as one query. | A rollup table refreshed when a run ends. |
| D8 | Medium | **Move run events into the database** (instead of one `runs/<id>.jsonl` per run): Replay and Debug as queries ("only tool errors"), retention as one delete, one file to back up. | After the current version has proved itself. Keep an export to `.jsonl`. |
| D9 | Low | **Similar-run lookup:** store an embedding per run / output and compare in Go (enough for tens of thousands of runs), to find runs like this one even with different words. | Builds on the FTS search. No native SQLite extension. |
| D10 | Low | **Playground and AI-designer history:** earlier conversations, wizard drafts, why each flow version was saved, searchable. | Same index. |
| D11 | Low | **Small settings files into the database:** `housekeeping.json`, `tool-tags.json`, `packages.json`, `profile.json` (safe saves, one backup). | Not urgent. |
| D12 | Medium | **Stop writing the duplicate files** (run summaries `runs/<id>.json`, `usage.jsonl`, `triggers/<ws>.json`) after one release, once older builds are not in use. | The database is read first already; the files are written for older builds. |
| D13 | Medium | **Import again what an older build wrote.** The import from files runs once; runs an older build records afterwards are not in the database. Re-import files newer than the database's newest run on open. | Only matters while both builds are used. |
| D14 | **Done (part)** | **Backups:** a copy of the history database a day while the app is open (`backups/history-YYYY-MM-DD.db`, newest 7 kept, consistent copy with `VACUUM INTO`); Settings › Runs shows its size, what it holds, the copies, and "Make a copy now". Not yet: the retention settings do not delete from the index; restoring is by hand (quit, put a copy in place of history.db). | |
| D15 | Low | **A shared PostgreSQL backend for teams** (one history for several people), behind the same store interface. | Only if a team asks. PGlite (Postgres in WebAssembly) is not a fit for the Go backend. |

**Stays as files (decided):** flows, project tools and run result folders (they belong to the project: git, agents, people read them); admin keys and trusted admins (security material); the secrets key (Keychain, or its private key file).

## Triggers

| Id | Priority | Idea | Notes |
|---|---|---|---|
| T1 | High | **Shared trigger settings:** conditions (only on a branch, during work hours, if the last run failed); limits (at most N runs per hour, a minimum gap). | With D3 (queue). |
| T2 | Medium | **Git events:** a new commit on a branch, a branch switch, pulled changes, a new tag; checked on the scheduler tick, no service needed. | Release notes on a tag, checks on new commits to `main`. |
| T3 | Medium | **Something new appears in a service:** new Jira issues matching a search, a new PR or issue, a failed CI build; polled with an existing tool every N minutes; one run per new item or per batch. | Needs D4. Better than webhooks for a desktop app (nothing has to reach the computer). |
| T4 | Medium | **A watched value changes:** run a tool on a schedule, start the flow only when its output changes or matches a condition ("open bugs > 20"). | Covers many services without a trigger each. |
| T5 | Low | **When the app or a project opens** (at most once a day): morning reports, refreshing dependencies. | |
| T6 | Low | **When a spec workflow step finishes** (Plan, Tasks): start a review flow. | |
| T7 | Low | **When an approval comes back:** a review or Improve step approved or rejected starts a follow-up flow. | |
| T8 | Low | **Manual presets:** a named button that starts a flow with set inputs (menu bar, command palette, a system shortcut). | |
| T9 | Low | **Webhook:** a local address with a token (saved like trigger secrets); the request body as inputs. | Most powerful, but security and reachability work (a tunnel for remote services). Last. |
| T10 | Low | **System events:** wake from sleep, network back, idle for N minutes (heavy flows while away). | macOS-specific. |
| T11 | Low | **Rename `schedule_flow`** to something like `trigger_flow`, now that it sets up every kind (keep the old name as an alias). | |

## Flows

| Id | Priority | Idea | Notes |
|---|---|---|---|
| F1 | Medium | **"Improve until done" for any section of steps**, not only the step before or after it. | The user asked for this "later" when the Improve step was added. |
