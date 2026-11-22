// duckdb_data_query: query data with DuckDB SQL (DuckDB 2.0) and get the
// rows back as JSON (or CSV, or a Markdown table).
//
// Sources: files and globs (CSV, JSON, Parquet, Excel), URLs and S3 / Azure
// paths, the previous flow step's output (the table "input"), and
// databases attached by connection: Postgres, MySQL, SQLite, SQL Server,
// Oracle, DuckDB files. Extensions are installed and loaded as needed.
//
// Arguments: by name from SpecWright (the JSON in $SPECWRIGHT_ARGS), or
// positional in this order when run by hand:
//   sql          the query; several statements run in turn, the last one's
//                rows are the result
//   sources      JSON object: table name -> file, glob, URL or s3:// path,
//                e.g. {"orders": "data/orders.csv", "events": "s3://bkt/*.parquet"}
//   connections  JSON array: [{"name": "pg", "type": "postgres",
//                "dsn": "host=db port=5432 dbname=sales user=me", "read_only": true}]
//                ${NAME} in a dsn is replaced from the environment (secrets
//                such as PGPASSWORD reach the tool there)
//   extensions   comma-separated, e.g. "httpfs, aws, oracle@community"
//   input        a file with the previous step's output (a flow passes
//                {{input_file}}): the table "input"
//   format       json (default) | csv | markdown
//   save_to      also write the result to this file (.csv, .json, .parquet)
//   max_rows     at most this many rows in the answer (default 1000)
//
// Credentials: AWS from its usual chain (environment, ~/.aws profiles, SSO);
// Postgres PGPASSWORD, MySQL MYSQL_PWD; ${NAME} placeholders in a dsn.
// Settings from the environment: DUCKDB_EXTENSION_DIRECTORY,
// DUCKDB_EXTENSION_REPOSITORY (a mirror, for networks that block
// extensions.duckdb.org), DUCKDB_HTTP_PROXY.
"use strict";
const fs = require("node:fs");
const path = require("node:path");

const ARGS = ["sql", "sources", "connections", "extensions", "input", "format", "save_to", "max_rows"];
function readArgs() {
  let named = null;
  try {
    if (process.env.SPECWRIGHT_ARGS) named = JSON.parse(process.env.SPECWRIGHT_ARGS);
  } catch {
    named = null;
  }
  const pos = process.argv.slice(2);
  return ARGS.map((k, i) => {
    if (named && typeof named === "object") {
      const v = named[k];
      return v === undefined || v === null ? "" : typeof v === "object" ? JSON.stringify(v) : String(v);
    }
    return pos[i] || "";
  });
}
const [sqlArg, sourcesArg, connsArg, extsArg, inputArg, formatArg, saveTo, maxRowsArg] = readArgs();

function fail(msg) {
  process.stderr.write("duckdb_data_query: " + msg + "\n");
  process.exit(1);
}

const q = (s) => "'" + String(s).replace(/'/g, "''") + "'"; // a SQL string
const ident = (s) => '"' + String(s).replace(/"/g, '""') + '"'; // a SQL name

function parseJSON(text, what) {
  const t = String(text || "").trim();
  if (!t) return null;
  try {
    return JSON.parse(t);
  } catch (e) {
    fail(`${what} is not valid JSON: ${e.message}`);
  }
}

// ${NAME} -> the environment variable NAME
const expandEnv = (s) =>
  String(s).replace(/\$\{([A-Za-z_][A-Za-z0-9_]*)\}/g, (_, n) => {
    if (process.env[n] === undefined) fail(`the environment variable ${n} (used in a connection) is not set`);
    return process.env[n];
  });

// a reader for a file, glob or URL (DuckDB guesses CSV, JSON and Parquet)
function scanOf(loc) {
  const l = String(loc).trim();
  if (/\.xlsx?$/i.test(l)) return `read_xlsx(${q(l)})`;
  if (/\.(json|jsonl|ndjson)$/i.test(l)) return `read_json_auto(${q(l)})`;
  return q(l);
}

const CONN_TYPES = { postgres: "postgres", postgresql: "postgres", mysql: "mysql", sqlite: "sqlite", duckdb: "duckdb", sqlserver: "mssql", mssql: "mssql", oracle: "oracle" };
const COMMUNITY = new Set(["oracle", "mssql", "snowflake", "bigquery", "mongo"]);

function toCSV(cols, rows) {
  const cell = (v) => {
    if (v === null || v === undefined) return "";
    const s = typeof v === "object" ? JSON.stringify(v) : String(v);
    return /[",\n\r]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
  };
  return [cols.map(cell).join(","), ...rows.map((r) => cols.map((c) => cell(r[c])).join(","))].join("\n") + "\n";
}

function toMarkdown(cols, rows) {
  const cell = (v) =>
    v === null || v === undefined ? "" : (typeof v === "object" ? JSON.stringify(v) : String(v)).replace(/\|/g, "\\|").replace(/\r?\n/g, "<br>");
  return (
    ["| " + cols.map(cell).join(" | ") + " |", "|" + cols.map(() => " --- |").join(""), ...rows.map((r) => "| " + cols.map((c) => cell(r[c])).join(" | ") + " |")].join(
      "\n",
    ) + "\n"
  );
}

async function main() {
  const sql = sqlArg.trim();
  if (!sql) fail("give the SQL to run (sql)");
  const format = (formatArg || "json").trim().toLowerCase();
  if (!["json", "csv", "markdown", "md"].includes(format)) fail(`format ${format}: json, csv or markdown`);
  const maxRows = Math.max(1, Math.min(1000000, parseInt(maxRowsArg, 10) || 1000));
  const sources = parseJSON(sourcesArg, "sources") || {};
  if (typeof sources !== "object" || Array.isArray(sources)) fail('sources must be a JSON object: {"name": "path or URL"}');
  let conns = parseJSON(connsArg, "connections") || [];
  if (!Array.isArray(conns)) conns = [conns];

  let duckdb;
  try {
    duckdb = await import("@duckdb/node-api");
  } catch (e) {
    fail("the DuckDB client (@duckdb/node-api) is not installed: " + e.message);
  }
  const config = {};
  if (process.env.DUCKDB_EXTENSION_DIRECTORY) config.extension_directory = process.env.DUCKDB_EXTENSION_DIRECTORY;
  if (process.env.DUCKDB_EXTENSION_REPOSITORY) config.custom_extension_repository = process.env.DUCKDB_EXTENSION_REPOSITORY;
  if (process.env.DUCKDB_HTTP_PROXY) config.http_proxy = process.env.DUCKDB_HTTP_PROXY;
  const instance = await duckdb.DuckDBInstance.create(":memory:", config);
  const conn = await instance.connect();
  const run = (s) => conn.run(s);

  // extensions: the ones asked for, and those the sources and connections need
  const want = new Set();
  for (const e of String(extsArg || "").split(",").map((x) => x.trim().toLowerCase()).filter(Boolean)) want.add(e);
  const everything = sql + " " + JSON.stringify(sources);
  if (/s3:\/\/|s3a:\/\/|gcs:\/\/|gs:\/\//i.test(everything)) want.add("httpfs").add("aws");
  if (/https?:\/\//i.test(everything)) want.add("httpfs");
  if (/az:\/\/|abfss?:\/\//i.test(everything)) want.add("azure");
  if (/\.xlsx?\b/i.test(everything)) want.add("excel");
  for (const c of conns) {
    const t = CONN_TYPES[String(c.type || "").toLowerCase()];
    if (t && t !== "duckdb") want.add(t);
  }
  for (const raw of want) {
    const [name, repo] = raw.split("@");
    if (!/^[a-z0-9_]+$/.test(name)) fail(`extension name ${raw}`);
    const from = repo ? ` FROM ${repo === "community" ? "community" : q(repo)}` : COMMUNITY.has(name) ? " FROM community" : "";
    try {
      await run(`INSTALL ${name}${from}`);
      await run(`LOAD ${name}`);
    } catch (e) {
      fail(`the DuckDB extension ${name} could not be installed or loaded: ${e.message}`);
    }
  }
  if (want.has("aws")) {
    // S3 credentials from the usual AWS chain (environment, profiles, SSO)
    await run("CREATE SECRET IF NOT EXISTS sw_s3 (TYPE s3, PROVIDER credential_chain)").catch(() => {});
  }

  // connections: attached (read-only unless read_only is false)
  for (const c of conns) {
    const name = String(c.name || "").trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) fail(`connection name ${JSON.stringify(c.name)}: letters, digits and _`);
    const type = CONN_TYPES[String(c.type || "").toLowerCase()];
    if (!type) fail(`connection ${name}: type ${c.type}; use postgres, mysql, sqlite, sqlserver, oracle or duckdb`);
    const dsn = expandEnv(c.dsn || c.path || "");
    if (!dsn) fail(`connection ${name}: give its dsn (or path, for sqlite and duckdb)`);
    const opts = [type === "duckdb" ? null : `TYPE ${type}`, c.read_only === false ? null : "READ_ONLY"].filter(Boolean);
    try {
      await run(`ATTACH ${q(dsn)} AS ${ident(name)}${opts.length ? " (" + opts.join(", ") + ")" : ""}`);
    } catch (e) {
      fail(`could not connect ${name} (${type}): ${e.message.replace(dsn, "<dsn>")}`);
    }
  }

  // sources: one view per name
  for (const [name, loc] of Object.entries(sources)) {
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) fail(`source name ${name}: letters, digits and _`);
    await run(`CREATE VIEW ${ident(name)} AS SELECT * FROM ${scanOf(loc)}`).catch((e) => fail(`source ${name} (${loc}): ${e.message}`));
  }
  // the previous step's output: the table "input"
  if (inputArg && fs.existsSync(inputArg)) {
    const text = fs.readFileSync(inputArg, "utf8").trim();
    if (text) {
      const kind = /^[[{]/.test(text) ? `read_json_auto(${q(inputArg)})` : `read_csv_auto(${q(inputArg)})`;
      await run(`CREATE VIEW input AS SELECT * FROM ${kind}`).catch((e) => fail(`the input (${inputArg}): ${e.message}`));
    }
  }

  // the SQL: every statement in turn; the last one's rows are the result
  const stmts = await conn.extractStatements(sql).catch((e) => fail(e.message));
  let reader = null;
  for (let i = 0; i < stmts.count; i++) {
    const p = await stmts.prepare(i).catch((e) => fail(e.message));
    reader = await p.runAndReadAll().catch((e) => fail(e.message));
  }
  const cols = reader ? reader.columnNames() : [];
  let rows = reader ? reader.getRowObjectsJS().map(plain) : [];
  const total = rows.length;

  if (saveTo.trim()) {
    const dest = saveTo.trim();
    const ext = path.extname(dest).toLowerCase();
    fs.mkdirSync(path.dirname(path.resolve(dest)), { recursive: true });
    if (ext === ".parquet") {
      // the last statement again, written by DuckDB
      await run(`COPY (${sqlLast(sql)}) TO ${q(dest)} (FORMAT parquet)`).catch((e) => fail("save_to (parquet): " + e.message));
    } else if (ext === ".json") {
      fs.writeFileSync(dest, JSON.stringify(rows, null, 2) + "\n");
    } else {
      fs.writeFileSync(dest, toCSV(cols, rows));
    }
    process.stderr.write(`saved ${total} rows to ${dest}\n`);
  }

  if (rows.length > maxRows) {
    process.stderr.write(`${total} rows; the first ${maxRows} are shown (max_rows). Use save_to for all of them.\n`);
    rows = rows.slice(0, maxRows);
  }
  if (format === "csv") process.stdout.write(toCSV(cols, rows));
  else if (format === "markdown" || format === "md") process.stdout.write(toMarkdown(cols, rows));
  else process.stdout.write(JSON.stringify(rows, null, 2) + "\n");
}

// plain JSON values: whole numbers that fit as numbers (bigger ones as
// text), dates as ISO text, bytes as base64, lists and structs as they are
function plain(v) {
  if (typeof v === "bigint") return v >= BigInt(Number.MIN_SAFE_INTEGER) && v <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(v) : v.toString();
  if (v instanceof Date) return isNaN(v) ? null : v.toISOString();
  if (v instanceof Uint8Array) return Buffer.from(v).toString("base64");
  if (Array.isArray(v)) return v.map(plain);
  if (v && typeof v === "object") {
    if (typeof v.toString === "function" && v.constructor && v.constructor !== Object && !(Symbol.iterator in v)) {
      const t = v.toString(); // DuckDB values without a JS form (decimal, interval, uuid…)
      const n = Number(t);
      return t !== "" && isFinite(n) && String(n) === t ? n : t;
    }
    const o = {};
    for (const [k, x] of Object.entries(v)) o[k] = plain(x);
    return o;
  }
  return v;
}

// the last statement of the SQL (for writing Parquet)
function sqlLast(sql) {
  const parts = sql.split(/;\s*(?=(?:[^']*'[^']*')*[^']*$)/).map((s) => s.trim()).filter(Boolean);
  return parts[parts.length - 1];
}

main().catch((e) => fail(e && e.message ? e.message : String(e)));
