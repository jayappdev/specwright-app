// SpecWright's tools for GitHub Copilot, as a Copilot extension: for
// machines where Copilot does not load SpecWright's MCP server (agentbridge),
// a company policy for MCP servers, say. SpecWright writes this file and
// keeps it up to date: changes to it are overwritten.
//
// It forwards to the SpecWright app that started the Copilot run (the
// CODEGEN_BRIDGE_URL / _TOKEN it set). Copilot run on its own has neither,
// and then this extension registers no tools.
import { joinSession } from "@github/copilot-sdk/extension";

const url = process.env.CODEGEN_BRIDGE_URL;
const token = process.env.CODEGEN_BRIDGE_TOKEN;

async function bridge(path, body) {
  const r = await fetch(url + path, {
    method: body ? "POST" : "GET",
    headers: { Authorization: "Bearer " + token, "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!r.ok) throw new Error(`SpecWright answered ${r.status}`);
  return r.json();
}

// what a tool call answers: its output, or its error with the output
function answer(res) {
  if (!res) return "";
  const out = res.output ?? res.Output ?? "";
  const err = res.error ?? res.Error ?? "";
  if (err) return "Error: " + err + (out && out !== err ? "\n" + out : "");
  if (res.ok === false && res.exitCode !== undefined) return `exit code ${res.exitCode}\n${out}`;
  return out;
}

const tools = [];
if (url && token) {
  try {
    const builtins = (await bridge("/builtins")) || [];
    const builtinNames = new Set(builtins.map((b) => b.name));
    for (const b of builtins.filter((x) => x.app)) {
      tools.push({
        name: "specwright_" + b.name,
        description: b.description + " (SpecWright's " + b.name + ", the same tool as agentbridge-" + b.name + ".)",
        parameters: b.inputSchema || { type: "object" },
        handler: async (args) => answer(await bridge("/builtin", { name: b.name, arguments: args || {} })),
      });
    }
    let project = [];
    try {
      project = ((await bridge("/list")) || []).map((t) => t.name);
    } catch {
      /* no project tools */
    }
    const others = builtins.filter((x) => !x.app).map((x) => x.name);
    tools.push({
      name: "specwright_tool",
      description:
        "Run one of SpecWright's other tools by name, with its arguments as an object. Built-ins: " +
        others.join(", ") +
        (project.length ? ". The project's tools: " + project.join(", ") : "") +
        ". (The same tools as agentbridge-<name> and agentbridge-extension_managed.)",
      parameters: {
        type: "object",
        properties: {
          name: { type: "string", description: "The tool's name", enum: [...others, ...project] },
          arguments: { type: "object", description: "The tool's arguments", additionalProperties: true },
        },
        required: ["name"],
      },
      handler: async ({ name, arguments: args }) =>
        builtinNames.has(name)
          ? answer(await bridge("/builtin", { name, arguments: args || {} }))
          : answer(await bridge("/call", { name, arguments: args || {}, agent: "copilot" })),
    });
  } catch {
    /* SpecWright is not answering: no tools */
  }
}

await joinSession({ tools });
