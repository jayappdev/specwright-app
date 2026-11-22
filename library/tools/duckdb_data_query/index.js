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
//   sources      JSON object: table name -> file, glob, folder, URL or
//                s3:// path, e.g. {"orders": "data/orders.csv",
//                "events": "s3://bkt/events/"} (a folder: every data file
//                under it, in sub-folders too, read as one table with the
//                columns of all of them and a "filename" column; hive
//                folders such as year=2026/ become columns). A source can
//                also be {"path": ..., "format": "parquet|csv|json",
//                "filename": false, "hive": false}.
//   connections  JSON array, one entry per database (as many as needed, of
//                any types): {"name": "sales", "type": "postgres",
//                "dsn": "host=db1 dbname=sales user=me password=${DB_SALES}"}
//                or the parts: {"name": "hr", "type": "oracle", "host": "ora1",
//                "port": 1521, "service": "HRPDB", "user": "rep",
//                "password": "${DB_HR}"}. dsn may also be a URL or a JDBC
//                string (jdbc:postgresql://…, jdbc:oracle:thin:@//…); user and
//                password are added to it. "schema" limits a Postgres
//                connection to one schema; "read_only": false allows writes.
//                ${NAME} is filled from the environment: a DB_* secret
//                (SpecWright asks for DB_SALES the first time) or any variable
//                set; values are quoted for the connect string.
//   extensions   comma-separated, e.g. "httpfs, aws, oracle@community"
//   input        a file with the previous step's output (a flow passes
//                {{input_file}}): the table "input"
//   format       json (default) | csv | markdown
//   save_to      also write the result to this file (.csv, .json, .jsonl,
//                .parquet), local or s3://
//   max_rows     at most this many rows in the answer (default 1000)
//   save_mode    overwrite (default) | append: add the rows to what save_to
//                already holds (accumulate results across runs)
//
// Credentials: AWS keys from AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY
// (/ AWS_SESSION_TOKEN), else its usual chain (~/.aws profiles, SSO);
// AWS_REGION; AWS_ENDPOINT_URL for S3-compatible storage (MinIO, R2, Ceph);
// Postgres PGPASSWORD, MySQL MYSQL_PWD; ${NAME} placeholders in a dsn.
// Settings from the environment: DUCKDB_EXTENSION_DIRECTORY,
// DUCKDB_EXTENSION_REPOSITORY (a mirror, for networks that block
// extensions.duckdb.org), DUCKDB_HTTP_PROXY.
"use strict";
const fs = require("node:fs");
const path = require("node:path");

const ARGS = ["sql", "sources", "connections", "extensions", "input", "format", "save_to", "max_rows", "save_mode"];
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
const [sqlArg, sourcesArg, connsArg, extsArg, inputArg, formatArg, saveTo, maxRowsArg, saveModeArg] = readArgs();

function fail(msg) {
  process.stderr.write("duckdb_data_query: " + hide(msg) + "\n");
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

// --- connect strings ---------------------------------------------------------

// the secret values put into connect strings: kept out of error messages
const hidden = [];
function envValue(n, what) {
  const v = process.env[n];
  if (v === undefined || v === "") fail(`${n} (used in ${what}) has no value: set it as a secret (DB_* names are asked for when a run needs them) or in the environment`);
  hidden.push(v);
  return v;
}
const hide = (msg) => hidden.reduce((m, v) => (v.length >= 3 ? m.split(v).join("***") : m), String(msg));

// a value written into a connect string of a style, quoted as it needs
const quoteFor = {
  // libpq / MySQL key=value: 'it''s' style quoting when needed
  kv: (v) => (v === "" || /[\s'\\=]/.test(v) ? "'" + v.replace(/\\/g, "\\\\").replace(/'/g, "\\'") + "'" : v),
  url: (v) => encodeURIComponent(v),
  // Oracle Easy Connect: "quoted" when it has more than letters and digits
  oracle: (v) => (/^[A-Za-z0-9_$#]*$/.test(v) ? v : '"' + v.replace(/"/g, "") + '"'),
  // SQL Server (ADO): {braced} when it has ; = { } or spaces
  ado: (v) => (/[;={}\s]/.test(v) ? "{" + v.replace(/}/g, "}}") + "}" : v),
  raw: (v) => v,
};

// ${NAME} in a connect string, each value quoted for the string's style;
// a placeholder already inside quotes is only escaped
function expandIn(str, style, what) {
  return String(str).replace(/(['"]?)\$\{([A-Za-z_][A-Za-z0-9_]*)\}(['"]?)/g, (m, q1, n, q2) => {
    const v = envValue(n, what);
    if (q1 && q1 === q2) return q1 + (q1 === "'" ? v.replace(/\\/g, "\\\\").replace(/'/g, "\\'") : v.replace(/"/g, "")) + q2;
    return q1 + quoteFor[style](v) + q2;
  });
}

// JDBC strings as DuckDB's drivers take them
function fromJDBC(d, type) {
  let m;
  if (!/^jdbc:/i.test(d)) return d;
  if ((m = d.match(/^jdbc:postgresql:\/\/(.*)$/i))) return "postgresql://" + m[1];
  if ((m = d.match(/^jdbc:oracle:thin:(?:([^@]*)@)?(.*)$/i))) {
    const cred = m[1] ? m[1] + "@" : "";
    let target = m[2].replace(/^\/\//, "");
    const sid = target.match(/^([^:/]+):(\d+):([^/:]+)$/); // host:port:SID
    if (sid) target = `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=${sid[1]})(PORT=${sid[2]}))(CONNECT_DATA=(SID=${sid[3]})))`;
    return cred + target;
  }
  if ((m = d.match(/^jdbc:mysql:\/\/([^/:?]+)(?::(\d+))?(?:\/([^?]*))?(?:\?(.*))?$/i))) {
    const q = new URLSearchParams(m[4] || "");
    return [`host=${m[1]}`, m[2] && `port=${m[2]}`, m[3] && `database=${m[3]}`, q.get("user") && `user=${q.get("user")}`, q.get("password") && `password=${q.get("password")}`].filter(Boolean).join(" ");
  }
  if ((m = d.match(/^jdbc:sqlserver:\/\/([^;:]+)(?::(\d+))?;?(.*)$/i))) {
    const kv = Object.fromEntries(m[3].split(";").filter(Boolean).map((p) => p.split("=")).map(([k, v]) => [k.toLowerCase(), v]));
    return [`Server=${m[1]}${m[2] ? "," + m[2] : ""}`, (kv.databasename || kv.database) && `Database=${kv.databasename || kv.database}`, kv.user && `User Id=${kv.user}`, kv.password && `Password=${kv.password}`].filter(Boolean).join(";");
  }
  fail(`the ${type} connection's JDBC string is not one I can convert: give it as a dsn or as host, port, database, user and password`);
}

// the connect string of a connection: its dsn (or URL, or JDBC string) with
// user and password added, or built from its parts
function dsnOf(c, type, name) {
  const what = `the connection ${name}`;
  const part = (k, style) => (c[k] === undefined || c[k] === null || c[k] === "" ? "" : expandIn(String(c[k]), style, what));
  const raw = String(c.dsn || c.url || c.connect_string || c.path || "").trim();
  const db = c.database || c.dbname || c.db;
  if (type === "sqlite" || type === "duckdb") return expandIn(raw || db || "", "raw", what);
  if (type === "oracle") {
    const user = part("user", "oracle"), pw = part("password", "oracle");
    let target = raw ? expandIn(fromJDBC(raw, type), "oracle", what) : `${c.host || "localhost"}:${c.port || 1521}/${c.service || c.service_name || db || ""}`;
    if (!raw && c.sid) target = `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=${c.host || "localhost"})(PORT=${c.port || 1521}))(CONNECT_DATA=(SID=${c.sid})))`;
    if (user && !/^[^()]*@/.test(target)) target = `${user}${pw ? "/" + pw : ""}@${target}`;
    return target;
  }
  if (type === "mssql") {
    const parts = raw ? [expandIn(fromJDBC(raw, type), "ado", what)] : [`Server=${c.host || "localhost"}${c.port ? "," + c.port : ""}`, db && `Database=${db}`];
    if (c.user) parts.push(`User Id=${part("user", "ado")}`);
    if (c.password) parts.push(`Password=${part("password", "ado")}`);
    return parts.filter(Boolean).join(";");
  }
  // postgres and mysql: key=value, or a URL
  const conv = raw ? fromJDBC(raw, type) : "";
  if (/^[a-z]+:\/\//i.test(conv)) {
    const u = new URL(expandIn(conv, "url", what));
    if (c.user) u.username = encodeURIComponent(part("user", "raw"));
    if (c.password) u.password = encodeURIComponent(part("password", "raw"));
    return u.toString();
  }
  const pairs = conv ? [expandIn(conv, "kv", what)] : [];
  const add = (k, v) => v !== undefined && v !== null && v !== "" && pairs.push(`${k}=${quoteFor.kv(String(v))}`);
  if (!conv) {
    add("host", c.host);
    add("port", c.port);
    add(type === "mysql" ? "database" : "dbname", db);
  }
  if (c.user) pairs.push("user=" + part("user", "kv"));
  if (c.password) pairs.push("password=" + part("password", "kv"));
  return pairs.join(" ");
}

// the data format of a file name
const FORMATS = { parquet: "parquet", csv: "csv", tsv: "csv", txt: "csv", json: "json", jsonl: "json", ndjson: "json", xlsx: "xlsx", xls: "xlsx" };
const formatOf = (f) => FORMATS[(String(f).toLowerCase().match(/\.([a-z0-9]+)(?:\.(?:gz|zst))?$/) || [])[1]];
const isGlob = (l) => /[*?[]/.test(l);
const isFolder = (l) => /\/$/.test(l);

// a reader for one or more files of one format: a list of files (or one
// glob) read as one table, columns matched by name, with the file each row
// came from and hive partition columns (year=2026/) when asked
function reader(fmt, files, many, o) {
  const list = Array.isArray(files) ? "[" + files.map(q).join(", ") + "]" : q(files);
  if (fmt === "xlsx") {
    if (Array.isArray(files) && files.length > 1) fail("Excel files are read one at a time; name each file as its own source");
    return `read_xlsx(${Array.isArray(files) ? q(files[0]) : list})`;
  }
  const opts = many ? [`union_by_name = true`, `filename = ${o.filename === false ? "false" : "true"}`, `hive_partitioning = ${o.hive === false ? "false" : "true"}`] : [];
  const fn = fmt === "parquet" ? "read_parquet" : fmt === "json" ? "read_json_auto" : "read_csv_auto";
  return `${fn}(${[list, ...opts].join(", ")})`;
}

// the SQL that reads a source: a file, URL, glob, or folder (every data file
// under it, in all formats found, as one table)
async function scanOf(run, all, src) {
  const o = typeof src === "object" && src !== null ? src : { path: src };
  const l = String(o.path || "").trim();
  if (!l) fail("a source has no path");
  const fmt = o.format ? String(o.format).toLowerCase() : "";
  if (fmt && !["parquet", "csv", "json", "xlsx"].includes(fmt)) fail(`format ${fmt}: parquet, csv, json or xlsx`);
  if (isFolder(l)) {
    // list the folder (S3 and Azure list by prefix), skip markers and hidden files
    const rows = await all(`SELECT file FROM glob(${q(l + "**")})`).catch((e) => fail(`could not list ${l}: ${e.message}`));
    const groups = {};
    for (const { file } of rows) {
      const base = String(file).split("/").pop();
      if (!base || base.startsWith(".") || base.startsWith("_")) continue;
      const f = fmt || formatOf(base);
      if (f && (!fmt || formatOf(base) === fmt || !formatOf(base))) (groups[f] ||= []).push(file);
    }
    const kinds = Object.keys(groups);
    if (kinds.length === 0) fail(`no data files (parquet, csv, json, xlsx) under ${l}`);
    return kinds.map((f) => `SELECT * FROM ${reader(f, groups[f], true, o)}`).join(" UNION ALL BY NAME ");
  }
  const f = fmt || formatOf(l);
  if (!f) return `SELECT * FROM ${q(l)}`; // DuckDB guesses
  return `SELECT * FROM ${reader(f, l, isGlob(l), o)}`;
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
  const all = async (s) => (await conn.runAndReadAll(s)).getRowObjectsJS().map(plain);

  // extensions: the ones asked for, and those the sources and connections need
  const want = new Set();
  for (const e of String(extsArg || "").split(",").map((x) => x.trim().toLowerCase()).filter(Boolean)) want.add(e);
  const everything = sql + " " + JSON.stringify(sources) + " " + saveTo;
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
  if (want.has("aws")) await s3Secret(run);

  // connections: attached (read-only unless read_only is false), each with
  // its own connect string and credentials
  const names = new Set(Object.keys(sources));
  for (const c of conns) {
    const name = String((c && c.name) || "").trim();
    if (names.has(name)) fail(`two connections or sources are called ${name}: give each its own name`);
    names.add(name);
  }
  for (const c of conns) {
    const name = String(c.name || "").trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) fail(`connection name ${JSON.stringify(c.name)}: letters, digits and _`);
    const type = CONN_TYPES[String(c.type || "").toLowerCase()];
    if (!type) fail(`connection ${name}: type ${c.type}; use postgres, mysql, sqlite, sqlserver, oracle or duckdb`);
    const dsn = dsnOf(c, type, name);
    if (!dsn) fail(`connection ${name}: give its dsn (or url, or host and database; path for sqlite and duckdb)`);
    const opts = [type === "duckdb" ? null : `TYPE ${type}`, c.read_only === false ? null : "READ_ONLY", type === "postgres" && c.schema ? `SCHEMA ${q(c.schema)}` : null].filter(Boolean);
    try {
      await run(`ATTACH ${q(dsn)} AS ${ident(name)}${opts.length ? " (" + opts.join(", ") + ")" : ""}`);
    } catch (e) {
      fail(hide(`could not connect ${name} (${type}): ${e.message.split(dsn).join("<dsn>")}`));
    }
  }

  // sources: one view per name
  for (const [name, loc] of Object.entries(sources)) {
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) fail(`source name ${name}: letters, digits and _`);
    const shown = typeof loc === "object" && loc ? loc.path : loc;
    await run(`CREATE VIEW ${ident(name)} AS ${await scanOf(run, all, loc)}`).catch((e) => fail(`source ${name} (${shown}): ${e.message}`));
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
  // (kept as a table when it is saved, so it runs once)
  const dest = saveTo.trim();
  const mode = (saveModeArg || "overwrite").trim().toLowerCase();
  if (!["overwrite", "append"].includes(mode)) fail(`save_mode ${mode}: overwrite or append`);
  const stmts = await conn.extractStatements(sql).catch((e) => fail(e.message));
  let res = null,
    kept = false;
  for (let i = 0; i < stmts.count; i++) {
    if (dest && i === stmts.count - 1) {
      kept = await run(`CREATE TEMP TABLE __sw_result AS ${sqlLast(sql)}`).then(
        () => true,
        () => false,
      );
      if (kept) {
        res = await conn.runAndReadAll("SELECT * FROM __sw_result");
        break;
      }
    }
    const p = await stmts.prepare(i).catch((e) => fail(e.message));
    res = await p.runAndReadAll().catch((e) => fail(e.message));
  }
  const cols = res ? res.columnNames() : [];
  let rows = res ? res.getRowObjectsJS().map(plain) : [];
  // DATE columns as 2026-11-22 (not a timestamp at midnight)
  const dates = res ? cols.filter((c, i) => String(res.columnType(i)) === "DATE") : [];
  if (dates.length) for (const r of rows) for (const c of dates) if (typeof r[c] === "string") r[c] = r[c].slice(0, 10);
  const total = rows.length;

  if (dest) {
    if (!kept) fail("save_to needs the last statement to be a query (SELECT …)");
    await save(run, all, dest, mode, total);
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

// S3 access: keys from the environment (secrets) or the AWS credential
// chain (profiles, SSO); AWS_REGION; AWS_ENDPOINT_URL for S3-compatible
// storage (MinIO, Cloudflare R2, Ceph), addressed by path
async function s3Secret(run) {
  const env = process.env;
  const opts = ["TYPE s3"];
  if (env.AWS_ACCESS_KEY_ID && env.AWS_SECRET_ACCESS_KEY) {
    opts.push(`KEY_ID ${q(env.AWS_ACCESS_KEY_ID)}`, `SECRET ${q(env.AWS_SECRET_ACCESS_KEY)}`);
    if (env.AWS_SESSION_TOKEN) opts.push(`SESSION_TOKEN ${q(env.AWS_SESSION_TOKEN)}`);
  } else {
    opts.push("PROVIDER credential_chain");
  }
  const region = env.AWS_REGION || env.AWS_DEFAULT_REGION;
  if (region) opts.push(`REGION ${q(region)}`);
  const ep = (env.AWS_ENDPOINT_URL_S3 || env.AWS_ENDPOINT_URL || "").trim();
  if (ep) {
    const u = ep.match(/^(https?):\/\/([^/]+)/i);
    if (!u) fail(`AWS_ENDPOINT_URL ${ep}: give it as http(s)://host[:port]`);
    opts.push(`ENDPOINT ${q(u[2])}`, `URL_STYLE ${q(env.AWS_S3_URL_STYLE || "path")}`, `USE_SSL ${u[1].toLowerCase() === "https"}`);
  }
  try {
    await run(`CREATE OR REPLACE SECRET sw_s3 (${opts.join(", ")})`);
  } catch (e) {
    // no credentials found: public buckets still work
    if (opts.includes("PROVIDER credential_chain")) return;
    fail("S3 credentials: " + e.message);
  }
}

// write the result (the table __sw_result) to dest; append adds it to the
// rows dest already holds (matched by column name)
async function save(run, all, dest, mode, n) {
  const fmt = formatOf(dest);
  if (!fmt || fmt === "xlsx") fail(`save_to ${dest}: end it with .csv, .json, .jsonl or .parquet`);
  const remote = /^[a-z0-9]+:\/\//i.test(dest);
  if (!remote) fs.mkdirSync(path.dirname(path.resolve(dest)), { recursive: true });
  let before = 0;
  let table = "__sw_result";
  if (mode === "append") {
    // (glob lists the prefix; a name without a wildcard would come back as is)
    const exists = remote
      ? await all(`SELECT file FROM glob(${q(dest + "*")})`).then((r) => r.some((x) => x.file === dest), () => false)
      : fs.existsSync(dest);
    if (exists) {
      await run(`CREATE TEMP TABLE __sw_before AS SELECT * FROM ${reader(fmt, dest, false, {})}`).catch((e) => fail(`could not read ${dest} to add to it: ${e.message}`));
      before = Number((await all("SELECT count(*) AS n FROM __sw_before"))[0].n);
      await run("CREATE TEMP TABLE __sw_all AS SELECT * FROM __sw_before UNION ALL BY NAME SELECT * FROM __sw_result").catch((e) => fail(`the rows do not fit ${dest}: ${e.message}`));
      table = "__sw_all";
    }
  }
  const as = fmt === "parquet" ? "FORMAT parquet" : fmt === "csv" ? "FORMAT csv, HEADER true" : /\.json$/i.test(dest) ? "FORMAT json, ARRAY true" : "FORMAT json";
  await run(`COPY ${table} TO ${q(dest)} (${as})`).catch((e) => fail(`save_to ${dest}: ${e.message}`));
  process.stderr.write(mode === "append" ? `added ${n} rows to ${dest} (${before + n} in all)\n` : `saved ${n} rows to ${dest}\n`);
}

// the last statement of the SQL (kept as a table when the result is saved)
function sqlLast(sql) {
  const parts = sql.split(/;\s*(?=(?:[^']*'[^']*')*[^']*$)/).map((s) => s.trim()).filter(Boolean);
  return parts[parts.length - 1];
}

if (require.main === module) main().catch((e) => fail(hide(e && e.message ? e.message : String(e))));
else module.exports = { dsnOf, fromJDBC }; // for the tests
