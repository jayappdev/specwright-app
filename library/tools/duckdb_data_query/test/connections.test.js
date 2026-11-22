// duckdb_data_query: connect strings for several databases, each with its
// own credentials (no database needed).
//   node --test library/tools/duckdb_data_query/test/
"use strict";
const test = require("node:test");
const assert = require("node:assert");

process.env.DB_A = "p w'd\\x";
process.env.DB_O = "p@ss/1";
process.env.DB_S = "a;b";
const { dsnOf } = require("../index.js");

test("postgres: parts, key=value, URL and JDBC, passwords quoted", () => {
  assert.strictEqual(dsnOf({ host: "db1", port: 5432, database: "sales", user: "rep", password: "${DB_A}" }, "postgres", "a"), "host=db1 port=5432 dbname=sales user=rep password='p w\\'d\\\\x'");
  assert.strictEqual(dsnOf({ dsn: "host=db2 dbname=crm password=${DB_A}" }, "postgres", "b"), "host=db2 dbname=crm password='p w\\'d\\\\x'");
  assert.strictEqual(dsnOf({ dsn: "host=db2 password='${DB_A}'" }, "postgres", "b"), "host=db2 password='p w\\'d\\\\x'");
  assert.strictEqual(dsnOf({ dsn: "jdbc:postgresql://db3:5433/hr?sslmode=require", user: "u", password: "${DB_A}" }, "postgres", "c"), "postgresql://u:p%20w'd%5Cx@db3:5433/hr?sslmode=require");
});

test("oracle: service, JDBC service and SID, passwords quoted", () => {
  assert.strictEqual(dsnOf({ host: "ora1", service: "HRPDB", user: "rep", password: "${DB_O}" }, "oracle", "d"), 'rep/"p@ss/1"@ora1:1521/HRPDB');
  assert.strictEqual(dsnOf({ dsn: "jdbc:oracle:thin:@//ora2:1522/FIN", user: "fin", password: "${DB_O}" }, "oracle", "e"), 'fin/"p@ss/1"@ora2:1522/FIN');
  assert.strictEqual(dsnOf({ dsn: "jdbc:oracle:thin:@ora3:1521:ORCL", user: "x", password: "plain" }, "oracle", "f"), "x/plain@(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=ora3)(PORT=1521))(CONNECT_DATA=(SID=ORCL)))");
  assert.strictEqual(dsnOf({ dsn: "scott/tiger@ora4:1521/XE", user: "ignored" }, "oracle", "g"), "scott/tiger@ora4:1521/XE");
});

test("sql server and mysql from JDBC", () => {
  assert.strictEqual(dsnOf({ dsn: "jdbc:sqlserver://sql1:1433;databaseName=ops", user: "sa", password: "${DB_S}" }, "mssql", "h"), "Server=sql1,1433;Database=ops;User Id=sa;Password={a;b}");
  assert.strictEqual(dsnOf({ dsn: "jdbc:mysql://my1:3306/shop", user: "me", password: "plain" }, "mysql", "i"), "host=my1 port=3306 database=shop user=me password=plain");
  assert.strictEqual(dsnOf({ path: "data/app.db" }, "sqlite", "j"), "data/app.db");
});
