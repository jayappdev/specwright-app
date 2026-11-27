// Bundles src/ into dist/ (embedded by main.go). `--serve` runs a dev
// server on http://localhost:5173 where the UI talks to mockBackend.js.
import * as esbuild from "esbuild";
import { copyFile, mkdir, rm, writeFile } from "node:fs/promises";

const serve = process.argv.includes("--serve");
// the dev server uses its own folder so it never empties the dist/ that main.go embeds
const outdir = serve ? ".dev" : "dist";
await rm(outdir, { recursive: true, force: true });
await mkdir(outdir, { recursive: true });
// dist/.gitkeep is tracked so `go:embed all:frontend/dist` compiles on a fresh clone: keep it
if (!serve) await writeFile(`${outdir}/.gitkeep`, "");
await copyFile("index.html", `${outdir}/index.html`);
// one source for the app icon, the favicon and the in-app logo
await copyFile("../build/appicon.svg", `${outdir}/logo.svg`);

const opts = {
  entryPoints: { app: "src/main.jsx" },
  bundle: true,
  outdir,
  format: "esm",
  // large parts used only sometimes (Mermaid, for diagrams in previews)
  // load when first needed, from chunks next to app.js
  splitting: true,
  chunkNames: "chunks/[name]-[hash]",
  target: ["safari15", "chrome110"],
  jsx: "automatic",
  loader: { ".css": "css", ".svg": "dataurl", ".woff2": "file" },
  assetNames: "fonts/[name]-[hash]",
  minify: !serve,
  sourcemap: serve ? "inline" : false,
  define: { "process.env.NODE_ENV": JSON.stringify(serve ? "development" : "production") },
  logLevel: "info",
};

if (serve) {
  const ctx = await esbuild.context(opts);
  await ctx.watch();
  const { port } = await ctx.serve({ servedir: outdir, port: 5173 });
  console.log(`UI with mock backend: http://localhost:${port}`);
} else {
  await esbuild.build(opts);
}
