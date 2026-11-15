// A stand-in for core.Hub so the UI runs in a plain browser (npm run dev).
// It mirrors the Go API method-for-method and emits events with the same
// flattened shape as bus.Event, driven by a scripted agent.

import { stepNames, isStepField, isLoopField, applyStepIDs } from "./flowNames.js";
import { shapes as flowShapes, parseSchema, checkPaths, sampleOf, toolOutput } from "./schema.js";
import { COND_OPS, AGG_FNS, condCheck, condTemplates, filterRows, aggregateRows, aggregateCheck, decide, branchLabel, liveParents } from "./flowRows.js";
import { BUILTINS } from "./builtins.generated.js";

// built-in tools as flow tools ("builtin:<name>"), shaped like project tools (mirrors core.FlowBuiltinTools)
const NOT_IN_FLOWS = new Set(["todo_list", "get_command_output"]);
function flowBuiltins() {
  return BUILTINS.filter((t) => !NOT_IN_FLOWS.has(t.name)).map((t) => {
    const props = t.inputSchema?.properties || {}, req = new Set(t.inputSchema?.required || []);
    const names = Object.keys(props).sort((a, b) => (req.has(b) - req.has(a)) || a.localeCompare(b));
    return { name: "builtin:" + t.name, desc: t.title + ": " + t.description, kind: "builtin", scope: "builtin", status: "tested", approved: true, lang: t.group,
      params: names.map((n) => ({ name: n, type: props[n].type || "string", required: req.has(n), ...(props[n].enum ? { enum: props[n].enum } : {}), desc: (props[n].description || "") + (props[n].type === "array" ? " (one per line, or a JSON array)" : props[n].type === "object" ? " (JSON)" : "") })),
      secrets: [], perms: t.readOnly ? [] : ["writes"], ...(t.outputBy ? { outputBy: t.outputBy } : {}) };
  });
}

const KEY = "codegen.mock.v1";
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const clone = (x) => JSON.parse(JSON.stringify(x));
const now = () => new Date().toISOString();

const FILES = {
  "pom.xml": `<project>\n  <modelVersion>4.0.0</modelVersion>\n  <groupId>com.acme</groupId>\n  <artifactId>orders-service</artifactId>\n  <version>0.1.0</version>\n  <properties>\n    <java.version>21</java.version>\n  </properties>\n</project>`,
  "package.json": `{\n  "name": "orders-service-tools",\n  "private": true,\n  "scripts": {\n    "ext:openapi-lint": "spectral lint api/openapi.yaml"\n  }\n}`,
  "api/openapi.yaml": `openapi: 3.0.3\ninfo:\n  title: Orders\n  version: 0.1.0\npaths:\n  /orders:\n    post:\n      summary: Place an order`,
  "inputs/context-diagram.drawio": `<mxfile><diagram name="context">Customer → Orders → Stock, Kafka</diagram></mxfile>`,
  ".codegen/skills/global/acme-java-style/SKILL.md": "---\nname: acme-java-style\ndescription: Corporate coding standard and review checklist\n---\n",
  ".codegen/skills/global/security-baseline/SKILL.md": "---\nname: security-baseline\ndescription: OWASP rules, secret scanning, dependency policy\n---\n",
  ".codegen/skills/global/logging-tracing/SKILL.md": "---\nname: logging-tracing\ndescription: Structured logs and OpenTelemetry conventions\n---\n",
  ".codegen/skills/project/orders-domain.md": "# Domain glossary and aggregate rules for Orders\n",
  ".codegen/skills/project/api-contracts.md": "# OpenAPI conventions for this team\n",
  ".codegen/scripts/gen-dto.sh": "#!/usr/bin/env bash\n# Generates DTOs from the OpenAPI file\n",
  "src/main/java/com/acme/orders/domain/OrderStatus.java": `package com.acme.orders.domain;\n\npublic enum OrderStatus { NEW, PLACED, REJECTED }`,
  "src/main/java/com/acme/orders/ports/OrderRepository.java": `package com.acme.orders.ports;\n\nimport com.acme.orders.domain.Order;\n\npublic interface OrderRepository {\n    Order save(Order order);\n}`,
  "src/main/java/com/acme/orders/ports/StockPort.java": `package com.acme.orders.ports;\n\nimport com.acme.orders.domain.Order;\n\npublic interface StockPort {\n    boolean reserve(Order order);\n}`,
};

const WRITES = {
  ".specify/memory/constitution.md": (inp) => `# Orders Service Constitution\n\n## Core principles\n1. Every public service method has a unit test.\n2. No secrets in source; read them from the vault at run time.\n3. Follow acme-java-style.\n${inp ? `\n## From the team\n- ${inp}\n` : ""}\n## Governance\nChanges to this file need two approvals.`,
  "specs/001-orders/spec.md": (inp) => `# Feature: Place an order\n\n## User story\n${inp || "As a customer I can place an order so that stock is reserved."}\n\n## Acceptance\n- Given stock, when I order, then an OrderPlaced event is emitted.\n- Given no stock, the order is rejected with a reason.\n- [NEEDS CLARIFICATION] partial shipments\n- [NEEDS CLARIFICATION] currency`,
  "specs/001-orders/plan.md": (inp) => `# Plan\n\n- ${inp || "Java 21, Spring Boot 3"}\n- PostgreSQL via Spring Data JPA\n- Kafka for OrderPlaced\n- Hexagonal: domain / application / adapters\n\nConstitution check: passed`,
  "specs/001-orders/data-model.md": () => `# Data model\n\n- Order(id, lines, status)\n- OrderLine(sku, quantity, unitPrice)`,
  "specs/001-orders/tasks.md": () => `# Tasks\n\n- [ ] T001 Create Order aggregate\n- [ ] T002 [P] OrderRepository port\n- [ ] T003 PlaceOrderService with stock check\n- [ ] T004 Kafka publisher adapter\n- [ ] T005 REST controller`,
  "src/main/java/com/acme/orders/domain/Order.java": () => `package com.acme.orders.domain;\n\nimport java.math.BigDecimal;\nimport java.util.List;\nimport java.util.UUID;\n\n/** Order aggregate. Generated from specs/001-orders. */\npublic final class Order {\n    private final UUID id;\n    private final List<OrderLine> lines;\n    private OrderStatus status = OrderStatus.NEW;\n\n    public Order(UUID id, List<OrderLine> lines) {\n        if (lines.isEmpty()) throw new IllegalArgumentException("an order needs at least one line");\n        this.id = id;\n        this.lines = List.copyOf(lines);\n    }\n\n    public BigDecimal total() {\n        return lines.stream().map(OrderLine::subtotal).reduce(BigDecimal.ZERO, BigDecimal::add);\n    }\n\n    public void place() { this.status = OrderStatus.PLACED; }\n    public UUID id() { return id; }\n    public OrderStatus status() { return status; }\n}`,
  "src/main/java/com/acme/orders/domain/OrderLine.java": () => `package com.acme.orders.domain;\n\nimport java.math.BigDecimal;\n\npublic record OrderLine(String sku, int quantity, BigDecimal unitPrice) {\n    public BigDecimal subtotal() {\n        return unitPrice.multiply(BigDecimal.valueOf(quantity));\n    }\n}`,
  "src/main/java/com/acme/orders/application/PlaceOrderService.java": () => `package com.acme.orders.application;\n\nimport com.acme.orders.domain.Order;\nimport com.acme.orders.ports.OrderRepository;\nimport com.acme.orders.ports.StockPort;\n\npublic class PlaceOrderService {\n    private final OrderRepository orders;\n    private final StockPort stock;\n\n    public PlaceOrderService(OrderRepository orders, StockPort stock) {\n        this.orders = orders;\n        this.stock = stock;\n    }\n\n    // Rule R-014: reject when any line is out of stock\n    public Order place(Order order) {\n        if (!stock.reserve(order)) {\n            throw new IllegalStateException("out of stock");\n        }\n        order.place();\n        return orders.save(order);\n    }\n}`,
};

const SCRIPT = {
  constitution: [["Read", ".codegen/skills/global/acme-java-style/SKILL.md"], ["Write", ".specify/memory/constitution.md"], ["say", "Drafted a constitution with 3 core principles (testing, secrets, style) and a governance section."]],
  specify: [["Read", "inputs/context-diagram.drawio"], ["Write", "specs/001-orders/spec.md"], ["say", "Wrote the spec: 1 user story, 2 acceptance scenarios. Marked 2 items [NEEDS CLARIFICATION]: partial shipments, currency."]],
  clarify: [["say", "Q1: Are partial shipments allowed?  Q2: Single currency or multi-currency?"], ["Edit", "specs/001-orders/spec.md"], ["say", "Recorded your answers in the Clarifications section."]],
  plan: [["Read", "specs/001-orders/spec.md"], ["Write", "specs/001-orders/plan.md"], ["Write", "specs/001-orders/data-model.md"], ["say", "Plan: Java 21, Spring Boot 3, PostgreSQL, Kafka, hexagonal layout. Constitution check passed."]],
  tasks: [["Write", "specs/001-orders/tasks.md"], ["say", "Broke the plan into 5 tasks, 1 parallel. Tests come first for each service."]],
  analyze: [["Read", "specs/001-orders/spec.md"], ["Read", "specs/001-orders/plan.md"], ["Read", "specs/001-orders/tasks.md"], ["say", "Analysis: 0 critical, 1 medium (FR-004 has no task), 2 low. No constitution conflicts. Ready to implement after adding a task for FR-004."]],
  implement: [["Write", "src/main/java/com/acme/orders/domain/Order.java"], ["Write", "src/main/java/com/acme/orders/domain/OrderLine.java"], ["Write", "src/main/java/com/acme/orders/application/PlaceOrderService.java"], ["ext", "gen-dto"], ["say", "Implemented T001–T003. T004–T005 are left for the next pass."]],
};

const step = (id, title, ask, ph, tpl) => ({ id, title, cmd: "/speckit." + id, ask, placeholder: ph, template: tpl, input: "", instructions: "", feedback: "", options: { rules: true, global: true, project: true, diagrams: true, tools: false }, gate: true, retries: 1, status: "pending", lastRun: "" });
const specKit = () => ({ version: 1, id: "speckit", name: "Spec workflow", budget: { maxTokens: 2000000, maxUsd: 10, maxPremiumRequests: 30 }, steps: [
  step("constitution", "Constitution", "Project principles (code quality, testing, security, performance)", "e.g. Every public service has unit tests; no secrets in code; follow ACME Java style.", "Keep principles short and testable. Reuse corporate rules instead of restating them."),
  step("specify", "Specify", "What to build and why (no tech stack yet)", "e.g. An order service that accepts orders, validates stock and emits OrderPlaced events.", "Describe what and why only. Mark anything unknown as [NEEDS CLARIFICATION]."),
  step("clarify", "Clarify", "Answers to open questions, or areas to probe", "e.g. Partial shipments are allowed; currency is always EUR.", "Ask at most 5 questions, highest impact first. Record answers in the spec."),
  step("plan", "Plan", "Tech stack and architecture choices", "e.g. Java 21, Spring Boot 3, PostgreSQL, Kafka, hexagonal layout.", "Check the plan against the constitution. Prefer the corporate golden path."),
  step("tasks", "Tasks", "Constraints for the task breakdown", "e.g. Tests first; keep tasks under half a day; mark parallel tasks.", "Order tasks by dependency, tests first. Mark tasks that can run in parallel with [P]."),
  { ...step("analyze", "Analyze", "What to check before implementation", "e.g. Every requirement has tasks; the plan follows the constitution.", "Read-only: report gaps, duplicates and conflicts across spec, plan and tasks, constitution conflicts first."), input: "Check spec.md, plan.md and tasks.md for gaps, ambiguities and conflicts with the constitution before implementation." },
  step("implement", "Implement", "Scope for this implementation pass", "e.g. Implement phase 1 and 2 tasks only.", "Work task by task, run the build after each one, stop on the first failure."),
] });

const ext = (o) => ({ kind: "script", scope: "project", status: "tested", lang: "Bash", params: [], secrets: [], perms: ["read files"], revision: 1, approved: true, ...o });
function seed() {
  return {
    root: "~/src/orders-service",
    profile: { lang: "Java", build: "Maven", global: ["global:skill:acme-java-style", "global:skill:security-baseline"], globalMode: "incremental", project: ["project:skill:orders-domain", "project:script:gen-dto.sh"], projectMode: "incremental",
      folders: [{ path: ".codegen/rules", note: "Functionality rules (exportable)" }, { path: ".codegen/skills/global", note: "Corporate skills, read-only copy" }, { path: ".codegen/skills/project", note: "Project skills" }, { path: ".codegen/scripts", note: "Deterministic scripts" }, { path: ".codegen/tools", note: "Tools and extensions" }],
      foldersMode: "incremental", diagrams: { context: "inputs/context-diagram.drawio" },
      rules: [{ id: "R-001", text: "Every aggregate exposes an id() accessor" }, { id: "R-007", text: "Monetary values use BigDecimal, never double" }, { id: "R-014", text: "Reject an order when any line is out of stock" }] },
    wf: specKit(),
    files: clone(FILES),
    exts: [
      ext({ name: "gen-dto", desc: "Regenerate DTOs from the OpenAPI file.", params: [{ name: "spec", type: "string", required: true, desc: "Path to openapi.yaml" }], perms: ["read files", "write src/"], command: "bash .codegen/tools/project/gen-dto.sh {{spec}}", scriptPath: ".codegen/tools/project/gen-dto.sh", script: "#!/usr/bin/env bash\nset -euo pipefail\nopenapi-generator generate -i \"$1\" -g java -o build/generated --global-property models", sampleTest: "Regenerate the DTOs", testPrompt: "Regenerate the DTOs", call: { spec: "api/openapi.yaml" }, sampleOutput: "Generated 6 model classes in build/generated" }),
      ext({ name: "sonar-scan", desc: "Run the corporate Sonar quality gate and return blocking issues.", scope: "global", secrets: [{ name: "SONAR_TOKEN", desc: "Sonar token" }], perms: ["read files", "network: sonar.acme.internal"], command: "bash .codegen/tools/global/sonar-scan.sh", script: "#!/usr/bin/env bash\nset -euo pipefail\n./mvnw -q sonar:sonar -Dsonar.token=\"$SONAR_TOKEN\" -Dsonar.qualitygate.wait=true -Dsonar.format=json", sampleTest: "Does this pass the quality gate?", testPrompt: "Does this pass the quality gate?", call: {},
        sampleOutput: JSON.stringify({ gate: "FAILED", summary: { errors: 2, warnings: 5 }, issues: [{ rule: "java:S2259", severity: "BLOCKER", file: "src/main/java/com/acme/orders/domain/Order.java", line: 42, message: "Possible null pointer" }, { rule: "java:S1192", severity: "MINOR", file: "src/main/java/com/acme/orders/api/OrderController.java", line: 18, message: "Duplicated string literal" }] }, null, 2),
        output: { type: "object", properties: { gate: { type: "string", enum: ["PASSED", "FAILED"], description: "quality gate result" }, summary: { type: "object", properties: { errors: { type: "integer", description: "blocking issues" }, warnings: { type: "integer" } } },
          issues: { type: "array", items: { type: "object", properties: { rule: { type: "string" }, severity: { type: "string", enum: ["BLOCKER", "CRITICAL", "MAJOR", "MINOR", "INFO"] }, file: { type: "string", description: "path in the project" }, line: { type: "integer" }, message: { type: "string" } } } } }, required: ["gate", "summary", "issues"] } }),
    ],
    runs: [], events: {},
    pkg: { proxy: { http: "", https: "", noProxy: "" }, caFile: "", npm: { registry: "" }, pip: { indexUrl: "", extraIndexUrl: "", trustedHost: "" }, go: { proxy: "", private: "", noSumDb: "", sumDb: "", insecure: "", toolchain: "" }, maven: { mirrorUrl: "", mirrorOf: "" }, applyToAgents: false },
    ledger: [
      { at: "2026-11-04T15:00:00Z", workspace: "~/src/billing-api", step: "implement", state: "done", steps: 6, files_created: 23, input_tokens: 412000, output_tokens: 38000, cost_usd: 2.41, premium_requests: 0, model_calls: 61 },
      { at: "2026-11-02T10:00:00Z", workspace: "~/src/inventory-sync", step: "tasks", state: "failed", steps: 4, files_created: 9, input_tokens: 150000, output_tokens: 12000, cost_usd: 0.88, premium_requests: 0, model_calls: 22 },
    ],
  };
}

const HOURS = { constitution: 0.5, specify: 3, clarify: 1.5, plan: 4, tasks: 2, analyze: 1, implement: 14 };

// A minimal zip writer (stored, no compression) for the preview's download.
const CRC_T = (() => { const t = new Uint32Array(256); for (let n = 0; n < 256; n++) { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; t[n] = c >>> 0; } return t; })();
const crc32 = (d) => { let c = 0xffffffff; for (let i = 0; i < d.length; i++) c = CRC_T[(c ^ d[i]) & 0xff] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
function zipStore(entries) {
  const enc = new TextEncoder(), parts = [], central = []; let off = 0;
  const le = (n, b) => { const a = new Uint8Array(b); for (let i = 0; i < b; i++) a[i] = (n >>> (8 * i)) & 0xff; return a; };
  for (const e of entries) {
    const name = enc.encode(e.name), crc = crc32(e.data), sz = e.data.length;
    const head = [le(0x04034b50, 4), le(20, 2), le(0x0800, 2), le(0, 2), le(0, 2), le(0x21, 2), le(crc, 4), le(sz, 4), le(sz, 4), le(name.length, 2), le(0, 2), name];
    central.push([le(0x02014b50, 4), le(20, 2), le(20, 2), le(0x0800, 2), le(0, 2), le(0, 2), le(0x21, 2), le(crc, 4), le(sz, 4), le(sz, 4), le(name.length, 2), le(0, 2), le(0, 2), le(0, 2), le(0, 2), le(0, 4), le(off, 4), name]);
    parts.push(...head, e.data); off += head.reduce((n, a) => n + a.length, 0) + sz;
  }
  const cd = central.flat(), cdLen = cd.reduce((n, a) => n + a.length, 0);
  const all = [...parts, ...cd, le(0x06054b50, 4), le(0, 2), le(0, 2), le(entries.length, 2), le(entries.length, 2), le(cdLen, 4), le(off, 4), le(0, 2)];
  const out = new Uint8Array(all.reduce((n, a) => n + a.length, 0)); let p = 0; for (const a of all) { out.set(a, p); p += a.length; } return out;
}

export function createMockBackend(methods) {
  let db; try { db = JSON.parse(localStorage.getItem(KEY) || "null"); } catch { db = null; }
  if (!db || !db.wf) db = seed();
  if (db.wf.name === "Spec Kit") db.wf.name = "Spec workflow";
  // Analyze (between Tasks and Implement) for workflows saved before it existed
  if (db.wf.id === "speckit" && !db.wf.steps.some((x) => x.id === "analyze")) { const k = db.wf.steps.findIndex((x) => x.id === "implement"); if (k > 0) db.wf.steps.splice(k, 0, { ...specKit().steps.find((x) => x.id === "analyze"), options: { ...db.wf.steps[0].options }, status: db.wf.steps[k].status === "done" ? "done" : "pending" }); }
  const persist = () => { try { localStorage.setItem(KEY, JSON.stringify(db)); } catch { /* ignore */ } };
  const listeners = new Set();
  const secrets = {};
  // environment variables the preview pretends are exported in ~/.zshrc
  const ENV = { SONAR_TOKEN: "sonar-preview-token", PGHOST: "localhost", PGUSER: "orders_ro" };
  const pendingAsks = new Map();
  const pendingPerms = new Map();
  const cxProject = {}; // Copilot extensions loaded into the preview project
  const infoOf = (s) => {
    const src = s.source || "auto", env = s.env || s.name, envSet = env in ENV, inSession = s.name in secrets;
    const via = src === "env" ? (envSet ? "env" : "missing") : src === "session" ? (inSession ? "session" : "missing") : src === "ask" ? (inSession ? "session" : "ask") : inSession ? "session" : envSet ? "env" : "ask";
    return { name: s.name, source: src, env, envSet, via };
  };
  // like Hub.ResolveSecrets: session → env → ask the user through the UI
  const resolveSecrets = async (tool, list) => {
    const vals = {}, ask = [];
    for (const s of list || []) {
      const st = infoOf(s);
      if (st.via === "session") vals[s.name] = secrets[s.name];
      else if (st.via === "env") vals[s.name] = ENV[st.env];
      else if (st.via === "ask") ask.push(s);
      else throw new Error(st.source === "env" ? `environment variable ${st.env} is not set (needed for ${s.name})` : `${s.name} has no value for this session`);
    }
    if (ask.length) {
      const id = Math.random().toString(16).slice(2);
      const answer = await new Promise((res) => { pendingAsks.set(id, res); emit({ type: "secret.request", id, tool: tool.name, secrets: ask, reason: tool.desc, summary: `${tool.name} needs ${ask.map((x) => x.name).join(", ")}` }); });
      pendingAsks.delete(id);
      emit({ type: "secret.resolved", id, ok: !answer.cancel });
      if (answer.cancel) throw new Error("you declined to provide " + ask.map((x) => x.name).join(", "));
      if (answer.remember) Object.assign(secrets, answer.values);
      Object.assign(vals, answer.values);
    }
    return vals;
  };
  let agent = "claude", live = null;
  const emit = (e) => { e.ts ||= now(); listeners.forEach((f) => f(e)); };

  const libraries = () => {
    const out = [];
    for (const p of Object.keys(db.files)) {
      let m;
      if ((m = p.match(/^\.codegen\/skills\/(global|project)\/([^/]+)\/SKILL\.md$/))) {
        const dir = `.codegen/skills/${m[1]}/${m[2]}`;
        const files = Object.keys(db.files).filter((f) => f.startsWith(dir + "/") && f !== p && !f.slice(dir.length + 1).split("/").some((x) => x.startsWith("."))).map((f) => f.slice(dir.length + 1)).sort();
        out.push({ id: `${m[1]}:skill:${m[2]}`, name: m[2], kind: "skill", scope: m[1], path: p, dir, files, note: (db.files[p].match(/description: (.*)/) || [])[1] || (db.files[p].split("\n").find((x) => x.trim() && x.trim() !== "---") || "").replace(/^#+\s*/, "") });
      }
      else if ((m = p.match(/^\.codegen\/skills\/(global|project)\/([^/]+)\.md$/))) out.push({ id: `${m[1]}:skill:${m[2]}`, name: m[2], kind: "skill", scope: m[1], path: p, note: db.files[p].replace(/^#\s*/, "").split("\n")[0] });
      else if ((m = p.match(/^\.codegen\/scripts\/([^/]+)$/))) out.push({ id: `project:script:${m[1]}`, name: m[1], kind: "script", scope: "project", path: p, note: (db.files[p].split("\n")[1] || "").replace(/^#\s*/, "") });
    }
    return out;
  };
  // --- admin (browser preview: no real crypto; the desktop app signs with Ed25519) ---
  const AKEY = "codegen.mock.admin.v1";
  let adm; try { adm = JSON.parse(localStorage.getItem(AKEY) || "null"); } catch { adm = null; }
  adm ||= { key: null, managed: [
    { name: "corp-sonar-gate", desc: "Run the corporate Sonar quality gate on this project.", kind: "script", lang: "Bash", params: [], secrets: [{ name: "SONAR_TOKEN", desc: "Sonar token", source: "" }], perms: ["read files", "network: sonar.acme.internal"],
      command: "bash .codegen/tools/corp-sonar-gate.sh", script: "#!/usr/bin/env bash\nset -euo pipefail\n./mvnw -q sonar:sonar -Dsonar.token=\"$SONAR_TOKEN\" -Dsonar.qualitygate.wait=true && echo 'Quality gate: PASSED'\n", sampleTest: "Does this pass the quality gate?", call: {}, sampleOutput: "Quality gate: PASSED (0 blocking issues)",
      managed: { version: 3, signedBy: "sw1-4f2a9c1e7b3d5a60", signer: "Platform team", signedAt: "2026-11-02T09:00:00Z", status: "verified" } },
  ] };
  let unlocked = false;
  adm.libs ||= [];
  // corporate libraries: copy the catalog into the project, drop retired ones
  const syncLibs = () => {
    db.mlibs ||= [];
    const clear = (n) => { for (const f of Object.keys(db.files)) if (f.startsWith(`.codegen/skills/global/${n}/`)) delete db.files[f]; };
    for (const l of adm.libs) {
      if (db.mlibs.includes(l.name)) clear(l.name);
      db.files[`.codegen/skills/global/${l.name}/SKILL.md`] = l.content;
      for (const [f, c] of Object.entries(l.files || {})) db.files[`.codegen/skills/global/${l.name}/${f}`] = c;
    }
    for (const n of db.mlibs) if (!adm.libs.some((l) => l.name === n)) clear(n);
    db.mlibs = adm.libs.map((l) => l.name); persist();
  };
  const annotated = () => { syncLibs(); return libraries().map((l) => { const m = l.scope === "global" && adm.libs.find((x) => x.name === l.name); return m ? { ...l, note: m.desc || l.note, managed: { version: m.version, signer: m.signer, signedAt: m.signedAt, status: "verified", source: "app" } } : l; }); };
  const savAdm = () => { try { localStorage.setItem(AKEY, JSON.stringify(adm)); } catch { /* ignore */ } };
  const managedView = () => adm.managed.map((t) => ({ ...clone(t), scope: "managed", status: "managed", approved: true }));
  const isManaged = (n) => adm.managed.some((t) => t.name === n);
  const refuse = (n) => { throw new Error(`${n} is a managed tool published by an admin; it cannot be changed here`); };
  const status = () => ({ hasKey: !!adm.key, keyId: adm.key?.id, name: adm.key?.name, publicKey: adm.key?.pub, unlocked, keyTrusted: !!adm.key,
    trusted: [{ id: "sw1-4f2a9c1e7b3d5a60", name: "Platform team", source: "policy", publicKey: "" }, ...(adm.key ? [{ id: adm.key.id, name: adm.key.name, source: "local", publicKey: adm.key.pub }] : [])],
    policyPath: "/Library/Application Support/SpecWright/policy.json", policyFound: true, allowLocal: true,
    managedFile: "~/Library/Application Support/codegen/managed-tools.json", machineCatalog: "/Library/Application Support/SpecWright/managed-tools.json" });
  const hash = (x) => [...x].reduce((a, c) => (a * 31 + c.charCodeAt(0)) >>> 0, 7).toString(16);

  const extensions = () => {
    const list = clone(db.exts);
    const pkg = (() => { try { return JSON.parse(db.files["package.json"] || "{}"); } catch { return {}; } })();
    for (const [k, v] of Object.entries(pkg.scripts || {})) {
      if (!k.startsWith("ext:")) continue;
      const name = k.slice(4), saved = list.find((e) => e.name === name);
      if (saved) { if (saved.script !== v) { saved.status = "needs approval"; saved.approved = false; } continue; }
      list.push({ name, desc: `npm script ${k}: ${v}`, kind: "npm", scope: "project", status: "needs approval", lang: "Node.js", command: "npm run --silent " + k, script: v, params: [], secrets: [], perms: ["runs Node.js"], revision: 1, approved: false });
    }
    for (const n of Object.keys(cxProject)) {
      if (n !== "html-formatter" || list.some((e) => e.name === "format_html")) continue;
      list.push({ name: "format_html", desc: "Format an existing HTML file in the current workspace with Prettier. Modifies the file in place; accepts a workspace-relative or absolute path.", kind: "copilot", scope: "project", status: "untested", lang: "Node.js",
        command: "copilot-extension .github/extensions/html-formatter/extension.mjs format_html", tool: "format_html", entry: "extension.mjs", dir: ".github/extensions/html-formatter", deps: ["prettier@3.6.2"],
        params: [{ name: "path", type: "string", required: true, desc: "Path to an existing .html or .htm file inside the current workspace." }], secrets: [], perms: ["runs Node.js", "Copilot extension html-formatter"], revision: 1, approved: true,
        call: { path: "docs/report.html" }, sampleOutput: "Formatted: docs/report.html", sampleTest: "Format docs/report.html" });
    }
    return [...managedView(), ...list.filter((e) => !isManaged(e.name))];
  };
  const wsState = () => ({ root: db.root, name: db.root.split("/").pop(), profile: db.profile, libraries: annotated(), workflow: clone(db.wf), extensions: extensions(), fileCount: Object.keys(db.files).length, specKit: !!skInfo().initialized, speckit: skInfo(), projects: mockProjects() });
  // several Spec Kit projects per workspace, each with its own workflow (mirrors core/projects.go)
  const wfKey = (p) => (p ? "speckit@" + p : "speckit");
  function mockProjects() {
    const cur = db.profile.specProject || "", names = new Set();
    for (const f of Object.keys(db.files)) { const m = f.match(/^(?:([A-Za-z0-9][A-Za-z0-9._-]*)\/)?\.specify\//); if (m) names.add(m[1] || ""); }
    if (cur) names.add(cur);
    return [...names].sort((a, b) => (a === "") !== (b === "") ? (a === "" ? -1 : 1) : a.localeCompare(b)).map((n) => {
      const w = n === cur ? db.wf : (db.wfs || {})[wfKey(n)], done = w ? w.steps.filter((x) => x.status === "done").length : 0;
      return { name: n, label: n || db.root.split("/").pop() + " (top folder)", current: n === cur, done, steps: w ? w.steps.length : 0, next: w ? (w.steps.find((x) => x.status !== "done")?.title || "") : "" };
    });
  }
  function selectProject(name) {
    if (live) throw new Error("finish or cancel the running step before switching project");
    const cur = db.profile.specProject || "";
    if (cur === name && db.wf.id === wfKey(name)) return;
    db.wfs ||= {}; db.wfs[wfKey(cur)] = db.wf;
    let w = db.wfs[wfKey(name)];
    if (!w) { // a new project: the same choices, every step pending
      w = specKit(); w.id = wfKey(name); w.model = db.wf.model; w.budget = clone(db.wf.budget); w.context = clone(db.wf.context || {});
      for (const st of w.steps) { const o = db.wf.steps.find((x) => x.id === st.id); if (o) Object.assign(st, { options: clone(o.options), gate: o.gate, retries: o.retries }); }
    }
    db.wf = w; db.profile.specProject = name; persist();
  }
  // Spec Kit (mirrors internal/speckit): the bundled v1.4.0 template
  const SK_STEPS = ["analyze", "checklist", "clarify", "constitution", "converge", "implement", "plan", "specify", "tasks"];
  const SK_CORE = [".specify/.gitignore", ".specify/memory/.constitution-template.json", ".specify/memory/constitution.md", ...["check-prerequisites", "common", "create-new-feature", "resolve-template", "setup-plan", "setup-tasks"].map((n) => `.specify/scripts/bash/${n}.sh`),
    ...["checklist", "constitution", "plan", "spec", "tasks"].map((n) => `.specify/templates/${n}-template.md`), ".specify/workflows/speckit/workflow.yml"];
  const skDir = (i) => (i === "copilot" ? ".github/skills" : ".claude/skills");
  const pd = () => (db.profile.specProject ? db.profile.specProject + "/" : ""); // the Spec Kit project folder
  const up = (p) => (pd() && p && !p.startsWith(pd()) ? "../" + p : p && pd() ? p.slice(pd().length) : p);
  // the Spec Kit download (the app fetches it through the proxy; the preview pretends)
  const skDl = { running: false, progress: "", error: "" };
  const skDownload = () => {
    if (skDl.running) return skDl.promise;
    Object.assign(skDl, { running: true, error: "", progress: "Looking up the latest Spec Kit release…" });
    skDl.promise = (async () => {
      for (const l of ["Downloading Spec Kit 1.4.0 with uv from https://github.com/github/spec-kit/archive/refs/tags/v1.4.0.zip…", "Running specify init for claude…", "Running specify init for copilot…"]) { skDl.progress = l; await wait(900); }
      db.speckitAvailable = "1.4.0"; persist(); Object.assign(skDl, { running: false, progress: "" });
    })();
    return skDl.promise;
  };
  const dlInfo = () => ({ available: db.speckitAvailable || "", downloading: skDl.running, progress: skDl.progress, error: skDl.error });
  function skInfo() { return { ...skInfoProject(), ...dlInfo() }; }
  function skInfoProject() {
    const ij = db.files[pd() + ".specify/integration.json"], project = db.profile.specProject || "";
    if (!ij && !Object.keys(db.files).some((f) => f.startsWith(pd() + ".specify/"))) return { initialized: false, project };
    const j = ij ? JSON.parse(ij) : { installed_integrations: ["claude"], integration: "claude", version: "0.x" };
    const c = db.files[pd() + ".specify/memory/constitution.md"] || "";
    return { project, initialized: true, version: j.version, integration: j.integration, integrations: j.installed_integrations, constitution: !!c && !c.includes("[PROJECT_NAME]") };
  }
  const CTX_BEGIN = "<!-- SPECWRIGHT:BEGIN — managed by SpecWright (Choose input); edits inside this block are replaced -->", CTX_END = "<!-- SPECWRIGHT:END -->";
  function writeAgentContext() {
    const info = skInfo(); if (!info.initialized) return [];
    const p = db.profile, c = db.wf.context || {}, has = (l, v) => !l || l.includes(v), libs = libraries();
    const lines = ["## Project context (from SpecWright)", "", "This project follows spec-driven development with GitHub Spec Kit (`.specify/`). Run one `speckit-*` step at a time and keep to its scope; the constitution is `.specify/memory/constitution.md` and feature specs live in `specs/`.", "", `Language: ${p.lang} · build: ${p.build}`];
    const rules = p.rules.filter((r) => has(c.rules, r.id)); if (rules.length) lines.push("", "### Rules", ...rules.map((r) => `- ${r.id}: ${r.text}`));
    const g = libs.filter((l) => p.global.includes(l.id)), pr = libs.filter((l) => p.project.includes(l.id));
    if (g.length) lines.push("", "### Corporate skills (read before you start)", ...g.map((l) => `- ${l.name} (${l.kind}): \`${up(l.path)}\``));
    if (pr.length) lines.push("", "### Project skills and scripts", ...pr.map((l) => `- ${l.name} (${l.kind}): \`${up(l.path)}\``));
    const ts = db.exts.filter((t) => t.approved && has(c.tools, t.name)); if (ts.length) lines.push("", "### Project tools", ...ts.map((t) => `- ${t.name}: ${t.desc}`));
    const bs = c.tools ? flowBuiltins().filter((t) => c.tools.includes(t.name)) : []; if (bs.length) lines.push("", "### Built-in tools to use", ...bs.map((t) => `- ${t.name.slice(8)}`));
    if (pd()) lines.push("", `SpecWright's skills, rules and inputs live in the workspace one level up (\`../.codegen/\`); this project folder is \`${pd()}\`.`);
    const block = CTX_BEGIN + "\n" + lines.join("\n") + "\n" + CTX_END + "\n", out = [];
    for (const i of info.integrations || [info.integration]) {
      const f = pd() + (i === "copilot" ? ".github/copilot-instructions.md" : "CLAUDE.md"), cur = db.files[f] || "";
      const a = cur.indexOf(CTX_BEGIN), b = cur.indexOf(CTX_END);
      const next = a >= 0 && b > a ? cur.slice(0, a) + block + cur.slice(b + CTX_END.length).replace(/^\n/, "") : (cur ? cur.replace(/\n*$/, "\n\n") : "") + block;
      if (next !== cur) { db.files[f] = next; out.push(f); }
    }
    return out;
  }
  const SK_SCOPE = { constitution: "Only create or update `.specify/memory/constitution.md` (with its Sync Impact Report). Do not create specs, plans, tasks or code; list anything else as Next Actions.", specify: "Create this feature's spec only: the feature folder under `specs/` (made by `.specify/scripts/bash/create-new-feature.sh`), its `spec.md` and requirements checklist. No plan, tasks or code.", clarify: "Only update the current feature's `spec.md`. This run is not interactive: use the answers given above, and record any question still open in the spec instead of waiting for a reply.", plan: "Only write the plan documents in the current feature folder: `plan.md`, `research.md`, `data-model.md`, `contracts/` and `quickstart.md`. No tasks or code.", tasks: "Only write `tasks.md` in the current feature folder.", analyze: "Read-only: report findings; do not change any file.", implement: "Implement the tasks in `tasks.md` in order, marking each one done ([X]) when it is finished. Stop at the first failing build or test and report it." };

  function writeFile(path, content, run) {
    const old = db.files[path];
    db.files[path] = content;
    const n = content.split("\n").length, o = old ? old.split("\n") : [];
    const added = old ? content.split("\n").filter((l) => !o.includes(l)).length : n;
    const removed = old ? o.filter((l) => !content.split("\n").includes(l)).length : 0;
    run.pub(old == null ? "fs.write" : "fs.edit", `${old == null ? "Created" : "Modified"} ${path}`, { path, added, removed, op: old == null ? "created" : "modified" });
    emit({ type: "workspace.files", summary: "1 file(s) changed", changes: [{ path, op: old == null ? "created" : "modified" }] });
  }

  function setStatus(st, status, runId) {
    st.status = status; st.lastRun = runId; persist();
    const r = db.runs.find((x) => x.id === runId); if (r) r.state = status;
    emit({ type: "workflow.changed", run_id: runId, step: st.id, summary: status });
  }

  async function execute(run, st, prompt) {
    const R = ((s) => () => ((s = (s * 9301 + 49297) % 233280) / 233280))(prompt.length + st.id.length);
    const model = agent === "copilot" ? "copilot/default" : "claude-sonnet-5-5";
    let ctx = 2400 + Math.round(prompt.length / 3.6);
    run.pub("prompt.compose", `Prompt composed (${prompt.length} chars)`, { prompt });
    // spec steps run in the Spec Kit project folder: their files go there
    const inProj = !st.script && pd() && (st.cmd || "").startsWith("/speckit");
    const steps = (st.script || SCRIPT[st.id] || [["say", "Done."]]).map(([k, a]) => [k, inProj && ["Read", "Write", "Edit"].includes(k) && !a.startsWith(".codegen/") && !a.startsWith("inputs/") ? pd() + a : a]);
    for (const [kind, arg] of steps) {
      await wait(280 + R() * 420); if (run.cancelled) return "cancelled";
      run.pub("model.request", "Request → " + model, { input_tokens: ctx, cached_tokens: Math.round(ctx * 0.8), model });
      await wait(300 + R() * 500); if (run.cancelled) return "cancelled";
      if (kind === "say") {
        const out = 120 + Math.round(R() * 260); ctx += out;
        run.pub("model.response", "Response · end_turn", { output_tokens: out, stop_reason: "end_turn" });
        run.pub("model.text", arg, { text: arg });
        continue;
      }
      const out = 80 + Math.round(R() * (kind === "Write" ? 900 : 120));
      run.pub("model.response", "Response · tool_use", { output_tokens: out, stop_reason: "tool_use" });
      const id = "t" + run.seq;
      if (kind === "WebFetch") {
        // a tool with no standing permission: ask, as Claude does through agentbridge
        const pid = "p" + Math.random().toString(16).slice(2, 10);
        run.pub("tool.call", "WebFetch " + arg, { tool: "WebFetch", args: { url: arg }, id });
        const d = await new Promise((res) => { pendingPerms.set(pid, res); run.pub("permission.request", "Allow WebFetch? " + arg, { id: pid, tool: "WebFetch", input: { url: arg, prompt: "Read the install instructions" }, target: arg }); });
        pendingPerms.delete(pid);
        run.pub("permission.resolved", "WebFetch: " + d.decision, { id: pid, tool: "WebFetch", decision: d.decision });
        const ok = d.decision !== "deny";
        run.pub("tool.result", `WebFetch ${ok ? "ok" : "denied"} · 420ms`, { tool: "WebFetch", id, ok, duration_ms: 420, output: ok ? "Install with: npm i -g example-cli" : "The user denied permission to use WebFetch." + (d.note ? " They said: " + d.note : "") });
        continue;
      }
      if (kind === "ext") {
        const e = db.exts.find((x) => x.name === arg);
        run.pub("tool.call", "extension_managed " + arg, { tool: "mcp__agentbridge__extension_managed", args: { name: arg, arguments: e?.call || {} }, id });
        await wait(500);
        run.pub("ext.result", `${arg} → exit 0`, { name: arg, ok: true, exit_code: 0, duration_ms: 640, output: e?.sampleOutput || "ok" });
        run.pub("tool.result", "extension_managed ok · 640ms", { tool: "mcp__agentbridge__extension_managed", id, ok: true, duration_ms: 640 });
      } else {
        run.pub("tool.call", `${kind} ${arg}`, { tool: kind, args: { file_path: arg }, id });
        await wait(200 + R() * 300);
        if (kind === "Write") writeFile(arg, (WRITES[arg] || (() => "// generated\n"))(st.input.trim()), run);
        if (kind === "Edit") writeFile(arg, (db.files[arg] || "") + `\n\n## Clarifications\n- ${st.input.trim() || "Partial shipments are allowed; currency is always EUR."}`, run);
        const d = 40 + Math.round(R() * 400);
        run.pub("tool.result", `${kind} ok · ${d}ms`, { tool: kind, id, ok: true, duration_ms: d, bytes: 300 + Math.round(R() * 4200) });
      }
      ctx += 400 + out;
    }
    const u = run.usage();
    const cost = agent === "copilot" ? 0 : ((u.in - u.cached) * 3 + u.cached * 0.3 + u.out * 15) / 1e6;
    run.pub("model.usage", agent === "copilot" ? "Copilot: 1 premium request(s)" : `Claude: $${cost.toFixed(4)}`, agent === "copilot" ? { premium_requests: 1 } : { cost_usd: cost });
    return "ok";
  }


  // --- run ids, results folders and conversation context (like core/results.go) ---
  const pad2 = (n) => String(n).padStart(2, "0");
  const newRunId = () => { const d = new Date(); return `${d.getFullYear()}${pad2(d.getMonth() + 1)}${pad2(d.getDate())}-${pad2(d.getHours())}${pad2(d.getMinutes())}${pad2(d.getSeconds())}-` + (crypto.randomUUID ? crypto.randomUUID() : "00000000-0000-4000-8000-" + Math.random().toString(16).slice(2, 14).padEnd(12, "0")); };
  const resultsDir = (id) => `.codegen/runs/${id.slice(0, 4)}-${id.slice(4, 6)}-${id.slice(6, 8)}/${id}`;
  const kindOf = (r) => r.kind || (r.step === "flow" ? "flow" : r.workflow === "playground" ? "playground" : "workflow");
  const digest = (id) => {
    const r = db.runs.find((x) => x.id === id); if (!r) return null;
    const evs = db.events[id] || [], kind = kindOf(r);
    const st = [...evs].reverse().find((e) => e.type === "flow.state")?.state;
    const answer = kind === "flow" && st?.outputs?.length ? st.outputs.map((o) => `### ${o.title} (${o.format})\n${o.content}`).join("\n\n") : evs.filter((e) => e.type === "model.text").map((e) => e.text).join("\n\n");
    const files = [...new Set(evs.filter((e) => e.type.startsWith("fs.")).map((e) => e.path))];
    return { id, kind, title: r.title || (r.ask || r.step).split("\n")[0].slice(0, 100), ask: r.ask || "", answer, state: r.state, started: r.started, ended: r.ended, agent: r.agent, model: r.model, usage: r.usage || {}, parents: r.parents || [], results: r.results || "", files, outputs: st?.outputs || [], workflow: r.workflow };
  };
  const thread = (id, seen = new Set(), out = []) => { if (seen.has(id) || out.length >= 12) return out; seen.add(id); const d = digest(id); if (!d) return out; for (const p of d.parents) thread(p, seen, out); out.push(d); return out; };
  const contextFor = (parents) => {
    const all = [], seen = new Set();
    for (const p of parents) { if (!db.runs.some((x) => x.id === p)) throw new Error(`the earlier run ${p} is no longer recorded`); for (const d of thread(p)) if (!seen.has(d.id)) { seen.add(d.id); all.push(d); } }
    all.sort((a, b) => String(a.started).localeCompare(String(b.started)));
    const label = { playground: "Prompt", workflow: "Workflow step", flow: "Flow run" };
    return "This continues earlier work in this project. The earlier runs are below, oldest first. Their complete results are in the folders named; read those files when you need more detail.\n" +
      all.slice(-10).map((d, i) => `\n### ${i + 1}. ${label[d.kind] || "Run"}: ${d.title} (${String(d.started).slice(0, 16).replace("T", " ")}, ${d.state})\n${d.results ? "Results folder: " + d.results + "/\n" : ""}Request: ${d.ask}\nAnswer:\n${(d.answer || "(no text answer)").slice(0, 6000)}\n`).join("") + "\n---\n\nNow, continuing from the above:\n";
  };
  const writeResults = (id) => {
    const r = db.runs.find((x) => x.id === id), d = digest(id); if (!r || !d) return;
    const dir = resultsDir(id), evs = db.events[id] || [], put = (n, c) => { db.files[`${dir}/${n}`] = c; };
    const prompt = [...evs].reverse().find((e) => e.type === "prompt.compose")?.prompt || d.ask;
    const calls = evs.filter((e) => e.type === "tool.call");
    put("prompt.md", prompt + "\n");
    if (d.answer) put("answer.md", d.answer + "\n");
    if (calls.length) put("tools.md", `# Tool calls (${calls.length})\n\n` + calls.map((c, i) => `## ${i + 1}. ${String(c.tool).replace("mcp__agentbridge__", "")}\n\n${c.summary}\n`).join("\n"));
    const extOf = (t) => { t = String(t || "").trim(); if (/^\s*(<!doctype|<html|<(div|p|h[1-6]|table|body)\b)/i.test(t)) return "html"; if (/^[[{]/.test(t)) { try { JSON.parse(t); return "json"; } catch { /* no */ } } const l = t.split("\n"), n = (l[0].match(/,/g) || []).length; if (l.length > 1 && n && l.slice(1).every((x) => !x.trim() || (x.match(/,/g) || []).length === n)) return "csv"; return /^(#{1,6}\s|\s*[-*]\s)/m.test(t) ? "md" : "txt"; };
    calls.forEach((c, i) => {
      const dirc = `tools/${pad2(i + 1)}-${String(c.tool).replace(/^mcp__agentbridge__/, "").replace(/[^A-Za-z0-9._-]+/g, "_")}`;
      const res = evs.find((x) => x.type === "tool.result" && x.seq > c.seq && (c.id ? x.id === c.id : x.tool === c.tool));
      if (c.args) put(dirc + "/input.json", JSON.stringify(c.args, null, 2) + "\n");
      if (res) put(dirc + "/output." + extOf(res.output), res.output || "");
    });
    if (d.files.length) put("files.md", "# Files changed\n\n" + d.files.map((f) => "- " + f).join("\n") + "\n");
    if (d.kind === "flow") { const st = [...evs].reverse().find((e) => e.type === "flow.state")?.state; if (st) { put("state.json", JSON.stringify(st, null, 2)); st.steps.forEach((x) => x.output && put(`steps/${x.id}.txt`, x.output)); st.outputs.forEach((o, i) => put(`outputs/${pad2(i + 1)}-${o.title.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "")}.${o.format}`, o.content)); } }
    put("events.jsonl", evs.map((e) => JSON.stringify(e)).join("\n") + "\n");
    r.results = dir;
    put("run.json", JSON.stringify(r, null, 2));
    const kindLabel = { playground: "Playground prompt", workflow: "Workflow step", flow: "Flow run" }[d.kind];
    put("summary.md", `# ${kindLabel}: ${d.title}\n\n- **Run id:** \`${id}\`\n- **Started:** ${d.started}${r.ended ? " · **ended** " + r.ended : ""}\n- **State:** ${r.state}\n- **Agent:** ${r.agent}${r.model ? " · " + r.model : ""}\n${d.parents.length ? "- **Continues:** " + d.parents.join(", ") + "\n" : ""}\n## Files here\n\n- \`prompt.md\`, \`answer.md\`, \`tools.md\`, \`events.jsonl\`, \`run.json\`${d.kind === "flow" ? ", `outputs/`, `steps/`, `state.json`" : ""}\n${d.answer ? "\n## Answer\n\n" + d.answer.slice(0, 1500) + "\n" : ""}`);
    persist();
    emit({ type: "results.saved", run_id: id, dir, summary: dir });
  };
  function makeRun(st) {
    const id = newRunId();
    const t0 = Date.now();
    const evs = (db.events[id] = []);
    const run = { id, seq: 0, cancelled: false, decision: null, waiter: null,
      pub(type, summary, data = {}) { const e = { seq: ++run.seq, ts: now(), run_id: id, step: st.id, type, summary, dt: Date.now() - t0, ...data }; evs.push(e); emit(e); return e; },
      usage() { const u = { in: 0, out: 0, cached: 0, tools: 0, files: new Set() }; for (const e of evs) { if (e.type === "model.request") { u.in += e.input_tokens; u.cached += e.cached_tokens; } if (e.type === "model.response") u.out += e.output_tokens; if (e.type === "tool.call") u.tools++; if (e.type.startsWith("fs.")) u.files.add(e.path); } return u; },
      decide() { return new Promise((r) => { run.waiter = r; }); },
    };
    db.runs.unshift({ id, workspace: db.root, workflow: st.id === "playground" ? "playground" : "speckit", step: st.id, agent, model: agent === "copilot" ? "copilot/default" : "claude-sonnet-5-5", state: "running", started: now(), usage: {},
      kind: st.id === "playground" ? "playground" : "workflow", title: st.runTitle || (st.id === "playground" ? (st.ask || st.input || "").split("\n")[0].slice(0, 100) : st.title), ask: st.ask || st.input || "", parents: st.parents || [] });
    return run;
  }

  async function startRun(st) {
    const run = makeRun(st); live = run;
    setStatus(st, "running", run.id);
    run.pub("run.start", `Started ${st.id} with ${agent}`, { agent, model: agent === "copilot" ? "copilot/default" : "claude-sonnet-5-5", cwd: db.root });
    (async () => {
      let feedback = "", final = "done", reason = "approved";
      for (;;) {
        const res = await execute(run, st, st.rawPrompt ?? compose(st, feedback));
        if (res === "cancelled") { final = "cancelled"; reason = "cancelled by you"; break; }
        const u = run.usage();
        run.pub("run.usage", `Usage: ${u.in} in · ${u.out} out · ${u.tools} tools · ${u.files.size} files`, { input_tokens: u.in, output_tokens: u.out, cached_tokens: u.cached, tool_calls: u.tools, files_changed: u.files.size });
        if (!st.gate) { reason = "completed"; break; }
        setStatus(st, "paused", run.id);
        run.pub("gate.wait", "Paused for human approval", { gate: "approval" });
        const d = await run.decide();
        if (d.kind === "cancel") { final = "cancelled"; reason = "cancelled at the gate"; break; }
        if (d.kind === "approve") { run.pub("gate.approved", "Approved by you"); break; }
        if (d.kind === "reject") { run.pub("gate.rejected", "Rejected by you"); final = "failed"; reason = "rejected"; break; }
        run.pub("gate.changes", "Changes requested", { feedback: d.feedback }); feedback = d.feedback;
        setStatus(st, "running", run.id);
      }
      run.pub("run.end", "Run " + final, { state: final, reason });
      const u = run.usage(), r = db.runs.find((x) => x.id === run.id);
      const evs = db.events[run.id], cost = evs.filter((e) => e.type === "model.usage").reduce((a, e) => a + (e.cost_usd || 0), 0), prem = evs.filter((e) => e.type === "model.usage").reduce((a, e) => a + (e.premium_requests || 0), 0);
      const created = evs.filter((e) => e.type === "fs.write").length;
      Object.assign(r, { state: final, reason, ended: now(), events: run.seq, usage: { input_tokens: u.in, output_tokens: u.out, cached_tokens: u.cached, tool_calls: u.tools, files_created: created, files_modified: u.files.size - created, cost_usd: cost, premium_requests: prem, model_calls: evs.filter((e) => e.type === "model.request").length } });
      setTimeout(() => writeResults(run.id), 150);
      db.ledger.push({ at: now(), workspace: db.root, workflow: st.id === "playground" ? "playground" : "speckit", step: st.id, state: final, files_created: created, input_tokens: u.in, output_tokens: u.out, cost_usd: cost, premium_requests: prem, model_calls: r.usage.model_calls });
      live = null; setStatus(st, final, run.id);
    })();
    return run.id;
  }

  function compose(st, feedback) {
    const p = db.profile, o = st.options, libs = libraries(), c = db.wf.context || {}, has = (list, v) => !list || list.includes(v);
    const rules = p.rules.filter((r) => has(c.rules, r.id));
    const exts = db.exts.filter((t) => t.approved && t.status !== "in review" && has(c.tools, t.name)), bts = c.tools ? flowBuiltins().filter((t) => c.tools.includes(t.name)) : [];
    const pick = (ids) => libs.filter((l) => ids.includes(l.id)).map((l) => `- ${l.name} (${l.kind}): ${l.path}` + (l.files?.length ? `\n  Its folder ${l.dir}/ also has these files (paths in SKILL.md are relative to that folder):\n` + l.files.map((f) => `  - ${f}`).join("\n") : "")).join("\n");
    const info = skInfo(), skill = [skDir(info.integration), skDir(info.integration === "copilot" ? "claude" : "copilot")].map((d) => `${d}/speckit-${st.id}/SKILL.md`).find((f) => pd() + f in db.files);
    const cmd = st.cmd.startsWith("/speckit") ? (skill ? "/speckit-" + st.id : st.cmd) : st.cmd;
    const guide = st.cmd.startsWith("/speckit") && [skill && `This is the Spec Kit command \`${cmd}\`: read \`${skill}\` and follow it exactly, with the text after the command above as its input ($ARGUMENTS).`, SK_SCOPE[st.id], pd() && `Your working directory is the project folder \`${pd()}\` (where \`.specify/\` is): keep every spec, plan and source file inside it. SpecWright's skills and inputs are one level up, in \`../.codegen/\`.`, "You run without a person at the keyboard: where the command would ask and wait, use the input above or make a reasonable assumption and say so."].filter(Boolean).join("\n");
    return [`${cmd} ${st.input.trim()}`, guide && `\n## How to run this step\n${guide}`,
      st.instructions.trim() && `\n## Instructions\n${st.instructions.trim()}`,
      o.rules && rules.length && `\n## Rules\n${rules.map((r) => `- ${r.id}: ${r.text}`).join("\n")}`,
      o.global && p.global.length && `\n## Corporate skills (read before you start)\n${pick(p.global)}`,
      o.project && p.project.length && `\n## Project skills and scripts\n${pick(p.project)}`,
      o.diagrams && Object.keys(p.diagrams || {}).length && `\n## Input diagrams\n${Object.entries(p.diagrams).map(([k, v]) => `- ${k} diagram: ${v}`).join("\n")}`,
      o.tools && bts.length && `\n## Built-in tools to use\nThey are tools of the MCP server agentbridge, called by these names.\n${bts.map((t) => `- ${t.name.slice(8)}: ${t.desc.replace(/^[^:]*:\s*/, "")}`).join("\n")}`,
      o.tools && exts.length && `\n## Tools available\nCall them with the extension_managed tool (MCP server agentbridge).\n${exts.map((t) => `- ${t.name}: ${t.desc}${t.secrets.length ? ` (env: ${t.secrets.map((s) => s.name + "=[REDACTED]").join(", ")})` : ""}`).join("\n")}`,
      `\n## Project\nLanguage: ${p.lang} · build: ${p.build}`,
      feedback && `\n## Change request from the reviewer\n${feedback}\nRevise your previous output for this step accordingly.`,
    ].filter(Boolean).join("\n");
  }


  // --- flows (browser preview: a JS subset of the Go designer helpers) ---
  const FLOW_OPS = {
    template: { name: "Fill a template", group: "Text", help: "Write the result with {{input}} and any other variable.", template: true },
    trim: { name: "Trim whitespace", group: "Text" }, uppercase: { name: "UPPERCASE", group: "Text" }, lowercase: { name: "lowercase", group: "Text" },
    truncate: { name: "Keep the first N characters", group: "Text", expr: "N" },
    "filter-lines": { name: "Keep matching lines", group: "Text", expr: "Regular expression" },
    "regex-extract": { name: "Extract with a pattern", group: "Text", help: "Every match on its own line (the first group if the pattern has one).", expr: "Regular expression" },
    "regex-replace": { name: "Find and replace", group: "Text", help: "$1, $2 … refer to groups.", expr: "Regular expression", replace: true },
    "split-lines": { name: "Lines → JSON array", group: "Convert" },
    "json-path": { name: "Pick a JSON value", group: "JSON", help: "A path like items[0].name, items[*].id or $.data.total.", expr: "Path" },
    "json-pick": { name: "Keep JSON fields", group: "JSON", help: "Comma-separated field names; works on an object or an array of objects.", expr: "Fields" },
    "json-template": { name: "JSON → JSON (template)", group: "JSON", help: "Write the JSON to produce. {{this.<path>}} reads the input JSON; a placeholder that is a whole string value (\"{{this.total}}\") keeps its type (number, list, object). For nested lists, an object whose only key is \"{{#each this.lines}}\" repeats its template for each element; inside it {{parent.<path>}} is the level above, {{root.<path>}} the whole input and {{this_index}} the position. Set a list path to apply the whole template to each element and get a JSON array.", expr: "For each element of (optional list path, e.g. items)", exprOptional: true, template: true },
    "json-stringify": { name: "JSON → string (stringify)", group: "JSON", help: "Turns JSON into a JSON string: {\"a\":1} becomes \"{\\\"a\\\":1}\" — for an API or tool that wants JSON inside a string field. With a field path, only that field is turned into a string (items[*].payload: in every element); the rest stays as it is.", expr: "Field (optional: empty is the whole input)", exprOptional: true },
    "json-parse": { name: "String → JSON (destringify)", group: "Text", help: "Turns a JSON string back into JSON: \"{\\\"a\\\":1}\" becomes {\"a\":1} (strings encoded twice are undone too). With a field path, only that field is parsed (items[*].payload: in every element); the rest stays as it is.", expr: "Field (optional: empty is the whole input)", exprOptional: true },
    "json-pretty": { name: "Format JSON", group: "JSON" }, "json-minify": { name: "Minify JSON", group: "JSON" },
    "csv-to-json": { name: "CSV → JSON", group: "Convert" }, "json-to-csv": { name: "JSON → CSV", group: "Convert" },
    "md-to-html": { name: "Markdown → HTML", group: "Convert" }, "html-to-text": { name: "HTML → text", group: "Convert" },
    ai: { name: "Ask AI to transform", group: "AI", help: "Describe the change; {{input}} is the data. Runs with the agent when the flow runs.", template: true, ai: true },
  };
  db.flows ||= { "todo-report": { version: 1, id: "todo-report", name: "TODO report", description: "Collect open TODOs, keep the important ones and publish a report.",
    inputs: [{ name: "path", label: "Package or folder", type: "text", required: true, default: "src/main/java" }, { name: "focus", label: "What to focus on", type: "multiline" }],
    context: { text: "Orders service, Java 21 / Spring Boot. Reports go to the team channel.", files: [] }, skills: [{ path: ".codegen/skills/project/orders-domain/SKILL.md", name: "orders-domain" }],
    nodes: [
      { id: "start", type: "start", title: "Inputs", x: 40, y: 160 },
      { id: "todos", type: "tool", title: "Find TODOs", x: 320, y: 90, tool: "find-todos", args: { path: "{{inputs.path}}" } },
      { id: "lint", type: "tool", title: "Lint the API", x: 320, y: 250, tool: "openapi-lint", args: {} },
      { id: "fixme", type: "transform", title: "Keep FIXMEs", x: 600, y: 90, op: "filter-lines", expr: "FIXME|TODO\\(urgent\\)" },
      { id: "merge", type: "merge", title: "Combine", x: 870, y: 170, mode: "json-object" },
      { id: "summary", type: "ai", title: "Summarize", x: 1140, y: 170, prompt: "Summarize {{steps.merge}} for the team. Focus on {{inputs.focus}}." },
      { id: "md", type: "output", title: "Report (md)", x: 1420, y: 80, format: "md", actions: [{ type: "save", path: "reports/todos.md" }, { type: "show" }] },
      { id: "json", type: "output", title: "Data (json)", x: 1420, y: 260, format: "json", template: "{{steps.merge}}", actions: [{ type: "save", path: "reports/todos.json" }] },
    ],
    edges: [{ from: "start", to: "todos" }, { from: "start", to: "lint" }, { from: "todos", to: "fixme" }, { from: "fixme", to: "merge" }, { from: "lint", to: "merge" }, { from: "merge", to: "summary" }, { from: "summary", to: "md" }, { from: "merge", to: "json" }],
    updated: new Date().toISOString() } };
  // --- trial runs (flow.DryRunFlow): tools and AI mocked, everything else run for real ---
  const dryRunFlow = (f, test = {}) => {
    const order = flowOrder(f), li = loopStacks(f), node = (id) => f.nodes.find((x) => x.id === id), label = (n) => n.title || n.id;
    const tools = Object.fromEntries([...extensions(), ...flowBuiltins()].map((e) => [e.name, e]));
    const steps = Object.fromEntries(order.map((id) => [id, { id, title: label(node(id)), type: node(id).type, status: "not run", runs: 0 }]));
    const inputs = Object.fromEntries((f.inputs || []).map((i) => [i.name, test.inputs?.[i.name] || i.default || (i.type === "number" ? "3" : i.options?.[0] || "sample " + (i.label || i.name))]));
    const mock = (n, shape, what) => { const m = test.mocks?.[n.id]; if (m !== undefined) return typeof m === "string" ? m : JSON.stringify(m, null, 2); return shape ? JSON.stringify(sampleOf(shape, n.id), null, 2) : `(sample ${what} of ${label(n)})`; };
    const exec = (n, d, live) => {
      const input = live.map((p) => d.steps[p] ?? "").join("\n\n"), dd = { ...d, input }, rnd = (m) => render(m, dd);
      switch (n.type) {
        case "start": return [JSON.stringify(d.inputs, null, 2), false];
        case "tool": { const t = tools[n.tool]; if (!t) throw new Error(`the tool "${n.tool}" is not available`);
          if (!(n.instruction || "").trim()) for (const p of t.params || []) if (p.required && !render(n.args?.[p.name] || "", dd).trim()) throw new Error(`the required argument "${p.name}" is empty with the sample data`);
          return [mock(n, toolOutput(t.output, t.outputBy, n.args), "output"), true]; }
        case "ai": return [mock(n, n.outputSchema, "answer"), true];
        case "transform": { const src = n.source ? render(n.source, dd) : input; if (n.op === "ai" || n.op === "command") return [mock(n, null, "result"), true]; return [transform(n, src, dd), false]; }
        case "merge": { const vals = live.map((p) => d.steps[p] ?? ""), js = (v) => { try { return JSON.parse(v); } catch { return v; } };
          if (n.mode === "json-object") return [JSON.stringify(Object.fromEntries(live.map((p, i) => [label(node(p)), js(vals[i])])), null, 2), false];
          if (n.mode === "json-array") return [JSON.stringify(vals.map(js), null, 2), false];
          if (n.mode === "template") return [render(n.template, dd), false];
          return [joinText(vals, (n.separator || "\\n\\n").replace(/\\n/g, "\n")), false]; }
        case "filter": return [filterRows(n, n.source ? render(n.source, dd) : input, rnd), false];
        case "aggregate": return [aggregateRows(n, n.source ? render(n.source, dd) : input, rnd), false];
        case "decision": { const src = n.source ? render(n.source, dd) : input; decide(n, src, rnd); return [src, false]; }
        case "output": return [convert(n.template ? render(n.template, dd) : input, n.format), false];
      }
      return [input, false];
    };
    const run = (ids, d, status) => {
      const handled = new Set(); let firstErr = "";
      for (const id of ids) {
        if (handled.has(id)) continue;
        const n = node(id), st = steps[id], ps = f.edges.filter((e) => e.to === id).map((e) => e.from);
        let live = [];
        if (ps.length) {
          if (ps.some((p) => status[p] === "failed")) { status[id] = "failed"; if (st.status === "not run") Object.assign(st, { status: "blocked", error: "a step before it failed" }); continue; }
          live = liveParents(f, id, (p) => status[p] === "off", d.branches);
          if (!live.length) { status[id] = "off"; if (st.status === "not run") st.status = "off"; continue; }
        }
        if (n.type === "loop") {
          const body = ids.filter((x) => x !== id && (li.stack[x] || []).includes(id)), g = li.gather[id];
          body.forEach((x) => handled.add(x)); handled.add(g); st.runs++;
          const src = (n.source || "").trim() ? render(n.source, { ...d, input: live.map((p) => d.steps[p]).join("\n") }) : live.map((p) => d.steps[p] ?? "").join("\n");
          let items; try { items = splitItems(src, n); } catch (e) { status[id] = "failed"; Object.assign(st, { status: "failed", error: e.message }); continue; }
          Object.assign(st, { status: "done", output: JSON.stringify(items, null, 2) });
          const results = [], gp = f.edges.filter((e) => e.to === g).map((e) => e.from); let failedItem = "";
          items.slice(0, 3).forEach((it, k) => { if (failedItem) return;
            const di = { ...d, steps: { ...d.steps, [id]: it }, files: { ...d.files }, branches: { ...d.branches }, item: it, itemIndex: k + 1, itemCount: Math.min(items.length, 3), loops: { ...(d.loops || {}), [id]: { item: it, index: k + 1, count: Math.min(items.length, 3) } } }, si = { ...status, [id]: "done" };
            const err = run(body, di, si); const r = { item: it, output: gp.filter((p) => si[p] === "done").map((p) => di.steps[p] ?? "").join("\n\n") };
            if (err) { r.error = err; if (!n.continueOnError) failedItem = `item ${k + 1}: ${err}`; } results.push(r); });
          if (failedItem) { status[id] = "failed"; Object.assign(st, { status: "failed", error: failedItem }); continue; }
          status[id] = "done"; d.steps[id] = st.output;
          const gs = steps[g]; gs.runs++;
          try { const out = gatherItems(node(g), results, d); Object.assign(gs, { status: "done", output: out }); status[g] = "done"; d.steps[g] = out; } catch (e) { status[g] = "failed"; Object.assign(gs, { status: "failed", error: e.message }); }
          continue;
        }
        st.runs++;
        try {
          const [out, mocked] = exec(n, d, live);
          if (n.type === "decision") { const bs = decide(n, out, (m) => render(m, d)); d.branches = { ...d.branches, [id]: bs }; st.path = bs.map((b) => branchLabel(n, b)).join(", "); }
          status[id] = "done"; if (st.status !== "failed") st.status = "done"; Object.assign(st, { output: String(out).slice(0, 4000), mocked }); d.steps[id] = out; d.files[id] = ".codegen/runs/dry/steps/" + id + "/output.txt";
        } catch (e) { status[id] = "failed"; Object.assign(st, { status: "failed", error: e.message }); if (!firstErr) firstErr = label(n) + ": " + e.message; }
      }
      return firstErr;
    };
    run(order, { inputs, steps: {}, files: {}, branches: {}, context: f.context?.text || "" }, {});
    const checks = [], all = order.map((id) => steps[id]);
    const broken = all.filter((s) => s.status === "failed" || s.status === "blocked");
    broken.forEach((s) => checks.push({ label: s.title + " runs", ok: false, note: s.error }));
    if (!broken.length) checks.push({ label: "Every step on the path runs", ok: true });
    const outs = f.nodes.filter((n) => n.type === "output").map((n) => steps[n.id]).filter((s) => s.status === "done");
    outs.filter((s) => !String(s.output || "").trim()).forEach((s) => checks.push({ label: s.title + " has content", ok: false, note: "the output is empty" }));
    if (!outs.length) checks.push({ label: "An output is produced", ok: false, note: "no output step was reached with the sample data" });
    for (const e of test.expect || []) { const s = steps[e.step]; if (!s) continue; if (s.status !== "done") { checks.push({ label: s.title + " gives the expected result", ok: false, note: `it did not run (${s.status})` }); continue; }
      for (const c of e.contains || []) checks.push({ label: `${s.title} contains "${c}"`, ok: String(s.output).toLowerCase().includes(String(c).toLowerCase()) }); }
    return { ok: checks.every((c) => c.ok), steps: all, checks, inputs };
  };

  // --- fixing one step (scripted): FixFlowStep ---
  const runFlowCopy = (st) => { const f = clone(db.flows[st.flowId]); if (!f) return null; for (const t of st.trial?.steps || []) { const i = f.nodes.findIndex((x) => x.id === t.id); if (i >= 0) f.nodes[i] = { ...t.node, id: t.id, x: f.nodes[i].x, y: f.nodes[i].y }; } return f; };
  const fixFlowStep = async (r) => {
    const st = await api.FlowRun(r.runId), f = runFlowCopy(st), n = f?.nodes.find((x) => x.id === r.stepId);
    if (!n || n.type === "start") throw new Error(`no step "${r.stepId}" to fix`);
    const s = st.steps.find((x) => x.id === n.id) || {}, failed = s.status === "failed", nn = clone(n);
    const avail = new Set([...extensions().map((e) => e.name), ...flowBuiltins().map((b) => b.name)]);
    let why;
    if (n.type === "tool" && !avail.has(n.tool)) { nn.tool = "builtin:text_search"; nn.args = { query: /todo|fixme/i.test(n.tool) ? "TODO|FIXME" : "{{inputs.path}}", is_regex: "true", path: n.args?.path || "{{inputs.path}}" }; why = `${n.tool} is not in this project; the built-in text search finds the same lines.`; }
    else if (n.type === "transform" && failed) { nn.op = "template"; nn.template = "{{input}}"; delete nn.expr; why = "The step before gives text, not JSON, so it passes the text on instead of reading a JSON path."; }
    else if (n.type === "ai") { nn.prompt = (n.prompt || "").trim() + (r.prompt ? "\n\n" + r.prompt : "\n\nBe brief: at most 5 bullet points."); why = r.prompt ? "Adds your instruction to the prompt." : "Asks for a shorter, structured answer."; }
    else if (n.type === "output") { nn.template = n.template || "# " + (n.title || "Report") + "\n\n{{input}}"; why = "Adds a heading to the report."; }
    else { nn.title = (n.title || n.id); nn.note = (r.prompt || "Reviewed by AI").slice(0, 200); why = "Keeps the step and records the request as a note."; }
    const cp = clone(f), i = cp.nodes.findIndex((x) => x.id === n.id); cp.nodes[i] = nn;
    const issues = validateFlow(cp).filter((x) => x.node === n.id || !x.node);
    const keys = [...new Set([...Object.keys(n), ...Object.keys(nn)])].filter((k) => !["x", "y", "id"].includes(k)).sort();
    const changes = keys.filter((k) => JSON.stringify(n[k]) !== JSON.stringify(nn[k])).map((k) => (n[k] === undefined ? "Adds " : nn[k] === undefined ? "Removes " : "Changes ") + k);
    return { runId: r.runId, flowId: f.id, stepId: n.id, before: n, node: nn, why, changes: changes.length ? changes : ["No changes"], issues, usage: { input_tokens: 2100, output_tokens: 300 } };
  };

  // --- the AI designer, scripted for the preview ---
  const designFlows = (r) => {
    const base = r.flowId ? db.flows[r.flowId] : null, q = String(r.prompt || "").toLowerCase();
    const exts = extensions().filter((e) => e.approved), has = (n) => exts.some((e) => e.name === n);
    const pick = /quality|sonar|gate|blocker|issue/.test(q) && has("sonar-scan") ? "sonar-scan" : /api|spec|openapi|lint/.test(q) && has("openapi-lint") ? "openapi-lint" : /dto|generat/.test(q) && has("gen-dto") ? "gen-dto" : "";
    const t = pick ? exts.find((e) => e.name === pick) : null, toolOut = t?.output, rows = toolOut && Object.values(toolOut.properties || {}).find((p) => p.type === "array");
    const out = (id, title, format = "md") => ({ id, type: "output", title, format, actions: [{ type: "show" }] });
    const E = (...ids) => ids.slice(1).map((to, i) => ({ from: ids[i], to }));
    const opts = [];
    if (base) {
      const avail = new Set([...extensions().map((e) => e.name), ...flowBuiltins().map((b) => b.name)]); // missing, not merely unapproved
      const missing = base.nodes.filter((n) => n.type === "tool" && !avail.has(n.tool));
      if (missing.length) {
        const fx = clone(base.nodes);
        for (const n of fx) if (n.type === "tool" && !avail.has(n.tool)) { const was = n.tool; n.tool = "builtin:text_search"; n.args = { query: /todo|fixme/i.test(was) ? "TODO|FIXME" : "{{inputs.path}}", is_regex: "true", path: n.args?.path || "{{inputs.path}}" }; n.title ||= was; }
        opts.push({ title: "Replace the missing tool", summary: `${missing.map((n) => n.tool).join(", ")} is not in this project: the built-in text search does the same job.`, why: "Fixes the flow's problem with a built-in tool; the other steps stay as they are.", flow: { name: base.name, inputs: base.inputs, nodes: fx, edges: clone(base.edges) } });
      }
      const nodes = clone(base.nodes), edges = clone(base.edges), first = nodes.find((n) => n.type === "tool") || nodes.find((n) => n.type !== "start");
      const v1n = clone(nodes), v1e = clone(edges);
      if (first) { v1n.push({ id: "nothing", type: "decision", title: "Anything to report?", mode: "first", branches: [{ id: "some", label: "Something found", when: { match: "all", rules: [{ field: "", op: "not_empty" }] } }] }); v1n.push(out("none", "Nothing to report", "txt"));
        for (const e of v1e) if (e.from === first.id) e.from = "nothing", e.branch = "some"; v1e.push({ from: first.id, to: "nothing" }, { from: "nothing", to: "none", branch: "else" }); }
      opts.push({ title: "Stop early when there is nothing to report", summary: "Adds a Decision after " + (first?.title || first?.id || "the first step") + " so the rest only runs when it found something.", why: "Deterministic: no extra AI calls, and runs that find nothing finish at once.", flow: { name: base.name, inputs: base.inputs, nodes: v1n, edges: v1e } });
      const v2n = clone(nodes), v2e = clone(edges), ai = v2n.filter((n) => n.type === "ai");
      for (const n of ai) n.outputSchema ||= { type: "object", properties: { summary: { type: "string" }, actions: { type: "array", items: { type: "string" } } } };
      opts.push({ title: ai.length ? "Structured AI answers" : "Also save the data as JSON", summary: ai.length ? `Gives ${ai.map((n) => n.title || n.id).join(", ")} an expected reply, so later steps can rely on its fields.` : "Adds a JSON output next to the report.", why: "Keeps the flow, makes the AI part predictable.", flow: { name: base.name, inputs: base.inputs, nodes: ai.length ? v2n : [...v2n, out("json", "Data (json)", "json")], edges: ai.length ? v2e : [...v2e, ...(first ? [{ from: first.id, to: "json" }] : [])] } });
    } else {
      if (t) {
        const n = [{ id: "start", type: "start" }, { id: "run", type: "tool", tool: t.name, title: t.name, args: Object.fromEntries((t.params || []).map((p) => [p.name, ""])) }];
        if (rows) { const f1 = Object.keys(rows.items?.properties || {}), sev = f1.find((k) => /sever|level|status|priority/.test(k)), grp = f1.find((k) => /file|path|module|component/.test(k)) || f1[0];
          n.push({ id: "important", type: "filter", title: "Important rows", mode: "keep", cond: { match: "all", rules: [sev ? { field: sev, op: "in", value: (rows.items.properties[sev].enum || ["BLOCKER", "CRITICAL"]).slice(0, 2).join(", ") } : { field: grp, op: "not_empty" }] } },
            { id: "grouped", type: "aggregate", title: "Grouped", mode: "group", groupBy: [grp], orderBy: ["-count"], measures: [{ fn: "count", as: "count" }, { fn: "list", field: f1.find((k) => /message|title|name/.test(k)) || grp, as: "items" }] }, out("report", "Report", "json"));
          const sample = (() => { try { return JSON.parse(t.sampleOutput); } catch { return undefined; } })(), firstRow = sample && Object.values(sample).find(Array.isArray)?.find((r) => !sev || (rows.items.properties[sev].enum || ["BLOCKER"]).slice(0, 2).includes(r[sev]));
          opts.push({ title: "Tool, filter and group (no AI)", summary: `Runs ${t.name}, keeps the important rows and groups them by ${grp}.`, why: `Uses ${t.name}'s output fields directly, so every run gives the same, checkable result.`, flow: { name: "Report from " + t.name, nodes: n, edges: E("start", "run", "important", "grouped", "report") },
            test: { mocks: sample ? { run: sample } : {}, expect: [{ step: "report", notEmpty: true, ...(firstRow?.[grp] ? { contains: [String(firstRow[grp]).split("/").pop()] } : {}) }] } });
        } else { n.push(out("report", "Result", "md")); opts.push({ title: `Run ${t.name}`, summary: `Runs ${t.name} and shows what it prints.`, why: "The tool already does the job; nothing else is needed.", flow: { name: "Run " + t.name, nodes: n, edges: E("start", "run", "report") } }); }
        opts.push({ title: `${t.name} with an AI summary`, summary: `Runs ${t.name}, then the AI writes a short summary for the team.`, why: "The tool gathers the facts; AI only writes the prose.",
          test: { mocks: { ...(t.sampleOutput ? { run: t.sampleOutput } : {}), summary: "- 2 blocking issues: Order.java (null pointer), OrderController.java\n- Fix the null pointer first" }, expect: [{ step: "md", contains: ["blocking"] }] }, flow: { name: t.name + " summary", nodes: [{ id: "start", type: "start" }, { id: "run", type: "tool", tool: t.name, title: t.name, args: {} }, { id: "summary", type: "ai", title: "Summarise", prompt: `Summarise the result of ${t.name} for the team in a few bullet points:

{{run.buffer_output}}` }, out("md", "Summary")], edges: E("start", "run", "summary", "md") } });
      } else {
        opts.push({ title: "Search the project, then list", summary: "Searches the files with the built-in text search and shows the matches.", why: "No project tool fits, but a built-in search does it without AI.", flow: { name: "Search", inputs: [{ name: "query", label: "What to look for", type: "text", required: true }], nodes: [{ id: "start", type: "start" }, { id: "find", type: "tool", tool: "builtin:text_search", title: "Search", args: { query: "{{inputs.query}}" } }, out("md", "Matches")], edges: E("start", "find", "md") } });
      }
      opts.push({ title: "Ask the AI", summary: "One AI step does the whole job from your description.", why: "Quickest to set up, but each run costs tokens and can vary.", test: { inputs: { request: "for the orders team" }, mocks: { do: "Here is what I found…" }, expect: [{ step: "md", notEmpty: true }] }, flow: { name: (r.prompt || "AI flow").slice(0, 40), inputs: [{ name: "request", label: "Details", type: "multiline" }], nodes: [{ id: "start", type: "start" }, { id: "do", type: "ai", title: "Do it", prompt: (r.prompt || "Do the task") + "\n\n{{inputs.request}}" }, out("md", "Answer")], edges: E("start", "do", "md") } });
    }
    const checked = opts.map((o) => {
      const f = { ...o.flow, inputs: o.flow.inputs || [], context: base?.context || { text: "", files: [] }, skills: base?.skills || [] };
      for (const n of f.nodes) if (n.type === "start") n.title ||= "Inputs";
      const depth = { start: 0 }; for (let k = 0; k < f.nodes.length; k++) for (const e of f.edges) depth[e.to] = Math.max(depth[e.to] ?? 0, (depth[e.from] ?? 0) + 1);
      const rowsAt = {}; for (const n of f.nodes) { const d = depth[n.id] ?? 0, rr = (rowsAt[d] = (rowsAt[d] ?? -1) + 1); n.x = 40 + d * 260; n.y = 80 + rr * 140; }
      const issues = validateFlow(f), aiSteps = f.nodes.filter((n) => n.type === "ai" || (n.type === "transform" && n.op === "ai")).length;
      let changes;
      if (base) { const old = Object.fromEntries(base.nodes.map((n) => [n.id, n])); changes = [];
        for (const n of f.nodes) { if (!old[n.id]) changes.push(`Adds ${n.title || n.id} (${n.type})`); else { const a = { ...old[n.id], x: 0, y: 0 }, b = { ...n, x: 0, y: 0 }; if (JSON.stringify(a) !== JSON.stringify(b)) changes.push("Changes " + (n.title || n.id)); delete old[n.id]; } }
        for (const n of Object.values(old)) changes.push("Removes " + (n.title || n.id)); if (!changes.length) changes = ["No changes to the steps"]; }
      let errors = issues.filter((i) => i.level === "error").length, trial;
      if (!errors) { trial = dryRunFlow(f, o.test || {}); if (!trial.ok) { errors++; issues.push({ level: "error", message: "Trial run with sample data failed: " + trial.checks.filter((c) => !c.ok).map((c) => c.label + (c.note ? ": " + c.note : "")).join("; ") }); } }
      let kept = 0, rewrite = false, fixes = 0;
      if (base) { const steps = base.nodes.filter((n) => n.type !== "start"); kept = steps.filter((n) => f.nodes.some((x) => x.id === n.id)).length; rewrite = steps.length > 0 && kept * 2 < steps.length;
        if (rewrite) issues.push({ level: "warning", message: `This replaces most of the current flow (it keeps ${kept} of its ${steps.length} steps).` });
        fixes = Math.max(0, validateFlow(base).filter((i) => i.level === "error").length - errors); }
      return { ...o, flow: f, issues, errors, trial, test: o.test || {}, aiSteps, deterministic: !aiSteps, tools: [...new Set(f.nodes.filter((n) => n.type === "tool").map((n) => n.tool))], changes, kept, rewrite, fixes };
    });
    checked.sort((a, b) => (!!a.errors - !!b.errors) || (a.rewrite - b.rewrite) || (b.fixes - a.fixes) || a.aiSteps - b.aiSteps || b.tools.length - a.tools.length);
    checked.forEach((o, i) => { o.recommended = i === 0 && !o.errors && !o.rewrite; });
    return { options: checked, usage: { input_tokens: 5400, output_tokens: 1300, premium_requests: 0, cost_usd: 0 } };
  };
  const flowSummary = (f) => ({ id: f.id, name: f.name, description: f.description, steps: f.nodes.filter((n) => !["start", "output"].includes(n.type)).length, outputs: f.nodes.filter((n) => n.type === "output").length, updated: f.updated, parent: f.parent, revision: f.revision });
  const slug = (n) => (n || "flow").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 50) || "flow";
  const flowCmds = (f) => f.nodes.filter((n) => n.type === "transform" && n.op === "command" && (n.expr || "").trim()).map((n) => ({ step: n.id, title: n.title || n.id, command: n.expr, approved: ((db.cmdOK || {})[db.root] || []).includes(n.expr.trim()) }));
  const uniqueFlowId = (id) => { let c = id, k = 2; while (db.flows[c]) c = `${id}-${k++}`; return c; };
  const flowVars = (s) => [...String(s || "").matchAll(/\{\{\s*([^{}]+?)\s*\}\}/g)].map((m) => m[1]);
  const flowAnc = (f, id) => { const seen = new Set(), walk = (n) => f.edges.filter((e) => e.to === n).forEach((e) => { if (!seen.has(e.from)) { seen.add(e.from); walk(e.from); } }); walk(id); return seen; };
  const validateFlow = (f) => {
    const out = [], add = (node, level, message) => out.push({ node, level, message });
    const tools = [...extensions(), ...flowBuiltins()], inputs = new Set(f.inputs.map((i) => i.name)), ids = new Set(f.nodes.map((n) => n.id));
    if (!f.name?.trim()) add("", "error", "Give the flow a name.");
    if (!f.nodes.some((n) => n.type === "output")) add("", "warning", "Add an output so the flow produces something.");
    for (const i of f.inputs) if (i.type === "secret" && i.default) add("start", "error", `Input "${i.name}" is a secret: remove its default value, which would be saved in the flow file.`);
    const { names: stepAlias } = stepNames(f), byID = (h) => { const id = stepAlias[h], s = id && f.nodes.find((x) => x.id === id); return s ? ` Steps are referred to by their id: use "${id}" (the id of ${s.title || id}).` : ""; };
    const check = (n, what, tpl) => { const anc = flowAnc(f, n.id); for (const v of flowVars(tpl)) { const [h, r = ""] = [v.split(".")[0], v.split(".").slice(1).join(".")]; const name = r.split(".")[0];
      if (h === "inputs" && !inputs.has(name)) add(n.id, "error", `${what} uses {{${v}}}, but there is no input called "${name}".`);
      if (h === "steps" && !ids.has(name)) add(n.id, "error", `${what} uses {{${v}}}, but there is no step "${name}".`);
      else if (h === "steps" && !anc.has(name)) add(n.id, "error", `${what} uses {{${v}}}, but "${name}" does not run before this step. Connect it first.`);
      else if (!["inputs", "steps", "context", "skills", "input", "input_file", "output", "flow", "date", "now", "item", "item_index", "item_count"].includes(h) && isLoopField(r)) {
        const ln = f.nodes.find((x) => x.id === h);
        if (!ln) add(n.id, "error", `${what} uses {{${v}}}, but there is no step with the id "${h}".${byID(h)}`);
        else if (ln.type !== "loop") add(n.id, "error", `${what} uses {{${v}}}, but ${ln.title || ln.id} is not a Loop step.`);
        else if (!(loopStacks(f).stack[n.id] || []).includes(h)) add(n.id, "error", `${what} uses {{${v}}}, but it does not run inside the loop ${ln.title || ln.id} (only the steps between it and its Gather do).`);
      }
      else if (!["inputs", "steps", "context", "skills", "input", "input_file", "output", "flow", "date", "now"].includes(h) && isStepField(r)) {
        const sid = h;
        if (!ids.has(h)) add(n.id, "error", `${what} uses {{${v}}}, but there is no step with the id "${h}".${byID(h)}`);
        else if (sid === n.id) add(n.id, "error", `${what} uses {{${v}}}: a step cannot read its own output.`);
        else if (!anc.has(sid)) add(n.id, "error", `${what} uses {{${v}}}, but "${h}" does not run before this step. Connect it first.`);
      } } };
    for (const n of f.nodes) {
      const label = n.title || n.id;
      if (n.type !== "start" && !f.edges.some((e) => e.to === n.id) && !(n.type === "loop" && (n.source || "").trim())) add(n.id, "warning", `${label} is not connected to anything before it.`);
      if (n.type === "loop" && !loopStacks(f).gather[n.id]) add(n.id, "error", `${label} needs a Gather step after the steps it repeats, to collect their results.`);
      if (n.type === "gather" && !Object.values(loopStacks(f).gather).includes(n.id)) add(n.id, "error", `${label} has no Loop before it: connect it after the steps of a Loop.`);
      if (n.type === "tool") { const t = tools.find((x) => x.name === n.tool); if (!n.tool) add(n.id, "error", `Choose a tool for ${label}.`); else if (!t) add(n.id, "error", `The tool "${n.tool}" is not available in this project.`);
        else if (!(n.instruction || "").trim()) (t.params || []).forEach((p) => { if (p.required && !(n.args?.[p.name] || "").trim()) add(n.id, "error", `${label}: the required parameter "${p.name}" is empty (fill it in, or describe the step and let AI build it).`); });
        for (const k in n.args || {}) check(n, `${label} › ${k}`, n.args[k]);
        const feeders = f.edges.filter((e) => e.to === n.id).map((e) => f.nodes.find((x) => x.id === e.from)).filter((x) => x && x.type !== "start");
        const uses = Object.values(n.args || {}).some((v) => /\{\{\s*input(_file)?\b/.test(v) || (feeders.some((x) => x.type === "loop") && /\{\{\s*item\b/.test(v)) || feeders.some((x) => new RegExp("\\{\\{\\s*steps\\." + x.id + "(?=[.}\\s])").test(v)
          || [...String(v).matchAll(/\{\{\s*([^.{}\s]+)\.(output_file|buffer_output)/g)].some((m) => m[1] === x.id || false)));
        if (feeders.length && !(n.instruction || "").trim() && !uses) add(n.id, "warning", `None of ${label}'s fields use the result of ${feeders.map((x) => x.title || x.id).join(" and ")}. It is still piped in, which only helps tools that read piped data. To use it in a field, click “← ${feeders[0].title || feeders[0].id}” next to that field, or describe the step and let AI fill the fields.`); }
      if (n.type === "ai") { if (!(n.prompt || "").trim()) add(n.id, "error", `${label} needs a prompt.`); check(n, label, n.prompt); }
      if (n.type === "merge" && f.edges.filter((e) => e.to === n.id).length < 2) add(n.id, "warning", `${label} merges fewer than two steps.`);
      if (n.type === "transform") { if (!FLOW_OPS[n.op]) add(n.id, "error", `${label}: choose a transformation.`); else if (FLOW_OPS[n.op].expr && !FLOW_OPS[n.op].exprOptional && !(n.expr || "").trim()) add(n.id, "error", `${label}: fill in the expression.`); if (n.op === "json-template") { if (!(n.template || "").trim()) add(n.id, "error", `${label}: write the template`); else try { parseTpl(n.template); } catch (e) { add(n.id, "error", `${label}: ${e.message}`); } } check(n, label, (n.source || "") + (n.template || "")); }
      if (n.type === "decision") {
        if (!(n.branches || []).length) add(n.id, "error", `${label}: add a path (a condition and where it leads).`);
        const bids = new Set(["else"]);
        for (const b of n.branches || []) { const name = b.label || b.id; if (!b.id || b.id === "else") add(n.id, "error", `${label}: the path "${name}" needs an id other than "else".`); else if (bids.has(b.id)) add(n.id, "error", `${label}: two paths have the id "${b.id}".`); bids.add(b.id); const e = condCheck(b.when); if (e) add(n.id, "error", `${label} › ${name}: ${e}`); condTemplates(b.when).forEach((t) => check(n, label + " › " + name, t)); }
        const used = new Set(); for (const e of f.edges.filter((x) => x.from === n.id)) { const to = f.nodes.find((x) => x.id === e.to)?.title || e.to; if (!e.branch) add(n.id, "warning", `${label}: the connection to ${to} is not on a path, so it always runs. Click it to choose its path.`); else if (!bids.has(e.branch)) add(n.id, "error", `${label}: the connection to ${to} is on a path that no longer exists (${e.branch}). Click it to choose a path.`); used.add(e.branch); }
        for (const b of n.branches || []) if (b.id && !used.has(b.id)) add(n.id, "warning", `${label}: nothing is connected to the path "${b.label || b.id}", so nothing runs when it is taken.`);
      }
      if (n.type === "filter") { const e = condCheck(n.cond); if (e) add(n.id, "error", `${label}: ${e}`); condTemplates(n.cond).forEach((t) => check(n, label, t)); }
      if (n.type === "aggregate") { const e = aggregateCheck(n); if (e) add(n.id, "error", `${label}: ${e}`); }
      if (n.type === "output") { check(n, label, n.template); for (const a of n.actions || []) { if (a.type === "save" && (!a.path || a.path.startsWith("/") || a.path.includes(".."))) add(n.id, "error", `${label}: save inside the project (a relative path without ..).`); check(n, label, a.path); } }
    }
    try { out.push(...checkPaths(f, shapesOf(f), loopStacks(f).stack)); } catch { /* a flow with a cycle */ }
    return out;
  };
  // what every step gets and gives (flow.Shapes): declared tool shapes, derived steps, samples
  const shapesOf = (f, samples) => { let order; try { order = flowOrder(f); } catch { return {}; } const all = [...extensions(), ...adm.managed, ...flowBuiltins()], tools = Object.fromEntries(all.filter((e) => e.output).map((e) => [e.name, e.output])), toolsBy = Object.fromEntries(all.filter((e) => e.outputBy).map((e) => [e.name, e.outputBy])); return flowShapes(f, order, { tools, toolsBy, samples: samples || {} }); };
  const renderRaw = (tpl, d) => String(tpl || "").replace(/\{\{\s*([^{}]+?)\s*\}\}/g, (_, v) => { const [h, ...r] = v.split("."); if (h === "inputs") return d.inputs?.[r[0]] ?? ""; if (h === "steps") { const s = d.steps?.[r[0]] ?? ""; if (r[1] === "json") { try { return jpath(JSON.parse(s), r.slice(2).join(".")); } catch { return ""; } } return s; } if (h === "input") return d.input ?? ""; if (h === "item") { if (r[0] === "json") { try { return jpath(JSON.parse(d.item), r.slice(1).join(".")); } catch { return ""; } } return d.item ?? ""; } if (h === "item_index") return d.itemCount ? String(d.itemIndex) : ""; if (h === "item_count") return d.itemCount ? String(d.itemCount) : "";
    if (isLoopField(r.join("."))) { const l = d.loops?.[h]; if (!l) return ""; if (r[0] === "item_index") return String(l.index); if (r[0] === "item_count") return String(l.count); if (r[1] === "json") { try { return jtext(jpath(JSON.parse(l.item), r.slice(2).join("."))); } catch { return ""; } } return l.item; }
    if (h === "input_file") return d.inputFile || "/tmp/specwright/flow-input/step.txt";
    if (isStepField(r.join("."))) { const id = h; if (id && id in (d.steps || {})) { if (r[0] === "output_file") return d.files?.[id] || ""; if (r[1] === "json") { try { return jtext(jpath(JSON.parse(d.steps[id]), r.slice(2).join("."))); } catch { return ""; } } return d.steps[id]; } return ""; }
    return d[h] ?? ""; });
  // render (flow.Render): on a Markdown table row a value stays in its cell
  // (line breaks as <br>, | escaped), so multi-line results keep the table whole
  const tableCell = (v) => String(v).replace(/\r\n/g, "\n").trim().replace(/\n/g, "<br>").replace(/(^|[^\\])\|/g, "$1\\|").replace(/(^|[^\\])\|/g, "$1\\|");
  const render = (tpl, d) => {
    tpl = String(tpl || "");
    if (!tpl.includes("|") || !tpl.includes("{{")) return renderRaw(tpl, d);
    return tpl.split("\n").map((l) => (/^\s*\|.*\|\s*$/.test(l) ? l.replace(/\{\{\s*([^{}]+?)\s*\}\}/g, (m) => tableCell(renderRaw(m, d))) : renderRaw(l, d))).join("\n");
  };
  const jtext = (v) => (typeof v === "string" ? v : v == null ? "" : typeof v === "object" ? JSON.stringify(v, null, 2) : String(v));
  // JSON → JSON (template); mirrors internal/flow/jsontemplate.go
  const EACH = /^\{\{\s*#each\s+([^{}]+?)\s*\}\}$/, WHOLE = /^\{\{\s*([^{}]+?)\s*\}\}$/;
  const quoteBare = (tpl) => { let out = "", inStr = false; for (let i = 0; i < tpl.length;) { const c = tpl[i];
    if (inStr && c === "\\") { out += tpl.slice(i, i + 2); i += 2; continue; }
    if (c === '"') inStr = !inStr;
    else if (!inStr && tpl.startsWith("{{", i)) { const m = tpl.slice(i).match(/^\{\{\s*[^{}]+?\s*\}\}/); if (m) { out += JSON.stringify(m[0]); i += m[0].length; continue; } }
    out += c; i++; } return out; };
  const parseTpl = (tpl) => { let t; try { t = JSON.parse(quoteBare(tpl || "")); } catch (e) { throw new Error("the template is not valid JSON: " + e.message); }
    const chk = (v) => { if (Array.isArray(v)) v.forEach(chk); else if (v && typeof v === "object") { const ks = Object.keys(v); for (const k of ks) { if (EACH.test(k) && ks.length > 1) throw new Error(`"${k}" must be the only key of its object: put the other fields inside its template`); if (k.includes("#each") && !EACH.test(k)) throw new Error(`"${k}": write an each block as "{{#each this.<list>}}"`); chk(v[k]); } } };
    chk(t); return t; };
  const eachOf = (t) => { if (!t || typeof t !== "object" || Array.isArray(t)) return null; const ks = Object.keys(t); const m = ks.length === 1 && ks[0].match(EACH); return m ? [m[1], t[ks[0]]] : null; };
  const jsonTemplate = (n, input, d) => {
    let src; try { src = JSON.parse(input); } catch (e) { throw new Error("the input is not JSON: " + e.message); }
    const tpl = parseTpl(n.template);
    const value = (v, sc) => { v = v.trim(); let up = false;
      for (;;) { if (v === "parent") return sc.parent ? sc.parent.this : null; if (!v.startsWith("parent.")) break; if (!sc.parent) return null; sc = sc.parent; v = v.slice(7); up = true; }
      if (up && !["this", "root", "this_index"].includes(v.split(".")[0])) v = "this." + v;
      const [h, ...r] = v.split(".");
      if (h === "this" || h === "root") { try { return jpathRaw(h === "root" ? sc.root : sc.this, r.join(".")); } catch { return null; } }
      if (h === "this_index") return sc.index;
      const s = render("{{" + v + "}}", { ...d, input }); try { return s.trim() ? JSON.parse(s) : s; } catch { return s; } };
    const text = (s, sc) => String(s).replace(/\{\{\s*([^{}]+?)\s*\}\}/g, (_, v) => { const x = value(v, sc); return x == null ? "" : typeof x === "string" ? x : JSON.stringify(x); });
    const each = (path, body, sc) => { const got = value(path, sc); if (got == null) return []; if (!Array.isArray(got)) throw new Error(`{{#each ${path}}}: that is not a list`);
      return got.map((el, i) => ev(body, { this: el, index: i + 1, parent: sc, root: sc.root })); };
    const ev = (t, sc) => {
      if (typeof t === "string") { const m = t.match(WHOLE); return m ? value(m[1], sc) ?? null : text(t, sc); }
      if (Array.isArray(t)) return t.flatMap((e) => { const b = eachOf(e); return b ? each(b[0], b[1], sc) : [ev(e, sc)]; });
      if (t && typeof t === "object") { const b = eachOf(t); if (b) return each(b[0], b[1], sc); const o = {}; for (const k of Object.keys(t)) o[text(k, sc)] = ev(t[k], sc); return o; }
      return t; };
    const top = { this: src, index: 1, parent: null, root: src };
    if (!(n.expr || "").trim()) return JSON.stringify(ev(tpl, top), null, 2);
    let list; try { list = jpathRaw(src, n.expr.trim()); } catch (e) { throw new Error(`nothing at ${n.expr}: ${e.message}`); }
    if (!Array.isArray(list)) throw new Error(`${n.expr} is not a list`);
    return JSON.stringify(list.map((el, i) => ev(tpl, { this: el, index: i + 1, parent: top, root: src })), null, 2);
  };
  const jpath = (v, path) => jtext(jpathRaw(v, path));
  const jpathRaw = (v, path) => { let cur = [v], wild = false; for (const m of String(path).replace(/^\$\.?/, "").matchAll(/\[(\d+|\*)\]|([^.[\]]+)/g)) { const nx = []; for (const c of cur) { if (m[1] === "*") { wild = true; nx.push(...(Array.isArray(c) ? c : Object.values(c || {}))); } else if (m[1] != null) { if (Array.isArray(c) && +m[1] < c.length) nx.push(c[+m[1]]); } else if (Array.isArray(c)) { wild = true; c.forEach((e) => e && m[2] in e && nx.push(e[m[2]])); } else if (c && typeof c === "object" && m[2] in c) nx.push(c[m[2]]); } cur = nx; if (!cur.length) throw new Error(`nothing at "${m[0]}"`); } return wild ? cur : cur[0]; };
  const toCsv = (v) => { const rows = (Array.isArray(v) ? v : [v]).map((r) => (r && typeof r === "object" ? r : { value: r })); const head = [...new Set(rows.flatMap((r) => Object.keys(r)))]; const q = (x) => { const s = jtext(x).replace(/\n/g, " "); return /[",]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s; }; return [head.join(","), ...rows.map((r) => head.map((k) => (k in r ? q(r[k]) : "")).join(","))].join("\n") + "\n"; };
  const fromCsv = (t) => { const [h, ...rows] = t.trim().split("\n").map((l) => l.split(",")); return JSON.stringify(rows.map((r) => Object.fromEntries(h.map((k, i) => [k, r[i] ?? ""]))), null, 2); };
  const mdHtml = (md) => md.split(/\n{2,}/).map((b) => /^#{1,6}\s/.test(b) ? `<h${b.match(/^#+/)[0].length}>${b.replace(/^#+\s*/, "")}</h${b.match(/^#+/)[0].length}>` : /^[-*]\s/.test(b) ? `<ul>${b.split("\n").map((l) => `<li>${l.replace(/^[-*]\s+/, "")}</li>`).join("")}</ul>` : `<p>${b.replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>").replace(/`(.+?)`/g, "<code>$1</code>")}</p>`).join("\n");
  const convert = (c, fmt) => { const t = String(c).trim(); let js = null; try { if (/^[[{]/.test(t)) js = JSON.parse(t); } catch { /* not json */ }
    if (fmt === "json") return js ? JSON.stringify(js, null, 2) : JSON.stringify({ content: c }, null, 2);
    if (fmt === "csv") return js ? toCsv(js) : "line\n" + t.split("\n").join("\n") + "\n";
    if (fmt === "html") return `<!doctype html><html><body style="font:15px/1.5 sans-serif;margin:1.5rem">${js ? `<pre>${t.replace(/</g, "&lt;")}</pre>` : /^\s*</.test(t) ? t : mdHtml(t)}</body></html>`;
    if (fmt === "md" && js) return "```json\n" + t + "\n```\n";
    return c; };
  const transform = (n, input, d) => {
    const re = () => new RegExp(n.expr, "g");
    switch (n.op) {
      case "template": return render(n.template, { ...d, input });
      case "trim": return input.trim(); case "uppercase": return input.toUpperCase(); case "lowercase": return input.toLowerCase();
      case "truncate": return input.slice(0, +n.expr || 0);
      case "filter-lines": return input.split("\n").filter((l) => new RegExp(n.expr).test(l)).join("\n");
      case "regex-extract": return [...input.matchAll(re())].map((m) => m[1] ?? m[0]).join("\n");
      case "regex-replace": return input.replace(re(), n.replace || "");
      case "split-lines": return JSON.stringify(input.split("\n").filter((l) => l.trim()), null, 2);
      case "json-path": return jpath(JSON.parse(input), n.expr);
      case "json-pick": { const keys = n.expr.split(",").map((k) => k.trim()); const pick = (o) => Object.fromEntries(keys.filter((k) => k in o).map((k) => [k, o[k]])); const v = JSON.parse(input); return JSON.stringify(Array.isArray(v) ? v.map(pick) : pick(v), null, 2); }
      case "json-template": return jsonTemplate(n, input, d);
      case "json-stringify": case "json-parse": {
        const field = (n.expr || "").trim(), parseStr = (s) => { let v = s; for (let k = 0; k < 3 && typeof v === "string"; k++) { try { v = JSON.parse(v.trim()); } catch { return v; } } return v; };
        if (!field) {
          if (n.op === "json-stringify") { try { return JSON.stringify(JSON.stringify(JSON.parse(input))); } catch { return JSON.stringify(input); } }
          let v; try { v = JSON.parse(input.trim()); } catch (e) { throw new Error("the input is not a JSON string or JSON: " + e.message); }
          v = parseStr(v); return typeof v === "string" ? v : JSON.stringify(v, null, 2);
        }
        let v; try { v = JSON.parse(input); } catch (e) { throw new Error("the input is not JSON: " + e.message); }
        const fn = n.op === "json-stringify" ? (x) => (typeof x === "string" ? x : JSON.stringify(x)) : (x) => (typeof x === "string" ? parseStr(x) : x);
        const toks = [...field.replace(/^\$\.?/, "").matchAll(/\[(\d+|\*)\]|([^.[\]]+)/g)];
        const walk = (cur, i) => {
          if (i === toks.length) return fn(cur);
          const m = toks[i];
          if (Array.isArray(cur)) { if (m[1] != null && m[1] !== "*") { cur[+m[1]] = walk(cur[+m[1]], i + 1); return cur; } const nx = m[1] === "*" ? i + 1 : i; return cur.map((x) => walk(x, nx)); }
          if (cur && typeof cur === "object") { const k = m[2]; if (!(k in cur)) throw new Error(`${field}: there is no field "${k}" (its fields: ${Object.keys(cur).join(", ")})`); cur[k] = walk(cur[k], i + 1); return cur; }
          throw new Error(`${field}: nothing at "${m[0]}"`);
        };
        return JSON.stringify(walk(v, 0), null, 2);
      }
      case "json-pretty": return JSON.stringify(JSON.parse(input), null, 2); case "json-minify": return JSON.stringify(JSON.parse(input));
      case "csv-to-json": return fromCsv(input); case "json-to-csv": return toCsv(JSON.parse(input));
      case "md-to-html": return mdHtml(input); case "html-to-text": return input.replace(/<[^>]+>/g, "");
      case "ai": throw new Error("AI transformations run with the agent when the flow runs");
    }
    throw new Error(`unknown transformation "${n.op}"`);
  };

  // --- flow runs (browser preview: simulated executor with the same events and decisions) ---
  const kcStore = {};
  const kcOther = {}; // other services' items, by "kind service/account"
  const kcNorm = (ref = {}, account = "") => ({ service: (ref?.service || "").trim() || "SpecWright", account: (ref?.account || "").trim() || account, kind: ref?.kind === "internet" ? "internet" : "" });
  const kcWhere = (r) => (r.kind === "internet" ? "Keychain internet password · " : "Keychain · ") + r.service + " / " + r.account;
  const kcGetRef = (r) => (r.service === "SpecWright" && !r.kind ? kcStore[r.account] : kcOther[`${r.kind} ${r.service}/${r.account}`]);
  const kcSetRef = (r, v) => { if (r.service === "SpecWright" && !r.kind) kcStore[r.account] = v; else kcOther[`${r.kind} ${r.service}/${r.account}`] = v; };
  const flowLive = new Map();
  // loops (mirrors internal/flow/loop.go)
  const splitItems = (src, n) => {
    src = String(src || "").trim(); let mode = n.split || "auto", raw = [];
    // the list inside a JSON object (flow.SplitItems): the loop's field, else the only field, else the only list
    const field = (n.field || "").trim();
    if (mode !== "regex" && (field || src.startsWith("{"))) {
      let v, ok = true; try { v = JSON.parse(src); } catch { ok = false; if (field) throw new Error(`the list should be taken from the field "${field}", but the input is not JSON`); }
      if (ok) {
        let picked, has = false;
        if (field) { const got = field.split(".").reduce((o, k) => (o && typeof o === "object" && k in o ? o[k] : undefined), v); if (got === undefined) throw new Error(`the input has no field "${field}"` + (v && typeof v === "object" && !Array.isArray(v) ? ` (its fields: ${Object.keys(v).join(", ")})` : "")); picked = got; has = true; }
        else if (v && typeof v === "object" && !Array.isArray(v)) { const ks = Object.keys(v), arrs = ks.filter((k) => Array.isArray(v[k]));
          if (ks.length === 1) { picked = v[ks[0]]; has = true; } else if (arrs.length === 1) { picked = v[arrs[0]]; has = true; }
          else throw new Error(`the input is a JSON object with the fields ${ks.join(", ")}: choose the field that holds the list in “List from”`); }
        if (has) {
          if (Array.isArray(picked)) { src = JSON.stringify(picked); if (mode === "auto" || mode === "lines") mode = "json"; }
          else { src = typeof picked === "string" ? picked.trim() : picked == null ? "" : JSON.stringify(picked); if (mode === "json" && !src.startsWith("[")) throw new Error("the list is text, not a JSON array: split it “One item per line” (or Detect)"); }
        }
      }
    }
    if (mode === "auto") { mode = "lines"; if (src.startsWith("[")) { try { JSON.parse(src); mode = "json"; } catch { /* lines */ } } }
    if (mode === "json") raw = JSON.parse(src).map((v) => (typeof v === "string" ? v : JSON.stringify(v)));
    else if (mode === "csv") { const [h, ...rows] = src.split("\n").filter((l) => l.trim()); const ks = h.split(","); raw = rows.map((r) => JSON.stringify(Object.fromEntries(r.split(",").map((v, i) => [ks[i], v])))); }
    else if (mode === "regex") { const re = new RegExp(n.expr, "g"); raw = [...src.matchAll(re)].map((m) => m[1] ?? m[0]); }
    else raw = src.split("\n").map((l) => l.trim()).filter(Boolean);
    const keep = n.filter ? new RegExp(n.filter) : null, seen = new Set(), out = [];
    for (const r of raw) { if (keep && !keep.test(r)) continue; if (n.unique && seen.has(r)) continue; seen.add(r); out.push(r); if (out.length >= (n.max || 1000)) break; }
    return out;
  };
  const itemTitle = (it) => { let s = String(it).trim(); if (s.startsWith("{")) { try { const o = JSON.parse(s); for (const k of ["name", "id", "title", "symbol", "key", "path", "file"]) if (k in o) { s = String(o[k]); break; } } catch { /* text */ } } return s.length > 120 ? s.slice(0, 120) + "…" : s; };
  const asVal = (s) => { try { return JSON.parse(s); } catch { return s; } };
  // joinText (flow.JoinText): a blank line between parts, but one line break
  // between table rows or list items, so Markdown tables and lists stay whole
  const joinText = (parts, sep) => {
    if (sep.replace(/^[ \t]+|[ \t]+$/g, "") !== "\n\n" && sep !== "\n") return parts.join(sep);
    const row = /^\s*\|.*\|\s*$/, li = /^\s*([-*+]|\d+[.)])\s+\S/;
    let out = "", prev = "";
    parts.forEach((p, i) => {
      p = String(p); if (sep === "\n\n") p = p.replace(/\n+$/, "");
      if (i > 0) { const last = prev.replace(/[\n ]+$/, "").split("\n").pop(), first = p.replace(/^\n+/, "").split("\n")[0]; out += (row.test(last) && row.test(first)) || (li.test(last) && li.test(first)) ? "\n" : sep; }
      out += p; if (p.trim()) prev = p;
    });
    return out;
  };
  const gatherItems = (n, rs, d) => {
    const ok = (r) => (r.error ? "ERROR: " + r.error : String(r.output).trim()), sep = (n.separator || "").replace(/\\n/g, "\n");
    switch (n.mode || "concat") {
      case "sections": return rs.map((r) => `## ${itemTitle(r.item)}\n\n${ok(r)}`).join("\n\n");
      case "json-array": return JSON.stringify(rs.map((r) => (r.error ? { item: asVal(r.item), error: r.error } : asVal(String(r.output).trim()))), null, 2);
      case "json-object": return JSON.stringify(Object.fromEntries(rs.map((r) => [itemTitle(r.item), r.error ? { error: r.error } : asVal(String(r.output).trim())])), null, 2);
      case "template": return joinText(rs.map((r, i) => render(n.template, { ...d, item: r.item, itemIndex: i + 1, itemCount: rs.length, input: ok(r) })), sep || "\n");
      default: return joinText(rs.map(ok), sep || "\n\n");
    }
  };
  const loopStacks = (f) => { // id → loops it runs inside; loop → its gather
    const memo = {}, par = (id) => f.edges.filter((e) => e.to === id).map((e) => e.from), node = (id) => f.nodes.find((x) => x.id === id);
    const into = (p) => (node(p)?.type === "loop" ? [...stk(p), p] : stk(p));
    const stk = (id) => { if (id in memo) return memo[id]; memo[id] = []; const ps = par(id); let s = ps.length ? into(ps[0]) : []; if (node(id)?.type === "gather" && s.length) s = s.slice(0, -1); return (memo[id] = s); };
    const stack = Object.fromEntries(f.nodes.map((n) => [n.id, stk(n.id)])), gather = {};
    for (const n of f.nodes) if (n.type === "gather") { const ps = par(n.id), s = ps.length ? into(ps[0]) : []; if (s.length) gather[s[s.length - 1]] = n.id; }
    return { stack, gather };
  };
  const flowOrder = (f) => { const indeg = Object.fromEntries(f.nodes.map((n) => [n.id, 0])); f.edges.forEach((e) => indeg[e.to]++); const ready = f.nodes.filter((n) => !indeg[n.id]).sort((a, b) => a.x - b.x || a.y - b.y).map((n) => n.id), out = [];
    while (ready.length) { const n = ready.shift(); out.push(n); f.edges.filter((e) => e.from === n).forEach((e) => { if (!--indeg[e.to]) ready.push(e.to); }); ready.sort((a, b) => { const A = f.nodes.find((x) => x.id === a), B = f.nodes.find((x) => x.id === b); return A.x - B.x || A.y - B.y; }); } return out; };
  const flowMinutes = (f) => f.minutesSaved || f.nodes.reduce((m, n) => m + ({ tool: 10, ai: 20, merge: 5, transform: 5, output: 5 }[n.type] || 0), 0);
  const startFlowRun = (f, o) => {
    const id = newRunId(), t0 = Date.now();
    const st = { id, flowId: f.id, flowName: f.name, status: "running", started: now(), options: o, outputs: [], usage: { input_tokens: 0, output_tokens: 0, cost_usd: 0, premium_requests: 0, model_calls: 0, tool_calls: 0, tool_errors: 0 },
      agent, model: agent === "copilot" ? "copilot/default" : "claude-sonnet-5-5", pass: 1, passes: Math.max(1, +o.repeat || 1), item: 1, items: 1, changes: [], minutesSaved: flowMinutes(f), steps: [], ...(o.trial ? { trial: o.trial } : {}) };
    const lr = { st, f, seq: 0, decide: null, stop: false, noReview: false };
    flowLive.set(id, lr);
    const evs = (db.events[id] = []);
    const pub = (type, summary, data = {}) => { const e = { seq: ++lr.seq, ts: now(), run_id: id, type, summary, dt: Date.now() - t0, ...data }; evs.push(e); emit(e); return e; };
    const secretVals = Object.values(runSecrets.get(o) || {}).filter((v) => v.length >= 4);
    lr.redact = (x) => { if (!secretVals.length) return clone(x); let s = JSON.stringify(x); for (const v of secretVals) s = s.split(JSON.stringify(v).slice(1, -1)).join("[REDACTED]"); return JSON.parse(s); };
    const state = () => pub("flow.state", st.status, { state: lr.redact(st), flow: f.id });
    const stepsFresh = () => flowOrder(f).map((nid) => { const n = f.nodes.find((x) => x.id === nid); return { id: n.id, title: n.title || n.id, type: n.type, tool: n.tool, status: "pending", attempts: 0, durationMs: 0, notes: [], usage: {} }; });
    db.runs.unshift({ id, workspace: db.root, workflow: f.id, step: "flow", agent, model: st.model, state: "running", started: st.started, usage: {}, kind: o.trial ? "flow-trial" : "flow", title: f.name + (o.trial ? " (trial)" : ""), ask: Object.entries(o.inputs || {}).filter(([, v]) => v).map(([k, v]) => `${k}: ${String(v).slice(0, 300)}`).join("\n") });
    const R = (tpl, d) => render(tpl, d);
    const aiAnswer = (prompt) => { const m = prompt.match(/Revise it: (.*)/); if (m) return `Revised answer (${m[1].split("\n")[0]})`; const dm = prompt.match(/Data:\n```\n([\s\S]*?)\n```/); if (dm) return dm[1].toUpperCase(); return "Summary: " + prompt.split("\n")[0].slice(0, 160); };
    const ai = async (step, prompt) => {
      const ctx = 2000 + Math.round(Math.random() * 3000);
      pub("model.request", "Request → " + st.model, { step, input_tokens: ctx, cached_tokens: Math.round(ctx * 0.7), model: st.model }); await wait(500);
      const out = 120 + Math.round(Math.random() * 240), text = aiAnswer(prompt);
      pub("model.response", "Response · end_turn", { step, output_tokens: out }); pub("model.text", text, { step, text });
      const cost = agent === "copilot" ? 0 : ((ctx * 0.3) * 3 + ctx * 0.7 * 0.3 + out * 15) / 1e6;
      pub("model.usage", agent === "copilot" ? "Copilot: 1 premium request" : "Claude: 1 turn · $" + cost.toFixed(4), { step, cost_usd: cost, premium_requests: agent === "copilot" ? 1 : 0 });
      Object.assign(st.usage, { input_tokens: st.usage.input_tokens + ctx, output_tokens: st.usage.output_tokens + out, model_calls: st.usage.model_calls + 1, cost_usd: st.usage.cost_usd + cost, premium_requests: st.usage.premium_requests + (agent === "copilot" ? 1 : 0) });
      return text;
    };
    // a rough stand-in for the built-in tools, over the mock's files
    const mockBuiltin = (name, a) => {
      const files = Object.keys(db.files).sort();
      if (name === "read_file") { const t = db.files[a.path]; if (t == null) return `${a.path}: no such file`; const ls = t.split("\n"), from = Number(a.start_line) || 1, to = Number(a.end_line) || ls.length; return ls.slice(from - 1, to).map((l, i) => String(from + i).padStart(4) + " | " + l).join("\n"); }
      if (name === "list_directory") { const d = (a.path || "").replace(/\/$/, ""); return files.filter((f) => !d || f.startsWith(d + "/")).map((f) => f.slice(d ? d.length + 1 : 0)).join("\n"); }
      if (name === "file_search") { const re = new RegExp("^" + (a.pattern || "*").replace(/[.+^$()|[\]\\]/g, "\\$&").replace(/\*\*\//g, "(?:.*/)?").replace(/\*/g, "[^/]*").replace(/\?/g, ".") + "$"); return files.filter((f) => re.test(f) || re.test(f.split("/").pop())).join("\n") || "No files match."; }
      if (name === "text_search" || name === "find_usages") { const q = String(a.query || a.symbol || ""), out = []; for (const f of files) db.files[f].split("\n").forEach((l, i) => { if (q && l.toLowerCase().includes(q.toLowerCase())) out.push(`${f}:${i + 1}: ${l.trim()}`); }); return out.slice(0, 200).join("\n") || `No matches for "${q}".`; }
      if (name === "create_file") { db.files[a.path] = a.content || ""; return `Created ${a.path} (${(a.content || "").split("\n").length} lines)`; }
      if (name === "workspace_changes") return "On branch main\nChanges not staged for commit:\n  modified: src/main/java/com/acme/orders/domain/Order.java";
      if (name === "run_command") return `$ ${a.command}\nok\n(exit code 0)`;
      if (name === "get_problems") return "Build OK: no errors or warnings.";
      if (name === "fetch_webpage") {
        if (String(a.format || "").trim() === "json") { const api = /api|json|\.json/i.test(a.url || "");
          return JSON.stringify({ url: a.url, status: 200, statusText: "200 OK", ok: true, contentType: api ? "application/json" : "text/html", ...(api ? {} : { title: "Preview page" }), headers: { "content-type": api ? "application/json" : "text/html" }, ...(api ? { json: { items: [{ id: 1, name: "first" }, { id: 2, name: "second" }] } } : { body: "The page's text (preview)." }), size: 120, truncated: false }, null, 2); }
        if (String(a.format || "").trim() === "raw") return /api|json|\.json/i.test(a.url || "") ? JSON.stringify({ items: [{ id: 1, name: "first" }, { id: 2, name: "second" }] }) : "<html><body><h1>Preview page</h1></body></html>";
        return `${a.url} → 200 OK\n\nThe page's text (preview).`;
      }
      return name + ": ok";
    };
    const tool = async (step, name, args) => {
      const e = db.exts.find((x) => x.name === name) || adm.managed.find((x) => x.name === name);
      const cid = step + "-" + lr.seq;
      pub("tool.call", `${name} ${Object.entries(args).map(([k, v]) => k + "=" + v).join(" ")}`, { step, tool: name, args, id: cid });
      if (e?.secrets?.length) {
        for (const x of e.secrets) if (x.name in kcStore && !(x.name in secrets)) secrets[x.name] = kcStore[x.name]; // found in the keychain
        await resolveSecrets(e, e.secrets);
      }
      await wait(450);
      const out = name.startsWith("builtin:") ? mockBuiltin(name.slice(8), args) : e?.sampleOutput || (name === "find-todos" ? "src/main/java/com/acme/orders/domain/OrderStatus.java:4: TODO add CANCELLED\nsrc/main/java/com/acme/orders/ports/StockPort.java:9: FIXME timeout handling\nsrc/main/java/com/acme/orders/ports/OrderRepository.java:12: TODO(urgent) paging" : name + ": ok");
      const sdir = resultsDir(id) + "/steps/" + step, isOut = f.nodes.find((x) => x.id === step)?.type === "output", pre = isOut ? name + "-" : "";
      const ext = /^\s*</.test(out) ? "html" : /^[[{]/.test(out.trim()) ? "json" : out.includes(",") && out.split("\n").length > 1 ? "csv" : "txt";
      const inputText = (lr.curInput || "");
      if (inputText) db.files[`${sdir}/${isOut ? "action-input" : "input"}.txt`] = inputText;
      db.files[`${sdir}/${pre}args.json`] = JSON.stringify(args, null, 2);
      db.files[`${sdir}/${pre}output.${ext}`] = out;
      const sst = st.steps.find((x) => x.id === step);
      if (sst && !isOut) { sst.outputFile = `${sdir}/output.${ext}`; if (inputText) sst.inputFile = `${sdir}/input.txt`; }
      pub("tool.result", `${name} ok · 450ms`, { step, tool: name, id: cid, ok: true, output: out, duration_ms: 450, output_file: `${sdir}/${pre}output.${ext}`, args_file: `${sdir}/${pre}args.json`, ...(inputText ? { input_file: `${sdir}/${isOut ? "action-input" : "input"}.txt` } : {}) });
      st.usage.tool_calls++;
      // a tool that calls AI itself (counted by the aishim launchers in the app)
      if (/lint|uml|diagram|summar/i.test(name)) {
        pub("model.usage", `${name} used AI: 1 AI call via Copilot · 1 premium request · 1840 in / 310 out tokens`, { step, tool: name, source: "tool", calls: 1, premium_requests: 1, input_tokens: 1840, output_tokens: 310, model: "gpt-5" });
        st.usage.premium_requests = (st.usage.premium_requests || 0) + 1; st.usage.input_tokens = (st.usage.input_tokens || 0) + 1840; st.usage.output_tokens = (st.usage.output_tokens || 0) + 310;
      }
      return out;
    };
    const exec = async (n, d) => {
      const parents = d.live || f.edges.filter((e) => e.to === n.id).map((e) => e.from), input = parents.map((p) => d.steps[p] ?? "").join("\n\n");
      d.input = input; lr.curInput = input;
      switch (n.type) {
        case "start": return JSON.stringify(maskSecrets(f, d.inputs), null, 2);
        case "tool": {
          let args = Object.fromEntries(Object.entries(n.args || {}).map(([k, v]) => [k, R(v, d)]).filter(([, v]) => v.trim() || !(n.instruction || "").trim()));
          let why = "";
          const req = R(n.instruction || "", d).trim();
          if (req) {
            const t = db.exts.find((x) => x.name === n.tool) || adm.managed.find((x) => x.name === n.tool);
            pub("flow.args", "Working out the arguments for " + n.tool, { step: n.id, tool: n.tool, request: req });
            pub("tool.call", "Read db/migrations/V1__orders.sql", { step: n.id, tool: "read_file", args: { path: "db/migrations/V1__orders.sql" } }); await wait(300);
            pub("tool.result", "read_file ok · 40ms", { step: n.id, tool: "read_file", ok: true, duration_ms: 40 });
            await ai(n.id, "Build the arguments for " + n.tool + ": " + req);
            for (const p of t?.params || []) if (!(p.name in args)) args[p.name] = /sql|query/i.test(p.name + p.desc) ? `-- ${req}\nSELECT c.email, count(o.id) AS orders\nFROM customers c JOIN orders o ON o.customer_id = c.id\nWHERE o.placed_at >= date_trunc('month', now()) - interval '1 month'\nGROUP BY c.email ORDER BY orders DESC LIMIT 10;` : "AI: " + req;
            why = "Read the orders and customers tables in db/migrations, then built the query from the request.";
            pub("flow.args", "Arguments for " + n.tool + ": " + Object.keys(args).join(", "), { step: n.id, tool: n.tool, args, why });
          }
          Object.assign(stepOf(n.id), { args, why });
          return tool(n.id, n.tool, args);
        }
        case "ai": return ai(n.id, R(n.prompt, d) + (f.context?.text ? "\n\n## Context\n" + f.context.text : ""));
        case "gather": return gatherItems(n, gathered[n.id] || [], d);
        case "merge": { const r = await api.PreviewFlowStep({ ...f, edges: f.edges.filter((e) => e.to !== n.id || parents.includes(e.from)) }, n.id, { inputs: d.inputs, steps: d.steps }); if (r.error) throw new Error(r.error); return r.output; }
        case "decision": case "filter": case "aggregate": {
          const src = n.source ? R(n.source, d) : input, rnd = (m) => R(m, d);
          if (n.type === "filter") return filterRows(n, src, rnd);
          if (n.type === "aggregate") return aggregateRows(n, src, rnd);
          const bs = decide(n, src, rnd), path = bs.map((b) => branchLabel(n, b)).join(", ");
          Object.assign(stepOf(n.id), { branches: bs, path }); pub("flow.decision", `${n.title || n.id} → ${path}`, { step: n.id, branches: bs });
          return src;
        }
        case "transform": {
          const src = n.source ? R(n.source, d) : input;
          if (n.op === "ai") return ai(n.id, R(n.template, { ...d, input: src }) + "\n\nData:\n```\n" + src + "\n```");
          if (n.op === "command") { pub("tool.call", "$ " + n.expr, { step: n.id, tool: "command", args: { command: n.expr } }); await wait(200); const out = /tr a-z A-Z|upper/.test(n.expr) ? src.toUpperCase() : /sort/.test(n.expr) ? src.split("\n").sort().join("\n") : src; pub("tool.result", "command ok · 200ms", { step: n.id, tool: "command", ok: true, output: out, duration_ms: 200 }); return out; }
          return transform(n, src, d);
        }
        case "output": {
          const content = n.template ? R(n.template, d) : input, conv = convert(content, n.format);
          const o = { node: n.id, title: n.title || n.id, format: n.format, content: conv, item: st.itemLabel || "", saved: [], tools: [], errors: [] };
          for (const a of n.actions || []) {
            if (a.type === "save") { const p = R(a.path, { ...d, output: conv }).replace(/^\/+/, ""); if (p.includes("..")) o.errors.push(p + " is not inside the project"); else { db.files[p] = conv; o.saved.push(p); pub("fs.write", "Saved " + p, { step: n.id, path: p, added: conv.split("\n").length, removed: 0 }); } }
            if (a.type === "tool") { await tool(n.id, a.tool, Object.fromEntries(Object.entries(a.args || {}).map(([k, v]) => [k, R(v, { ...d, output: conv })]))); o.tools.push(a.tool); }
            if (a.type === "clipboard") { navigator.clipboard?.writeText(conv).then(() => {}, () => {}); o.copied = true; }
            if (a.type === "show") o.show = true;
          }
          st.outputs.push(o); pub("flow.output", `${o.title} (${n.format.toUpperCase()})`, { step: n.id, node: n.id, format: n.format, saved: o.saved });
          return conv;
        }
      }
      return "";
    };
    const stepOf = (nid) => st.steps.find((s) => s.id === nid);
    const gathered = {}; // a Gather's per-item results
    const runStep = async (n, d) => {
      const s = stepOf(n.id), t = Date.now();
      Object.assign(s, { status: "running", error: "", attempts: s.attempts + 1, started: now() }); state();
      pub("flow.step", "Running " + s.title, { step: n.id, node: n.id, kind: n.type, status: "running" });
      try { await wait(250); const out = await exec(n, d); Object.assign(s, { status: "done", output: String(out).slice(0, 24000), durationMs: Date.now() - t, ended: now() }); pub("flow.step", s.title + " done", { step: n.id, node: n.id, status: "done", duration_ms: Date.now() - t }); state(); return out; }
      catch (e) { Object.assign(s, { status: "failed", error: e.message, durationMs: Date.now() - t }); pub("flow.step", s.title + " failed: " + e.message, { step: n.id, node: n.id, status: "failed", duration_ms: Date.now() - t }); state(); throw e; }
    };
    const wants = (n) => !lr.noReview && n.type !== "start" && (o.review === "each" || (o.review === "marked" ? n.review : o.review !== "none" && (n.review || n.type === "ai" || (n.type === "transform" && n.op === "ai"))));
    const review = async (n, d, out) => {
      for (;;) {
        const s = stepOf(n.id);
        st.status = "review"; st.review = { step: n.id, title: s.title, type: n.type, output: String(out).slice(0, 24000) }; s.status = "review"; s.output = st.review.output; state();
        pub("flow.review", "Waiting for your review of " + s.title, { step: n.id, node: n.id });
        const dec = await new Promise((res) => { lr.decide = res; });
        lr.decide = null; st.status = "running"; st.review = null;
        if (dec.action === "continue" || dec.action === "no-more-reviews") { if (dec.action === "no-more-reviews") lr.noReview = true; s.status = "done"; pub("gate.approved", dec.action === "continue" ? "Accepted " + s.title : "Accepted; no more reviews in this run", { step: n.id }); state(); return out; }
        if (dec.action === "stop") { s.status = "done"; pub("gate.rejected", "Stopped at " + s.title, { step: n.id }); throw Object.assign(new Error("stopped by you"), { stopped: true }); }
        let note = "";
        if (dec.action === "edit") { out = dec.output; note = "Edited by hand"; }
        if (dec.action === "refine") { pub("gate.changes", "Refining with AI: " + dec.prompt, { step: n.id }); out = await ai(n.id, n.type === "ai" ? R(n.prompt, d) + "\n\nYour previous answer:\n" + out + "\n\nRevise it: " + dec.prompt : dec.prompt + "\n\nData:\n```\n" + out + "\n```"); note = "Refined with AI: " + dec.prompt;
          if (dec.save) { const fl = db.flows[f.id], cur = fl.nodes.find((x) => x.id === n.id); if (cur.type === "ai") { cur.prompt += "\n\nAlso: " + dec.prompt; st.changes.push(`${s.title}: added to the prompt: "${dec.prompt}"`); } else { st.changes.push(`added an AI step after ${s.title}`); } persist(); pub("flow.saved", "Saved to the flow: " + st.changes[st.changes.length - 1], { step: n.id }); } }
        if (dec.action === "command") { pub("tool.call", "$ " + dec.command, { step: n.id, tool: "command", args: { command: dec.command } }); await wait(200); out = /tr a-z A-Z|upper/.test(dec.command) ? String(out).toUpperCase() : /sort/.test(dec.command) ? String(out).split("\n").sort().join("\n") : out; pub("tool.result", "command ok · 200ms", { step: n.id, tool: "command", ok: true, output: out, duration_ms: 200 }); note = "Ran: " + dec.command;
          if (dec.save) { st.changes.push(`added the command step after ${s.title}`); pub("flow.saved", "Saved to the flow: " + st.changes[st.changes.length - 1], { step: n.id }); } }
        if (dec.action === "retry") { const nn = { ...n, ...(dec.node || {}), id: n.id, type: n.type }; try { out = await runStep(nn, d); note = "Ran again with changed settings"; if (dec.save) { const fl = db.flows[f.id], i = fl.nodes.findIndex((x) => x.id === n.id); fl.nodes[i] = { ...fl.nodes[i], ...dec.node, id: n.id, type: n.type, x: fl.nodes[i].x, y: fl.nodes[i].y }; persist(); st.changes.push(`saved the new settings of ${s.title}`); } } catch { /* the step shows the error */ } }
        if (note) { s.notes = [...(s.notes || []), note]; s.output = String(out).slice(0, 24000); }
      }
    };
    (async () => {
      let final = "done", reason = "";
      try {
        if (o.startAt && Date.parse(o.startAt) > Date.now()) { st.status = "scheduled"; state(); pub("flow.scheduled", "Starts at " + new Date(o.startAt).toLocaleString()); while (Date.parse(o.startAt) > Date.now() && !lr.stop) await wait(500); if (lr.stop) throw Object.assign(new Error("stopped before it started"), { stopped: true }); st.status = "running"; }
        let items = [null];
        if (o.forEach?.input) { const src = o.inputs[o.forEach.input] || ""; let raw = o.forEach.split === "json" ? JSON.parse(src || "[]").map((v) => (typeof v === "string" ? v : JSON.stringify(v))) : src.split("\n").filter((l) => l.trim()); if (o.forEach.filter) raw = raw.filter((x) => new RegExp(o.forEach.filter).test(x)); items = raw.slice(0, +o.forEach.max || 100); if (!items.length) throw new Error(`no items in ${o.forEach.input}${o.forEach.filter ? " match the filter" : ""}`); }
        st.items = items.length;
        let failed = false;
        for (let pass = 1; pass <= st.passes; pass++) for (let i = 0; i < items.length; i++) {
          const inputs = { ...o.inputs, ...(runSecrets.get(o) || {}) }; if (items[i] != null) inputs[o.forEach.input] = items[i];
          Object.assign(st, { pass, item: i + 1, itemLabel: items[i] != null ? String(items[i]).slice(0, 60) : "", status: "running", steps: stepsFresh() });
          if (st.passes > 1 || items.length > 1) pub("flow.pass", [st.passes > 1 ? `Run ${pass} of ${st.passes}` : "", items.length > 1 ? `item ${i + 1} of ${items.length}` : ""].filter(Boolean).join(" · ") + (st.itemLabel ? ": " + st.itemLabel : ""));
          state();
          const d = { inputs, steps: {}, files: {}, context: f.context?.text || "", off: new Set(), branches: {} }, bad = new Set();
          const li = loopStacks(f), order = flowOrder(f);
          // run steps ids with data d; a Loop runs the steps inside it once per item
          const runNodes = async (ids, d, bad) => {
          const handled = new Set();
          for (const nid of ids) {
            if (handled.has(nid)) continue;
            const ln = f.nodes.find((x) => x.id === nid);
            // after a Decision: only the steps on the path it took run
            { const ps = f.edges.filter((e) => e.to === nid).map((e) => e.from);
              d.live = null;
              if (ps.length) { const live = liveParents(f, nid, (p) => d.off.has(p), d.branches);
                if (!live.length && !ps.some((p) => bad.has(p))) { const os = stepOf(nid); if (os) Object.assign(os, { status: "skipped", error: "not on the path the flow took" }); d.off.add(nid); state(); continue; }
                d.live = live; } }
            // an earlier run's result: not run again (trying a changed step)
            if (o.reuse?.[nid] && !d.itemCount) {
              const r = o.reuse[nid], ids2 = [nid];
              if (ln.type === "loop") { const g = li.gather[nid]; for (const b of [...order.filter((x) => (li.stack[x] || []).includes(nid)), g]) { handled.add(b); if (o.reuse[b]) { d.steps[b] = o.reuse[b].output; ids2.push(b); } else { const bs = stepOf(b); if (bs) bs.status = "done"; } } }
              d.steps[nid] = r.output; if (ln.type === "decision") d.branches = { ...d.branches, [nid]: r.branches || [] };
              for (const x of ids2) { const xs = stepOf(x); if (xs) Object.assign(xs, { status: "done", output: String(d.steps[x]).slice(0, 24000), notes: ["reused from the earlier run"], ...(x === nid && ln.type === "decision" ? { branches: r.branches, path: (r.branches || []).map((b) => branchLabel(ln, b)).join(", ") } : {}) }); }
              state(); continue;
            }
            if (ln?.type === "loop" && !f.edges.some((e) => e.to === nid && bad.has(e.from))) {
              const body = ids.filter((x) => x !== nid && (li.stack[x] || []).includes(nid)), g = li.gather[nid], ls = stepOf(nid), t = Date.now();
              body.forEach((x) => handled.add(x)); handled.add(g);
              Object.assign(ls, { status: "running", started: now(), attempts: ls.attempts + 1, iteration: 0, failedItems: 0 }); state();
              const src = (ln.source || "").trim() ? R(ln.source, d) : f.edges.filter((e) => e.to === nid).map((e) => d.steps[e.from] ?? "").join("\n");
              let items; try { items = splitItems(src, ln); } catch (e) { Object.assign(ls, { status: "failed", error: e.message }); bad.add(nid); body.forEach((x) => bad.add(x)); bad.add(g); failed = true; reason = `${ls.title} failed: ${e.message}`; state(); continue; }
              Object.assign(ls, { iterations: items.length, output: JSON.stringify(items, null, 2) }); pub("flow.loop", `${ls.title}: ${items.length} items`, { step: nid, items: items.length }); state();
              const results = [], gp = f.edges.filter((e) => e.to === g).map((e) => e.from); let stopErr = "";
              for (let k = 0; k < items.length; k++) {
                ls.iteration = k + 1; state(); pub("flow.loop", `${ls.title} · item ${k + 1} of ${items.length}: ${String(items[k]).slice(0, 60)}`, { step: nid, item: k + 1, items: items.length });
                const di = { ...d, steps: { ...d.steps, [nid]: items[k] }, files: { ...d.files }, item: items[k], itemIndex: k + 1, itemCount: items.length, loops: { ...(d.loops || {}), [nid]: { item: items[k], index: k + 1, count: items.length } }, off: new Set(d.off), branches: { ...d.branches } }, bi = new Set(bad);
                const errs = await runNodes(body, di, bi);
                const r = { item: items[k], output: gp.filter((p) => !bi.has(p)).map((p) => di.steps[p] ?? "").join("\n\n") };
                if (errs) { r.error = errs; ls.failedItems++; if (!ln.continueOnError) { stopErr = `item ${k + 1} of ${items.length}: ${errs}`; break; } }
                results.push(r);
              }
              if (stopErr) { Object.assign(ls, { status: "failed", error: stopErr }); bad.add(nid); bad.add(g); const gs = stepOf(g); if (gs) Object.assign(gs, { status: "skipped", error: "the loop " + ls.title + " failed" }); failed = true; reason = `${ls.title} failed: ${stopErr}`; state(); continue; }
              Object.assign(ls, { status: "done", durationMs: Date.now() - t, ended: now(), notes: ls.failedItems ? [`${ls.failedItems} of ${items.length} items failed; the loop went on`] : [] }); state();
              d.steps[nid] = JSON.stringify(items, null, 2);
              gathered[g] = results;
              const gn = f.nodes.find((x) => x.id === g);
              try { d.steps[g] = await runStep(gn, d); } catch (e) { if (e.stopped) throw e; bad.add(g); failed = true; reason = `${stepOf(g).title} failed: ${e.message}`; }
              continue;
            }
            if (lr.stop) throw Object.assign(new Error("stopped by you"), { stopped: true });
            const n = ln, s = stepOf(nid);
            const blocked = f.edges.some((e) => e.to === nid && bad.has(e.from));
            if ((o.skip || []).includes(nid) || blocked) { s.status = "skipped"; s.error = blocked ? "a step before it failed" : "skipped by you"; if (blocked) bad.add(nid); d.steps[nid] = ""; state(); continue; }
            if (d.itemCount) Object.assign(s, { iteration: d.itemIndex, iterations: d.itemCount });
            let out;
            try { out = await runStep(n, d); } catch (e) { if (e.stopped) throw e; bad.add(nid); if (!d.itemCount) { failed = true; reason = `${s.title} failed: ${e.message}`; } if (d.itemCount) return e.message; continue; }
            if (wants(n) && (!d.itemCount || n.review)) out = await review(n, d, out);
            if (n.type === "decision") d.branches[nid] = stepOf(nid).branches || [];
            d.steps[nid] = out;
            { const ext = /^\s*</.test(out) ? "html" : /^[[{]/.test(String(out).trim()) ? "json" : "txt", rel = resultsDir(id) + "/steps/" + nid + (d.itemCount ? "/items/" + String(d.itemIndex).padStart(3, "0") : "/output") + "." + ext;
              db.files[rel] = out; d.files[nid] = db.root + "/" + rel; const ss = st.steps.find((x) => x.id === nid); if (ss && !d.itemCount) ss.outputFile = rel; }
            if (o.stopAfter === nid) { st.steps.filter((x) => x.status === "pending").forEach((x) => { x.status = "skipped"; x.error = "the run stops after " + s.title; }); state(); break; }
          }
          return "";
          };
          await runNodes(order, d, bad);
        }
        if (failed) final = "failed";
      } catch (e) { final = e.stopped ? "stopped" : "failed"; reason = e.message; }
      Object.assign(st, { status: final, error: final === "done" ? "" : reason, ended: now(), review: null });
      st.usage.duration_ms = Date.now() - t0;
      pub("run.usage", `Usage: ${st.usage.input_tokens} in · ${st.usage.output_tokens} out · ${st.usage.tool_calls} tools · ${st.usage.model_calls} AI calls`, { ...st.usage });
      state(); pub("run.end", "Flow " + final, { state: final, reason });
      const run = db.runs.find((x) => x.id === id); Object.assign(run, { state: final, ended: st.ended, usage: st.usage });
      db.ledger.push({ at: now(), workspace: db.root, workflow: "flow:" + f.id, step: "flow", state: final, minutes_saved: final === "done" ? st.minutesSaved * st.passes * st.items : 0, ...st.usage });
      flowLive.delete(id); persist();
      setTimeout(() => writeResults(id), 150);
    })();
    return clone(st);
  };
  const blobs = new Map(); // attachments, kept in memory for previews
  const runSecrets = new WeakMap(); // run options → secret input values (like FlowRunOptions.Secrets: never in the state)
  const SECRET_MASK = "••••••••";
  const maskSecrets = (f, inputs) => Object.fromEntries(Object.entries(inputs || {}).map(([k, v]) => [k, v && f.inputs.some((i) => i.name === k && i.type === "secret") ? SECRET_MASK : v]));
  // mirrors core.flowInputValue
  const inputValue = (i, text, files) => {
    const label = i.label || i.name;
    files = [...new Set(files.map((p) => p.trim()).filter(Boolean))];
    for (const p of files) if (!(p in db.files) && !p.startsWith(".codegen/attachments/")) throw new Error(`${label}: the file ${p} was not found`);
    if (i.type === "secret") { if (/[\r\n]/.test(text)) throw new Error(`${label} must be on one line`); return text; }
    if (!text.trim() && !files.length) { text = i.default || ""; files = i.files || []; }
    const t = text.trim();
    if (i.type === "number") { if (t && isNaN(Number(t))) throw new Error(`${label} must be a number, not "${t}"`); return t; }
    if (i.type === "boolean") return /^(true|yes|y|1|on)$/i.test(t) ? "true" : "false";
    if (i.type === "multichoice") { const ls = text.split("\n").map((x) => x.trim()).filter(Boolean); for (const l of ls) if ((i.options || []).length && !i.options.includes(l)) throw new Error(`${label}: "${l}" is not one of: ${i.options.join(", ")}`); return (i.options || []).filter((o) => ls.includes(o)).join("\n"); }
    if (i.type === "choice") { if (t && (i.options || []).length && !i.options.includes(t)) throw new Error(`${label} must be one of: ${i.options.join(", ")}`); return t; }
    if (i.type === "file" || i.type === "files") { const ps = files.length ? files : t.split("\n").map((x) => x.trim()).filter(Boolean); if (i.type === "file" && ps.length > 1) throw new Error(`${label} takes one file; ${ps.length} were given`); return ps.join("\n"); }
    if (!files.length) return text;
    return t ? text.replace(/\n+$/, "") + "\n\nAttached files:\n- " + files.join("\n- ") : files.join("\n");
  };
  const api = {
    kind: "mock",
    on(cb) { listeners.add(cb); return () => listeners.delete(cb); },
    async Info() {
      return { version: "0.1.0-mock", workspace: db.root, agent, model: "", hasToken: false, storeDir: "~/Library/Application Support/codegen", node: "/usr/local/bin/node",
        agents: [{ name: "claude", label: "Claude Code", available: true, path: "/usr/local/bin/claude", version: "2.1.0 (Claude Code)", billing: "usd" }, { name: "copilot", label: "GitHub Copilot CLI", available: true, path: "/usr/local/bin/copilot", version: "0.0.354", billing: "premium" }, { name: "mock", label: "Mock agent (no CLI)", available: true, version: "scripted", billing: "usd" }],
        languages: { Java: ["Maven", "Gradle"], Kotlin: ["Gradle"], TypeScript: ["npm", "pnpm"], Python: ["uv", "pip"], "C#": ["dotnet"], Go: ["go"] }, langOrder: ["Java", "Kotlin", "TypeScript", "Python", "C#", "Go"], runtimes: RUNTIMES };
    },
    async DetectCredentials() {
      await wait(700);
      return [
        { id: "env-copilot", provider: "copilot", label: "COPILOT_GITHUB_TOKEN", where: "environment variable", found: false },
        { id: "env-gh", provider: "copilot", label: "GH_TOKEN / GITHUB_TOKEN", where: "environment variable", found: false },
        { id: "copilot-cli", provider: "copilot", label: "Copilot CLI login", where: "~/.copilot/config.json · keychain", found: true, who: "signed in with `copilot`" },
        { id: "gh-cli", provider: "copilot", label: "GitHub CLI", where: "gh auth token · github.com", found: true, who: "signed in as octo-dev", warn: "OAuth token: works for Copilot only if your org allows the GitHub CLI app" },
        { id: "env-anthropic", provider: "claude", label: "ANTHROPIC_API_KEY / CLAUDE_CODE_OAUTH_TOKEN", where: "environment variable", found: false },
        { id: "claude-cli", provider: "claude", label: "Claude Code login", where: "~/.claude · keychain", found: true, who: "signed in with `claude` (keychain)" },
      ];
    },
    async SetAgent(name, token) {
      if (!["claude", "copilot", "mock"].includes(name)) throw new Error(`unknown agent "${name}"`);
      if (name === "copilot" && token?.startsWith("ghp_")) throw new Error("the Copilot CLI rejects classic tokens (ghp_…); create a fine-grained token (github_pat_…) with the “Copilot Requests” permission");
      agent = name;
    },
    async ClearToken() {},
    async OpenWorkspace(path) { if (!path?.trim()) throw new Error("enter a folder"); if (path !== db.root) db.root = path.trim(); persist(); return wsState(); },
    async PickFolder() { return wsState(); },
    async Workspace() { return wsState(); },
    async SpecKitInfo() { return skInfo(); },
    async SpecProjects() { return mockProjects(); },
    async SwitchSpecProject(name) {
      name = (name || "").trim();
      if (!Object.keys(db.files).some((f) => f.startsWith((name ? name + "/" : "") + ".specify/"))) throw new Error(`${name || "the top folder"} has no Spec Kit project yet: set it up on Choose input`);
      selectProject(name); return wsState();
    },
    async DownloadSpecKit(force) { if (force || !db.speckitAvailable) skDownload(); return skInfo(); },
    async InitSpecKit(project, integration) {
      if (!db.speckitAvailable) await skDownload();
      await wait(400);
      integration ||= "claude"; project = (project || "").trim().replace(/^\/+|\/+$/g, "") || db.profile.specProject || "";
      if (project && (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(project))) throw new Error(`"${project}" is not a good project folder name: use letters, digits, - . _ (e.g. orders-service)`);
      selectProject(project); const P = pd();
      const res = { version: "1.4.0", integration, created: [], updated: [], kept: [] };
      for (const f0 of [...SK_CORE, ...SK_STEPS.map((s) => `${skDir(integration)}/speckit-${s}/SKILL.md`)]) {
        const f = P + f0;
        if (f in db.files) { res.kept.push(f); continue; }
        db.files[f] = f.endsWith("constitution.md") ? "# [PROJECT_NAME] Constitution\n\n(Browser preview: the real file comes from the Spec Kit downloaded on your computer.)\n" : f.endsWith(".sh") ? "#!/usr/bin/env bash\n# Spec Kit script (preview)\n" : `# ${f.split("/").slice(-2).join("/")} (Spec Kit 1.4.0, preview)\n`;
        res.created.push(f);
      }
      const prev = db.files[P + ".specify/integration.json"] ? JSON.parse(db.files[P + ".specify/integration.json"]) : { installed_integrations: [] };
      const inst = [...new Set([...(prev.installed_integrations || []), integration])];
      db.files[P + ".specify/integration.json"] = JSON.stringify({ version: "1.4.0", integration_state_schema: 1, installed_integrations: inst, integration, default_integration: integration }, null, 2);
      db.files[P + ".specify/init-options.json"] = JSON.stringify({ ai: integration, integration, script: "sh", speckit_version: "1.4.0" }, null, 2);
      const context = writeAgentContext(); persist();
      return { ...res, context, info: skInfo() };
    },
    async ExportProjectZip(withContext) {
      await wait(300);
      const P = pd(), skip = /(^|\/)(node_modules|target|build|dist|\.git)\//;
      let names = Object.keys(db.files).filter((f) => (P ? f.startsWith(P) : !f.startsWith(".codegen/runs/")) && !skip.test(f));
      if (P && withContext) {
        const sel = new Set([...db.profile.global, ...db.profile.project]);
        for (const l of libraries().filter((x) => sel.has(x.id))) names.push(...Object.keys(db.files).filter((f) => f === l.path || (l.dir && f.startsWith(l.dir + "/"))));
        names.push(...Object.keys(db.files).filter((f) => f.startsWith(".codegen/rules/")), ...Object.values(db.profile.diagrams || {}).filter((f) => f in db.files));
      }
      names = [...new Set(names)].sort();
      const enc = new TextEncoder(), entries = names.map((n) => ({ name: n, data: enc.encode(String(db.files[n])) }));
      const blob = new Blob([zipStore(entries)], { type: "application/zip" }), name = (db.profile.specProject || db.root.split("/").pop()) + "-" + new Date().toISOString().slice(0, 16).replace(/[-:T]/g, "").replace(/^(\d{8})/, "$1-") + ".zip";
      const a = document.createElement("a"); a.href = URL.createObjectURL(blob); a.download = name; a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 2000);
      return { path: name + " (browser download)", name, files: entries.length, bytes: entries.reduce((n, e) => n + e.data.length, 0), skipped: [] };
    },
    async SaveProfile(p) { db.profile = clone(p); persist(); },
    async BuildRules(section, mode) { await wait(300); return mode === "bulk" ? "Bulk load: rewrote 2 file(s) in .codegen/rules." : "Incremental: 1 file(s) updated, 1 unchanged."; },
    async SaveRulesDoc(format, content) {
      const name = { md: "rules.md", html: "rules.html" }[format];
      if (!name) throw new Error(`unknown rules format "${format}" (want md or html)`);
      const rel = ".codegen/rules/" + name; db.files[rel] = content; persist();
      emit({ type: "workspace.files", summary: "1 file(s) changed", changes: [{ path: rel, op: "modified" }] });
      return rel;
    },
    async ListFiles() { return Object.keys(db.files).sort(); },
    async ReadFile(rel) { if (!(rel in db.files)) throw new Error(`open ${rel}: no such file or directory`); return db.files[rel]; },
    async SetDiagram(kind, rel) { db.profile.diagrams ||= {}; if (rel) db.profile.diagrams[kind] = rel; else delete db.profile.diagrams[kind]; persist(); },
    async PickDiagram() { return ""; },
    async SaveTextFile() { return ""; },
    async Compile() {
      const javaFiles = Object.keys(db.files).filter((p) => p.endsWith(".java") && p.includes("/main/")).length;
      const lines = ["[INFO] Scanning for projects...", "[INFO] Building orders-service 0.1.0", "[INFO] --- compiler:3.13.0:compile (default-compile) ---", `[INFO] Compiling ${javaFiles} source files with javac [release 21]`, "[INFO] BUILD SUCCESS"];
      emit({ type: "build.start", summary: "./mvnw -q -B compile" });
      for (const l of lines) { await wait(300); emit({ type: "build.line", summary: l }); }
      emit({ type: "build.end", summary: "ok", ok: true });
      return { ok: true, command: "./mvnw -q -B compile", output: lines.join("\n") };
    },
    async Metrics() { return api.MetricsIn({}); },
    async MetricsIn(r = {}) {
      const from = r.from ? Date.parse(r.from) : -Infinity, to = r.to ? Date.parse(r.to) : Infinity;
      if (Number.isNaN(from) || Number.isNaN(to)) throw new Error("metrics: not a valid time");
      if (from >= to) throw new Error("metrics: the start time must be before the end time");
      const ledger = db.ledger.filter((e) => { const t = Date.parse(e.at); return t >= from && t < to; });
      const all = !r.from && !r.to;
      const flowMetrics = () => {
        const fl = ledger.filter((e) => String(e.workflow).startsWith("flow:")), top = {};
        const m = { executed: fl.length, completed: 0, failed: 0, stopped: 0, toolExecutions: 0, aiCalls: 0, hoursSaved: 0, projectRuns: 0, projectHours: 0, usage: { input_tokens: 0, output_tokens: 0, cost_usd: 0, premium_requests: 0 }, top: [] };
        for (const e of fl) {
          m[e.state === "done" ? "completed" : e.state === "stopped" ? "stopped" : "failed"]++;
          m.toolExecutions += e.tool_calls || 0; m.aiCalls += e.model_calls || 0; const h = (e.minutes_saved || 0) / 60; m.hoursSaved += h;
          if (e.workspace === db.root) { m.projectRuns++; m.projectHours += h; }
          for (const k of ["input_tokens", "output_tokens", "cost_usd", "premium_requests"]) m.usage[k] += e[k] || 0;
          const id = e.workflow.slice(5), t = (top[id] ||= { flow: id, runs: 0, completed: 0, tools: 0, hoursSaved: 0, costUsd: 0, premium: 0, last: e.at });
          t.runs++; if (e.state === "done") t.completed++; t.tools += e.tool_calls || 0; t.hoursSaved += h; t.costUsd += e.cost_usd || 0; t.premium += e.premium_requests || 0; if (e.at > t.last) t.last = e.at;
        }
        m.top = Object.values(top).sort((a, b) => b.runs - a.runs).slice(0, 8);
        return m;
      };
      const groups = {};
      for (const e of ledger) {
        if (e.workflow === "playground" || String(e.workflow).startsWith("flow:")) continue; // usage only, not workflow progress
        const g = (groups[e.workspace] ||= { name: e.workspace.split("/").pop(), at: e.at, approved: 0, steps: 6, files: 0, hours: 0, status: e.state, seen: {} });
        if (e.at >= g.at) { g.at = e.at; g.status = e.state; }
        g.files += e.files_created || 0;
        if (e.steps) { g.approved = e.state === "done" ? e.steps : e.steps; g.hours = e.state === "done" ? 32.5 : 7.5; }
        else if (e.state === "done" && !g.seen[e.step]) { g.seen[e.step] = 1; g.approved++; g.hours += HOURS[e.step]; }
      }
      const recent = Object.values(groups).map(({ seen, ...g }) => ({ ...g, status: g.approved >= g.steps ? "done" : g.status === "done" ? "paused" : g.status })).sort((a, b) => b.at.localeCompare(a.at));
      const sum = (k) => ledger.reduce((a, e) => a + (e[k] || 0), 0);
      const mine = groups[db.root];
      return {
        project: { filesCreated: mine?.files || 0, files: Object.keys(db.files).length, rules: db.profile.rules.length, hoursSaved: mine?.hours || 0, usage: {} },
        allTime: { timesUsed: (all ? 14 : 0) + recent.filter((r) => r.status === "done").length, hoursSaved: (all ? 63.5 : 0) + recent.reduce((a, r) => a + r.hours, 0), usage: { input_tokens: sum("input_tokens"), output_tokens: sum("output_tokens"), cost_usd: sum("cost_usd"), premium_requests: sum("premium_requests"), model_calls: sum("model_calls") } },
        perStep: Object.entries(HOURS).map(([step, hours]) => ({ step, hours })), recent, flows: flowMetrics(), range: { from: r.from || "", to: r.to || "" }, entries: ledger.length,
      };
    },
    async Workflow() { return clone(db.wf); },
    async SaveWorkflow(w) {
      for (const s of w.steps) { const d = db.wf.steps.find((x) => x.id === s.id); if (d) Object.assign(d, { input: s.input, instructions: s.instructions, options: s.options, feedback: s.feedback, gate: s.gate, retries: s.retries }); }
      db.wf.budget = w.budget; db.wf.model = (w.model || "").trim(); db.wf.context = w.context || {}; persist();
    },
    async ResetWorkflow() { if (live) throw new Error("a step is still running"); db.wf.steps.forEach((s) => (s.status = "pending")); persist(); emit({ type: "workflow.changed" }); },
    async Compose(id, fb) { return compose(db.wf.steps.find((s) => s.id === id), fb); },
    async RunPrompt(prompt, o = {}) {
      if (!prompt?.trim()) throw new Error("type a prompt first");
      if (live) throw new Error("another step is still running or waiting for approval");
      const asked = o.ask || prompt.trim(), ctxText = o.parents?.length ? contextFor(o.parents) : "";
      const meta = { ask: asked, runTitle: o.title || asked.split("\n")[0].slice(0, 100), parents: o.parents || [] };
      const startRun0 = startRun; const startRunP = (st) => startRun0({ ...st, ...meta, rawPrompt: ctxText + (st.rawPrompt || "") });
      const q = prompt.trim(), req = (q.match(/\nRequest: (.*)/) || [])[1] || q, short = req.length > 80 ? req.slice(0, 80) + "…" : req;
      if (/^You are helping in SpecWright's file browser/.test(q) && /packages|dependenc|build errors|Compile the project/i.test(req)) {
        const pk = /packages|dependenc/i.test(req);
        return startRunP({ id: "playground", title: "Playground", input: q, rawPrompt: q, gate: false, status: "pending", lastRun: "", script: [
          ["Read", "pom.xml"],
          ...(pk ? [["Bash", "./mvnw -q -B dependency:resolve"]] : [["Read", "src/main/java/com/acme/orders/domain/OrderStatus.java"]]),
          ["Bash", "./mvnw -q -B compile"],
          ["say", pk ? "All dependencies in pom.xml resolve through the configured registry; nothing was missing.\n\n• Checked imports against pom.xml: spring-boot-starter-web, spring-kafka and postgresql are declared with pinned versions.\n• `./mvnw -q -B dependency:resolve` downloaded 0 new artifacts.\n• `./mvnw -q -B compile` succeeds.\n\n(Browser preview: the desktop app lets the real agent edit pom.xml and run the build.)"
            : "The project compiles.\n\n• src/main/java/com/acme/orders/domain/OrderStatus.java:1 needed no changes.\n• `./mvnw -q -B compile` → BUILD SUCCESS.\n\n(Browser preview: the desktop app lets the real agent fix the code and rebuild.)"]] });
      }
      const script = [["Read", "specs/001-orders/spec.md"], ["Read", "src/main/java/com/acme/orders/domain/OrderStatus.java"]];
      if (o.tools && /dto|openapi|generat/i.test(q)) script.push(["ext", "gen-dto"]);
      if (/web|fetch|url|http|install|setup|set up/i.test(req)) script.push(["WebFetch", "https://docs.example.com/install"]);
      script.push(["say", `Here is what I found for “${short}”:\n\n• The project is an orders service (Java 21, Maven) laid out hexagonally: domain, application, ports.\n• The spec in specs/001-orders/spec.md covers placing an order and rejecting it when stock is missing.\n• OrderStatus has three states: NEW, PLACED and REJECTED.\n\nThis is the browser preview's scripted agent; the desktop app sends your prompt to ${agent === "copilot" ? "the Copilot CLI" : "Claude Code"}.`]);
      if (o.parents?.length) script.unshift(["Read", resultsDir(o.parents[o.parents.length - 1]) + "/answer.md"]);
      return startRunP({ id: "playground", title: "Playground", input: q, rawPrompt: q, gate: false, script, status: "pending", lastRun: "" });
    },
    async RunStep(id) {
      const i = db.wf.steps.findIndex((s) => s.id === id), st = db.wf.steps[i];
      if (!st) throw new Error(`no step "${id}"`);
      if (live) throw new Error("another step is still running or waiting for approval");
      if (i > 0 && db.wf.steps[i - 1].status !== "done") throw new Error(`cannot run ${st.title}: ${db.wf.steps[i - 1].title} is not approved yet`);
      if (st.cmd.startsWith("/speckit") && !skInfo().initialized) throw new Error("this project is not set up for Spec Kit yet: use Set up Spec Kit on Choose input first");
      writeAgentContext();
      if (!st.input.trim()) throw new Error(`cannot run ${st.title}: inputs are missing`);
      return startRun(st);
    },
    async Decide(runId, kind, feedback) { if (!live || live.id !== runId || !live.waiter) throw new Error(`run ${runId} is not waiting for approval`); const w = live.waiter; live.waiter = null; w({ kind, feedback }); },
    async CancelRun(runId) { if (!live || live.id !== runId) throw new Error(`run ${runId} is not live`); live.cancelled = true; if (live.waiter) { const w = live.waiter; live.waiter = null; w({ kind: "cancel" }); } },
    async Live() { return live ? { id: live.id } : null; },
    // housekeeping: archive, groups, auto-archive, tool tags
    async ArchiveRuns(ids, archived) { let n = 0; for (const r of db.runs) if (ids.includes(r.id) && !(archived && r.state === "running")) { r.archived = archived; r.archivedAt = archived ? now() : undefined; n++; } persist(); return n; },
    async GroupRuns(ids, group) { if (String(group).length > 80) throw new Error("a group name is at most 80 characters"); for (const r of db.runs) if (ids.includes(r.id)) r.group = String(group || "").trim(); persist(); },
    async RunGroups() { return [...new Set(db.runs.map((r) => r.group).filter(Boolean))].sort(); },
    async Housekeeping() { return clone(db.housekeeping ||= { playgroundDays: 0, historyDays: 0 }); },
    async SaveHousekeeping(h) {
      const pd = +h.playgroundDays || 0, hd = +h.historyDays || 0;
      if (pd < 0 || hd < 0 || pd > 3650 || hd > 3650) throw new Error("days must be between 0 (never) and 3650");
      let n = 0; const t = Date.now();
      for (const r of db.runs) {
        if (r.archived || ["running", "paused", "review", "scheduled"].includes(r.state)) continue;
        const days = r.step === "playground" || r.kind === "playground" ? pd : hd;
        if (days > 0 && t - Date.parse(r.ended || r.started) >= days * 864e5) { r.archived = true; r.archivedAt = now(); n++; }
      }
      db.housekeeping = { playgroundDays: pd, historyDays: hd, lastRun: now(), lastArchived: n }; persist(); return clone(db.housekeeping);
    },
    async ToolTags() { return clone(db.toolTags ||= {}); },
    async SetToolTags(tool, tags) {
      const clean = [...new Set((tags || []).map((t) => String(t).trim().toLowerCase().split(/\s+/).join("-")).filter((t) => t && t.length <= 40))];
      if (clean.length > 20) throw new Error("at most 20 tags per tool");
      db.toolTags ||= {}; if (clean.length) db.toolTags[tool] = clean; else delete db.toolTags[tool]; persist(); return clone(db.toolTags);
    },
    async Runs(limit) { return clone(db.runs.filter((r) => r.workspace === db.root).slice(0, limit || 50)); },
    // (archived runs stay in Runs, with archived: true; the lists filter them)
    async RunEvents(id) { return clone(db.events[id] || []); },
    async Extensions() { return extensions(); },
    async FlowBuiltinTools() { return flowBuiltins(); },
    async BuiltinTools() { return unlocked ? clone(BUILTINS) : []; },
    async CallBuiltin(name, a = {}) {
      if (!unlocked) throw new Error("built-in tools can only be viewed and tried in admin mode; agents can still use them");
      // a few tools emulated over the preview's in-memory files; the desktop app runs the real Go tools
      await wait(120);
      const files = Object.keys(db.files).sort();
      const num = (s) => s.split("\n");
      switch (name) {
        case "read_file": {
          const p = a.path; if (!(p in db.files)) throw new Error(`${p} does not exist`);
          const ls = num(db.files[p]), s0 = Math.max(1, a.start_line || 1), e0 = Math.min(ls.length, a.end_line || ls.length), w = String(e0).length;
          return `== ${p} (lines ${s0}-${e0} of ${ls.length})\n` + ls.slice(s0 - 1, e0).map((l, i) => `${String(s0 + i).padStart(w)} | ${l}`).join("\n");
        }
        case "list_directory": return "./ (workspace root)\n" + files.map((f) => "  " + f).join("\n");
        case "file_search": {
          const re = new RegExp("^" + (a.pattern || "*").replace(/[.+^$()|]/g, "\\$&").replace(/\{([^}]*)\}/g, (_, x) => "(" + x.split(",").join("|") + ")").replace(/\*\*\//g, "(.*/)?").replace(/\*\*/g, ".*").replace(/\*/g, "[^/]*").replace(/\?/g, "[^/]") + "$");
          const hits = files.filter((f) => re.test(a.pattern?.includes("/") ? f : f.split("/").pop()));
          return hits.length ? `${hits.length} files match "${a.pattern}":\n${hits.join("\n")}` : `No files match "${a.pattern}".`;
        }
        case "text_search": case "find_usages": {
          const q = a.query || a.symbol || "", re = new RegExp(a.is_regex ? q : q.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), /[A-Z]/.test(q) ? "" : "i");
          let n = 0; const out = [];
          for (const f of files) { const hits = num(db.files[f]).map((l, i) => [i + 1, l]).filter(([, l]) => re.test(l)); if (hits.length) { n += hits.length; out.push(f + "\n" + hits.map(([i, l]) => `  ${i}: ${l.trim()}`).join("\n")); } }
          return n ? `${n} matches in ${out.length} files for "${q}":\n\n${out.join("\n\n")}` : `No matches for "${q}".`;
        }
        case "todo_list": return "Todo list:\n" + (a.items || []).map((t, i) => `${i + 1}. ${t.status === "done" ? "[x]" : "[ ]"} ${t.title || t}`).join("\n");
      }
      return `${name} runs in the desktop app; the browser preview only emulates read_file, list_directory, file_search, text_search, find_usages and todo_list.`;
    },
    async AdminStatus() { return status(); },
    async CreateAdminKey(name, pass) {
      if (!name?.trim()) throw new Error("enter the admin's name (shown on every tool they publish)");
      if ((pass || "").length < 10) throw new Error("use a passphrase of at least 10 characters");
      if (adm.key) throw new Error("an admin key already exists on this computer; unlock it instead");
      const id = "sw1-" + hash(name + Date.now()).padStart(16, "0").slice(0, 16);
      adm.key = { id, name: name.trim(), pub: btoa(id).padEnd(44, "="), check: hash(pass) }; unlocked = true; savAdm(); return status();
    },
    async UnlockAdmin(pass) { if (!adm.key) throw new Error("there is no admin key on this computer; create one first"); await wait(400); if (hash(pass) !== adm.key.check) throw new Error("wrong passphrase"); unlocked = true; return status(); },
    async LockAdmin() { unlocked = false; },
    async AdminPolicySnippet() { if (!adm.key) throw new Error("create an admin key first"); return JSON.stringify({ admins: [{ name: adm.key.name, publicKey: adm.key.pub }], allowLocalTrust: false }, null, 2) + "\n"; },
    async TrustAdminKey() { },
    async UntrustAdminKey() { },
    async ManagedTools() { return managedView(); },
    async SaveManagedTool(e) {
      if (!unlocked) throw new Error("unlock admin mode first (Admin screen)");
      if (!/^[a-z][a-z0-9_-]{0,39}$/.test(e.name)) throw new Error("name must start with a letter and use lowercase letters, digits, - and _ (max 40)");
      const old = adm.managed.find((t) => t.name === e.name);
      const rec = { ...clone(e), kind: "script", scope: "managed", managed: { version: (old?.managed?.version || 0) + 1, signedBy: adm.key.id, signer: adm.key.name, signedAt: now(), status: "verified" } };
      adm.managed = [...adm.managed.filter((t) => t.name !== e.name), rec].sort((a, b) => a.name.localeCompare(b.name)); savAdm();
      emit({ type: "workspace.files", summary: "managed tools changed", changes: [] });
      return { ...rec, status: "managed", approved: true };
    },
    async DeleteManagedTool(name) { if (!unlocked) throw new Error("unlock admin mode first (Admin screen)"); adm.managed = adm.managed.filter((t) => t.name !== name); savAdm(); },
    async SaveExtension(e) {
      if (e.scope === "managed" || e.managed || isManaged(e.name)) refuse(e.name);
      if (!/^[a-z][a-z0-9_-]{0,39}$/.test(e.name)) throw new Error("name must start with a letter and use lowercase letters, digits, - and _ (max 40)");
      if (e.scope === "global" && !e.lastTest?.pass) throw new Error("global tools must pass a test before they can be submitted");
      const rec = { ...clone(e), kind: e.kind || "script", approved: true, approvedAt: now(), scriptPath: `.codegen/tools/${e.scope === "global" ? "global" : "project"}/${e.name}.${e.lang === "Python" ? "py" : e.lang === "Node.js" ? "js" : "sh"}` };
      rec.command = (rec.command || "").replace(/\S*\.codegen\/tools\/\S+/, rec.scriptPath);
      rec.status = e.scope === "global" ? "in review" : e.lastTest?.pass ? "tested" : "untested";
      db.files[rec.scriptPath] = rec.script || "";
      const k = db.exts.findIndex((x) => x.name === rec.name); if (k >= 0) db.exts[k] = rec; else db.exts.push(rec);
      persist(); return rec;
    },
    async ApproveExtension(name) {
      if (isManaged(name)) refuse(name);
      const e = extensions().find((x) => x.name === name); if (!e) throw new Error(`no extension named "${name}"`);
      const rec = { ...e, approved: true, status: "untested", approvedAt: now() };
      const k = db.exts.findIndex((x) => x.name === name); if (k >= 0) db.exts[k] = rec; else db.exts.push(rec);
      persist(); return rec;
    },
    async ArchiveTool(name) {
      if (isManaged(name)) refuse(name);
      const e = extensions().find((x) => x.name === name); if (!e) throw new Error(`no extension named "${name}"`);
      if (e.kind === "npm") throw new Error(`${name} is the npm script ext:${name} in package.json: delete it there and it disappears from Tools`);
      const d = new Date(), p2 = (n) => String(n).padStart(2, "0");
      const path = `.codegen/archive/${d.getFullYear()}${p2(d.getMonth() + 1)}${p2(d.getDate())}-${p2(d.getHours())}${p2(d.getMinutes())}${p2(d.getSeconds())}`;
      if (e.kind === "copilot") {
        const ext = e.dir.split("/").pop(), tools = extensions().filter((x) => x.kind === "copilot" && x.dir === e.dir).map((x) => x.name);
        delete cxProject[ext]; db.exts = db.exts.filter((x) => !(x.kind === "copilot" && x.dir === e.dir)); persist();
        return { path, tools, moved: [e.dir] };
      }
      db.exts = db.exts.filter((x) => x.name !== name); persist();
      return { path, tools: [name], moved: e.scriptPath ? [e.scriptPath] : [] };
    },
    async DeleteExtension(name) { if (isManaged(name)) refuse(name); db.exts = db.exts.filter((e) => e.name !== name); persist(); },
    async GenerateScript(r) {
      if (!(r.description || "").trim()) throw new Error(r.current ? "describe what to change in the script" : "describe what the script should do");
      await wait(900);
      const lang = r.language || (/json|api|http/i.test(r.description) ? "node" : /csv|data|parse|report/i.test(r.description) ? "python" : "bash");
      const ext = { bash: ".sh", python: ".py", node: ".mjs" }[lang];
      const slugged = r.description.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "").split("-").slice(0, 4).join("-") || "script";
      const name = r.fileName || slugged + ext;
      const head = { bash: "#!/usr/bin/env bash", python: "#!/usr/bin/env python3", node: "#!/usr/bin/env node" }[lang];
      const c = lang === "bash" ? "#" : lang === "python" ? "#" : "//";
      let body = lang === "bash"
        ? `set -euo pipefail\n\nif [[ "\${1:-}" == "-h" || "\${1:-}" == "--help" ]]; then\n  echo "usage: $0 [folder]" >&2\n  exit 0\nfi\ndir="\${1:-src}"\n\n# ${r.description}\ngrep -rn "TODO" "$dir" || true\n`
        : lang === "python" ? `import sys\n\n\ndef main(argv):\n    if len(argv) < 2 or argv[1] in ("-h", "--help"):\n        print("usage: %s <file>" % argv[0], file=sys.stderr)\n        return 2\n    # ${r.description}\n    with open(argv[1], encoding="utf-8") as f:\n        print(sum(1 for _ in f))\n    return 0\n\n\nif __name__ == "__main__":\n    sys.exit(main(sys.argv))\n`
        : `const [, , arg] = process.argv;\nif (!arg || arg === "-h" || arg === "--help") { console.error("usage: ${name} <url>"); process.exit(2); }\n// ${r.description}\nconst res = await fetch(arg);\nconsole.log(JSON.stringify(await res.json(), null, 2));\n`;
      if (r.current) body = r.current.split("\n").slice(1).join("\n").replace(/\n*$/, "\n") + `${c} Changed: ${r.description}\n`;
      return { fileName: name, content: r.current ? head + "\n" + body : `${head}\n${c} ${r.description.split("\n")[0]}\n${body}`, summary: (r.current ? "Updated: " : "") + r.description.split("\n")[0] + ". Run: .codegen/scripts/" + name + " --help" };
    },
    async GenerateExtension(r) {
      const lang = r.draft ? r.draft.lang : pickRuntime(r.runsIn);
      const lines = r.failure ? ["Reading the failed test", "Finding the cause", "Rewriting the script", "Re-checking secrets stay out of the code"]
        : r.draft ? ["Updating script and parameters", "Re-checking secrets stay out of the prompt"]
        : [`Choosing a runtime: ${lang}${r.runsIn === "Auto" ? " (Node.js → Go → Java → Bash)" : ""}`, "Writing the script", "Defining parameters and secrets", "Checking against security-baseline"];
      for (const l of lines) { await wait(380); emit({ type: "gen.model.text", summary: l }); }
      await wait(300);
      if (r.failure && (r.draft.deps || []).length && r.failure.checks.some((c) => c.label === "Dependencies installed" && !c.ok)) {
        const pin = { "Node.js": (x) => x + "@8.13.1", Python: (x) => x + "==3.2.3", Go: (x) => x + "@v5.7.1", Java: (x) => x + ":42.7.4" }[r.draft.lang] || ((x) => x);
        return { ...r.draft, deps: r.draft.deps.map((x) => /(@[\w.^~-]+$|==|:[\w.-]+:[\w.-]+$)/.test(x) ? x : pin(x)), revision: (r.draft.revision || 1) + 1 };
      }
      if (r.failure) {
        // rewrite from the tool's purpose, keeping its name
        const fresh = makeDraft(r.draft.desc || r.draft.name, lang, []);
        fresh.command = fresh.command.replace("/" + fresh.name + ".", "/" + r.draft.name + ".");
        return { ...r.draft, ...fresh, name: r.draft.name, secrets: r.draft.secrets, deps: fresh.deps || r.draft.deps, script: fresh.script + `\n${lang === "Bash" ? "#" : "//"} fixed after a failed test`, revision: (r.draft.revision || 1) + 1 };
      }
      if (r.draft) {
        const d = clone(r.draft), c = d.lang === "Bash" ? "#" : "//";
        d.script += /timeout/i.test(r.change) ? `\n${c} timeout: 30s enforced by the runner` : `\n${c} change: ${r.change.trim()}`;
        d.revision = (d.revision || 1) + 1; return d;
      }
      return { ...makeDraft(r.prompt, lang, r.connects || []), kind: "script", status: "untested", revision: 1, prompt: r.prompt };
    },
    async FlowOps() { return { ops: FLOW_OPS, mergeModes: ["concat", "json-object", "json-array", "template"], formats: ["md", "txt", "html", "csv", "json"], condOps: COND_OPS, condMatches: ["all", "any", "none"], aggFns: AGG_FNS, aggregateModes: ["group", "window"], rowFormats: ["auto", "json", "csv", "lines"] }; },
    async Flows() { return Object.values(db.flows).map(flowSummary).sort((a, b) => String(b.updated).localeCompare(String(a.updated))); },
    async LoadFlow(id) { if (!db.flows[id]) throw new Error(`no flow "${id}"`); return applyStepIDs(clone(db.flows[id])); },
    async NewFlow(name) { const id = uniqueFlowId(slug(name)); db.flows[id] = { version: 1, id, name: name || "New flow", inputs: [], context: { text: "", files: [] }, skills: [], nodes: [{ id: "start", type: "start", title: "Inputs", x: 40, y: 120 }], edges: [], updated: new Date().toISOString() }; persist(); return clone(db.flows[id]); },
    // the AI designer (scripted here: options built from the project's tools by keywords)
    async DesignFlows(r) { await wait(1200); return designFlows(r); },
    // fixing one step from Run flows (scripted here)
    async FixFlowStep(r) { await wait(900); return fixFlowStep(r); },
    async TryStepFix(fix) {
      const base = await api.FlowRun(fix.runId), f = runFlowCopy(base); if (!f) throw new Error("the flow is gone");
      const i = f.nodes.findIndex((x) => x.id === fix.stepId); if (i < 0) throw new Error(`the step ${fix.stepId} is no longer in the flow`);
      f.nodes[i] = { ...fix.node, id: fix.stepId, x: f.nodes[i].x, y: f.nodes[i].y };
      const errs = validateFlow(f).filter((x) => x.level === "error"); if (errs.length) throw new Error("the changed flow does not check out: " + errs.map((x) => x.message).join(" "));
      const prev = base.trial?.steps || [], before = prev.find((x) => x.id === fix.stepId)?.before || fix.before;
      const trial = { baseRun: base.trial?.baseRun || base.id, steps: [...prev.filter((x) => x.id !== fix.stepId), { id: fix.stepId, before, node: fix.node, why: fix.why }], reused: [] };
      const changed = new Set(trial.steps.map((x) => x.id)), li = loopStacks(f), order = flowOrder(f), anc = (id) => flowAnc(f, id);
      const rerun = new Set(); for (const c of changed) for (const l of li.stack[c] || []) { rerun.add(l); rerun.add(li.gather[l]); }
      const reuse = {};
      for (const id of order) { const n = f.nodes.find((x) => x.id === id), ps = base.steps.find((x) => x.id === id);
        if (changed.has(id) || id === "start" || n.type === "output" || rerun.has(id) || (li.stack[id] || []).length || [...changed].some((c) => anc(id).has(c)) || ps?.status !== "done") continue;
        reuse[id] = { output: ps.output || "", branches: ps.branches }; }
      for (const [l, g] of Object.entries(li.gather)) if (!!reuse[l] !== !!reuse[g]) { delete reuse[l]; delete reuse[g]; }
      trial.reused = order.filter((id) => reuse[id]);
      return startFlowRun(f, { inputs: { ...(base.options?.inputs || {}) }, review: "none", reuse, trial });
    },
    async ApplyStepFix(runId) {
      const st = await api.FlowRun(runId); if (!st.trial?.steps?.length) throw new Error("this run is not a trial of a change");
      const f = db.flows[st.flowId]; if (!f) throw new Error("the flow is gone");
      for (const t of st.trial.steps) { const i = f.nodes.findIndex((x) => x.id === t.id); if (i < 0) throw new Error(`the step ${t.id} is no longer in the flow`); f.nodes[i] = { ...t.node, id: t.id, x: f.nodes[i].x, y: f.nodes[i].y }; }
      f.updated = new Date().toISOString(); persist();
      const live = flowLive.get(runId); if (live) { live.st.trial.applied = true; live.st.changes.push("changed " + st.trial.steps.map((t) => t.node.title || t.id).join(", ")); }
      return { flow: clone(f), issues: validateFlow(f) };
    },
    async SaveFlowVersion(baseID, f) {
      const base = db.flows[baseID]; if (!base) throw new Error(`no flow "${baseID}"`);
      const family = base.parent || base.id; let rev = 2;
      for (const x of Object.values(db.flows)) if ((x.id === family || x.parent === family) && (x.revision || 0) >= rev) rev = x.revision + 1;
      const name = base.name.replace(/\s*\(v\d+\)$/, "");
      if (!base.revision && !base.parent) base.revision = 1;
      const v = { ...clone(f), id: uniqueFlowId(slug(name) + "-v" + rev), name: `${name} (v${rev})`, parent: family, revision: rev, updated: new Date().toISOString() };
      db.flows[v.id] = v; persist(); return { flow: clone(v), issues: validateFlow(v) };
    },
    async SaveFlow(f) { await wait(60); f = clone(f); if (!f.id) f.id = uniqueFlowId(slug(f.name)); f.updated = new Date().toISOString(); db.flows[f.id] = f; persist(); return { flow: clone(f), issues: validateFlow(f) }; },
    async DeleteFlow(id) { delete db.flows[id]; persist(); },
    async ValidateFlow(f) { return validateFlow(f); },
    async FlowShapes(f, samples) { return shapesOf(f, samples); },
    async ParseSchema(text) { return parseSchema(text); },
    async ExportFlow(id) { const f = db.flows[id]; const files = {}; for (const p of [...f.skills.map((x) => x.path), ...(f.context.files || [])]) if (p in db.files) files[p] = btoa(unescape(encodeURIComponent(db.files[p]))); return JSON.stringify({ format: "specwright-flow", version: 1, exported: new Date().toISOString(), flow: f, files }, null, 2); },
    async ImportFlow(text) { let b; try { b = JSON.parse(text); } catch (e) { throw new Error("not a flow file: " + e.message); } const f = b.format === "specwright-flow" ? b.flow : b; if (!f?.nodes || !f.name) throw new Error("not a SpecWright flow (export one from the Flow designer)"); f.id = uniqueFlowId(f.id || slug(f.name)); f.updated = new Date().toISOString(); db.flows[f.id] = f; persist(); return { flow: clone(f), issues: validateFlow(f), notes: [] }; },
    async PreviewFlowStep(f, id, d) {
      await wait(80);
      const n = f.nodes.find((x) => x.id === id), parents = f.edges.filter((e) => e.to === id).map((e) => e.from);
      const input = parents.map((p) => d.steps?.[p] ?? "").join("\n\n");
      try {
        if (n.type === "merge") {
          if (n.mode === "json-object") return { output: JSON.stringify(Object.fromEntries(parents.map((p) => { const t = f.nodes.find((x) => x.id === p)?.title || p; let v = d.steps[p] ?? ""; try { v = JSON.parse(v); } catch { /* text */ } return [t, v]; })), null, 2) };
          if (n.mode === "json-array") return { output: JSON.stringify(parents.map((p) => { let v = d.steps[p] ?? ""; try { v = JSON.parse(v); } catch { /* text */ } return v; }), null, 2) };
          if (n.mode === "template") return { output: render(n.template, d) };
          return { output: joinText(parents.map((p) => d.steps[p] ?? ""), (n.separator || "\\n\\n").replace(/\\n/g, "\n")) };
        }
        if (n.type === "loop") return { output: JSON.stringify(splitItems(n.source ? render(n.source, { ...d, input: parents.map((p) => d.steps?.[p] ?? "").join("\n") }) : parents.map((p) => d.steps?.[p] ?? "").join("\n"), n), null, 2) };
        if (n.type === "gather") return { output: gatherItems(n, ["first", "second", "third"].map((it, i) => ({ item: it, output: `(result for item ${i + 1})` })), d) };
        if (n.type === "transform") return { output: transform(n, n.source ? render(n.source, d) : input, d) };
        if (n.type === "filter" || n.type === "aggregate" || n.type === "decision") {
          const src = n.source ? render(n.source, d) : input, rnd = (m) => render(m, d);
          if (n.type === "filter") return { output: filterRows(n, src, rnd) };
          if (n.type === "aggregate") return { output: aggregateRows(n, src, rnd) };
          return { output: decide(n, src, rnd).map((b) => { const to = f.edges.filter((e) => e.from === n.id && e.branch === b).map((e) => f.nodes.find((x) => x.id === e.to)?.title || e.to); return "→ " + branchLabel(n, b) + (to.length ? " (runs " + to.join(", ") + ")" : " (nothing is connected to it)"); }).join("\n") };
        }
        if (n.type === "output") return { output: convert(n.template ? render(n.template, d) : input, n.format) };
        return { error: "tools and AI steps run when the flow runs; enter sample output for this step instead" };
      } catch (e) { return { error: e.message }; }
    },
    // flow commands need approval per project (mirrors internal/core/flowcommands.go)
    async FlowCommands(id) { const f = db.flows[id]; if (!f) throw new Error(`no flow "${id}"`); return flowCmds(f); },
    async ApproveFlowCommands(id, steps) {
      const f = db.flows[id]; if (!f) throw new Error(`no flow "${id}"`);
      const ok = (db.cmdOK ||= {})[db.root] ||= [];
      const cmds = flowCmds(f).filter((c) => steps.includes(c.step));
      if (!cmds.length) throw new Error("no such command step in this flow");
      for (const c of cmds) if (!ok.includes(c.command.trim())) ok.push(c.command.trim());
      persist(); return flowCmds(f);
    },
    async RunFlow(id, o = {}) {
      const f = db.flows[id]; if (!f) throw new Error(`no flow "${id}"`);
      const todo = flowCmds(f).filter((c) => !c.approved);
      if (todo.length) throw new Error("this flow runs commands that are not approved yet: " + todo.map((c) => `${c.title} ($ ${c.command})`).join("; ") + ". Review and approve them in Run flows first");
      const errs = validateFlow(f).filter((i) => i.level === "error"); if (errs.length) throw new Error("fix the flow first: " + errs.map((i) => i.message).join(" "));
      o = { ...o, inputs: { ...(o.inputs || {}) } };
      const secrets = {};
      for (const i of f.inputs) {
        o.inputs[i.name] = inputValue(i, o.inputs[i.name] || "", (o.files || {})[i.name] || []);
        if (i.required && i.type !== "secret" && !o.inputs[i.name].trim() && o.forEach?.input !== i.name) throw new Error(`${i.label || i.name} is required`);
        if (i.type === "secret") {
          const label = i.label || i.name, src = i.source || "ask", r = kcNorm(i.keychain, i.name);
          if (o.forEach?.input === i.name) throw new Error(`${label} is a secret: a run cannot repeat once per line of it`);
          let v = o.inputs[i.name];
          if (v && (o.saveSecrets || []).includes(i.name)) kcSetRef(r, v);
          if (!v && (src === "keychain" || src === "auto")) v = kcGetRef(r) || "";
          if (!v && src === "keychain") throw new Error(`${label}: nothing found in ${kcWhere(r)}. Type it in the run form (tick “Save to Keychain” to keep it), or add it with Keychain Access`);
          if (!v && src === "env") throw new Error(`${label}: the environment variable ${i.env} is not set`);
          if (!v && src === "auto" && i.required) throw new Error(`${label}: not found in ${kcWhere(r)}${i.env ? " or $" + i.env : ""}; type it in the run form`);
          if (!v && i.required && src === "ask") throw new Error(`${label} is required`);
          if (!v && src === "prompt") v = "prompted-in-preview"; // the desktop app asks in a dialog once the run starts
          secrets[i.name] = v; o.inputs[i.name] = v ? SECRET_MASK : "";
        }
      }
      runSecrets.set(o, secrets);
      return startFlowRun(clone(f), o);
    },
    async FlowRun(id) { const l = flowLive.get(id); if (l) return l.redact ? l.redact(l.st) : clone(l.st); const st = [...(db.events[id] || [])].reverse().find((e) => e.type === "flow.state")?.state; if (!st) throw new Error(`no flow run "${id}"`); return clone({ ...st, status: ["running", "review", "scheduled"].includes(st.status) ? "interrupted" : st.status }); },
    async FlowRuns(limit) { return clone(db.runs.filter((r) => r.step === "flow" && !r.archived && r.workspace === db.root).slice(0, limit || 100)); },
    async DecideFlow(id, d) { const l = flowLive.get(id); if (!l?.decide) throw new Error("the run is not waiting for a review"); l.decide(d); },
    async StopFlowRun(id) { const l = flowLive.get(id); if (!l) throw new Error(`run ${id} is not live`); l.stop = true; if (l.decide) l.decide({ action: "stop" }); },
    async FlowCredentials(id) {
      const f = db.flows[id], used = new Set(f.nodes.flatMap((n) => [n.type === "tool" ? n.tool : null, ...(n.actions || []).filter((a) => a.type === "tool").map((a) => a.tool)]).filter(Boolean));
      const out = new Map();
      for (const e of [...db.exts, ...adm.managed]) if (used.has(e.name)) for (const s of e.secrets || []) { const c = out.get(s.name) || { ...infoOf(s), desc: s.desc, tools: [] }; if (c.via === "ask" && s.name in kcStore) c.via = "keychain"; c.tools.push(e.name); out.set(s.name, c); }
      return [...out.values()];
    },
    async KeychainAvailable() { return true; },
    // keychain items: SpecWright's own (service "SpecWright", by name) live in kcStore
    async CheckKeychain(ref) { const r = kcNorm(ref); return { available: true, found: !!kcGetRef(r), where: kcWhere(r) }; },
    async SaveToKeychain(ref, value) { const r = kcNorm(ref); if (!r.account || !value) throw new Error("an account and a value are needed"); if (/[\r\n]/.test(value)) throw new Error("a value saved in the keychain must be on one line"); kcSetRef(r, value); },
    async FlowSecretStatus(id) {
      const f = db.flows[id]; if (!f) throw new Error(`no flow "${id}"`);
      return f.inputs.filter((i) => i.type === "secret").map((i) => {
        const src = i.source || "ask", r = kcNorm(i.keychain, i.name), kcHas = !!kcGetRef(r), envHas = false; // the browser preview has no environment
        const where = src === "env" ? "$" + (i.env || "") : src === "auto" && !kcHas && i.env ? kcWhere(r) + " or $" + i.env : kcWhere(r);
        return { name: i.name, source: src, found: src === "keychain" ? kcHas : src === "env" ? envHas : src === "auto" ? kcHas || envHas : false, where: src === "ask" || src === "prompt" ? "" : where };
      });
    },
    async SaveSecretToKeychain(name, value) { if (!name || !value) throw new Error("a name and a value are needed"); kcStore[name] = value; },
    async RemoveSecretFromKeychain(name) { delete kcStore[name]; },
    async RunResults(id) { const r = db.runs.find((x) => x.id === id); if (!r) throw new Error(`no run ${id}`); const dir = r.results || resultsDir(id); const files = Object.keys(db.files).filter((p) => p.startsWith(dir + "/")).map((p) => p.slice(dir.length + 1)).sort(); return { id, dir, abs: db.root + "/" + dir, files, ready: files.length > 0 }; },
    async RunThread(id) { const t = thread(id); if (!t.length) throw new Error(`no run ${id}`); return clone(t); },
    async RecentRuns(limit) { return clone(db.runs.filter((r) => !r.archived && r.workspace === db.root).slice(0, limit || 50).map((r) => digest(r.id))); },
    async RevealPath(p) { throw new Error("The browser preview cannot open Finder; the desktop app shows " + p + " there."); },
    async Libraries() { return annotated(); },
    async ManagedLibraries() { return clone(adm.libs.map((l) => ({ ...l, status: "verified", source: "app" }))); },
    async SaveManagedLibrary(name, desc, content) {
      await wait(250);
      if (!unlocked) throw new Error("unlock admin mode first (Admin screen)");
      if (!/^[a-z0-9][a-z0-9._-]{0,59}$/.test(name || "")) throw new Error("name must start with a lowercase letter or digit and use lowercase letters, digits, '.', '-' and '_' (max 60)");
      if (!content?.trim()) throw new Error("the skill is empty");
      const old = adm.libs.find((l) => l.name === name);
      const dir = `.codegen/skills/global/${name}/`;
      const files = old ? old.files || {} : Object.fromEntries(Object.keys(db.files).filter((f) => f.startsWith(dir) && f !== dir + "SKILL.md").map((f) => [f.slice(dir.length), db.files[f]]));
      const rec = { name, desc: desc || "", content, files, version: (old?.version || 0) + 1, signedBy: adm.key?.id || "sw1-mock", signer: adm.key?.name || "Admin", signedAt: new Date().toISOString() };
      adm.libs = [...adm.libs.filter((l) => l.name !== name), rec].sort((a, b) => a.name.localeCompare(b.name)); savAdm();
      return annotated();
    },
    async SaveManagedLibraryFile(name, file, content, oldFile) {
      await wait(200);
      if (!unlocked) throw new Error("unlock admin mode first (Admin screen)");
      const ok = (x) => !!x && x !== "SKILL.md" && x.split("/").length <= 6 && x.split("/").every((s) => /^[A-Za-z0-9_][A-Za-z0-9._-]{0,79}$/.test(s));
      if (!ok(file)) throw new Error("folder and file names use letters, digits, '.', '-' and '_'");
      if (!content?.trim()) throw new Error("the file is empty");
      const cur = adm.libs.find((l) => l.name === name);
      if (!cur) throw new Error(`publish the corporate skill ${name} first, then add files to it`);
      const files = { ...(cur.files || {}) };
      if (file in files && oldFile !== file) throw new Error(`${name} already has a file ${file}`);
      if (oldFile && oldFile !== file) delete files[oldFile];
      files[file] = content;
      const rec = { ...cur, files, version: cur.version + 1, signedBy: adm.key?.id || "sw1-mock", signer: adm.key?.name || "Admin", signedAt: new Date().toISOString() };
      adm.libs = adm.libs.map((l) => (l.name === name ? rec : l)); savAdm();
      return annotated();
    },
    async DeleteManagedLibraryFile(name, file) {
      if (!unlocked) throw new Error("unlock admin mode first (Admin screen)");
      const cur = adm.libs.find((l) => l.name === name);
      if (!cur || !(file in (cur.files || {}))) throw new Error(`${name} has no file ${file}`);
      const files = { ...cur.files }; delete files[file];
      adm.libs = adm.libs.map((l) => (l.name === name ? { ...cur, files, version: cur.version + 1, signedAt: new Date().toISOString() } : l)); savAdm();
      return annotated();
    },
    async DeleteManagedLibrary(name) { if (!unlocked) throw new Error("unlock admin mode first (Admin screen)"); adm.libs = adm.libs.filter((l) => l.name !== name); savAdm(); return annotated(); },
    async SaveProjectLibrary(r) {
      await wait(150);
      const name = (r.name || "").trim().replace(r.kind === "skill" ? /\.md$/ : /$^/, "");
      if (r.kind === "skill" && !/^[a-z0-9][a-z0-9._-]{0,59}$/.test(name)) throw new Error("skill names use lowercase letters, digits, '.', '-' and '_' (they become a folder name)");
      if (r.kind === "script" && !/^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$/.test(name)) throw new Error("script file names use letters, digits, '.', '-' and '_', e.g. gen-dto.sh");
      if (!r.content?.trim()) throw new Error(`the ${r.kind} is empty`);
      let rel = r.kind === "skill" ? `.codegen/skills/project/${name}/SKILL.md` : `.codegen/scripts/${name}`;
      if (r.kind === "skill" && r.oldPath === `.codegen/skills/project/${name}.md`) rel = r.oldPath;
      if (r.oldPath !== rel && rel in db.files) throw new Error(`a ${r.kind} called ${name} already exists`);
      if (r.kind === "skill" && r.oldPath && r.oldPath !== rel && r.oldPath.endsWith("/SKILL.md")) {
        // a skill folder keeps its docs and scripts
        const from = r.oldPath.replace(/\/SKILL\.md$/, "/"), to = rel.replace(/\/SKILL\.md$/, "/");
        for (const f of Object.keys(db.files)) if (f.startsWith(from) && f !== r.oldPath) { db.files[to + f.slice(from.length)] = db.files[f]; delete db.files[f]; }
      }
      db.files[rel] = r.content;
      if (r.oldPath && r.oldPath !== rel) {
        delete db.files[r.oldPath];
        const oldName = r.oldPath.replace(/\/SKILL\.md$|\.md$/, "").split("/").pop(), oldId = `project:${r.kind}:${oldName}`, newId = `project:${r.kind}:${name}`;
        db.profile.project = (db.profile.project || []).map((x) => (x === oldId ? newId : x));
      }
      persist(); return annotated();
    },
    async SaveSkillFile(r) {
      await wait(120);
      const clean = (x) => String(x || "").trim().replace(/^\/+|\/+$/g, "");
      const ok = (x) => !!x && x !== "SKILL.md" && x.split("/").length <= 6 && x.split("/").every((s) => /^[A-Za-z0-9_][A-Za-z0-9._-]{0,79}$/.test(s));
      const rel = clean(r.path), old = clean(r.oldPath);
      if (!ok(rel)) throw new Error(rel === "SKILL.md" ? "SKILL.md is the skill itself; edit it with Edit" : "folder and file names use letters, digits, '.', '-' and '_'");
      if (!r.content?.trim()) throw new Error("the file is empty");
      const name = (r.skill || "").trim(), dir = `.codegen/skills/project/${name}`;
      if (!(dir + "/SKILL.md" in db.files)) {
        if (`${dir}.md` in db.files) { db.files[dir + "/SKILL.md"] = db.files[`${dir}.md`]; delete db.files[`${dir}.md`]; }
        else throw new Error(`there is no project skill called ${name}; create it first`);
      }
      if (old !== rel && (dir + "/" + rel) in db.files) throw new Error(`${name} already has a file ${rel}`);
      db.files[dir + "/" + rel] = r.content;
      if (old && old !== rel) delete db.files[dir + "/" + old];
      persist(); return annotated();
    },
    async DeleteSkillFile(skill, file) {
      const k = `.codegen/skills/project/${skill}/${file}`;
      if (!(k in db.files) || file.includes("..")) throw new Error(`${k}: no such file`);
      delete db.files[k]; persist(); return annotated();
    },
    async DeleteProjectLibrary(path) {
      if (!/^\.codegen\/(skills\/project|scripts)\//.test(path || "") || path.includes("..")) throw new Error(/skills\/global/.test(path) ? "corporate libraries are managed by an admin; unlock admin mode to change them" : `${path} is not a project skill or script`);
      delete db.files[path];
      if (path.endsWith("/SKILL.md")) { const d = path.slice(0, -"SKILL.md".length); for (const f of Object.keys(db.files)) if (f.startsWith(d)) delete db.files[f]; }
      persist(); return annotated();
    },
    async PreviewFile(path) {
      await wait(80);
      const p = String(path || "").trim().replace(/^[`'"]|[`'"]$/g, "").replace(/^\.\//, "");
      const blob = blobs.get(p) || [...blobs.entries()].find(([k]) => ("/" + k).endsWith("/" + p))?.[1];
      if (blob) {
        const kind = blob.mime.startsWith("image/") ? "image" : blob.mime === "application/pdf" ? "pdf" : blob.mime.startsWith("text/") ? "text" : "binary";
        return { path: db.root + "/" + blob.rel, rel: blob.rel, name: blob.rel.split("/").pop(), size: blob.size, mod: new Date().toISOString(), kind, mime: blob.mime,
          ...(kind === "text" ? { text: new TextDecoder().decode(Uint8Array.from(atob(blob.b64), (c) => c.charCodeAt(0))) } : kind === "binary" ? {} : { data: blob.b64 }) };
      }
      const files = Object.keys(db.files);
      let rel = files.includes(p) ? p : files.filter((f) => ("/" + f).endsWith("/" + p)).sort((a, b) => a.length - b.length)[0];
      if (!rel) {
        const dir = files.filter((f) => f.startsWith(p.replace(/\/$/, "") + "/"));
        if (dir.length) return { path: db.root + "/" + p, rel: p, name: p.split("/").pop(), size: 0, kind: "dir", entries: [...new Set(dir.map((f) => f.slice(p.length + 1).split("/")[0] + (f.slice(p.length + 1).includes("/") ? "/" : "")))] };
        throw new Error(p.startsWith("/") || p.startsWith("~") ? `${p}: the browser preview can only show project files; the desktop app opens any file` : `${p} was not found in the project`);
      }
      const text = db.files[rel], matches = files.filter((f) => f !== rel && ("/" + f).endsWith("/" + p));
      return { path: db.root + "/" + rel, rel, name: rel.split("/").pop(), size: text.length, mod: new Date().toISOString(), kind: "text", mime: "text/plain", text, matches };
    },
    async SaveAttachment(name, data) {
      await wait(150);
      const b64 = String(data).replace(/^data:[^,]*,/, ""), size = Math.floor(b64.length * 3 / 4);
      if (!size) throw new Error("the file is empty");
      const safe = (name || "attachment").replace(/[^A-Za-z0-9._-]+/g, "-").replace(/^[-.]+|[-.]+$/g, "") || "attachment";
      const mime = (String(data).match(/^data:([^;,]+)/) || [])[1] || "";
      const kind = mime.startsWith("image/") ? "image" : mime === "application/pdf" || /\.pdf$/i.test(safe) ? "pdf" : mime.startsWith("text/") ? "text" : "file";
      const stamp = new Date().toISOString().replace(/[-:T]/g, "").slice(0, 14);
      const rel = `.codegen/attachments/${stamp.slice(0, 8)}-${stamp.slice(8)}-${safe}`;
      blobs.set(rel, { rel, b64, mime: mime || "application/octet-stream", size });
      return { path: rel, name, size, mime, kind };
    },
    async TestExtensionWith(d, q, files = []) {
      const r = await api.TestExtension(d, q);
      if (files.length) r.steps.unshift({ k: "attach", v: files.join("\n") });
      return r;
    },
    async TestExtension(d, q) {
      if (!q.trim()) throw new Error("write what you would ask the agent");
      await wait(1600);
      const deps = d.deps || [];
      let installStep = null;
      if (deps.length) {
        const reg = { "Node.js": db.pkg?.npm?.registry || "https://registry.npmjs.org/", Python: db.pkg?.pip?.indexUrl || "https://pypi.org/simple", Go: db.pkg?.go?.proxy || "https://proxy.golang.org,direct", Java: db.pkg?.maven?.mirrorUrl || "https://repo.maven.apache.org/maven2" }[d.lang];
        const unpinned = deps.filter((x) => !/(@[\w.^~-]+$|==|:[\w.-]+:[\w.-]+$)/.test(x));
        const cmd = { "Node.js": "$ npm install --no-audit --no-fund --no-package-lock --omit=dev", Python: "$ python3 -m venv .venv\n$ .venv/bin/python -m pip install -r requirements.txt", Go: "$ go get " + deps.join(" ") + "\n$ go mod tidy\n$ go build -o bin/" + d.name + " .", Java: "$ mvn -q -B -s .codegen/cache/maven-settings.xml dependency:copy-dependencies" }[d.lang] || "";
        if (!cmd) return { pass: false, steps: [{ k: "out", v: "error: Bash tools cannot declare dependencies" }], checks: [{ label: "Dependencies installed", ok: false }], output: "", at: now(), summary: "Failed: Bash tools cannot have dependencies." };
        const proxy = db.pkg?.proxy?.https || db.pkg?.proxy?.http;
        const log = [cmd, `registry: ${reg}${proxy ? " via " + proxy.replace(/:[^:@/]+@/, ":xxxxx@") : ""}`];
        if (unpinned.length) {
          log.push(`error: ${unpinned[0]}: no exact version given; the installer refuses floating versions`);
          return { pass: false, steps: [{ k: "install", v: log.join("\n") }, { k: "out", v: `error: installing ${unpinned.join(", ")} failed` }], checks: [{ label: "Dependencies installed", ok: false }], output: "", at: now(), summary: `Failed: installing ${deps.join(", ")} did not work. Check the package names and versions, or the proxy and registry under Packages & proxy.` };
        }
        log.push(...deps.map((x) => "added " + x), "Dependencies installed.");
        installStep = { k: "install", v: log.join("\n") };
      }
      let missing = [], secretErr = "";
      try { await resolveSecrets(d, d.secrets); } catch (e) { secretErr = e.message; missing = d.secrets || []; }
      const call = d.call || {};
      const placeholders = [...(d.command || "").matchAll(/\{\{(\w+)\}\}/g)].map((m) => m[1]);
      const argsOK = (d.params || []).every((p) => !p.required || (call[p.name] != null && placeholders.includes(p.name))) && placeholders.every((n) => (d.params || []).some((p) => p.name === n));
      const prints = /console\.log|echo |printf|print\(|fmt\.Print|System\.out/.test(d.script || "");
      const ran = !missing.length && argsOK;
      const out = missing.length ? `secrets: ${secretErr}` : !argsOK ? "error: arguments do not match the command placeholders" : prints ? d.sampleOutput || "ok" : "(no output)";
      const steps = [
        ...(installStep ? [installStep] : []),
        { k: "say", v: `Read the tool schema for ${d.name} (${d.params.length} parameter${d.params.length === 1 ? "" : "s"}).` },
        { k: "call", v: JSON.stringify({ tool: d.name, arguments: call }, null, 2) },
        { k: "run", v: "$ " + (d.command || "").replace(/\{\{(\w+)\}\}/g, (_, n) => call[n] ?? "").trim() },
        { k: "out", v: out },
      ];
      const checks = [...(installStep ? [["Dependencies installed", true]] : []), ["Agent picked this tool for the request", true], ["Arguments match the parameter schema", argsOK], ["Exit code 0", ran], ["Output answers the request", ran && prints], ["No secret values in prompt or output", true], ["Finished within 30 s", true]].map(([label, ok]) => ({ label, ok }));
      const pass = checks.every((c) => c.ok);
      const failed = checks.filter((c) => !c.ok).map((c) => c.label.toLowerCase()).join("; ");
      return { pass, steps, checks, output: out, at: now(), summary: pass ? `The tool answered “${q.trim()}” correctly.` : `Failed: ${failed}.${missing.length ? ` ${secretErr}.` : ""}` };
    },
    async PackageSettings() {
      db.pkg ||= seed().pkg;
      const pub = clone(db.pkg);
      for (const k of ["http", "https"]) pub.proxy[k] = (pub.proxy[k] || "").replace(/\/\/([^:@/]+):[^@/]*@/, "//$1:xxxxx@");
      return { saved: pub, caches: ".codegen/cache/{npm,pip,go-mod,m2}", tools: { npm: true, python3: true, go: true, mvn: false, java: false },
        detected: { proxy: { http: "", https: "", noProxy: "" }, caFile: "", npm: { registry: "https://registry.npmjs.org/" }, pip: { indexUrl: "https://pypi.org/simple", extraIndexUrl: "", trustedHost: "" }, go: { proxy: "https://proxy.golang.org,direct", private: "", noSumDb: "", sumDb: "sum.golang.org", insecure: "", toolchain: "auto" }, maven: { mirrorUrl: "https://repo.maven.apache.org/maven2", mirrorOf: "central" } } };
    },
    async SavePackageSettings(s) {
      if (!db.speckitAvailable) skDownload(); // proxies set up for the first time: fetch Spec Kit through them
      for (const u of [s.proxy.http, s.proxy.https, s.npm.registry, s.pip.indexUrl, s.pip.extraIndexUrl, s.maven.mirrorUrl]) if (u && !/^[a-z]+:\/\/[^/]+/i.test(u)) throw new Error(`"${u}" is not a URL (want e.g. https://host:port/path)`);
      const old = db.pkg || {}; const next = clone(s);
      for (const k of ["http", "https"]) if (/:xxxxx@/.test(next.proxy[k])) next.proxy[k] = old.proxy?.[k] || next.proxy[k];
      db.pkg = next; persist();
    },
    async CheckRegistries() {
      await wait(900);
      const s = db.pkg, via = s.proxy.https || s.proxy.http || "direct";
      const bad = /127\.0\.0\.1|localhost/.test(via);
      const row = (eco, base, path) => ({ ecosystem: eco, url: base.replace(/\/$/, "") + path, ok: !bad, status: bad ? "proxyconnect tcp: connection refused" : "200 OK", via: via.replace(/:[^:@/]+@/, ":xxxxx@"), ms: 80 + Math.round(Math.random() * 300) });
      return [row("npm", s.npm.registry || "https://registry.npmjs.org", "/-/ping"), row("pip", s.pip.indexUrl || "https://pypi.org/simple", "/pip/"), row("go", (s.go.proxy || "https://proxy.golang.org").split(",")[0], "/golang.org/x/text/@v/list"), row("maven", s.maven.mirrorUrl || "https://repo.maven.apache.org/maven2", "/org/apache/maven/plugins/maven-dependency-plugin/maven-metadata.xml")];
    },
    async PickCAFile() { return ""; },
    async SetSecret(name, value) { if (value) secrets[name] = value; else delete secrets[name]; },
    async SecretStatus(names) { return Object.fromEntries(names.map((n) => [n, ["session", "env"].includes(infoOf({ name: n }).via)])); },
    async SecretInfo(list) { return (list || []).map(infoOf); },
    async AnswerSecrets(id, values, remember) { const r = pendingAsks.get(id); if (!r) throw new Error("that request is no longer waiting"); r({ values, remember }); },
    async CopilotExtensions() {
      const user = { name: "html-formatter", source: "user", dir: "~/.copilot/extensions/html-formatter", entry: "extension.mjs", desc: "Formats an existing .html or .htm file in the workspace in place using Prettier.",
        tools: [{ name: "format_html", desc: "Format an existing HTML file in the current workspace with Prettier. Modifies the file in place; accepts a workspace-relative or absolute path." }],
        files: ["README.md", "extension.mjs", "formatter.mjs", "formatter.test.mjs", "package-lock.json", "package.json", "sdk-loader.mjs"], size: 9420, deps: { prettier: "3.6.2" }, modules: true };
      const out = [{ ...user, inProject: !!cxProject[user.name], projectSame: cxProject[user.name] === "same" }];
      for (const n of Object.keys(cxProject)) out.push({ ...user, name: n, source: "project", dir: db.root + "/.github/extensions/" + n, modules: true, fromUser: n === user.name, inProject: false, projectSame: false });
      return out;
    },
    async LoadCopilotExtension(name, replace, install) {
      if (cxProject[name] && !replace) throw new Error(".github/extensions/" + name + " already exists in this project");
      await wait(install ? 900 : 300);
      cxProject[name] = "same";
      return { path: ".github/extensions/" + name, tools: name === "html-formatter" ? ["format_html"] : [], log: install ? ["$ npm install --no-audit --no-fund --no-package-lock --omit=dev --loglevel=error", "added 1 package in 0.8s", "Added .github/extensions/*/node_modules/ to .gitignore."] : [] };
    },
    async RemoveCopilotExtension(name) { delete cxProject[name]; db.exts = db.exts.filter((e) => !(e.kind === "copilot" && e.dir === ".github/extensions/" + name)); return { path: ".codegen/archive/" + new Date().toISOString().replace(/[-:]/g, "").replace("T", "-").slice(0, 15), tools: name === "html-formatter" ? ["format_html"] : [], moved: [".github/extensions/" + name] }; },
    async Models() {
      return [
        { agent: "claude", current: "", models: [
          { id: "opus", label: "Opus", note: "Most capable for long, complex coding work", tier: "best", alias: true },
          { id: "fable", label: "Fable", note: "Frontier reasoning for the hardest problems", tier: "best", alias: true },
          { id: "sonnet", label: "Sonnet", note: "Fast and strong; a good everyday default", tier: "balanced", alias: true },
          { id: "haiku", label: "Haiku", note: "Fastest and cheapest, for quick questions", tier: "fast", alias: true },
          { id: "claude-opus-5-5", label: "Claude Opus 5.5", tier: "best" }, { id: "claude-fable-5-1", label: "Claude Fable 5.1", tier: "best" },
          { id: "claude-sonnet-5-5", label: "Claude Sonnet 5.5", tier: "balanced" }, { id: "claude-haiku-5-5", label: "Claude Haiku 5.5", tier: "fast" }] },
        { agent: "copilot", current: "", models: [
          { id: "auto", label: "Auto", note: "Copilot picks a model for each request", tier: "auto" },
          { id: "claude-sonnet-5.5", label: "Claude Sonnet 5.5", tier: "balanced" }, { id: "claude-opus-5.5", label: "Claude Opus 5.5", tier: "best" },
          { id: "gpt-5.6-sol", label: "GPT 5.6 Sol", tier: "balanced" }] },
        { agent: "mock", current: "", models: [{ id: "mock-fast", label: "Mock fast", tier: "fast" }, { id: "mock-best", label: "Mock best", tier: "best" }] },
      ];
    },
    async AnswerPermission(id, decision, note) { const r = pendingPerms.get(id); if (!r) throw new Error("that request is no longer waiting"); r({ decision, note }); },
    async CancelSecrets(id) { const r = pendingAsks.get(id); if (r) r({ cancel: true }); },
  };
  for (const m of methods) if (!api[m]) api[m] = async () => { throw new Error(m + " is not available in the browser preview"); };
  return api;
}

const slug = (s) => s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").replace(/^[^a-z]+/, "").split("-").slice(0, 3).join("-") || "tool";

// Tool languages "installed" in the preview, in priority order. Auto uses the first.
const RUNTIMES = ["Node.js", "Go", "Bash"];
const pickRuntime = (runsIn) => (runsIn && runsIn !== "Auto" ? runsIn : RUNTIMES[0]);
const EXT = { "Node.js": "js", Go: "go", Java: "java", Python: "py", Bash: "sh" };
const RUN = { "Node.js": "node", Go: "go run", Java: "java", Python: "python3", Bash: "bash" };

// Skeleton in lang that reads the parameters in order and runs body.
function skeleton(lang, name, desc, params, body = {}) {
  const ps = params.map((p) => p.name);
  switch (lang) {
    case "Go": return body.Go || `package main\n\n// ${name}: ${desc}\nimport (\n\t"fmt"\n\t"os"\n)\n\nfunc main() {\n${ps.map((p, i) => `\t${p} := arg(${i + 1})\n`).join("")}\tfmt.Println("processed", ${ps[0] || '""'})\n}\n\nfunc arg(i int) string {\n\tif len(os.Args) > i {\n\t\treturn os.Args[i]\n\t}\n\treturn ""\n}\n`;
    case "Java": return body.Java || `// ${name}: ${desc}\npublic class Tool {\n    public static void main(String[] args) {\n        String ${ps[0] || "input"} = args.length > 0 ? args[0] : "";\n        System.out.println("processed " + ${ps[0] || "input"});\n    }\n}\n`;
    case "Python": return body.Python || `#!/usr/bin/env python3\n"""${name}: ${desc}"""\nimport sys\n\nprint(f"processed {sys.argv[1]}")\n`;
    case "Bash": return body.Bash || `#!/usr/bin/env bash\n# ${name}: ${desc}\nset -euo pipefail\n${ps.map((p, i) => `${p}="\${${i + 1}:-}"\n`).join("")}echo "processed $${ps[0] || "1"}"\n`;
    default: return body["Node.js"] || `// ${name}: ${desc}\nconst [${ps.join(", ")}] = process.argv.slice(2);\n${ps[0] ? `if (!${ps[0]}) { console.error("${ps[0]} is required"); process.exit(2); }\n` : ""}console.log(\`processed \${${ps[0] || '""'}}\`);\n`;
  }
}

const EXAMPLES = [
  { re: /postgres|sql|database/, name: "postgres-query", desc: "Run a read-only SQL query against the project's PostgreSQL database and return the rows as JSON.",
    params: [{ name: "sql", type: "string", required: true, desc: "A single SELECT statement" }, { name: "limit", type: "number", required: false, desc: "Maximum rows, default 200" }],
    secrets: [{ name: "DATABASE_URL", desc: "postgres://user:password@host:5432/db" }], perms: ["network: database host", "read-only queries"],
    deps: { "Node.js": ["pg@8.13.1"], Python: ["psycopg[binary]==3.2.3"], Go: ["github.com/jackc/pgx/v5@v5.7.1"], Java: ["org.postgresql:postgresql:42.7.4"] },
    sampleTest: "Which tables have a created_at column?", call: { sql: "SELECT table_name FROM information_schema.columns WHERE column_name = 'created_at'", limit: 200 },
    sampleOutput: `[\n  { "table_name": "orders" },\n  { "table_name": "order_lines" }\n]`,
    body: { "Node.js": `// postgres-query: run a read-only SELECT and print rows as JSON\nconst { Client } = require("pg");\nconst [sql, limit = "200"] = process.argv.slice(2);\nif (!/^\\s*(select|with)\\b/i.test(sql || "")) { console.error("only SELECT statements are allowed"); process.exit(2); }\n(async () => {\n  const db = new Client({ connectionString: process.env.DATABASE_URL, statement_timeout: 20000 });\n  await db.connect();\n  try {\n    await db.query("BEGIN READ ONLY");\n    const { rows } = await db.query(\`SELECT * FROM (\${sql}) q LIMIT \${Number(limit) || 200}\`);\n    console.log(JSON.stringify(rows, null, 2));\n  } finally { await db.end(); }\n})().catch((e) => { console.error(e.message); process.exit(1); });\n` } },
  { re: /todo|fixme/, name: "find-todos", desc: "List TODO and FIXME comments under a path, with file and line.",
    params: [{ name: "path", type: "string", required: true, desc: "Folder or package path to scan" }, { name: "tags", type: "string", required: false, desc: "Comma-separated tags, default TODO,FIXME" }],
    secrets: [], perms: ["read files"], sampleTest: "List the TODOs in the orders domain package", call: { path: "src/main/java/com/acme/orders", tags: "TODO,FIXME" },
    sampleOutput: "src/main/java/com/acme/orders/application/PlaceOrderService.java:19  // TODO publish OrderPlaced via Kafka adapter (T004)\n1 match",
    body: { "Node.js": `// find-todos: list TODO/FIXME comments under a path\nconst fs = require("node:fs");\nconst path = require("node:path");\nconst [root, tags = "TODO,FIXME"] = process.argv.slice(2);\nif (!root) { console.error("path is required"); process.exit(2); }\nconst re = new RegExp(\`\\\\b(\${tags.split(",").join("|")})\\\\b\`);\nlet n = 0;\nfunction walk(p) {\n  const st = fs.statSync(p);\n  if (st.isDirectory()) { for (const e of fs.readdirSync(p)) if (!["node_modules", ".git", "target"].includes(e)) walk(path.join(p, e)); return; }\n  fs.readFileSync(p, "utf8").split("\\n").forEach((l, i) => { if (re.test(l)) { console.log(\`\${p}:\${i + 1}  \${l.trim()}\`); n++; } });\n}\nwalk(root);\nconsole.log(\`\${n} match\${n === 1 ? "" : "es"}\`);\n`,
      Bash: `#!/usr/bin/env bash\n# find-todos: list TODO/FIXME comments under a path\nset -euo pipefail\npath="\${1:?path is required}"\ntags="\${2:-TODO,FIXME}"\npattern="$(echo "$tags" | tr ',' '|')"\ngrep -rnE "($pattern)\\b" "$path" || echo "0 matches"\n` } },
  { re: /jira|ticket|issue/, name: "jira-ticket", desc: "Fetch a Jira ticket by key: summary, status and acceptance criteria.",
    params: [{ name: "key", type: "string", required: true, desc: "Ticket key, e.g. ORD-142" }],
    secrets: [{ name: "JIRA_TOKEN", desc: "Jira API token" }, { name: "JIRA_URL", desc: "Base URL, e.g. https://acme.atlassian.net" }], perms: ["network: JIRA_URL only"],
    sampleTest: "What are the acceptance criteria for ORD-142?", call: { key: "ORD-142" }, sampleOutput: `{\n  "key": "ORD-142",\n  "summary": "Reject orders when stock is insufficient",\n  "status": "In Progress"\n}`,
    body: { "Node.js": `// jira-ticket: fetch a ticket. Secrets come from the environment, never from the model.\nasync function main() {\nconst key = process.argv[2];\nif (!key) { console.error("key is required"); process.exit(2); }\nconst res = await fetch(\`\${process.env.JIRA_URL}/rest/api/3/issue/\${key}?fields=summary,status\`, {\n  headers: { Authorization: \`Bearer \${process.env.JIRA_TOKEN}\`, Accept: "application/json" },\n  signal: AbortSignal.timeout(10000),\n});\nif (!res.ok) { console.error(\`Jira: \${res.status}\`); process.exit(1); }\nconst { fields: f } = await res.json();\nconsole.log(JSON.stringify({ key, summary: f.summary, status: f.status.name }, null, 2));\n}\nmain().catch((e) => { console.error(e.message); process.exit(1); });\n` } },
  { re: /test/, name: "run-tests", desc: "Run unit tests for one package and report only the failures.",
    params: [{ name: "package", type: "string", required: true, desc: "Java package, e.g. com.acme.orders" }], secrets: [], perms: ["read files", "run build tool"],
    sampleTest: "Are the order service tests passing?", call: { package: "com.acme.orders" }, sampleOutput: "Tests run: 1, Failures: 0, Errors: 0, Skipped: 0\nall tests passed",
    body: { "Node.js": `// run-tests: unit tests for one package, failures only\nconst { spawnSync } = require("node:child_process");\nconst pkg = process.argv[2];\nif (!pkg) { console.error("package is required"); process.exit(2); }\nconst r = spawnSync("./mvnw", ["-q", "test", \`-Dtest=\${pkg}.**\`, "-DfailIfNoTests=false"], { encoding: "utf8", timeout: 600000 });\nconst lines = (r.stdout + r.stderr).split("\\n").filter((l) => /FAIL|ERROR|Tests run:/.test(l));\nconsole.log(lines.join("\\n") || "all tests passed");\nprocess.exit(r.status ?? 1);\n` } },
  { re: /depend|vulnerab|cve|security/, name: "dep-audit", desc: "Check dependencies for known vulnerabilities and list the high and critical ones.",
    params: [{ name: "minSeverity", type: "enum(low,medium,high,critical)", required: false, desc: "Lowest severity to report, default high" }],
    secrets: [{ name: "NVD_API_KEY", desc: "NVD API key for faster lookups" }], perms: ["read files", "network: nvd.nist.gov"],
    sampleTest: "Do we have any critical vulnerabilities?", call: { minSeverity: "critical" }, sampleOutput: "no dependencies at or above critical",
    body: { "Node.js": `// dep-audit: OWASP dependency-check, filtered by severity\nconst { spawnSync } = require("node:child_process");\nconst fs = require("node:fs");\nconst min = process.argv[2] || "high";\nconst order = ["low", "medium", "high", "critical"];\nconst r = spawnSync("./mvnw", ["-q", "org.owasp:dependency-check-maven:check", \`-DnvdApiKey=\${process.env.NVD_API_KEY}\`, "-Dformat=JSON"], { stdio: "inherit" });\nif (r.status) process.exit(r.status);\nconst rep = JSON.parse(fs.readFileSync("target/dependency-check-report.json", "utf8"));\nconst hits = rep.dependencies.flatMap((d) => (d.vulnerabilities || []).filter((v) => order.indexOf(v.severity.toLowerCase()) >= order.indexOf(min)).map((v) => \`\${d.fileName}: \${v.name} (\${v.severity})\`));\nconsole.log(hits.join("\\n") || \`no dependencies at or above \${min}\`);\n` } },
];

// tools with dependencies live in their own folder (same layout as the Go hub)
function folderCommand(lang, name, params) {
  const d = `.codegen/tools/project/${name}`;
  const run = { "Node.js": `node ${d}/index.js`, Python: `${d}/.venv/bin/python ${d}/main.py`, Go: `${d}/bin/${name}`, Java: `java -cp ${d}/lib/* ${d}/Tool.java` }[lang];
  return run + " " + params.map((p) => `{{${p.name}}}`).join(" ");
}

function makeDraft(prompt, lang, connects) {
  const p = prompt.toLowerCase();
  const ex = EXAMPLES.find((e) => e.re.test(p));
  const name = ex ? ex.name : slug(prompt);
  const params = ex ? clone(ex.params) : [{ name: "input", type: "string", required: true, desc: "What to work on" }];
  const desc = ex ? ex.desc : prompt.trim().replace(/\.$/, "") + ".";
  const file = lang === "Python" ? name.replace(/-/g, "_") : name;
  return {
    name, desc, lang, params,
    secrets: ex ? clone(ex.secrets) : connects.includes("rest") || connects.includes("db") ? [{ name: name.toUpperCase().replace(/-/g, "_") + "_TOKEN", desc: "Credential for the connected system" }] : [],
    perms: ex ? ex.perms : connects.includes("rest") ? ["network"] : ["read files"],
    command: `${RUN[lang] || "bash"} .codegen/tools/${file}.${EXT[lang] || "sh"} ${params.map((x) => `{{${x.name}}}`).join(" ")}`,
    script: skeleton(lang, name, desc, params, ex?.body),
    sampleTest: ex ? ex.sampleTest : "Use the tool on the orders service",
    call: ex ? clone(ex.call) : { input: "orders-service" },
    sampleOutput: ex ? ex.sampleOutput : "processed orders-service",
    ...(ex?.deps?.[lang] ? { deps: [...ex.deps[lang]], command: folderCommand(lang, name, params) } : {}),
  };
}
