// duckdb_data_query: Kerberos logins for Oracle and Postgres. No KDC here:
// the connect strings and sqlnet.ora are checked as text, and the Postgres
// path (psql) runs end to end with stand-in kinit and psql scripts.
//   DUCKDB_TOOL_DIR=<folder with @duckdb/node-api> node --test library/tools/duckdb_data_query/test/
"use strict";
const test = require("node:test");
const assert = require("node:assert");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { execFile } = require("node:child_process");
const { promisify } = require("node:util");

const { oracleKerberosDsn, sqlnetOra, psqlDsn, psqlTables } = require("../index.js");

test("oracle: an external login (/@target), whatever the dsn holds", () => {
  assert.strictEqual(oracleKerberosDsn({ host: "ora1", service: "HRPDB" }, "hr"), "/@ora1:1521/HRPDB");
  assert.strictEqual(oracleKerberosDsn({ dsn: "jdbc:oracle:thin:@//ora2:1522/FIN" }, "fin"), "/@ora2:1522/FIN");
  assert.strictEqual(oracleKerberosDsn({ dsn: "scott/tiger@ora3:1521/XE" }, "x"), "/@ora3:1521/XE");
  assert.match(oracleKerberosDsn({ host: "ora4", sid: "ORCL" }, "y"), /^\/@\(DESCRIPTION=.*\(SID=ORCL\)/);
});

test("oracle: sqlnet.ora points at the ticket file and krb5.conf", () => {
  const ora = sqlnetOra({ krb5_conf: "/etc/krb5-corp.conf", kerberos_service: "orasvc" }, "hr", { KRB5CCNAME: "FILE:/tmp/cc_hr" });
  assert.match(ora, /SQLNET.AUTHENTICATION_SERVICES = \(BEQ, KERBEROS5PRE, KERBEROS5\)/);
  assert.match(ora, /SQLNET.KERBEROS5_CC_NAME = \/tmp\/cc_hr\n/);
  assert.match(ora, /SQLNET.KERBEROS5_CONF = \/etc\/krb5-corp.conf\n/);
  assert.match(ora, /SQLNET.AUTHENTICATION_KERBEROS5_SERVICE = orasvc\n/);
});

test("postgres: krbsrvname, no password, tables and queries", () => {
  assert.strictEqual(psqlDsn({ host: "pg1", database: "sales", user: "ann", password: "x" }, "s"), "host=pg1 dbname=sales user=ann krbsrvname=postgres");
  assert.strictEqual(psqlDsn({ dsn: "jdbc:postgresql://pg2:5433/crm?sslmode=require", kerberos_service: "pgsvc" }, "c"), "sslmode=require host=pg2 port=5433 dbname=crm krbsrvname=pgsvc");
  assert.strictEqual(psqlDsn({ dsn: "host=pg3 dbname=hr password=secret" }, "h").trim(), "host=pg3 dbname=hr krbsrvname=postgres");
  assert.deepStrictEqual(psqlTables({ tables: ["public.orders", "regions"] }, "s"), [
    { schema: "public", table: "orders", sql: "SELECT * FROM public.orders" },
    { schema: "main", table: "regions", sql: "SELECT * FROM regions" },
  ]);
  assert.deepStrictEqual(psqlTables({ tables: { recent: "SELECT id FROM orders WHERE day > now() - interval '7 days';" } }, "s"), [
    { schema: "main", table: "recent", sql: "SELECT id FROM orders WHERE day > now() - interval '7 days'" },
  ]);
});

const toolDir = process.env.DUCKDB_TOOL_DIR;
const skip = !toolDir || !fs.existsSync(path.join(toolDir, "node_modules", "@duckdb", "node-api")) ? "set DUCKDB_TOOL_DIR to a folder with @duckdb/node-api installed" : false;

test("postgres through psql: a keytab ticket, two tables copied in and joined", { skip }, async (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sw-krb-test-"));
  const log = path.join(dir, "log.jsonl");
  // stand-ins: kinit writes the cache; psql answers the \copy with CSV and logs what it got
  fs.writeFileSync(path.join(dir, "kinit"), `#!/usr/bin/env node\nconst a=process.argv.slice(2);require("fs").appendFileSync(${JSON.stringify(log)},JSON.stringify({kinit:a})+"\\n");require("fs").writeFileSync(a[a.indexOf("-c")+1].replace(/^FILE:/,""),"ticket");\n`, { mode: 0o755 });
  fs.writeFileSync(
    path.join(dir, "psql"),
    `#!/usr/bin/env node
const fs=require("fs");const a=process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(log)},JSON.stringify({psql:a,cc:process.env.KRB5CCNAME,pw:process.env.PGPASSWORD||null})+"\\n");
const c=a[a.indexOf("-c")+1];const m=c.match(/^\\\\copy \\((.*)\\) TO '(.*)' WITH/);
const data=/orders/.test(m[1])?"id,region,total\\n1,EU,120.5\\n2,US,80\\n3,EU,200\\n":"region,manager\\nEU,Ana\\nUS,Bob\\n";
fs.writeFileSync(m[2],data);
`,
    { mode: 0o755 },
  );
  const script = path.join(toolDir, "index.sw-krb-test.js");
  fs.copyFileSync(path.join(__dirname, "..", "index.js"), script);
  t.after(() => {
    fs.rmSync(script, { force: true });
    fs.rmSync(dir, { recursive: true, force: true });
  });
  const args = {
    sql: "SELECT o.region, sum(o.total) AS revenue, r.manager FROM sales.public.orders o JOIN sales.regions r USING (region) GROUP BY ALL ORDER BY o.region",
    connections: [{ name: "sales", type: "postgres", auth: "kerberos", host: "pg1.corp", database: "sales", keytab: "/etc/svc.keytab", principal: "svc@CORP", psql: path.join(dir, "psql"), tables: ["public.orders", "regions"] }],
  };
  const { stdout } = await promisify(execFile)("node", [script], { env: { ...process.env, PGPASSWORD: "never", KINIT: path.join(dir, "kinit"), SPECWRIGHT_ARGS: JSON.stringify(args) } });
  assert.deepStrictEqual(JSON.parse(stdout), [
    { region: "EU", revenue: 320.5, manager: "Ana" },
    { region: "US", revenue: 80, manager: "Bob" },
  ]);
  const calls = fs.readFileSync(log, "utf8").trim().split("\n").map(JSON.parse);
  assert.deepStrictEqual(calls[0].kinit.slice(0, 3), ["-k", "-t", "/etc/svc.keytab"]);
  assert.strictEqual(calls[0].kinit[5], "svc@CORP");
  const psqls = calls.filter((x) => x.psql);
  assert.strictEqual(psqls.length, 2);
  for (const p of psqls) {
    assert.strictEqual(p.psql[p.psql.indexOf("-d") + 1], "host=pg1.corp dbname=sales krbsrvname=postgres");
    assert.ok(p.psql.includes("-w"), "never prompts for a password");
    assert.match(p.cc, /^FILE:.*cc_sales$/);
    assert.strictEqual(p.pw, null, "no password is passed");
  }
});
