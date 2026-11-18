// npm run deps:age [-- hours]   (from frontend/)
// Lists every package version in package-lock.json published less than
// `hours` (default 72) ago, so new installs can be checked against a
// "no packages newer than 72 hours" rule. Exits 1 when it finds any.
// Pin a too-new transitive package with "overrides" in package.json.
import { readFileSync } from "node:fs";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
const run = promisify(execFile);
const lock = JSON.parse(readFileSync(new URL("../package-lock.json", import.meta.url), "utf8"));
const hours = +(process.argv[2] || 72);
const cutoff = Date.now() - hours * 3600e3;
const pkgs = new Map();
for (const [path, v] of Object.entries(lock.packages)) {
  if (!path || !v.version) continue;
  const name = v.name || path.slice(path.lastIndexOf("node_modules/") + 13);
  pkgs.set(name + "@" + v.version, { name, version: v.version });
}
const list = [...pkgs.values()];
const times = new Map();
let i = 0;
async function worker() {
  while (i < list.length) {
    const p = list[i++];
    if (!times.has(p.name)) {
      times.set(p.name, null);
      try {
        const { stdout } = await run("npm", ["view", p.name, "time", "--json"], { maxBuffer: 64 << 20 });
        times.set(p.name, JSON.parse(stdout));
      } catch (e) {
        times.set(p.name, { error: String(e.message).slice(0, 80) });
      }
    }
  }
}
await Promise.all(Array.from({ length: 12 }, worker));
const recent = [],
  unknown = [];
for (const p of list) {
  const t = times.get(p.name);
  const at = t?.[p.version];
  if (!at) unknown.push(`${p.name}@${p.version}`);
  else if (Date.parse(at) > cutoff) recent.push(`${p.name}@${p.version}  ${at}`);
}
console.log(`${list.length} package versions checked; cutoff ${new Date(cutoff).toISOString()}`);
console.log("published within the window:\n  " + (recent.join("\n  ") || "(none)"));
if (unknown.length) console.log("no publish time found:\n  " + unknown.join("\n  "));
if (recent.length) process.exitCode = 1;
