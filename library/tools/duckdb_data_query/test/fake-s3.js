// A small S3-compatible server for testing duckdb_data_query (path-style): ListObjectsV2,
// HEAD, GET (ranges), PUT, multipart upload, DELETE. Requests must be signed
// with the access key swtest (the signature itself is not checked).
"use strict";
const http = require("node:http");
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const uploads = {};
const etag = (b) => '"' + crypto.createHash("md5").update(b).digest("hex") + '"';
const esc = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
function walk(dir, base = "") {
  let out = [];
  if (!fs.existsSync(dir)) return out;
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const rel = base ? base + "/" + e.name : e.name;
    if (e.isDirectory()) out = out.concat(walk(path.join(dir, e.name), rel));
    else out.push(rel);
  }
  return out;
}
// start serves ROOT (one folder per bucket) on a free port; resolves to the
// server, with .endpoint set to http://127.0.0.1:<port>
function start(ROOT) {
  const log = [];
  const srv = http.createServer((req, res) => {
    const u = new URL(req.url, "http://x");
    const parts = decodeURIComponent(u.pathname).replace(/^\/+/, "").split("/");
    const bucket = parts.shift();
    const key = parts.join("/");
    log.push(`${req.method} /${bucket}/${key}${u.search}`);
    if (u.pathname === "/__log") return res.end(log.join("\n"));
    if (!/Credential=swtest\//.test(req.headers.authorization || "")) {
      res.writeHead(403, { "Content-Type": "application/xml" });
      return res.end("<Error><Code>AccessDenied</Code><Message>not signed with swtest</Message></Error>");
    }
    const file = path.join(ROOT, bucket, key);
    const body = [];
    req.on("data", (c) => body.push(c));
    req.on("end", () => {
      const data = Buffer.concat(body);
      const q = u.searchParams;
      if (req.method === "GET" && !key) {
        const prefix = q.get("prefix") || "";
        const keys = walk(path.join(ROOT, bucket)).filter((k) => k.startsWith(prefix)).sort();
        const items = keys
          .map((k) => {
            const st = fs.statSync(path.join(ROOT, bucket, k));
            return `<Contents><Key>${esc(k)}</Key><LastModified>${st.mtime.toISOString()}</LastModified><ETag>"x"</ETag><Size>${st.size}</Size><StorageClass>STANDARD</StorageClass></Contents>`;
          })
          .join("");
        res.writeHead(200, { "Content-Type": "application/xml" });
        return res.end(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>${bucket}</Name><Prefix>${esc(prefix)}</Prefix><KeyCount>${keys.length}</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>${items}</ListBucketResult>`);
      }
      if (req.method === "POST" && q.has("uploads")) {
        const id = crypto.randomUUID();
        uploads[id] = {};
        res.writeHead(200, { "Content-Type": "application/xml" });
        return res.end(`<?xml version="1.0" encoding="UTF-8"?><InitiateMultipartUploadResult><Bucket>${bucket}</Bucket><Key>${esc(key)}</Key><UploadId>${id}</UploadId></InitiateMultipartUploadResult>`);
      }
      if (req.method === "PUT" && q.has("uploadId")) {
        uploads[q.get("uploadId")][Number(q.get("partNumber"))] = data;
        res.writeHead(200, { ETag: etag(data) });
        return res.end();
      }
      if (req.method === "POST" && q.has("uploadId")) {
        const p = uploads[q.get("uploadId")];
        const all = Buffer.concat(Object.keys(p).sort((a, b) => a - b).map((n) => p[n]));
        fs.mkdirSync(path.dirname(file), { recursive: true });
        fs.writeFileSync(file, all);
        delete uploads[q.get("uploadId")];
        res.writeHead(200, { "Content-Type": "application/xml" });
        return res.end(`<?xml version="1.0" encoding="UTF-8"?><CompleteMultipartUploadResult><Bucket>${bucket}</Bucket><Key>${esc(key)}</Key><ETag>${etag(all)}</ETag></CompleteMultipartUploadResult>`);
      }
      if (req.method === "PUT") {
        fs.mkdirSync(path.dirname(file), { recursive: true });
        fs.writeFileSync(file, data);
        res.writeHead(200, { ETag: etag(data) });
        return res.end();
      }
      if (req.method === "DELETE") {
        fs.rmSync(file, { force: true });
        res.writeHead(204);
        return res.end();
      }
      if ((req.method === "GET" || req.method === "HEAD") && fs.existsSync(file) && fs.statSync(file).isFile()) {
        const buf = fs.readFileSync(file);
        const st = fs.statSync(file);
        const h = { "Last-Modified": st.mtime.toUTCString(), ETag: etag(buf), "Accept-Ranges": "bytes", "Content-Type": "application/octet-stream" };
        const r = (req.headers.range || "").match(/bytes=(\d+)-(\d*)/);
        if (r && req.method === "GET") {
          const a = Number(r[1]);
          const b = r[2] ? Math.min(Number(r[2]), buf.length - 1) : buf.length - 1;
          res.writeHead(206, { ...h, "Content-Length": b - a + 1, "Content-Range": `bytes ${a}-${b}/${buf.length}` });
          return res.end(buf.subarray(a, b + 1));
        }
        res.writeHead(200, { ...h, "Content-Length": buf.length });
        return res.end(req.method === "HEAD" ? undefined : buf);
      }
      res.writeHead(404, { "Content-Type": "application/xml" });
      res.end(req.method === "HEAD" ? undefined : "<Error><Code>NoSuchKey</Code><Message>no such key</Message></Error>");
    });
  });
  return new Promise((resolve) =>
    srv.listen(0, "127.0.0.1", () => {
      srv.endpoint = "http://127.0.0.1:" + srv.address().port;
      srv.log = log;
      resolve(srv);
    }),
  );
}

module.exports = { start };
