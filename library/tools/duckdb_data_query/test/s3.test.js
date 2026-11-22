// duckdb_data_query against S3 (a local S3-compatible server): folders,
// globs, hive partitions, saving and appending to s3:// files.
//
// Needs @duckdb/node-api: point DUCKDB_TOOL_DIR at a folder where it is
// installed (e.g. the tool's own folder in a project,
// .codegen/tools/project/duckdb_data_query) and run
//   DUCKDB_TOOL_DIR=... node --test library/tools/duckdb_data_query/test/
"use strict";
const test = require("node:test");
const assert = require("node:assert");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { execFile } = require("node:child_process");
const { promisify } = require("node:util");
const { start } = require("./fake-s3.js");

const toolDir = process.env.DUCKDB_TOOL_DIR;
const skip = !toolDir || !fs.existsSync(path.join(toolDir, "node_modules", "@duckdb", "node-api")) ? "set DUCKDB_TOOL_DIR to a folder with @duckdb/node-api installed" : false;

test("duckdb_data_query on S3", { skip }, async (t) => {
  const data = fs.mkdtempSync(path.join(os.tmpdir(), "sw-s3-"));
  const put = (k, v) => {
    fs.mkdirSync(path.dirname(path.join(data, k)), { recursive: true });
    fs.writeFileSync(path.join(data, k), v);
  };
  put("lake/sales/year=2025/a.csv", "region,total\nEU,100\nUS,50\n");
  put("lake/sales/year=2026/q1/b.json", '[{"region":"EU","total":30,"channel":"web"}]');
  put("lake/sales/_SUCCESS", "");
  const srv = await start(data);
  // the script runs from a folder that can import @duckdb/node-api
  const script = path.join(toolDir, "index.sw-test.js");
  fs.copyFileSync(path.join(__dirname, "..", "index.js"), script);
  t.after(() => {
    srv.close();
    fs.rmSync(script, { force: true });
    fs.rmSync(data, { recursive: true, force: true });
  });
  const env = { ...process.env, AWS_ACCESS_KEY_ID: "swtest", AWS_SECRET_ACCESS_KEY: "x", AWS_REGION: "us-east-1", AWS_ENDPOINT_URL: srv.endpoint };
  // async: the S3 server answers in this process
  const run = async (args, extra = {}) =>
    (await promisify(execFile)("node", [script], { env: { ...env, ...extra, SPECWRIGHT_ARGS: JSON.stringify(args) }, encoding: "utf8", timeout: 60000 })).stdout;

  await t.test("a folder: every file under it, nested, csv and json, hive columns, filename", async () => {
    const rows = JSON.parse(await await run({ sql: "SELECT region, sum(total) AS total, count(DISTINCT filename) AS files, list(DISTINCT year ORDER BY year) AS years FROM s GROUP BY region ORDER BY region", sources: { s: "s3://lake/sales/" } }));
    assert.deepStrictEqual(rows, [
      { region: "EU", total: 130, files: 2, years: [2025, 2026] },
      { region: "US", total: 50, files: 1, years: [2025] },
    ]);
  });
  await t.test("a glob", async () => {
    assert.deepStrictEqual(JSON.parse(await run({ sql: "SELECT count(*) AS n FROM s", sources: { s: "s3://lake/sales/**/*.csv" } })), [{ n: 2 }]);
  });
  await t.test("append accumulates rows across runs (parquet and csv)", async () => {
    for (const dest of ["s3://lake/out/history.parquet", "s3://lake/out/history.csv"]) {
      for (let i = 0; i < 3; i++) await run({ sql: "SELECT region, sum(total) AS total FROM s GROUP BY region", sources: { s: "s3://lake/sales/" }, save_to: dest, save_mode: "append" });
      const fn = dest.endsWith(".csv") ? "read_csv_auto" : "read_parquet";
      assert.deepStrictEqual(JSON.parse(await run({ sql: `SELECT region, count(*) AS runs, sum(total) AS total FROM ${fn}('${dest}') GROUP BY region ORDER BY region` })), [
        { region: "EU", runs: 3, total: 390 },
        { region: "US", runs: 3, total: 150 },
      ]);
    }
  });
  await t.test("a new column in a later run: earlier rows get null", async () => {
    await run({ sql: "SELECT 1 AS a", save_to: "s3://lake/out/grow.parquet", save_mode: "append" });
    await run({ sql: "SELECT 2 AS a, 'x' AS b", save_to: "s3://lake/out/grow.parquet", save_mode: "append" });
    assert.deepStrictEqual(JSON.parse(await run({ sql: "SELECT * FROM read_parquet('s3://lake/out/grow.parquet') ORDER BY a" })), [
      { a: 1, b: null },
      { a: 2, b: "x" },
    ]);
  });
  await t.test("overwrite replaces the file", async () => {
    await run({ sql: "SELECT 7 AS n", save_to: "s3://lake/out/history.csv" });
    assert.strictEqual(fs.readFileSync(path.join(data, "lake/out/history.csv"), "utf8").trim(), "n\n7");
  });
  await t.test("hive-partitioned output written to S3 reads back as a folder", async () => {
    await run({ sql: "COPY (SELECT range AS id, range % 4 AS k FROM range(1000)) TO 's3://lake/parts' (FORMAT parquet, PARTITION_BY (k)); SELECT 1 AS ok" });
    assert.deepStrictEqual(JSON.parse(await run({ sql: "SELECT count(*) AS n FROM p WHERE k = 2", sources: { p: "s3://lake/parts/" } })), [{ n: 250 }]);
  });
  await t.test("a wrong key is refused", async () => {
    await assert.rejects(run({ sql: "SELECT count(*) FROM s", sources: { s: "s3://lake/sales/" } }, { AWS_ACCESS_KEY_ID: "nope" }), /403/);
  });
});
