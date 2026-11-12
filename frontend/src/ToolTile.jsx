// Tool tiles: how tools are shown on the Tools screen and in the Development
// wizard. Each tool gets an icon and a color from what it does (database,
// API, tests, security, git, build, docs, search, code generation,
// messaging, cloud), worked out from its name, description, permissions and
// connections, plus a small chip for the language it runs in. Colors come
// from the theme's terminal palette, so they suit every theme.

const svg = (children) => function CatIcon() {
  return <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{children}</svg>;
};

export const CATEGORIES = [
  { id: "database", name: "Database", color: "--ansi-blue", re: /\b(sql|postgres|psql|mysql|maria|oracle|mongo|redis|database|\bdb\b|jdbc|flyway|liquibase|schema|query)/i,
    Icon: svg(<><ellipse cx="12" cy="5.5" rx="7" ry="2.8" /><path d="M5 5.5v6.5c0 1.5 3.1 2.8 7 2.8s7-1.3 7-2.8V5.5" /><path d="M5 12v6.5c0 1.5 3.1 2.8 7 2.8s7-1.3 7-2.8V12" /></>) },
  { id: "messaging", name: "Messaging", color: "--ansi-magenta", re: /\b(kafka|rabbit|amqp|queue|topic|pub\/?sub|event|sqs|sns|nats|stream)/i,
    Icon: svg(<><circle cx="5" cy="12" r="2.2" /><circle cx="19" cy="6" r="2.2" /><circle cx="19" cy="18" r="2.2" /><path d="M7.1 11l9.8-4M7.1 13l9.8 4" /></>) },
  { id: "security", name: "Security", color: "--ansi-red", re: /\b(secur|vuln|cve|owasp|sonar|audit|secret|sast|dast|snyk|trivy|scan for|license|gate)/i,
    Icon: svg(<><path d="M12 2.8l7.5 2.8v5.6c0 4.6-3.2 8.4-7.5 10-4.3-1.6-7.5-5.4-7.5-10V5.6z" /><path d="M8.8 12.2l2.2 2.2 4.4-4.6" /></>) },
  { id: "test", name: "Tests", color: "--ansi-green", re: /\b(test|junit|jest|vitest|pytest|spec runner|coverage|mutation|e2e|cypress|playwright)/i,
    Icon: svg(<><path d="M9.5 3h5M10.5 3v5.5L5 18.2A1.9 1.9 0 006.6 21h10.8a1.9 1.9 0 001.6-2.8L13.5 8.5V3" /><path d="M7.5 14.5h9" /></>) },
  { id: "git", name: "Git", color: "--ansi-yellow", re: /\b(git|branch|commit|merge|pull request|\bpr\b|diff|changelog|release)/i,
    Icon: svg(<><circle cx="6" cy="5" r="2.2" /><circle cx="6" cy="19" r="2.2" /><circle cx="18" cy="9" r="2.2" /><path d="M6 7.2v9.6M18 11.2c0 4-6 3.5-11 6" /></>) },
  { id: "api", name: "API & web", color: "--ansi-cyan", re: /\b(rest|http|api\b|graphql|grpc|webhook|jira|confluence|github|gitlab|slack|teams|url|endpoint|fetch|curl|servicenow)/i,
    Icon: svg(<><circle cx="12" cy="12" r="8.5" /><path d="M3.5 12h17M12 3.5c2.5 2.6 3.6 5.4 3.6 8.5S14.5 17.9 12 20.5M12 3.5C9.5 6.1 8.4 8.9 8.4 12s1.1 5.9 3.6 8.5" /></>) },
  { id: "cloud", name: "Cloud & deploy", color: "--syn-function", re: /\b(aws|azure|gcp|cloud|k8s|kube|helm|terraform|docker|container|deploy|lambda|s3\b)/i,
    Icon: svg(<path d="M7 18.5h10a4 4 0 00.6-8 5.5 5.5 0 00-10.6-1.4A4.6 4.6 0 007 18.5z" />) },
  { id: "build", name: "Build", color: "--warn", re: /\b(build|compile|maven|mvn|gradle|npm run|make\b|bundle|package|lint|format|prettier|eslint|checkstyle|spotless)/i,
    Icon: svg(<><path d="M14.5 5.5l4 4-9.5 9.5H5v-4z" /><path d="M12.5 7.5l4 4" /></>) },
  { id: "docs", name: "Docs & specs", color: "--info", re: /\b(doc|openapi|swagger|asyncapi|spec|readme|markdown|adr|diagram|wiki)/i,
    Icon: svg(<><path d="M6 2.8h8.5L19 7.3V21H6z" /><path d="M14 2.8V7.5h5M9 12h7M9 15.5h7M9 9h3" /></>) },
  { id: "generate", name: "Code generation", color: "--syn-keyword", re: /\b(gen|generat|scaffold|dto|codegen|template|stub|mock data|boilerplate)/i,
    Icon: svg(<><path d="M12 3l1.8 4.9L18.7 9.7 13.8 11.5 12 16.4 10.2 11.5 5.3 9.7 10.2 7.9z" /><path d="M18.5 15.5l.8 2 2 .8-2 .8-.8 2-.8-2-2-.8 2-.8z" /></>) },
  { id: "search", name: "Search", color: "--syn-property", re: /\b(find|search|grep|todo|fixme|lookup|look up|index|usages|locate)/i,
    Icon: svg(<><circle cx="10.5" cy="10.5" r="6" /><path d="M15 15l5.5 5.5" /></>) },
  { id: "terminal", name: "Command line", color: "--muted", re: /\b(cli|shell|bash|script|command|terminal|run )/i,
    Icon: svg(<><rect x="3" y="4.5" width="18" height="15" rx="2.5" /><path d="M7 10l3 2.5L7 15M12.5 15.5H17" /></>) },
];
const DEFAULT = { id: "tool", name: "Tool", color: "--activity-accent", Icon: svg(<path d="M14.7 6.3a4 4 0 00-5.2 5.2L3.8 17.2a1.5 1.5 0 002.1 2.1l5.7-5.7a4 4 0 005.2-5.2l-2.4 2.4-2.1-.6-.6-2.1z" />) };

const LANG = { "Node.js": "JS", Go: "GO", Java: "JV", Bash: "SH", Python: "PY", TypeScript: "TS" };

// The category a tool falls into: name first, then description, then what
// it connects to and its permissions.
export function toolCategory(t) {
  const fields = [t.name || "", t.desc || "", [...(t.connects || []), ...(t.perms || [])].join(" ")];
  for (const f of fields) {
    const c = CATEGORIES.find((x) => x.re.test(f.replace(/[-_]/g, " ")));
    if (c) return c;
  }
  if (t.kind === "npm") return CATEGORIES.find((c) => c.id === "build");
  return DEFAULT;
}

// built-in tools: by what they do (read, search, edit, run, web)
const BUILTIN_CAT = { read: "docs", search: "search", edit: "generate", execute: "terminal", web: "api" };
function categoryOf(t) {
  if (t.kind === "builtin") return CATEGORIES.find((c) => c.id === BUILTIN_CAT[t.lang]) || DEFAULT;
  return toolCategory(t);
}

export function ToolIcon({ t, size = 40 }) {
  const c = categoryOf(t), lang = t.kind === "npm" ? "npm" : t.kind === "copilot" ? "CP" : t.kind === "builtin" ? "SW" : LANG[t.lang] || "";
  return (
    <span className="tool-icon" style={{ "--tc": `var(${c.color})`, width: size, height: size }} title={c.name}>
      <c.Icon />
      {lang && <span className="tool-lang">{lang}</span>}
      {t.managed && <span className="tool-lock" title="Managed by admin"><svg viewBox="0 0 16 16" width="10" height="10" fill="currentColor"><path d="M4.5 7V5.2a3.5 3.5 0 017 0V7h.5a1 1 0 011 1v5a1 1 0 01-1 1H4a1 1 0 01-1-1V8a1 1 0 011-1zm1.5 0h4V5.2a2 2 0 00-4 0z" /></svg></span>}
    </span>
  );
}

function Status({ t }) {
  if (t.managed) return <span className={"badge " + (t.managed.status === "verified" ? "badge-info" : "badge-warn")}>{t.managed.status === "verified" ? "v" + t.managed.version : "blocked"}</span>;
  const s = t.status || "";
  return <span className={"badge " + (s === "tested" ? "badge-ok" : s === "in review" || s === "needs approval" ? "badge-warn" : "")}>{s}</span>;
}

// One tile. `compact` is the sidebar version (two per row, no actions).
export function ToolTile({ t, active, onOpen, action, compact }) {
  return (
    <div className={"tool-tile" + (compact ? " compact" : "") + (active ? " active" : "") + (t.managed ? " managed" : "")} role="button" tabIndex={0}
      title={`${t.name}: ${t.desc || ""}`} onClick={onOpen} onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), onOpen?.())}>
      <div className="tt-top"><ToolIcon t={t} size={compact ? 34 : 42} /><Status t={t} /></div>
      <b className="tt-name">{t.name}</b>
      {t.desc && <small className="tt-desc">{t.desc}</small>}
      {!compact && <div className="tt-foot"><span className="muted">{toolCategory(t).name}{(t.params || []).length ? ` · ${t.params.length} parameter${t.params.length === 1 ? "" : "s"}` : ""}{(t.secrets || []).length ? ` · ${t.secrets.length} secret${t.secrets.length === 1 ? "" : "s"}` : ""}</span>{action}</div>}
    </div>
  );
}

// A titled grid of tiles; shows `empty` when there are none.
export function ToolGrid({ title, tools, compact, empty = "None yet", render }) {
  return (
    <section className={"tool-group" + (compact ? " compact" : "")}>
      {title && <div className="tree-folder">{title} · {tools.length}</div>}
      {tools.length ? <div className={"tool-grid" + (compact ? " compact" : "")}>{tools.map(render)}</div> : <p className="muted tool-empty">{empty}</p>}
    </section>
  );
}
