// The Flow step guide: how each kind of step works, with examples. Every
// example is a small flow: sample data → the step → what follows. The guide
// previews the step on the sample data (so the result shown is what the app
// really produces), and "Try it in the designer" saves the example as a flow.

// a step that just produces sample data (a Transform › Fill a template)
const sample = (id, title, text) => ({
  id,
  type: "transform",
  op: "template",
  title,
  template: text,
  sample: true,
});
const out = (id, title, format = "md") => ({
  id,
  type: "output",
  title,
  format,
  actions: [{ type: "show" }],
});
const chain = (...ids) => ids.slice(1).map((to, i) => ({ from: ids[i], to }));

const ORDERS = `[
  {"id": "A-1001", "region": "EU", "customer": {"name": "Ana", "tier": "gold"}, "status": "open", "total": 120.5, "date": "2026-11-02", "items": [{"sku": "KB-01", "qty": 2}, {"sku": "MS-07", "qty": 1}]},
  {"id": "A-1002", "region": "US", "customer": {"name": "Bob", "tier": "silver"}, "status": "closed", "total": 80, "date": "2026-11-03", "items": [{"sku": "KB-01", "qty": 5}]},
  {"id": "A-1003", "region": "EU", "customer": {"name": "Cy", "tier": "gold"}, "status": "open", "total": 40, "date": "2026-11-05", "items": []},
  {"id": "A-1004", "region": "EU", "customer": {"name": "Ana", "tier": "gold"}, "status": "open", "total": 200, "date": "2026-11-09", "items": [{"sku": "HD-12", "qty": 1}]},
  {"id": "A-1005", "region": "US", "customer": {"name": "Dee", "tier": "gold"}, "status": "open", "total": 310, "date": "2026-11-10", "items": [{"sku": "HD-12", "qty": 2}, {"sku": "KB-01", "qty": 1}]}
]`;

export const GUIDE = [
  {
    type: "ai",
    name: "AI prompt",
    summary:
      "Asks the AI agent to write, summarise, classify or decide something, using the flow's context and skills.",
    when: [
      "Turn a tool's raw output into a readable summary or report.",
      "Draft text (a spec, acceptance criteria, release notes) from the flow's inputs.",
      "Classify or extract data and return JSON that later steps (Filter, Decision, Aggregate) can work on.",
    ],
    how: [
      "Write the prompt. Use the Insert buttons to add variables: {{inputs.<name>}} for an input, {{<step id>.buffer_output}} for an earlier step's result, {{context}} and {{skills}}.",
      "Whatever is connected into the step is added to the prompt automatically when the prompt does not mention it, under “Input from the previous step”.",
      "The flow's custom context and skills are always added, and files given as inputs (images, PDFs) are opened by the agent.",
      "Guidance for this step: instructions written for this step alone (tone, format, what to include or avoid; variables work) and skill files only it follows, on top of the flow's. Untick “Also follow the flow's skills” to leave the flow's skills out of this step. Improve, AI decisions and AI transforms have the same section.",
      "The step's result is the agent's answer. Ask for “only JSON” when a later step should read it as data.",
      "AI steps pause for your review when the run asks for it: accept, edit, refine with AI or stop.",
    ],
    settings: [
      ["Prompt", "What to ask; variables are filled in when the step runs."],
      ["Model", "A model for this step only; empty uses the one chosen for the run."],
      ["Instructions", "Guidance for this step: how to answer (tone, format, limits)."],
      ["Skills for this step", "Skill files this step follows, with or without the flow's."],
      ["Pause for review", "Stop after this step so you can check or refine the answer."],
    ],
    examples: [
      {
        title: "Summarise a tool's result for the team",
        text: "The step before produced a list of TODOs. The prompt refers to it by step id, so the agent gets the list and the instruction together.",
        nodes: [
          sample(
            "todos",
            "TODOs found",
            "src/order/OrderService.java:42 TODO validate stock\nsrc/order/OrderService.java:88 FIXME retry on timeout\nsrc/billing/Invoice.java:17 TODO round per currency",
          ),
          {
            id: "summary",
            type: "ai",
            title: "Summarise",
            prompt:
              "Summarise the open TODOs below for the team, grouped by folder, most urgent first (FIXME before TODO).\n\n{{todos.buffer_output}}",
          },
          out("report", "Report"),
        ],
        edges: chain("start", "todos", "summary", "report"),
        focus: "summary",
        result:
          "## src/order\n- **FIXME** OrderService.java:88 — retry on timeout\n- TODO OrderService.java:42 — validate stock\n\n## src/billing\n- TODO Invoice.java:17 — round per currency",
      },
      {
        title: "Draft from the flow's inputs",
        text: "Inputs are filled in when the flow runs (Run flows asks for them). Here the ticket text becomes acceptance criteria.",
        flowInputs: [{ name: "ticket", label: "Ticket", type: "multiline" }],
        nodes: [
          {
            id: "criteria",
            type: "ai",
            title: "Acceptance criteria",
            prompt:
              "Write acceptance criteria for this ticket as a Markdown checklist in Given / When / Then form. Mark anything unclear as [NEEDS CLARIFICATION].\n\nTicket:\n{{inputs.ticket}}",
          },
          out("doc", "Criteria"),
        ],
        edges: chain("start", "criteria", "doc"),
        focus: "criteria",
        result:
          "- [ ] **Given** a customer with items in the cart **When** they place the order **Then** stock is reserved\n- [ ] **Given** an item is out of stock **When** … [NEEDS CLARIFICATION: partial orders?]",
      },
      {
        title: "Return JSON for the next steps",
        text: "Asking for only JSON lets a Filter, Decision or Aggregate after it work on the answer. Here the bugs are kept.",
        nodes: [
          sample(
            "issues",
            "Issues",
            "1. Checkout crashes when the cart is empty\n2. Add dark mode\n3. Update the README badges\n4. Totals are off by one cent",
          ),
          {
            id: "classify",
            type: "ai",
            title: "Classify",
            prompt:
              'Classify each issue below as bug, feature or chore. Reply with only a JSON array of {"n": <number>, "title": <text>, "type": <bug|feature|chore>}, no explanation.\n\n{{issues.buffer_output}}',
          },
          {
            id: "bugs",
            type: "filter",
            title: "Bugs only",
            mode: "keep",
            cond: { match: "all", rules: [{ field: "type", op: "eq", value: "bug" }] },
          },
          out("list", "Bugs", "json"),
        ],
        edges: chain("start", "issues", "classify", "bugs", "list"),
        focus: "classify",
        result:
          '[\n  {"n": 1, "title": "Checkout crashes when the cart is empty", "type": "bug"},\n  {"n": 2, "title": "Add dark mode", "type": "feature"},\n  {"n": 3, "title": "Update the README badges", "type": "chore"},\n  {"n": 4, "title": "Totals are off by one cent", "type": "bug"}\n]',
      },
    ],
  },
  {
    type: "improve",
    name: "Improve until done",
    summary:
      "Pairs the AI with a tool and repeats until the result is good enough: the AI fixes how the tool runs, or the tool checks what the AI wrote.",
    when: [
      "A search or query that needs a few tries to get right: too many matches, an error, the wrong rows.",
      "Writing something a tool can check: code or config with tests, a linter or a validator after it.",
      "Any fix-and-check work you would otherwise repeat by hand until it passes.",
    ],
    how: [
      "Choose what it works with. “The tool before it”: connect one tool step into it. The tool runs; the AI judges the result and, when it is not good enough, changes the tool's arguments (or the input it gets) and runs it again.",
      "“The tool after it”: connect it to one tool step. The AI produces a result (say what in the prompt); the tool checks it, and its findings go back to the AI, which revises the result.",
      "Write “Done when”: what a good result looks like. Every round the AI judges against it and says why it is or is not done.",
      "Choose who decides: the AI alone, the AI then a person confirms, or a person after every round. A person can approve, edit the result, stop, or send feedback for another round.",
      "Its result is the final one: the tool's last output (tool before) or the AI's last version (tool after). Steps after it get that.",
      "Each round the AI gets the goal, the current result and findings, and a short note per earlier round, not their full text, so long runs stay within its context window. Every round's result is saved in the run's results folder.",
    ],
    settings: [
      ["Works with", "The tool before it (fix its run) or the tool after it (it checks the AI's result)."],
      ["Prompt", "Tool after: what the AI should produce. Tool before: optional instructions."],
      ["Done when", "What a good enough result is; the AI judges every round against it."],
      ["At most", "How many rounds it may take (5 by default, up to 20)."],
      [
        "Who decides it is done",
        "The AI alone; the AI, then a person confirms; or a person after every round.",
      ],
      ["When it never gets there", "The step fails, or (if ticked) the flow goes on with the last result."],
    ],
    examples: [
      {
        title: "Narrow a search until it finds the right place",
        text: "Search text runs first with a broad query. The step judges the matches and changes the query (or the folder) until it points at where order totals are rounded; then the matches go on to the report. Below: round 1 found 214 matches, mostly Math.round in UI code and tests, so the AI changed the query and the path; round 2 found these 3 and was done.",
        flowInputs: [
          { name: "topic", label: "What to find", type: "text", default: "where order totals are rounded" },
        ],
        nodes: [
          {
            id: "search",
            type: "tool",
            title: "Search the code",
            tool: "builtin:text_search",
            args: { query: "round" },
          },
          {
            id: "narrow",
            type: "improve",
            title: "Until it finds it",
            pair: "before",
            prompt: "Prefer a more exact query or a narrower path over raising max_results.",
            goal: "The matches show {{inputs.topic}}, with at most 20 matches and none from tests.",
            rounds: 5,
            approve: "done",
          },
          out("found", "Where it is"),
        ],
        edges: chain("start", "search", "narrow", "found"),
        focus: "narrow",
        result:
          "src/billing/Invoice.java\n  17: return total.setScale(2, RoundingMode.HALF_EVEN);\nsrc/billing/Totals.java\n  41: BigDecimal rounded = sum.setScale(2, RoundingMode.HALF_UP);\n  58: // round per currency",
      },
      {
        title: "Write JSON until the validator accepts it",
        text: "The AI writes a config file; Run command checks it with Python's JSON validator. Any error goes back to the AI, which fixes the JSON and the check runs again, until it passes.",
        flowInputs: [{ name: "service", label: "Service", type: "text", default: "orders" }],
        nodes: [
          {
            id: "config",
            type: "improve",
            title: "Write the config",
            pair: "after",
            prompt:
              "Write the JSON health-check config for the {{inputs.service}} service: name, url, interval_seconds, timeout_seconds and a list of alerts (each with channel and after_failures). Reply with only the JSON.",
            goal: "The check prints the JSON back with no error, and every field above is present.",
            rounds: 4,
            approve: "none",
          },
          {
            id: "check",
            type: "tool",
            title: "Validate JSON",
            tool: "builtin:run_command",
            args: { command: "python3 -m json.tool {{input_file}}" },
          },
          out("file", "Config", "json"),
        ],
        edges: chain("start", "config", "check", "file"),
        focus: "config",
        result:
          '{\n  "name": "orders",\n  "url": "https://orders.internal/health",\n  "interval_seconds": 30,\n  "timeout_seconds": 5,\n  "alerts": [\n    {"channel": "#orders-oncall", "after_failures": 3}\n  ]\n}',
      },
    ],
  },
  {
    type: "context",
    name: "Context",
    summary:
      "Gathers what the AI steps after it should know — files, notes, inputs, past runs, lessons, web pages and the results of the steps connected into it — keeps what matters for its purpose within a budget, and hands it over.",
    when: [
      "An AI step has to decide or write something and needs the project's rules, docs or history, not just the data.",
      "Several tools produced material and only the parts about one question matter.",
      "Large files would overflow the AI's context window: give it the relevant parts only.",
    ],
    how: [
      "Say what the context is for: its words rank every part. Parts that say nothing about it are left out; each source keeps its start.",
      "Add sources: files (patterns, read in full), text, the flow's inputs, past runs (history search), lessons from reviews, a web page. Steps connected into it are sources too.",
      "Each source gets a share of the budget by its priority (high, normal, low); what one does not need goes to the others. Repeated parts are dropped, secrets removed.",
      "The pack has one section per source, labelled [S1], [S2] …, so the AI can say where a fact came from.",
      "Condense (optional) asks the AI for a short brief for the purpose, keeping the labels: one AI call.",
      "The AI and Improve steps after it get the pack in their prompt by themselves; the run shows what each source gave and what was cut.",
    ],
    settings: [
      ["What is it for", "The purpose; its words decide what is relevant. Can use {{inputs.x}}."],
      [
        "Sources",
        "Files, text, inputs, past runs, lessons, web page; each with a name, a priority and words to focus on.",
      ],
      ["Budget", "Tokens for the whole pack (about 4 characters each); default 8,000."],
      ["Condense", "One AI call that turns the pack into a short brief."],
    ],
    examples: [
      {
        title: "A whole flow: handle a refund request",
        text: "The ticket, the refund policy, the support docs, past runs and lessons go into one context; only what is about the refund is kept, within 4,000 tokens. The AI decides between approving and escalating (asking you when it is less than 70% sure), and the AI step on the path taken writes the reply or the note. Both get the context by themselves, because it comes before them. In your own flow, replace the sample ticket with a Jira tool or an input. Try it in the designer to see the whole flow.",
        nodes: [
          sample(
            "ticket",
            "Ticket",
            "BILL-7: Customer asks for a refund of 620 EUR for a double charge on 3 Nov.\n\nThe customer also asked about the new invoice layout.",
          ),
          {
            id: "ctx",
            type: "context",
            title: "Refund context",
            goal: "decide the refund for this ticket, and who may approve it",
            budget: 4000,
            sources: [
              {
                kind: "text",
                label: "Refund policy",
                priority: "high",
                text: "Refunds up to 500 EUR are approved by support.\n\nRefunds over 500 EUR need a manager's approval.\n\nDouble charges are always refunded in full.\n\nInvoices are sent on the 1st of each month.",
              },
              {
                kind: "files",
                label: "Support docs",
                paths: ["docs/support/**/*.md"],
                query: "refund approval",
              },
              { kind: "history", label: "Past refund runs", query: "refund", priority: "low" },
              { kind: "lessons", priority: "low" },
            ],
          },
          {
            id: "decide",
            type: "decision",
            title: "Approve or escalate",
            decide: "ai",
            prompt: "Can support approve this refund, or does a manager need to?",
            confidence: 70,
            branches: [
              {
                id: "approve",
                label: "Approve",
                hint: "the policy allows it and the amount is within what support may approve",
              },
              {
                id: "escalate",
                label: "Escalate to a manager",
                hint: "the amount is over the limit, or the policy is unclear",
              },
            ],
          },
          {
            id: "reply",
            type: "ai",
            title: "Reply to the customer",
            prompt:
              "Write a short, friendly reply to the customer confirming the refund in {{steps.ticket}}: the amount, when it arrives, and that we are sorry. Follow the refund policy in the context.",
          },
          {
            id: "note",
            type: "ai",
            title: "Note for the manager",
            prompt:
              "Write a short note asking the finance manager to approve the refund in {{steps.ticket}}: the amount, the reason, and the policy that needs their approval (cite the context's [S] labels).",
          },
          out("customer", "Reply"),
          out("manager", "Approval request"),
        ],
        edges: [
          ...chain("start", "ticket", "ctx", "decide"),
          { from: "decide", to: "reply", branch: "approve" },
          { from: "decide", to: "note", branch: "escalate" },
          { from: "reply", to: "customer" },
          { from: "note", to: "manager" },
        ],
        focus: "ctx",
      },
    ],
  },
  {
    type: "merge",
    name: "Merge",
    summary:
      "Combines the outputs of several steps into one: joined text, a JSON object or array, or a template.",
    when: [
      "Two or more steps ran side by side and the next step needs all their results.",
      "Build one JSON document from several tools' outputs.",
      "Lay several results out in a fixed document structure.",
    ],
    how: [
      "Connect every step to combine into the Merge. Their order is the order of the connections, shown in the inspector.",
      "Join as text puts the outputs one after another with a separator (\\n is a new line).",
      "JSON object uses each step's title as the key; outputs that are JSON stay JSON, others become strings.",
      "Template lets you write the document yourself with {{<step id>.buffer_output}} for each part.",
      "After a Decision, only the steps on the path taken are merged.",
    ],
    settings: [
      ["How to merge", "Join as text, JSON object, JSON array or Template."],
      ["Separator", "For Join as text; default a blank line."],
      ["Template", "For Template; any variables."],
    ],
    examples: [
      {
        title: "Join two reports as text",
        text: "Lint and test results, one after the other with a divider.",
        nodes: [
          sample("lint", "Lint", "Lint: 2 warnings (unused import, long line)"),
          sample("tests", "Tests", "Tests: 128 passed, 1 skipped"),
          { id: "both", type: "merge", title: "Both", mode: "concat", separator: "\\n---\\n" },
          out("report", "Report"),
        ],
        edges: [
          ...chain("start", "lint"),
          ...chain("start", "tests"),
          { from: "lint", to: "both" },
          { from: "tests", to: "both" },
          { from: "both", to: "report" },
        ],
        focus: "both",
      },
      {
        title: "One JSON object from several steps",
        text: "Each step's title becomes a key. JSON outputs are kept as JSON, so the next step can read {{both.buffer_output.json.Coverage.percent}}.",
        nodes: [
          sample("cov", "Coverage", '{"percent": 81.4, "files": 212}'),
          sample("deps", "Outdated", '["jackson 2.15 → 2.17", "junit 5.9 → 5.11"]'),
          { id: "both", type: "merge", title: "Status", mode: "json-object" },
          out("status", "Status", "json"),
        ],
        edges: [
          ...chain("start", "cov"),
          ...chain("start", "deps"),
          { from: "cov", to: "both" },
          { from: "deps", to: "both" },
          { from: "both", to: "status" },
        ],
        focus: "both",
      },
      {
        title: "Release notes from a template",
        text: "The template places each part where it belongs.",
        nodes: [
          sample("changes", "Changes", "- Orders can be cancelled within 24 h\n- Faster invoice export"),
          sample("issues", "Known issues", "- Export of > 10k rows is slow"),
          {
            id: "notes",
            type: "merge",
            title: "Release notes",
            mode: "template",
            template:
              "# Release {{date}}\n\n## What's new\n{{changes.buffer_output}}\n\n## Known issues\n{{issues.buffer_output}}",
          },
          out("md", "Notes"),
        ],
        edges: [
          ...chain("start", "changes"),
          ...chain("start", "issues"),
          { from: "changes", to: "notes" },
          { from: "issues", to: "notes" },
          { from: "notes", to: "md" },
        ],
        focus: "notes",
      },
    ],
  },
  {
    type: "transform",
    name: "Transform",
    summary:
      "Reshapes data: pick JSON values, reshape JSON with a template, convert CSV, Markdown and HTML, find and replace, run a command, or ask AI.",
    when: [
      "Take one value (or a list) out of a JSON result.",
      "Turn one JSON shape into another, including nested lists.",
      "Clean up text: keep matching lines, extract with a pattern, find and replace.",
      "Convert between CSV, JSON, Markdown, HTML and text.",
    ],
    how: [
      "Choose the transformation; the fields it needs appear below it.",
      "The input is the output of the steps connected into it; “Input › Use instead” lets you take any variable instead.",
      "Paths look like items[0].name, items[*].id or $.data.total (a key on a list, items.id, collects it from every element).",
      'JSON → JSON (template): write the JSON to produce with {{this.<path>}}; a whole-string placeholder keeps its type; {"{{#each this.lines}}": {…}} repeats for nested lists, with {{parent.…}} and {{root.…}}.',
      "Run a command passes the data on standard input (e.g. jq '.items', sort -u); Ask AI describes the change in words.",
      "Use “Try it with sample data” in the inspector to check the result before running the flow.",
      "Values from outside the flow work in every step's fields: {{env.NAME}} (a variable), {{project_secret.NAME}} and {{keychain.NAME}} (secrets from Settings › Secrets, looked up when the run starts and hidden in results and history).",
    ],
    settings: [
      ["Transformation", "What to do; grouped as Text, JSON, Convert, Command and AI."],
      ["Path / pattern / fields", "What the transformation needs (shown per transformation)."],
      ["Template", "For Fill a template and JSON → JSON."],
      ["Input › Use instead", "A variable to transform instead of the connected output."],
    ],
    examples: [
      {
        title: "Pick values out of JSON",
        text: "items[*].sku collects the sku of every item of every order.",
        nodes: [
          sample("orders", "Orders", ORDERS),
          { id: "skus", type: "transform", title: "All SKUs", op: "json-path", expr: "[*].items[*].sku" },
          out("list", "SKUs", "json"),
        ],
        edges: chain("start", "orders", "skus", "list"),
        focus: "skus",
      },
      {
        title: "Reshape JSON, nested lists included",
        text: "One entry per order with its lines; each line knows its order through {{parent.id}}.",
        nodes: [
          sample("orders", "Orders", ORDERS),
          {
            id: "shape",
            type: "transform",
            title: "Reshape",
            op: "json-template",
            expr: "[*]",
            template:
              '{\n  "order": "{{this.id}}",\n  "customer": "{{this.customer.name}}",\n  "lines": { "{{#each this.items}}": { "sku": "{{this.sku}}", "qty": "{{this.qty}}", "order": "{{parent.id}}" } }\n}',
          },
          out("json", "Orders", "json"),
        ],
        edges: chain("start", "orders", "shape", "json"),
        focus: "shape",
      },
      {
        title: "Keep matching lines",
        text: "Only the ERROR lines of a log.",
        nodes: [
          sample(
            "log",
            "Log",
            "10:01 INFO started\n10:02 ERROR payment timeout\n10:02 WARN retrying\n10:03 ERROR payment timeout\n10:04 INFO done",
          ),
          { id: "errors", type: "transform", title: "Errors", op: "filter-lines", expr: "ERROR" },
          out("txt", "Errors", "txt"),
        ],
        edges: chain("start", "log", "errors", "txt"),
        focus: "errors",
      },
      {
        title: "CSV → JSON",
        text: "The header line becomes the keys; the result can go into a Filter or Aggregate.",
        nodes: [
          sample("csv", "Export", "sku,name,stock\nKB-01,Keyboard,14\nMS-07,Mouse,0\nHD-12,Headset,3"),
          { id: "json", type: "transform", title: "As JSON", op: "csv-to-json" },
          out("o", "Products", "json"),
        ],
        edges: chain("start", "csv", "json", "o"),
        focus: "json",
      },
    ],
  },
  {
    type: "loop",
    name: "Loop (for each)",
    summary:
      "Splits a list into items and runs the steps after it once per item, until a Gather collects the results.",
    when: [
      "Run a tool or an AI prompt for each file, symbol, ticket or row.",
      "Process the elements of a JSON array one at a time.",
      "Nested work: for each file, for each symbol in it.",
    ],
    how: [
      "Connect the step whose output is the list into the Loop (or write the list in “The list”).",
      "Choose how to split it: one per line, a JSON array, CSV rows or the matches of a pattern; Detect picks JSON or lines.",
      'When the list arrives inside a JSON object — connected to the Inputs step, the loop gets {"stocks": "AMZN\\nMSFT"} — set “List from” to the field that holds it (stocks, or a path like data.items). Empty: the object\'s only field, or its only list. Connected to Inputs, the loop picks the input itself.',
      "Every step connected after the Loop runs once per item, up to a Gather step, which you must add to collect the results.",
      "Inside the loop use {{item}} (and {{item.json.name}} for JSON items), {{item_index}} and {{item_count}}. With loops inside loops, use the loop's step id: {{files.item}}.",
      "Options: keep only items matching a pattern, at most N items, skip repeats, keep going when an item fails.",
      "A tool or AI step connected directly after the Loop gets the current item as its input.",
    ],
    settings: [
      ["Split the list into items", "Detect, lines, JSON array, CSV rows or pattern matches."],
      ["Keep only items matching", "A regular expression; empty keeps all."],
      ["At most", "Items to run (up to 1000)."],
      [
        "Skip repeated items / Keep going",
        "Unique items; continue when an item fails (its error is gathered).",
      ],
    ],
    examples: [
      {
        title: "One AI prompt per line",
        text: "Each class name is explained separately; Gather puts the answers under ## headings.",
        nodes: [
          sample("names", "Classes", "OrderService\nInvoiceRepository\nStockClient"),
          { id: "each", type: "loop", title: "Each class", split: "lines", max: 100 },
          {
            id: "explain",
            type: "ai",
            title: "Explain",
            prompt: "In two sentences, explain what the class {{item}} is likely responsible for.",
          },
          { id: "all", type: "gather", title: "All answers", mode: "sections" },
          out("doc", "Classes"),
        ],
        edges: chain("start", "names", "each", "explain", "all", "doc"),
        focus: "each",
      },
      {
        title: "A JSON array, filtered",
        text: "Only open orders' ids, at most 3. Each item is a JSON object, so later steps can read {{item.json.customer.name}}.",
        nodes: [
          sample("orders", "Orders", ORDERS),
          {
            id: "each",
            type: "loop",
            title: "Each open order",
            split: "json",
            filter: '"status":"open"',
            max: 3,
          },
          {
            id: "line",
            type: "transform",
            title: "Line",
            op: "template",
            template: "{{item_index}}/{{item_count}} {{item.json.id}} for {{item.json.customer.name}}",
          },
          { id: "all", type: "gather", title: "Lines", mode: "concat", separator: "\\n" },
          out("txt", "Open orders", "txt"),
        ],
        edges: chain("start", "orders", "each", "line", "all", "txt"),
        focus: "each",
      },
      {
        title: "Loops inside loops",
        text: "For each file, for each of its symbols. The inner steps name each loop by its step id: {{files.item}} and {{syms.item}}.",
        nodes: [
          { id: "files", type: "loop", title: "Each file", source: "a.go\nb.go", split: "lines" },
          { id: "syms", type: "loop", title: "Each symbol", source: "Run\nStop", split: "lines" },
          {
            id: "pair",
            type: "transform",
            title: "Pair",
            op: "template",
            template: "{{files.item}}: {{syms.item}}",
          },
          { id: "inner", type: "gather", title: "Symbols", mode: "concat", separator: ", " },
          { id: "outer", type: "gather", title: "Files", mode: "concat", separator: "\\n" },
          out("txt", "Pairs", "txt"),
        ],
        edges: chain("start", "files", "syms", "pair", "inner", "outer", "txt"),
        focus: "files",
      },
    ],
  },
  {
    type: "gather",
    name: "Gather",
    summary:
      "Ends a loop: collects what every item produced, as joined text, ## sections, a JSON array or object, or a template per item.",
    when: [
      "Every Loop needs one, after the steps it repeats.",
      "Turn per-item results into one report or one JSON document.",
    ],
    how: [
      "Connect the last step(s) of the loop into the Gather; the steps after the Gather run once, with the collected result.",
      "Sections writes “## <item>” and then that item's result; JSON object keys the results by item.",
      "Template per item: {{item}} is the item, {{input}} its result, {{item_index}} its position; the lines are joined with new lines.",
      "With “keep going” on the Loop, a failed item's error is gathered as “ERROR: …”.",
    ],
    settings: [
      ["How to gather", "Join as text, Sections, JSON array, JSON object or Template per item."],
      ["Separator", "For Join as text."],
      ["Template for each item", "For Template per item."],
    ],
    examples: [
      {
        title: "A section per item",
        text: "The preview uses three sample items; in a run they are the loop's items and results.",
        nodes: [
          sample("list", "Modules", "orders\nbilling\nstock"),
          { id: "each", type: "loop", title: "Each module", split: "lines" },
          {
            id: "check",
            type: "transform",
            title: "Check",
            op: "template",
            template: "Module {{item}} checked.",
          },
          { id: "all", type: "gather", title: "Report", mode: "sections" },
          out("md", "Report"),
        ],
        edges: chain("start", "list", "each", "check", "all", "md"),
        focus: "all",
      },
      {
        title: "A JSON array of results",
        text: "Results that are JSON stay JSON, so an Aggregate or Filter can follow.",
        nodes: [
          sample("list", "Modules", "orders\nbilling"),
          { id: "each", type: "loop", title: "Each module", split: "lines" },
          {
            id: "res",
            type: "transform",
            title: "Result",
            op: "template",
            template: '{"module": "{{item}}", "ok": true}',
          },
          { id: "all", type: "gather", title: "Results", mode: "json-array" },
          out("json", "Results", "json"),
        ],
        edges: chain("start", "list", "each", "res", "all", "json"),
        focus: "all",
      },
      {
        title: "A bullet per item",
        text: "Template per item: - **{{item}}**: {{input}}.",
        nodes: [
          sample("list", "Modules", "orders\nbilling"),
          { id: "each", type: "loop", title: "Each module", split: "lines" },
          { id: "res", type: "transform", title: "Result", op: "uppercase" },
          {
            id: "all",
            type: "gather",
            title: "Summary",
            mode: "template",
            template: "- **{{item}}** ({{item_index}}): {{input}}",
          },
          out("md", "Summary"),
        ],
        edges: chain("start", "list", "each", "res", "all", "md"),
        focus: "all",
      },
    ],
  },
  {
    type: "decision",
    name: "Decision",
    summary:
      "Checks the data and sends the flow down a path: only the steps on the path taken run. Conditions can nest to any depth.",
    when: [
      "Do different things depending on a result: errors found or not, a big order or a small one.",
      "Stop a flow early (a path with nothing connected) when there is nothing to do.",
      "Notify several teams when several conditions hold (“every one that matches”).",
    ],
    how: [
      "Add paths; each has a condition. Paths are checked in order (↑ moves one earlier); Otherwise is taken when none matches.",
      "Connect the steps that follow from the Decision. Each connection is on a path: new ones go on the next free path; click a connection to change it.",
      "A condition is all of / any of / none of its rules, and + Group adds a nested group. A rule compares a field — a path into the data, e.g. summary.errors or items[*].qty — with =, >, contains, is one of, is empty, count >, and more.",
      "The data passes on unchanged, so the steps on the path get what the Decision got. Steps on other paths show “not on the path the flow took”.",
      "Where paths meet again (a Merge or an Output), the step runs with what the path taken produced.",
      "Inside a loop, each item takes its own path.",
    ],
    settings: [
      ["When several paths match", "Take the first one, or every one that matches."],
      ["Paths", "A name and a condition each, checked in order."],
      ["Otherwise", "Taken when no path matches; connect a step on it, or leave it empty to stop."],
      ["Input › Use instead", "Check a variable instead of the connected output."],
    ],
    examples: [
      {
        title: "Let the AI decide, and ask when unsure",
        text: "The AI reads the test summary and chooses Ship it or Hold, with a reason and how sure it is. Below 70% sure it asks you to choose; your choice is kept as a lesson for later runs.",
        nodes: [
          sample(
            "tests",
            "Test summary",
            "Tests: 412 passed, 2 failed (flaky: PaymentRetryTest)\nLint: clean\nCoverage: 81%",
          ),
          {
            id: "go",
            type: "decision",
            title: "Ship or hold",
            decide: "ai",
            prompt: "Can this build be released today?",
            confidence: 70,
            branches: [
              { id: "ship", label: "Ship it", hint: "every check passed, or only known-flaky tests failed" },
              { id: "hold", label: "Hold", hint: "a real failure, or not enough information" },
            ],
          },
          out("ship", "Release notes"),
          out("hold", "Why we hold"),
        ],
        edges: [
          ...chain("start", "tests", "go"),
          { from: "go", to: "ship", branch: "ship" },
          { from: "go", to: "hold", branch: "hold" },
        ],
        focus: "go",
      },
      {
        title: "Errors found, or all clear",
        text: "A quality gate's summary decides between asking AI for fixes and an “all clear” output.",
        nodes: [
          sample(
            "gate",
            "Quality gate",
            '{"summary": {"errors": 3, "warnings": 12}, "files": ["OrderService.java", "Invoice.java"]}',
          ),
          {
            id: "check",
            type: "decision",
            title: "Any errors?",
            mode: "first",
            branches: [
              {
                id: "errors",
                label: "Has errors",
                when: { match: "all", rules: [{ field: "summary.errors", op: "gt", value: "0" }] },
              },
            ],
          },
          {
            id: "fix",
            type: "ai",
            title: "Suggest fixes",
            prompt: "Suggest fixes for the errors in {{check.buffer_output}}.",
          },
          out("ok", "All clear"),
        ],
        edges: [
          ...chain("start", "gate", "check"),
          { from: "check", to: "fix", branch: "errors" },
          { from: "check", to: "ok", branch: "else" },
        ],
        focus: "check",
      },
      {
        title: "Nested conditions",
        text: "Escalate when the ticket is open AND (priority ≥ 3 OR labelled urgent). The inner group is “any of”.",
        nodes: [
          sample(
            "ticket",
            "Ticket",
            '{"id": "PAY-142", "status": "open", "priority": 2, "labels": ["payments", "urgent"]}',
          ),
          {
            id: "triage",
            type: "decision",
            title: "Triage",
            mode: "first",
            branches: [
              {
                id: "escalate",
                label: "Escalate",
                when: {
                  match: "all",
                  rules: [
                    { field: "status", op: "eq", value: "open" },
                    {
                      group: {
                        match: "any",
                        rules: [
                          { field: "priority", op: "gte", value: "3" },
                          { field: "labels", op: "contains", value: "urgent" },
                        ],
                      },
                    },
                  ],
                },
              },
            ],
          },
          out("page", "Page on-call"),
          out("queue", "Normal queue"),
        ],
        edges: [
          ...chain("start", "ticket", "triage"),
          { from: "triage", to: "page", branch: "escalate" },
          { from: "triage", to: "queue", branch: "else" },
        ],
        focus: "triage",
      },
      {
        title: "Every path that matches",
        text: "A big EU order notifies both finance and the EU team. Mode: take every one that matches.",
        nodes: [
          sample("order", "Order", '{"id": "A-1005", "region": "EU", "total": 310}'),
          {
            id: "route",
            type: "decision",
            title: "Who to tell",
            mode: "all",
            branches: [
              {
                id: "big",
                label: "Big order",
                when: { match: "all", rules: [{ field: "total", op: "gte", value: "300" }] },
              },
              {
                id: "eu",
                label: "EU",
                when: { match: "all", rules: [{ field: "region", op: "in", value: "EU, UK" }] },
              },
            ],
          },
          out("finance", "Finance"),
          out("eu", "EU team"),
        ],
        edges: [
          ...chain("start", "order", "route"),
          { from: "route", to: "finance", branch: "big" },
          { from: "route", to: "eu", branch: "eu" },
        ],
        focus: "route",
      },
      {
        title: "On text: did the build fail?",
        text: "When the data is not JSON, leave the field empty to check the whole text. “ignore case” matches FAILED and failed.",
        nodes: [
          sample(
            "build",
            "Build log",
            "Compiling 212 files… done\nRunning tests… 1 failed\nBUILD FAILED in 41s",
          ),
          {
            id: "failed",
            type: "decision",
            title: "Failed?",
            mode: "first",
            branches: [
              {
                id: "fail",
                label: "Failed",
                when: {
                  match: "all",
                  rules: [{ field: "", op: "contains", value: "build failed", ignoreCase: true }],
                },
              },
            ],
          },
          out("alert", "Alert"),
          out("fine", "Passed"),
        ],
        edges: [
          ...chain("start", "build", "failed"),
          { from: "failed", to: "alert", branch: "fail" },
          { from: "failed", to: "fine", branch: "else" },
        ],
        focus: "failed",
      },
    ],
  },
  {
    type: "filter",
    name: "Filter",
    summary: "Keeps (or drops) the rows that match a condition; can also drop repeats, sort and limit.",
    when: [
      "Keep only the open orders, the errors, the files that changed.",
      "Drop rows with missing data.",
      "Top N: sort by a field and keep the first rows.",
    ],
    how: [
      "The input is a list of rows: a JSON array (or an object holding one), CSV with a header line, or one row per line.",
      "Build the condition like a Decision's: all of / any of / none of, with nested groups. Fields are paths into each row: status, customer.tier, items[*].qty.",
      "For a list field, a rule holds when any element matches; tick “every element” to require all of them.",
      "Sort by fields (-total sorts high to low), keep at most N rows, drop repeated rows.",
      "JSON and CSV come out as a JSON array; lines come out as lines.",
    ],
    settings: [
      ["Rows that match", "Keep or Drop."],
      ["Conditions", "Rules and nested groups."],
      ["Sort by / At most", "Order and limit the result."],
      ["Drop repeated rows", "Identical rows once."],
      ["Rows", "How to read the input: Detect, JSON, CSV or lines."],
    ],
    examples: [
      {
        title: "Open orders over 100, biggest first",
        text: "Two rules (all of), sorted by -total.",
        nodes: [
          sample("orders", "Orders", ORDERS),
          {
            id: "big",
            type: "filter",
            title: "Big open orders",
            mode: "keep",
            orderBy: ["-total"],
            cond: {
              match: "all",
              rules: [
                { field: "status", op: "eq", value: "open" },
                { field: "total", op: "gt", value: "100" },
              ],
            },
          },
          out("json", "Big orders", "json"),
        ],
        edges: chain("start", "orders", "big", "json"),
        focus: "big",
      },
      {
        title: "Nested rules and lists",
        text: "Gold customers in the EU, or any order with an item of quantity 5 or more (items[*].qty).",
        nodes: [
          sample("orders", "Orders", ORDERS),
          {
            id: "pick",
            type: "filter",
            title: "Pick",
            mode: "keep",
            cond: {
              match: "any",
              rules: [
                {
                  group: {
                    match: "all",
                    rules: [
                      { field: "customer.tier", op: "eq", value: "gold" },
                      { field: "region", op: "eq", value: "EU" },
                    ],
                  },
                },
                { field: "items[*].qty", op: "gte", value: "5" },
              ],
            },
          },
          out("json", "Picked", "json"),
        ],
        edges: chain("start", "orders", "pick", "json"),
        focus: "pick",
      },
      {
        title: "Drop rows, then the top 2",
        text: "Drop the orders without items, sort by date (newest first) and keep two.",
        nodes: [
          sample("orders", "Orders", ORDERS),
          {
            id: "recent",
            type: "filter",
            title: "Recent with items",
            mode: "drop",
            orderBy: ["-date"],
            max: 2,
            cond: { match: "all", rules: [{ field: "items", op: "empty" }] },
          },
          out("json", "Recent", "json"),
        ],
        edges: chain("start", "orders", "recent", "json"),
        focus: "recent",
      },
      {
        title: "Lines of text",
        text: "With one row per line, leave the field empty to test the line itself.",
        nodes: [
          sample(
            "log",
            "Log",
            "10:01 INFO started\n10:02 ERROR payment timeout\n10:03 error card declined\n10:04 INFO done",
          ),
          {
            id: "errs",
            type: "filter",
            title: "Errors",
            mode: "keep",
            split: "lines",
            cond: { match: "all", rules: [{ field: "", op: "contains", value: "error", ignoreCase: true }] },
          },
          out("txt", "Errors", "txt"),
        ],
        edges: chain("start", "log", "errs", "txt"),
        focus: "errs",
      },
    ],
  },
  {
    type: "aggregate",
    name: "Aggregate",
    summary:
      "Groups rows and calculates values — count, sum, average, min, max, distinct count — or adds window values to every row (rank, running sum, % of total, previous row).",
    when: [
      "Totals and counts per region, customer, status.",
      "A one-row summary of everything (no group fields).",
      "Rank rows within their group, running totals, compare each row with the previous one.",
    ],
    how: [
      "The input is rows, as for Filter. Group by lists the fields that make a group (region, customer.tier); with none, all rows are one group.",
      "Add the values to calculate: a function, the field it works on, and the name of the result. A list path like items[*].qty counts every element, so nested values can be summed.",
      "One row per group (like SQL GROUP BY) gives a row per group; Sort the result by orders it (-revenue for high to low).",
      "Every row, with its group's values (like SQL OVER (PARTITION BY … ORDER BY …)) keeps every row and adds the values; window functions — rank, running sum, lag / lead, % of total, ntile — follow “Order within the group”.",
      "Numbers are added up as numbers; min and max also work on text and dates.",
    ],
    settings: [
      ["Result", "One row per group, or every row with its group's values."],
      ["Group by / Partition by", "Fields that form a group."],
      ["Values to calculate", "Function, field, name (and an option for join, lag / lead, ntile)."],
      ["Sort / Order within the group", "Fields; -field for descending."],
      ["At most", "Rows to keep."],
    ],
    examples: [
      {
        title: "Revenue per region",
        text: "Count, sum and average per region, biggest revenue first.",
        nodes: [
          sample("orders", "Orders", ORDERS),
          {
            id: "byRegion",
            type: "aggregate",
            title: "By region",
            mode: "group",
            groupBy: ["region"],
            orderBy: ["-revenue"],
            measures: [
              { fn: "count", as: "orders" },
              { fn: "sum", field: "total", as: "revenue" },
              { fn: "avg", field: "total", as: "average" },
            ],
          },
          out("json", "Revenue", "json"),
        ],
        edges: chain("start", "orders", "byRegion", "json"),
        focus: "byRegion",
      },
      {
        title: "One row of totals",
        text: "No group fields: everything is one group. Min and max work on dates too.",
        nodes: [
          sample("orders", "Orders", ORDERS),
          {
            id: "totals",
            type: "aggregate",
            title: "Totals",
            mode: "group",
            groupBy: [],
            measures: [
              { fn: "count", as: "orders" },
              { fn: "sum", field: "total", as: "revenue" },
              { fn: "min", field: "date", as: "first" },
              { fn: "max", field: "date", as: "last" },
              { fn: "median", field: "total", as: "median" },
            ],
          },
          out("json", "Totals", "json"),
        ],
        edges: chain("start", "orders", "totals", "json"),
        focus: "totals",
      },
      {
        title: "Distinct counts and nested values",
        text: "Per customer tier: how many different customers, all units across the nested items, and the SKUs bought.",
        nodes: [
          sample("orders", "Orders", ORDERS),
          {
            id: "tiers",
            type: "aggregate",
            title: "By tier",
            mode: "group",
            groupBy: ["customer.tier"],
            measures: [
              { fn: "count_distinct", field: "customer.name", as: "customers" },
              { fn: "sum", field: "items[*].qty", as: "units" },
              { fn: "distinct", field: "items[*].sku", as: "skus" },
              { fn: "concat", field: "id", arg: " | ", as: "orders" },
            ],
          },
          out("json", "Tiers", "json"),
        ],
        edges: chain("start", "orders", "tiers", "json"),
        focus: "tiers",
      },
      {
        title: "Window: rank and running total per region",
        text: "Every order is kept, with its rank in its region (biggest first), the running total, its % of the region and the previous order's id.",
        nodes: [
          sample("orders", "Orders", ORDERS),
          {
            id: "ranked",
            type: "aggregate",
            title: "Ranked",
            mode: "window",
            groupBy: ["region"],
            orderBy: ["-total"],
            measures: [
              { fn: "rank", as: "rank" },
              { fn: "running_sum", field: "total", as: "running" },
              { fn: "pct_of_total", field: "total", as: "pct" },
              { fn: "lag", field: "id", as: "bigger_order" },
              { fn: "sum", field: "total", as: "region_total" },
            ],
          },
          out("json", "Ranked", "json"),
        ],
        edges: chain("start", "orders", "ranked", "json"),
        focus: "ranked",
      },
    ],
  },
  {
    type: "dataquery",
    name: "Data query (DuckDB)",
    icon: { type: "tool", tool: "duckdb_data_query" },
    resultLabel: "Result · an example (the query runs when the flow runs)",
    summary:
      "Queries data with DuckDB SQL: files (CSV, JSON, Parquet, Excel), URLs, S3 and Azure, the previous step's output, and databases (Postgres, MySQL, SQLite, SQL Server, Oracle). One query reads, joins, transforms and aggregates; the rows come back as JSON for the next steps.",
    when: [
      "Summarise or join files: CSV exports, JSON from an API, Parquet in a data lake.",
      "Read a database (read-only) and combine it with a file or an earlier step's result.",
      "Prepare rows for a chart, a report table or a decision.",
    ],
    how: [
      "Add a Tool step and choose duckdb_data_query (an admin publishes it to every project from library/tools; Admin › Import a tool file to publish).",
      'sql is the query. Name the inputs: sources ({"orders": "data/orders.csv"}; globs, s3:// paths, and folders ending in / — every data file under it, sub-folders too, as one table with a filename column, year=2026/ folders as columns), connections ([{"name": "pg", "type": "postgres", "dsn": "…"}], read-only unless "read_only": false), and input: {{input_file}} makes the previous step\'s output the table input.',
      'Passwords never go in the flow: give each connection its own as ${DB_<NAME>} ("password": "${DB_SALES}"): SpecWright asks for it once and keeps it as a secret (Run flows lists them, to save in the keychain for scheduled runs). PGPASSWORD and MYSQL_PWD are read by the drivers when a connection gives none. S3 uses AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY when set, otherwise your AWS profile or SSO; AWS_REGION, and AWS_ENDPOINT_URL for S3-compatible storage (MinIO, R2, Ceph).',
      "format: json (default; numbers stay numbers), csv or markdown. save_to also writes the whole result to .csv, .json, .jsonl or .parquet, locally or on s3://; save_mode append adds the rows to what the file already holds, so a scheduled flow builds up a history; max_rows (default 1000) caps what the next step gets.",
      "Extensions (S3, Postgres, Excel, Oracle…) install the first time they are needed. Where the network blocks them, set DUCKDB_EXTENSION_REPOSITORY (a mirror) or DUCKDB_HTTP_PROXY.",
    ],
    settings: [
      ["sql", "The DuckDB SQL; several statements run in turn, the last one's rows are the result."],
      ["sources", "Tables from files, globs, folders (path/), URLs or cloud paths."],
      ["connections", "Databases to attach: postgres, mysql, sqlite, sqlserver, oracle, duckdb."],
      ["input", "{{input_file}}: the previous step's output as the table input."],
      [
        "format, save_to, save_mode, max_rows",
        "How the rows come back, where to save all of them (overwrite, or append to accumulate), how many to pass on.",
      ],
      ["extensions", "Extra DuckDB extensions (most load by themselves)."],
    ],
    examples: [
      {
        title: "Summarise a CSV export",
        text: "Revenue and order count per region from a CSV file, largest first. The rows feed a Filter, an Aggregate or a chart.",
        nodes: [
          {
            id: "revenue",
            type: "tool",
            title: "Revenue by region",
            tool: "duckdb_data_query",
            args: {
              sql: "SELECT region, round(sum(total), 2) AS revenue, count(*) AS orders\nFROM orders\nGROUP BY region\nORDER BY revenue DESC",
              sources: '{"orders": "data/orders.csv"}',
            },
          },
        ],
        edges: chain("start", "revenue"),
        focus: "revenue",
        result:
          '[\n  {"region": "EU", "revenue": 360.5, "orders": 3},\n  {"region": "US", "revenue": 390, "orders": 2},\n  {"region": "APAC", "revenue": 212, "orders": 2}\n]',
      },
      {
        title: "Join the previous step's output with a file",
        text: "A tool returned this month's targets as JSON; the query joins them with the orders file (input is the step before) and returns a Markdown table for a report.",
        nodes: [
          sample(
            "targets",
            "Targets",
            '[{"region": "EU", "target": 300}, {"region": "US", "target": 450}, {"region": "APAC", "target": 200}]',
          ),
          {
            id: "vs",
            type: "tool",
            title: "Revenue vs. target",
            tool: "duckdb_data_query",
            args: {
              sql: "SELECT o.region, sum(o.total) AS revenue, t.target,\n       round(100 * sum(o.total) / t.target) AS pct\nFROM orders o JOIN input t USING (region)\nGROUP BY o.region, t.target\nORDER BY pct DESC",
              sources: '{"orders": "data/orders.csv"}',
              input: "{{input_file}}",
              format: "markdown",
            },
          },
        ],
        edges: chain("start", "targets", "vs"),
        focus: "vs",
        result:
          "| region | revenue | target | pct |\n| --- | --- | --- | --- |\n| EU | 360.5 | 300 | 120 |\n| APAC | 212 | 200 | 106 |\n| US | 390 | 450 | 87 |",
      },
      {
        title: "Postgres, read-only, password from a secret",
        text: "The connection is attached read-only; the password comes from the secret PGPASSWORD (Tools › Secrets or the keychain), never from the flow. {{inputs.days}} is an input of the flow.",
        flowInputs: [{ name: "days", label: "Days back", type: "text", default: "30" }],
        nodes: [
          {
            id: "top",
            type: "tool",
            title: "Top customers",
            tool: "duckdb_data_query",
            args: {
              sql: "SELECT c.name, count(*) AS orders, sum(o.amount) AS spent\nFROM pg.public.orders o JOIN pg.public.customers c ON c.id = o.customer_id\nWHERE o.created_at >= now() - INTERVAL {{inputs.days}} DAY\nGROUP BY c.name ORDER BY spent DESC LIMIT 10",
              connections:
                '[{"name": "pg", "type": "postgres", "dsn": "host=db.internal port=5432 dbname=sales user=reporter"}]',
            },
          },
        ],
        edges: chain("start", "top"),
        focus: "top",
        result:
          '[\n  {"name": "Acme GmbH", "orders": 42, "spent": 18250.5},\n  {"name": "Northwind", "orders": 31, "spent": 12990},\n  {"name": "Globex", "orders": 18, "spent": 9100.25}\n]',
      },
      {
        title: "Two Postgres databases and Oracle, each with its own password",
        text: "Any number of connections, each with its own connect string (parts, or a JDBC string) and its own password secret: DB_SALES, DB_CRM and DB_HR are asked for once. One query joins across them.",
        nodes: [
          {
            id: "multi",
            type: "tool",
            title: "Revenue, managers, headcount",
            tool: "duckdb_data_query",
            args: {
              sql: "SELECT o.region, sum(o.total) AS revenue, r.manager, h.headcount\nFROM sales.public.orders o\nJOIN crm.crm.regions r USING (region)\nJOIN hr.HR.REGION_HEADCOUNT h ON h.REGION = o.region\nGROUP BY ALL ORDER BY revenue DESC",
              connections:
                '[{"name": "sales", "type": "postgres", "host": "pg1.internal", "database": "sales", "user": "reporter", "password": "${DB_SALES}"},\n {"name": "crm", "type": "postgres", "dsn": "jdbc:postgresql://pg2.internal:5432/crm", "user": "reporter", "password": "${DB_CRM}"},\n {"name": "hr", "type": "oracle", "dsn": "jdbc:oracle:thin:@//ora1.internal:1521/HRPDB", "user": "rep", "password": "${DB_HR}"}]',
            },
          },
        ],
        edges: chain("start", "multi"),
        focus: "multi",
        result:
          '[\n  {"region": "EU", "revenue": 320.5, "manager": "Ana", "headcount": 42},\n  {"region": "US", "revenue": 80, "manager": "Bob", "headcount": 17}\n]',
      },
      {
        title: "Kerberos: Oracle and Postgres without passwords",
        text: "auth kerberos logs in with a Kerberos ticket (here from a service keytab, for scheduled runs). Oracle is attached live as /@service; DuckDB's Postgres driver has no Kerberos, so the Postgres tables listed in tables are read with psql and queried as sales.public.orders.",
        nodes: [
          {
            id: "krb",
            type: "tool",
            title: "Headcount and revenue",
            tool: "duckdb_data_query",
            args: {
              sql: "SELECT h.REGION AS region, h.HEADCOUNT AS headcount, sum(o.total) AS revenue\nFROM hr.HR.REGION_HEADCOUNT h\nJOIN sales.public.orders o ON o.region = h.REGION\nGROUP BY ALL ORDER BY revenue DESC",
              connections:
                '[{"name": "hr", "type": "oracle", "auth": "kerberos", "host": "ora1.corp", "service": "HRPDB", "keytab": "/etc/security/reporting.keytab", "principal": "reporting@CORP.EXAMPLE"},\n {"name": "sales", "type": "postgres", "auth": "kerberos", "host": "pg1.corp", "database": "sales", "user": "reporting", "keytab": "/etc/security/reporting.keytab", "principal": "reporting@CORP.EXAMPLE", "tables": ["public.orders"]}]',
            },
          },
        ],
        edges: chain("start", "krb"),
        focus: "krb",
        result:
          '[\n  {"region": "EU", "headcount": 42, "revenue": 320.5},\n  {"region": "US", "headcount": 17, "revenue": 80}\n]',
      },
      {
        title: "Oracle (needs Oracle Instant Client)",
        text: "The oracle community extension installs by itself; the machine needs Oracle Instant Client (19c, 21c or 23ai). ${ORACLE_PASSWORD} in the dsn is filled from that secret.",
        nodes: [
          {
            id: "open",
            type: "tool",
            title: "Open invoices",
            tool: "duckdb_data_query",
            args: {
              sql: "SELECT status, count(*) AS invoices, sum(amount) AS total\nFROM ora.FIN.INVOICES\nWHERE status <> 'PAID'\nGROUP BY status",
              connections:
                '[{"name": "ora", "type": "oracle", "dsn": "user=REPORTER password=${ORACLE_PASSWORD} dbq=//ora.internal:1521/FINPDB"}]',
            },
          },
        ],
        edges: chain("start", "open"),
        focus: "open",
        result:
          '[\n  {"status": "OPEN", "invoices": 128, "total": 412900},\n  {"status": "OVERDUE", "invoices": 17, "total": 58210.4}\n]',
      },
      {
        title: "Parquet on S3, the whole result saved as CSV",
        text: "A glob over a month of Parquet files in S3 (your AWS profile or SSO signs the requests). save_to writes every row to a CSV file; the next step gets the first 100 (max_rows).",
        nodes: [
          {
            id: "errors",
            type: "tool",
            title: "Errors by service",
            tool: "duckdb_data_query",
            args: {
              sql: "SELECT service, date_trunc('day', ts) AS day, count(*) AS errors\nFROM events\nWHERE level = 'ERROR'\nGROUP BY ALL ORDER BY day, errors DESC",
              sources: '{"events": "s3://acme-logs/2026/11/*.parquet"}',
              save_to: "out/errors-by-service.csv",
              max_rows: "100",
            },
          },
        ],
        edges: chain("start", "errors"),
        focus: "errors",
        result:
          '[\n  {"service": "billing", "day": "2026-11-01T00:00:00.000Z", "errors": 214},\n  {"service": "orders", "day": "2026-11-01T00:00:00.000Z", "errors": 97}\n]',
      },
      {
        title: "A whole S3 folder as one table",
        text: "A source ending in / reads every Parquet, CSV and JSON file under the prefix (sub-folders too) as one table: columns are matched by name, filename says which file a row came from, and hive folders such as region=eu/ become columns.",
        nodes: [
          {
            id: "lake",
            type: "tool",
            title: "Events by region",
            tool: "duckdb_data_query",
            args: {
              sql: "SELECT region, count(*) AS events, count(DISTINCT filename) AS files\nFROM events\nGROUP BY region ORDER BY events DESC",
              sources: '{"events": "s3://acme-lake/events/"}',
            },
          },
        ],
        edges: chain("start", "lake"),
        focus: "lake",
        result:
          '[\n  {"region": "eu", "events": 182344, "files": 31},\n  {"region": "us", "events": 97012, "files": 31}\n]',
      },
      {
        title: "Accumulate results across runs",
        text: "Each run adds today's totals to one Parquet file on S3 (save_mode append), then a second query reads the whole history. Schedule the flow to build it up day by day; a new column in a later run is added, earlier rows get null.",
        nodes: [
          {
            id: "today",
            type: "tool",
            title: "Add today's totals",
            tool: "duckdb_data_query",
            args: {
              sql: "SELECT current_date AS day, service, count(*) AS errors\nFROM events\nWHERE level = 'ERROR'\nGROUP BY ALL",
              sources: '{"events": "s3://acme-logs/today/"}',
              save_to: "s3://acme-reports/errors-history.parquet",
              save_mode: "append",
            },
          },
          {
            id: "history",
            type: "tool",
            title: "The last 7 days",
            tool: "duckdb_data_query",
            args: {
              sql: "SELECT day, sum(errors) AS errors\nFROM read_parquet('s3://acme-reports/errors-history.parquet')\nWHERE day > current_date - 7\nGROUP BY day ORDER BY day",
            },
          },
        ],
        edges: chain("start", "today", "history"),
        focus: "history",
        result:
          '[\n  {"day": "2026-11-20", "errors": 311},\n  {"day": "2026-11-21", "errors": 287},\n  {"day": "2026-11-22", "errors": 254}\n]',
      },
      {
        title: "Query, then a chart",
        text: "The query's rows go straight into a Visualization step (builtin:chart) that reads {{revenue.buffer_output}}.",
        nodes: [
          {
            id: "revenue",
            type: "tool",
            title: "Revenue by month",
            tool: "duckdb_data_query",
            args: {
              sql: "SELECT strftime(order_date, '%Y-%m') AS month, region, sum(total) AS revenue\nFROM read_csv('data/orders-*.csv')\nGROUP BY ALL ORDER BY month",
            },
          },
          {
            id: "pivot",
            type: "tool",
            title: "Revenue chart",
            tool: "builtin:chart",
            args: {
              type: "line",
              title: "Revenue by month",
              rows: "{{revenue.buffer_output}}",
              x: "month",
              y: "revenue",
            },
          },
        ],
        edges: chain("start", "revenue", "pivot"),
        focus: "revenue",
        result:
          '[\n  {"month": "2026-09", "region": "EU", "revenue": 18200},\n  {"month": "2026-10", "region": "EU", "revenue": 21450},\n  {"month": "2026-11", "region": "EU", "revenue": 19900}\n]',
      },
    ],
  },
  {
    type: "visualization",
    name: "Visualization",
    icon: { type: "tool", tool: "builtin:chart" },
    summary:
      "Draws data as a chart: bar, horizontal bar, stacked bar, line, multi-line, area, stacked area, scatter, pie, donut, heatmap, KPI tiles, gauges, bullet charts or sparklines, from the rows a tool, a query or an Aggregate returned. It is a tool step with the built-in chart tool (builtin:chart); the chart shows as a picture, with Export PNG.",
    when: [
      "Show the rows a tool or a query returned as a bar chart (test results by suite, revenue by region).",
      "Show a trend over time as a line, one line per series (orders per month by region).",
      "Show shares of a whole as a pie or donut (failures by cause, spend by team).",
      "Find patterns: a scatter of two numbers (size against defects), a heatmap of a grid (failed builds by day and hour).",
      "Show headline numbers against last period or a target: KPI tiles, gauges (SLOs, utilisation) or bullet charts (progress to target).",
      "Show many small trends at once: sparklines, a line per service, region or metric.",
      "Save a chart as SVG and PNG for a report, an email or a slide.",
    ],
    how: [
      "Add a Tool step and choose builtin:chart (in the Charts group). Its parameters show as fields; fill them with text and variables, such as {{<step id>.buffer_output}} for an earlier step's rows.",
      "builtin:chart takes rows (a JSON array of objects) with x (the label field) and y (the number fields; empty: every number field), or labels and series. type: bar, line, multiline or stacked_bar. It saves the chart in out/charts (or path) and returns it as SVG, shown as a picture with Export PNG; format png or both also saves a PNG.",
      "pie and donut draw the first series, a slice per label, with the share on each slice and the values in the legend (a donut shows the total in its hole). hbar puts the labels down the side, for long names. scatter needs numbers on x: name the x field. heatmap takes rows with x (the columns), y (the rows) and value (repeated cells are added up).",
      'kpi, gauge and bullet show each label\'s value; a second y field (last month, the target) is what it is compared with: the change in % on a tile, a target mark on a gauge or bullet. bands are the thresholds between bad, so-so and good ([90, 98]); lower_is_better turns them round (latency, cost); unit is written with the values ($, %, " ms").',
      "sparklines: a small line per series with its last value and its change since the first; from long rows, x (the points), y (the field naming each line) and value.",
      "show_values writes each value on its bar, point, slice or cell, so a PNG reads without the grid.",
      "Connect an Output step with HTML format to put the chart on a page of its own, saved where you like.",
    ],
    settings: [
      [
        "type",
        "bar, hbar, stacked_bar, line or multiline, area, stacked_area, scatter, pie, donut, heatmap, kpi, gauge, bullet, sparklines.",
      ],
      [
        "bands, lower_is_better, unit, min, max",
        "kpi, gauge, bullet: thresholds, which way is good, the unit, the scale.",
      ],
      ["show_values", "Write each value on the chart."],
      ["value", "heatmap with rows: the number field (y is the row field)."],
      ["rows, x, y", "The data as a JSON array of objects, the label field, and the number field(s)."],
      [
        "labels, series / data",
        "Or the labels and the series ([{name, values}]), or both in one JSON object.",
      ],
      ["title, x_label, y_label", "The chart's title and axis captions."],
      ["format, path", "svg (default), png or both; where to save (default out/charts)."],
    ],
    examples: [
      {
        title: "Bar chart: test results by suite",
        text: "Rows from a test run become a grouped bar chart: x is the label field, and every number field (passed, failed) is a bar colour.",
        nodes: [
          sample(
            "suites",
            "Test results",
            '[{"suite": "api", "passed": 120, "failed": 3}, {"suite": "billing", "passed": 80, "failed": 12}, {"suite": "orders", "passed": 95, "failed": 0}, {"suite": "ui", "passed": 60, "failed": 7}]',
          ),
          {
            id: "chart",
            type: "tool",
            title: "Results chart",
            tool: "builtin:chart",
            args: {
              type: "bar",
              title: "Test results by suite",
              rows: "{{suites.buffer_output}}",
              x: "suite",
            },
          },
        ],
        edges: chain("start", "suites", "chart"),
        focus: "chart",
      },
      {
        title: "Multi-line chart: orders per month by region",
        text: "One line per region: y names the series to draw, x_label and y_label caption the axes.",
        nodes: [
          sample(
            "orders",
            "Orders by month",
            '[{"month": "Jan", "EU": 120, "US": 80, "APAC": 40}, {"month": "Feb", "EU": 135, "US": 95, "APAC": 52}, {"month": "Mar", "EU": 128, "US": 110, "APAC": 61}, {"month": "Apr", "EU": 150, "US": 104, "APAC": 75}, {"month": "May", "EU": 171, "US": 120, "APAC": 70}, {"month": "Jun", "EU": 160, "US": 131, "APAC": 88}]',
          ),
          {
            id: "trend",
            type: "tool",
            title: "Orders trend",
            tool: "builtin:chart",
            args: {
              type: "multiline",
              title: "Orders per month",
              rows: "{{orders.buffer_output}}",
              x: "month",
              y: "EU, US, APAC",
              x_label: "Month",
              y_label: "Orders",
            },
          },
        ],
        edges: chain("start", "orders", "trend"),
        focus: "trend",
      },
      {
        title: "Donut: failures by cause",
        text: "Shares of a whole: one slice per cause, the share on each slice, the count and share in the legend, the total in the middle.",
        nodes: [
          sample(
            "causes",
            "Failures by cause",
            '[{"cause": "Assertion", "n": 22}, {"cause": "Timeout", "n": 14}, {"cause": "Network", "n": 9}, {"cause": "Setup", "n": 5}, {"cause": "Other", "n": 2}]',
          ),
          {
            id: "donut",
            type: "tool",
            title: "Causes donut",
            tool: "builtin:chart",
            args: {
              type: "donut",
              title: "Failures by cause",
              rows: "{{causes.buffer_output}}",
              x: "cause",
              y: "n",
              y_label: "failures",
            },
          },
        ],
        edges: chain("start", "causes", "donut"),
        focus: "donut",
      },
      {
        title: "Horizontal bars with values: the slowest tests",
        text: "Long names read best down the side; show_values writes each duration at the end of its bar.",
        nodes: [
          sample(
            "slow",
            "Slowest tests",
            '[{"test": "OrderServiceIntegrationTest.placesOrder", "seconds": 41.2}, {"test": "InvoiceExportTest.exportsPdf", "seconds": 30.8}, {"test": "ui/checkout.spec.ts", "seconds": 22.1}, {"test": "BillingRoundingTest.roundsHalfEven", "seconds": 12.5}]',
          ),
          {
            id: "hbar",
            type: "tool",
            title: "Slowest tests chart",
            tool: "builtin:chart",
            args: {
              type: "hbar",
              title: "Slowest tests (s)",
              rows: "{{slow.buffer_output}}",
              x: "test",
              show_values: "true",
            },
          },
        ],
        edges: chain("start", "slow", "hbar"),
        focus: "hbar",
      },
      {
        title: "Scatter: file size against defects",
        text: "Two numbers per row: x is lines of code, y the defects found; each point is a file.",
        nodes: [
          sample(
            "files",
            "Files",
            '[{"file": "Order.java", "loc": 120, "defects": 1}, {"file": "Billing.java", "loc": 480, "defects": 4}, {"file": "Invoice.java", "loc": 900, "defects": 9}, {"file": "Cart.java", "loc": 300, "defects": 2}, {"file": "Checkout.java", "loc": 1500, "defects": 11}, {"file": "Money.java", "loc": 60, "defects": 0}]',
          ),
          {
            id: "scatter",
            type: "tool",
            title: "Size vs. defects",
            tool: "builtin:chart",
            args: {
              type: "scatter",
              title: "File size vs. defects",
              rows: "{{files.buffer_output}}",
              x: "loc",
              y: "defects",
              x_label: "Lines of code",
              y_label: "Defects",
            },
          },
        ],
        edges: chain("start", "files", "scatter"),
        focus: "scatter",
      },
      {
        title: "Heatmap: failed builds by day and hour",
        text: "x is the columns (hour), y the rows (day), value the number; darker is more. Rows for the same cell are added up.",
        nodes: [
          sample(
            "builds",
            "Failed builds",
            '[{"day": "Mon", "hour": "09", "fails": 2}, {"day": "Mon", "hour": "12", "fails": 5}, {"day": "Mon", "hour": "17", "fails": 1}, {"day": "Tue", "hour": "09", "fails": 0}, {"day": "Tue", "hour": "12", "fails": 3}, {"day": "Tue", "hour": "17", "fails": 7}, {"day": "Wed", "hour": "09", "fails": 1}, {"day": "Wed", "hour": "12", "fails": 8}, {"day": "Wed", "hour": "17", "fails": 4}]',
          ),
          {
            id: "heat",
            type: "tool",
            title: "Failures heatmap",
            tool: "builtin:chart",
            args: {
              type: "heatmap",
              title: "Failed builds",
              rows: "{{builds.buffer_output}}",
              x: "hour",
              y: "day",
              value: "fails",
              show_values: "true",
            },
          },
        ],
        edges: chain("start", "builds", "heat"),
        focus: "heat",
      },
      {
        title: "Totals by region, saved as PNG too",
        text: "Rows that an Aggregate step (or a query) returns, one per region, become a bar chart; with format both a PNG is saved next to the SVG, for an email or a slide.",
        nodes: [
          sample(
            "totals",
            "Totals by region",
            '[{"region": "EU", "revenue": 360.5, "orders": 3}, {"region": "US", "revenue": 390, "orders": 2}, {"region": "APAC", "revenue": 212, "orders": 2}]',
          ),
          {
            id: "png",
            type: "tool",
            title: "Region chart",
            tool: "builtin:chart",
            args: {
              type: "bar",
              title: "Revenue by region",
              rows: "{{totals.buffer_output}}",
              x: "region",
              y: "revenue",
              format: "both",
              path: "out/charts/revenue-by-region",
            },
          },
        ],
        edges: chain("start", "totals", "png"),
        focus: "png",
      },
      {
        title: "A chart on its own page (Output, HTML)",
        text: "labels and series given as one JSON object (data). The chart step's SVG then goes into an Output step with HTML format: a page with the chart, saved as out/burn-up.html and shown in the run (with Export PNG).",
        nodes: [
          sample(
            "sprint",
            "Sprint",
            '{"labels": ["Mon", "Tue", "Wed", "Thu", "Fri"], "series": [{"name": "done", "values": [2, 5, 9, 12, 16]}, {"name": "planned", "values": [3, 6, 9, 12, 15]}]}',
          ),
          {
            id: "burn",
            type: "tool",
            title: "Burn-up chart",
            tool: "builtin:chart",
            args: { type: "line", title: "Sprint burn-up", data: "{{sprint.buffer_output}}" },
          },
          {
            id: "page",
            type: "output",
            title: "Burn-up (html)",
            format: "html",
            actions: [{ type: "save", path: "out/burn-up.html" }, { type: "show" }],
          },
        ],
        edges: chain("start", "sprint", "burn", "page"),
        focus: "burn",
      },
      {
        title: "KPI tiles: this month against last month",
        text: "A tile per metric: the value, and the change against the second y field (last month), green when it is better. lower_is_better would make a fall green (costs, refunds).",
        nodes: [
          sample(
            "kpis",
            "Metrics",
            '[{"metric": "Revenue", "now": 182400, "last_month": 161000}, {"metric": "Orders", "now": 1204, "last_month": 1290}, {"metric": "Avg order", "now": 151.5, "last_month": 124.8}]',
          ),
          {
            id: "tiles",
            type: "tool",
            title: "KPI tiles",
            tool: "builtin:chart",
            args: {
              type: "kpi",
              title: "November",
              rows: "{{kpis.buffer_output}}",
              x: "metric",
              y: '["now", "last_month"]',
            },
          },
        ],
        edges: chain("start", "kpis", "tiles"),
        focus: "tiles",
      },
      {
        title: "Gauges: SLOs against their targets",
        text: "A half dial per SLO: the arc is the value, coloured by bands (below 90 bad, 90 to 98 so-so, above good), the mark is the target.",
        nodes: [
          sample(
            "slo",
            "SLOs",
            '[{"slo": "Availability", "value": 99.2, "target": 99}, {"slo": "Latency < 300 ms", "value": 94.5, "target": 95}, {"slo": "Checks passing", "value": 87, "target": 95}]',
          ),
          {
            id: "dials",
            type: "tool",
            title: "SLO gauges",
            tool: "builtin:chart",
            args: {
              type: "gauge",
              title: "SLOs",
              unit: "%",
              bands: "[90, 98]",
              rows: "{{slo.buffer_output}}",
              x: "slo",
              y: '["value", "target"]',
            },
          },
        ],
        edges: chain("start", "slo", "dials"),
        focus: "dials",
      },
      {
        title: "Bullet chart: latency against its budget",
        text: "A bar per endpoint over green / amber / red zones, the budget as a mark. lower_is_better: under 200 ms is good.",
        nodes: [
          sample(
            "lat",
            "p95 latency",
            '[{"endpoint": "search", "p95": 180, "budget": 250}, {"endpoint": "checkout", "p95": 420, "budget": 300}, {"endpoint": "login", "p95": 260, "budget": 300}]',
          ),
          {
            id: "bullets",
            type: "tool",
            title: "Latency bullets",
            tool: "builtin:chart",
            args: {
              type: "bullet",
              title: "p95 latency",
              unit: " ms",
              bands: "[200, 400]",
              lower_is_better: "true",
              rows: "{{lat.buffer_output}}",
              x: "endpoint",
              y: '["p95", "budget"]',
            },
          },
        ],
        edges: chain("start", "lat", "bullets"),
        focus: "bullets",
      },
      {
        title: "Sparklines: a trend per service",
        text: "Long rows (day, service, errors) become a small line per service with its last value and change; lower_is_better makes a rise red.",
        nodes: [
          sample(
            "errs",
            "Errors per day",
            '[{"day": "Mon", "service": "api", "errors": 12}, {"day": "Tue", "service": "api", "errors": 9}, {"day": "Wed", "service": "api", "errors": 14}, {"day": "Thu", "service": "api", "errors": 6}, {"day": "Mon", "service": "billing", "errors": 2}, {"day": "Tue", "service": "billing", "errors": 3}, {"day": "Wed", "service": "billing", "errors": 7}, {"day": "Thu", "service": "billing", "errors": 11}]',
          ),
          {
            id: "trends",
            type: "tool",
            title: "Error trends",
            tool: "builtin:chart",
            args: {
              type: "sparklines",
              title: "Errors this week",
              rows: "{{errs.buffer_output}}",
              x: "day",
              y: "service",
              value: "errors",
              lower_is_better: "true",
            },
          },
        ],
        edges: chain("start", "errs", "trends"),
        focus: "trends",
      },
    ],
  },
  {
    type: "formattedtable",
    name: "Formatted table",
    icon: { type: "tool", tool: "builtin:table" },
    summary:
      "Turns rows (a query's or a tool's JSON) into a formatted HTML table: chosen columns with labels, numbers, currency, percent, dates, bytes and durations formatted, sorted, with a totals row, cells coloured by thresholds or rules, data bars and sparklines. A tool step with builtin:table; saved under out/tables and shown in SpecWright (opened in a browser, its headers sort).",
    when: [
      "A report needs the rows themselves, readable: money as money, shares as %, the worst ones red.",
      "Show a ranking with data bars (revenue by customer) or a trend per row (a sparkline of the last 30 days).",
      "Put a table on a Dashboard next to charts, or send it as Markdown (format markdown).",
    ],
    how: [
      "Add the Formatted table step (or a Tool step with builtin:table) and set rows to an earlier step's output: {{<step id>.buffer_output}}.",
      'columns picks and orders the columns: names ("region, revenue"), or objects: {"field": "revenue", "label": "Revenue", "format": "currency", "decimals": 0}. Without columns every field shows, its format guessed.',
      "Formats: number, integer, percent (0.25 or 25 → 25%), currency (unit $ by default), date, datetime, bytes, duration_ms, bar (a data bar), sparkline (a list of numbers, e.g. DuckDB list()), json, text.",
      'Colours: bands on a number column ([0.2, 0.3]: below red, between amber, above green; lower_is_better turns them round), or rules: [{"op": ">", "value": 300, "color": "bad"}] (good, warn, bad, info or #hex; "row": true colours the row).',
      'sort: a field, -field for largest first. totals: true sums the number columns, or {"orders": "sum", "margin": "avg"}.',
    ],
    settings: [
      ["rows", "The data: a JSON array of objects."],
      ["columns", "Which columns, in order, and how each looks."],
      ["sort, totals", "The order, and a totals row."],
      ["title, caption", "Above the table."],
      ["formats", 'A shortcut: {"field": "format"}.'],
      ["max_rows, format, path", "How many rows (default 500); html or markdown; where to save."],
    ],
    examples: [
      {
        title: "Regions: currency, data bars, colours and a sparkline",
        text: "Revenue as currency, orders as data bars, margin coloured by bands, p95 red over 300 ms, and the orders per day as a sparkline; largest revenue first, with totals.",
        nodes: [
          sample(
            "regions",
            "By region",
            '[{"region": "EU", "revenue": 182400.5, "orders": 1204, "margin": 0.31, "p95_ms": 180, "per_day": [12, 14, 13, 17, 19, 22]}, {"region": "US", "revenue": 161000, "orders": 1290, "margin": 0.18, "p95_ms": 420, "per_day": [20, 18, 19, 15, 14, 13]}, {"region": "APAC", "revenue": 47250, "orders": 388, "margin": 0.24, "p95_ms": 260, "per_day": [3, 4, 6, 5, 8, 9]}]',
          ),
          {
            id: "table",
            type: "tool",
            title: "Regions table",
            tool: "builtin:table",
            args: {
              title: "Regions",
              caption: "November, month to date",
              rows: "{{regions.buffer_output}}",
              sort: "-revenue",
              totals: "true",
              columns:
                '["region", {"field": "revenue", "format": "currency", "decimals": 0}, {"field": "orders", "format": "bar"}, {"field": "margin", "format": "percent", "bands": [0.2, 0.3]}, {"field": "p95_ms", "label": "p95", "format": "duration_ms", "rules": [{"op": ">", "value": 300, "color": "bad"}]}, {"field": "per_day", "label": "Orders / day", "format": "sparkline"}]',
            },
          },
        ],
        edges: chain("start", "regions", "table"),
        focus: "table",
      },
      {
        title: "Failed tests, the failing rows red",
        text: "Every field, formats guessed; a rule colours the whole row when status is failed.",
        nodes: [
          sample(
            "tests",
            "Test results",
            '[{"test": "login works", "status": "passed", "ms": 120}, {"test": "checkout total", "status": "failed", "ms": 840}, {"test": "search paging", "status": "passed", "ms": 95}]',
          ),
          {
            id: "t",
            type: "tool",
            title: "Results table",
            tool: "builtin:table",
            args: {
              title: "Test results",
              rows: "{{tests.buffer_output}}",
              columns:
                '["test", {"field": "status", "rules": [{"op": "=", "value": "failed", "color": "bad", "row": true}]}, {"field": "ms", "label": "Time", "format": "duration_ms"}]',
            },
          },
        ],
        edges: chain("start", "tests", "t"),
        focus: "t",
      },
    ],
  },
  {
    type: "dashboard",
    name: "Dashboard",
    icon: { type: "tool", tool: "builtin:dashboard" },
    summary:
      "Puts several results on one HTML page: charts, formatted tables, JSON rows (shown as a table), Markdown and text, in a grid under a title, an intro and when it was made. A tool step with builtin:dashboard; saved under out/dashboards and shown in SpecWright.",
    when: [
      "A weekly or daily report: KPI tiles, a trend, a table and a few notes on one page.",
      "Bring the results of a flow's branches together for a person to read.",
      "Run it on a schedule so the latest page is always in out/dashboards.",
    ],
    how: [
      "Connect the steps to show into the Dashboard step, then set panel_1 … panel_8 to their outputs: {{<step id>.buffer_output}}.",
      "Charts and formatted tables show their own titles; titles names the others, in order, separated by | (leave a place empty to keep a panel's own).",
      "columns: panels per row (default 2). wide: the panels that take a whole row ([3]); a wide table does by itself.",
      "intro is Markdown under the title, e.g. what the page shows and for whom. path saves it where you like (the default has the time in its name, so each run keeps its page).",
    ],
    settings: [
      ["panel_1 … panel_8", "Earlier steps' outputs."],
      ["title, intro", "The page's title and an introduction (Markdown)."],
      ["titles", "Panel titles, separated by |."],
      ["columns, wide", "The grid: panels per row, and the panels that take a whole row."],
      ["path", "Where to save the page."],
    ],
    examples: [
      {
        title: "A sales page: tiles, a table and notes",
        text: "KPI tiles and a formatted table are drawn from the same rows, then the dashboard puts them on one page with the notes.",
        nodes: [
          sample(
            "sales",
            "Sales",
            '[{"region": "EU", "revenue": 182400, "last_month": 161000, "orders": 1204}, {"region": "US", "revenue": 161000, "last_month": 170500, "orders": 1290}, {"region": "APAC", "revenue": 47250, "last_month": 39800, "orders": 388}]',
          ),
          sample(
            "notes",
            "Notes",
            "- **US** is down on last month: two large customers paused.\n- **APAC** keeps growing.",
          ),
          {
            id: "tiles",
            type: "tool",
            title: "Revenue tiles",
            tool: "builtin:chart",
            args: {
              type: "kpi",
              title: "Revenue",
              unit: "$",
              rows: "{{sales.buffer_output}}",
              x: "region",
              y: '["revenue", "last_month"]',
            },
          },
          {
            id: "table",
            type: "tool",
            title: "Sales table",
            tool: "builtin:table",
            args: {
              title: "By region",
              rows: "{{sales.buffer_output}}",
              sort: "-revenue",
              totals: "true",
              columns:
                '["region", {"field": "revenue", "format": "currency", "decimals": 0}, {"field": "orders", "format": "bar"}]',
            },
          },
          {
            id: "page",
            type: "tool",
            title: "Sales dashboard",
            tool: "builtin:dashboard",
            args: {
              title: "Sales — November",
              intro: "Month to date, by region.",
              panel_1: "{{tiles.buffer_output}}",
              panel_2: "{{table.buffer_output}}",
              panel_3: "{{notes.buffer_output}}",
              titles: "||Notes",
              wide: "[1]",
            },
          },
        ],
        edges: [
          ...chain("start", "sales", "tiles", "page"),
          ...chain("sales", "table", "page"),
          ...chain("start", "notes", "page"),
        ],
        focus: "page",
      },
    ],
  },
  {
    type: "chat",
    name: "Chat (Chatflows)",
    resultLabel: "A conversation · an example (your AI's answers will differ)",
    summary:
      "Makes the flow a Chatflow: people chat with it on the Chats screen. The steps before the Chat step set up a session once (load data, gather documents); each path after it is a tool the chat's AI runs only when a question needs it. Sessions keep their data and their conversation, so follow-up questions do not load anything again.",
    when: [
      "People ask many questions about the same data: sales numbers, a runbook, an incident's logs.",
      "Some questions need fresh work (a query, a chart, a web page) and most do not.",
      "The data needs secrets or settings the person chatting should never see.",
    ],
    how: [
      "Add a Chat step after the steps that load what a session needs; connect the Inputs step (directly or through them) into it.",
      "Connect steps after the Chat step: each path out of it is one tool, named after its first step. Leave a tool's fields empty for the AI to fill from the question, or set them; {{chat.message}} is what the AI passes on, {{session.<step id>}} a setup step's result.",
      "In the Chat step: the instructions (what the assistant is for), starter questions, a model and a cost limit per session; and the Chatflow's environment: variables (values) and secrets (names only: their values come from the keychain or are asked for once, and never show in the chat).",
      "Any field can read them: {{env.NAME}} for a variable (the environment's value of the same name wins), {{project_secret.NAME}} for a secret kept encrypted in the project, {{keychain.NAME}} for one in your computer's keychain (both set in Settings › Secrets, looked up when a session starts, hidden everywhere; keep them out of what the AI reads).",
      "Chat on the Chats screen: + New session asks for the inputs and runs the setup once. Tools that only read run at once; anything that writes asks you first. /refresh runs the setup again, /data and /steps show what the session holds and can run.",
    ],
    settings: [
      ["instructions", "What the assistant is for and how it answers."],
      ["starters", "Questions shown on an empty session."],
      ["model, costLimit", "The model, and a cost limit per session (USD)."],
      ["vars, secrets", "The Chatflow's environment, given to every step a session runs."],
    ],
    examples: [
      {
        title: "Chat about sales rows, with a chart tool",
        text: 'The setup loads the rows once; the chart tool\'s type and fields are left empty, so the AI fills them from the question ("chart revenue by region").',
        nodes: [
          sample("sales", "Sales rows", ORDERS),
          {
            id: "chat",
            type: "chat",
            title: "Sales assistant",
            instructions:
              "You answer questions about these orders. Show numbers in tables; draw a chart when asked.",
            starters: ["Which region has the most revenue?", "Chart revenue by region"],
          },
          {
            id: "chart",
            type: "tool",
            title: "Draw a chart",
            tool: "builtin:chart",
            args: { type: "", title: "", rows: "{{session.sales}}", x: "", y: "" },
          },
        ],
        edges: chain("start", "sales", "chat", "chart"),
        focus: "chat",
        result:
          "🧑 Which region has the most revenue?\n🤖 US: 390.00 over 2 orders (one of them closed), then EU with 360.50 over 3.\n\n🧑 Chart revenue by region\n🤖 ▸ ran Draw a chart · 0.2 s  (bar · x region · y total)\n   [the chart, with Export PNG]",
      },
      {
        title: "Docs Q&A that can fetch a page",
        text: "The setup gathers the runbook once; a question the runbook does not answer can fetch a page (its address filled by the AI).",
        nodes: [
          sample(
            "docs",
            "Runbook",
            "# Billing runbook\n\nRestart the invoice worker with `svc restart invoices`. Alerts page the billing on-call.",
          ),
          {
            id: "chat",
            type: "chat",
            title: "Runbook help",
            instructions:
              "Answer from the runbook. Quote the command to run. Fetch a page only when the runbook does not cover the question.",
          },
          {
            id: "fetch",
            type: "tool",
            title: "Fetch a page",
            tool: "builtin:fetch_webpage",
            args: { url: "", format: "markdown" },
          },
        ],
        edges: chain("start", "docs", "chat", "fetch"),
        focus: "chat",
        result:
          "🧑 How do I restart the invoice worker?\n🤖 Run `svc restart invoices` (from the Billing runbook).\n\n🧑 What does the status page say right now?\n🤖 ▸ ran Fetch a page · 1.1 s\n   All systems operational; the last incident was resolved yesterday.",
      },
    ],
  },
  {
    type: "output",
    name: "Output",
    summary:
      "The end of a path: formats the result as Markdown, text, HTML, CSV or JSON, then saves it, passes it to a tool, copies it or shows it.",
    when: [
      "Save a report into the project (out/report-{{date}}.md).",
      "Turn JSON rows into a CSV file, or an HTML or Markdown table.",
      "Show the result in Run flows, or copy it to the clipboard.",
    ],
    how: [
      "Choose the format. Content is converted: a JSON array becomes a CSV table, Markdown becomes HTML, and so on.",
      'A list of records (a JSON array of objects, or an object holding one, like a Gather\'s {"rows": [...]}) becomes a table in HTML and Markdown, its columns in the order the JSON has them; other JSON stays a code block. To choose or rename the columns, put a Transform › JSON array → HTML table (or → Markdown table) before it.',
      "Content (optional) is a template; empty uses the output of the steps connected into it.",
      "Actions: Save (a project path; variables allowed), Call a tool with the content ({{output}}), Copy to the clipboard, Show in the run.",
      "Each Output appears in Run flows with its saved files; a flow can have several outputs.",
    ],
    settings: [
      ["Format", "md, txt, html, csv or json."],
      ["Content", "A template; empty: the connected output."],
      ["Actions", "Save, call a tool, copy, show."],
    ],
    examples: [
      {
        title: "Save a Markdown report",
        text: "The content template adds a heading; Save writes it to out/report-<date>.md.",
        nodes: [
          sample("summary", "Summary", "- 3 open TODOs\n- 1 FIXME in billing"),
          {
            id: "report",
            type: "output",
            title: "Report (md)",
            format: "md",
            template: "# Weekly report ({{date}})\n\n{{summary.buffer_output}}",
            actions: [{ type: "save", path: "out/report-{{date}}.md" }, { type: "show" }],
          },
        ],
        edges: chain("start", "summary", "report"),
        focus: "report",
      },
      {
        title: "JSON rows as CSV",
        text: "A JSON array of objects becomes a CSV table with a header.",
        nodes: [
          sample("rows", "Stock", '[{"sku": "KB-01", "stock": 14}, {"sku": "MS-07", "stock": 0}]'),
          {
            id: "csv",
            type: "output",
            title: "Stock (csv)",
            format: "csv",
            actions: [{ type: "save", path: "out/stock.csv" }, { type: "show" }],
          },
        ],
        edges: chain("start", "rows", "csv"),
        focus: "csv",
      },
      {
        title: "Gathered rows as an HTML table",
        text: 'The rows a Gather collects ({"rows": [...]}) are saved as an HTML page with a table: one row per record, the columns in the order the JSON has them, nested values as compact JSON. The run view shows the same table.',
        nodes: [
          sample(
            "gather-rows",
            "Gathered rows",
            '{"rows": [\n  {"id": "A-1001", "customer": "Ana", "status": "open", "total": 120.5},\n  {"id": "A-1002", "customer": "Bob", "status": "closed", "total": 80},\n  {"id": "A-1005", "customer": "Dee", "status": "open", "total": 310, "items": [{"sku": "HD-12", "qty": 2}]}\n]}',
          ),
          {
            id: "table",
            type: "output",
            title: "Orders (html)",
            format: "html",
            actions: [{ type: "save", path: "out/orders-{{date}}.html" }, { type: "show" }],
          },
        ],
        edges: chain("start", "gather-rows", "table"),
        focus: "table",
      },
      {
        title: "Markdown shown as HTML",
        text: "The result is converted to HTML and shown in the run.",
        nodes: [
          sample("notes", "Notes", "## Release 2.3\n- Orders can be cancelled\n- **Faster** invoice export"),
          { id: "html", type: "output", title: "Notes (html)", format: "html", actions: [{ type: "show" }] },
        ],
        edges: chain("start", "notes", "html"),
        focus: "html",
      },
    ],
  },
];

// The example as a flow (nodes laid out left to right) and the sample data
// for previewing its focus step.
export function exampleFlow(ex) {
  const nodes = [
    { id: "start", type: "start", title: "Inputs" },
    ...ex.nodes.map((n) => {
      const c = JSON.parse(JSON.stringify(n));
      delete c.sample;
      return c;
    }),
  ];
  const depth = { start: 0 };
  for (let k = 0; k < nodes.length; k++)
    for (const e of ex.edges) depth[e.to] = Math.max(depth[e.to] ?? 0, (depth[e.from] ?? 0) + 1);
  const rows = {};
  for (const n of nodes) {
    const d = depth[n.id] ?? 0,
      r = (rows[d] = (rows[d] ?? -1) + 1);
    n.x = 40 + d * 260;
    n.y = 80 + r * 140;
  }
  const steps = {};
  for (const n of ex.nodes) if (n.sample) steps[n.id] = n.template;
  return { flow: { nodes, edges: ex.edges.map((e) => ({ ...e })), inputs: ex.flowInputs || [] }, steps };
}

// A tool's own usage guide (saved with the tool: project, global, admin-
// managed or Copilot) as a flow guide section. Each example is a flow: the
// example's sample (when it has one) as a template step "sample", then the
// tool step "run" with the example's arguments.
export function toolExampleFlow(name, ex) {
  const run = { id: "run", type: "tool", tool: name, title: ex.title || name, args: { ...(ex.args || {}) } };
  const nodes = ex.sample ? [sample("sample", "Sample input", ex.sample), run] : [run];
  return { nodes, edges: chain("start", ...nodes.map((n) => n.id)), focus: "run" };
}

export const hasToolGuide = (t) =>
  !!t?.guide &&
  !!(t.guide.summary || t.guide.when?.length || t.guide.how?.length || t.guide.examples?.length);

export function toolGuideSection(t) {
  const g = t.guide || {};
  return {
    type: "tool:" + t.name,
    tool: t,
    name: t.name,
    icon: { type: "tool", tool: t.name },
    summary: g.summary || t.desc || "",
    when: g.when || [],
    how: g.how || [],
    settings: (t.params || []).map((p) => [
      p.name + (p.required ? " (required)" : ""),
      [
        p.desc,
        p.default ? `Default: ${p.default}.` : "",
        p.enum?.length ? `One of: ${p.enum.join(", ")}.` : "",
      ]
        .filter(Boolean)
        .join(" ") ||
        p.type ||
        "string",
    ]),
    resultLabel: "Result · from the guide (Run example runs the tool)",
    examples: (g.examples || []).map((ex) => ({
      title: ex.title,
      text: ex.text || "",
      result: ex.result || "(no result saved yet: Run example runs it)",
      toolExample: ex,
      ...toolExampleFlow(t.name, ex),
    })),
  };
}
