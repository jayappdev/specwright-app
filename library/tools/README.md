# Library tools

Tools meant for the managed catalog (admin-signed, every project gets them).
Each is a tool file (`*.tools.json`, the format of Tools › Export) plus its
readable source; a test keeps the two in step (internal/core/librarytools_test.go).

To publish one: Admin › unlock admin mode › **Import a tool file to publish…**,
choose the `.tools.json`, review and test it, then **Publish to all projects**.

## duckdb_data_query

Query data with DuckDB 2.0 SQL (the official Node client, `@duckdb/node-api`
2.0.0-r.1, installed with the tool on first use; Node.js is needed, as for
Copilot CLI). Sources: CSV, JSON, Parquet and Excel files and globs, URLs,
S3 and Azure paths, the previous flow step's output (`input`), and databases
attached read-only: Postgres, MySQL, SQLite, SQL Server, Oracle, DuckDB.

- Extensions install on first use from extensions.duckdb.org (community ones,
  such as oracle and mssql, from community-extensions.duckdb.org). Where that
  is blocked: `DUCKDB_EXTENSION_REPOSITORY` (a mirror), `DUCKDB_HTTP_PROXY`,
  `DUCKDB_EXTENSION_DIRECTORY`.
- Credentials: AWS from its usual chain (profiles, SSO, environment); the
  optional secrets PGPASSWORD, MYSQL_PWD, AWS_*, AZURE_STORAGE_CONNECTION_STRING,
  ORACLE_PASSWORD, MSSQL_PASSWORD (Tools › Secrets, the keychain or the
  environment), and `${NAME}` placeholders in a connection's dsn.
- Oracle needs Oracle Instant Client on the machine (19c, 21c or 23ai).
- With DuckDB 2.0.0, the iceberg extension does not load (upstream), and the
  odbc and gsheets community extensions are not published yet.
