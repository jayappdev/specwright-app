// usage: node shoot.mjs plan.json outdir
// plan: [{name, view?, setupDone?(default true), theme?, w?, h?, js?[] (evaluated in order, 400ms apart), full?}]
import { spawn } from "node:child_process";
import { readFile, writeFile, mkdir, rm } from "node:fs/promises";
const [planFile, out] = process.argv.slice(2);
const plan = JSON.parse(await readFile(planFile, "utf8"));
await mkdir(out, { recursive: true });
const prof = (process.env.TMPDIR || "/tmp") + "/specwright-shoot-prof"; await rm(prof, { recursive: true, force: true });
const port = 9333;
const chrome = spawn("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", ["--headless=new", `--remote-debugging-port=${port}`, `--user-data-dir=${prof}`, "--hide-scrollbars", "--no-first-run", "--force-device-scale-factor=1", "about:blank"], { stdio: "ignore" });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let targets;
for (let i = 0; i < 40; i++) { try { targets = await (await fetch(`http://127.0.0.1:${port}/json`)).json(); if (targets.length) break; } catch {} await sleep(250); }
const ws = new WebSocket(targets.find((t) => t.type === "page").webSocketDebuggerUrl);
await new Promise((r) => (ws.onopen = r));
let id = 0; const pend = new Map();
ws.onmessage = (m) => { const d = JSON.parse(m.data); if (d.id && pend.has(d.id)) { pend.get(d.id)(d); pend.delete(d.id); } };
const cdp = (method, params = {}) => new Promise((r) => { const i = ++id; pend.set(i, r); ws.send(JSON.stringify({ id: i, method, params })); });
const ev = async (expr) => { const r = await cdp("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true }); if (r.result?.exceptionDetails) console.error("JS error:", expr.slice(0, 80), r.result.exceptionDetails.exception?.description?.slice(0, 200)); return r.result?.result?.value; };
await cdp("Page.enable"); await cdp("Runtime.enable");
const URL = "http://localhost:5173/";
for (const s of plan) {
  const w = s.w || 1440, h = s.h || 900;
  await cdp("Emulation.setDeviceMetricsOverride", { width: w, height: h, deviceScaleFactor: 1, mobile: false });
  await cdp("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: s.scheme || "dark" }] });
  await cdp("Page.navigate", { url: URL }); await sleep(500);
  if (s.reset) await ev(`localStorage.clear()`);
  const prefs = { appearance: { theme: s.theme || "system", skin: s.skin || "workbench", fontSize: 13, font: "", codeFont: "" }, view: s.view || "wizard", setup: s.setupDone === false ? { done: false } : { done: true, provider: "mock", source: "scripted", model: "" }, tab: s.tab || "activity" };
  await ev(`localStorage.setItem("codegen.prefs.v1", ${JSON.stringify(JSON.stringify(prefs))})`);
  await cdp("Page.navigate", { url: URL }); await sleep(s.wait || 1200);
  for (const j of s.js || []) { const v = await ev(j); if (v !== undefined && v !== null) console.log(s.name, "→", JSON.stringify(v).slice(0, 300)); await sleep(s.step || 500); }
  let clip;
  if (s.full) { const hh = await ev(`Math.max(document.documentElement.scrollHeight, ...[...document.querySelectorAll('.main, .view, .main *')].map(e=>e.scrollHeight||0))`); if (hh > h) { await cdp("Emulation.setDeviceMetricsOverride", { width: w, height: Math.min(hh + 60, 4000), deviceScaleFactor: 1, mobile: false }); await sleep(400); } }
  const r = await cdp("Page.captureScreenshot", { format: "png", ...(clip ? { clip } : {}) });
  await writeFile(`${out}/${s.name}.png`, Buffer.from(r.result.data, "base64"));
  console.log("shot", s.name);
}
ws.close(); chrome.kill();
