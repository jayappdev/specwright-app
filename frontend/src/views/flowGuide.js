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
      "The step's result is the agent's answer. Ask for “only JSON” when a later step should read it as data.",
      "AI steps pause for your review when the run asks for it: accept, edit, refine with AI or stop.",
    ],
    settings: [
      ["Prompt", "What to ask; variables are filled in when the step runs."],
      ["Model", "A model for this step only; empty uses the one chosen for the run."],
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
    type: "output",
    name: "Output",
    summary:
      "The end of a path: formats the result as Markdown, text, HTML, CSV or JSON, then saves it, passes it to a tool, copies it or shows it.",
    when: [
      "Save a report into the project (out/report-{{date}}.md).",
      "Turn JSON rows into a CSV file.",
      "Show the result in Run flows, or copy it to the clipboard.",
    ],
    how: [
      "Choose the format. Content is converted: a JSON array becomes a CSV table, Markdown becomes HTML, and so on.",
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
