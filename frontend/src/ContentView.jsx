// Shows tool output (or any text result) the same way everywhere: a
// Preview / Raw data toggle (HTML rendered, Markdown formatted, JSON and
// CSV as tables), links to the saved input and output files, and "Open in
// preview" for the side panel.
import { useState } from "react";
import { S, update } from "./store.js";
import { Icon } from "./ui.jsx";
import { mdRender } from "./markdown.js";
import { highlight } from "./highlight.js";
import { CopyButton } from "./CopyButton.jsx";
import { FileLink, Linkify } from "./FileLinks.jsx";

const EXT = { html: "html", json: "json", csv: "csv", md: "md", txt: "txt" };

// What kind of content a text is: html | json | csv | md | txt.
export function contentFormat(text, name = "") {
  const ext = (name.match(/\.([a-z0-9]+)$/i) || [])[1]?.toLowerCase();
  if (ext === "htm" || ext === "html") return "html";
  if (ext === "json") return "json";
  if (ext === "csv") return "csv";
  if (ext === "md" || ext === "markdown") return "md";
  const t = String(text || "").trim();
  if (!t) return "txt";
  if (/^\s*(<!doctype|<html|<(div|p|h[1-6]|table|ul|ol|section|article|body|svg)\b)/i.test(t)) return "html";
  if (/^[[{]/.test(t)) {
    try {
      JSON.parse(t);
      return "json";
    } catch {
      /* not json */
    }
  }
  const lines = t.split("\n"),
    n = (lines[0].match(/,/g) || []).length;
  if (
    lines.length > 1 &&
    n > 0 &&
    lines.slice(1, 20).every((l) => !l.trim() || (l.match(/,/g) || []).length === n)
  )
    return "csv";
  if (/^(#{1,6}\s|\s*[-*+]\s+\S|\s*\d+\.\s+\S|\|.+\|\s*$|```)/m.test(t)) return "md";
  return "txt";
}

export const canPreview = (fmt) => ["html", "md", "json", "csv"].includes(fmt);

function parseCSV(t) {
  const rows = [];
  let row = [],
    cell = "",
    q = false;
  for (let i = 0; i < t.length; i++) {
    const c = t[i];
    if (q) {
      if (c === '"' && t[i + 1] === '"') {
        cell += '"';
        i++;
      } else if (c === '"') q = false;
      else cell += c;
    } else if (c === '"') q = true;
    else if (c === ",") {
      row.push(cell);
      cell = "";
    } else if (c === "\n") {
      row.push(cell);
      rows.push(row);
      row = [];
      cell = "";
    } else if (c !== "\r") cell += c;
  }
  if (cell || row.length) {
    row.push(cell);
    rows.push(row);
  }
  return rows;
}

function Table({ head, rows, more }) {
  return (
    <div className="cv-table">
      <table className="table">
        <thead>
          <tr>
            {head.map((h, k) => (
              <th key={k}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, k) => (
            <tr key={k}>
              {r.map((c, j) => (
                <td key={j}>{typeof c === "object" && c !== null ? JSON.stringify(c) : String(c ?? "")}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      {more > 0 && <p className="muted">… {more} more rows (see Raw data)</p>}
    </div>
  );
}

// The rendered form of a text in a format.
export function RenderPreview({ text, format }) {
  const t = String(text || "");
  if (format === "html") return <iframe className="cv-html" sandbox="" srcDoc={t} title="HTML preview" />;
  if (format === "md")
    return <div className="rich md cv-md" dangerouslySetInnerHTML={{ __html: mdRender(t) }} />;
  if (format === "csv") {
    const rows = parseCSV(t.trim());
    if (rows.length > 1)
      return <Table head={rows[0]} rows={rows.slice(1, 501)} more={Math.max(0, rows.length - 501)} />;
  }
  if (format === "json") {
    try {
      const v = JSON.parse(t);
      const arr = Array.isArray(v)
        ? v
        : Array.isArray(v?.rows)
          ? v.rows
          : Array.isArray(v?.items)
            ? v.items
            : Array.isArray(v?.data)
              ? v.data
              : null;
      if (arr && arr.length && arr.every((x) => x && typeof x === "object" && !Array.isArray(x))) {
        const head = [...new Set(arr.slice(0, 200).flatMap((x) => Object.keys(x)))];
        return (
          <Table
            head={head}
            rows={arr.slice(0, 500).map((x) => head.map((h) => x[h]))}
            more={Math.max(0, arr.length - 500)}
          />
        );
      }
      const pretty = JSON.stringify(v, null, 2);
      return (
        <pre className="code lines cv-pre">
          {highlight(pretty, "x.json").map((l, k) => (
            <span className="ln" key={k}>
              {l.length
                ? l.map(([cls, s], j) =>
                    cls ? (
                      <span key={j} className={cls}>
                        {s}
                      </span>
                    ) : (
                      s
                    ),
                  )
                : " "}
            </span>
          ))}
        </pre>
      );
    } catch {
      /* fall through to raw */
    }
  }
  return (
    <pre className="code cv-pre">
      <Linkify text={t} />
    </pre>
  );
}

// Open a text in the side preview panel (no file needed).
export function openContentPreview(title, text, format) {
  const fmt = format || contentFormat(text);
  if (S.preview?.url) URL.revokeObjectURL(S.preview.url);
  const name = `${title}.${EXT[fmt] || "txt"}`;
  S.preview = {
    path: name,
    inline: true,
    line: 0,
    history: [],
    data: {
      kind: "text",
      name,
      path: name,
      size: new Blob([text || ""]).size,
      text: String(text || ""),
      mime: fmt,
    },
  };
  update();
}

const viewPref = {}; // remembers Preview / Raw per format for this session

export function ContentView({
  text,
  name = "",
  format,
  title = "Result",
  files = [],
  compact,
  empty = "(no output)",
}) {
  const fmt = format || contentFormat(text, name);
  const [mode, setMode] = useState(viewPref[fmt] || (canPreview(fmt) ? "preview" : "raw"));
  const set = (m) => {
    viewPref[fmt] = m;
    setMode(m);
  };
  const t = String(text ?? "");
  const shown = files.filter((f) => f?.path);
  return (
    <div className={"cv" + (compact ? " compact" : "")}>
      <div className="cv-bar">
        <span className="badge cv-fmt">{fmt.toUpperCase()}</span>
        {canPreview(fmt) && t.trim() && (
          <div className="seg cv-seg" role="group" aria-label="View">
            <button type="button" aria-pressed={mode === "preview"} onClick={() => set("preview")}>
              Preview
            </button>
            <button type="button" aria-pressed={mode === "raw"} onClick={() => set("raw")}>
              Raw data
            </button>
          </div>
        )}
        <span className="cv-files">
          {shown.map((f) => (
            <FileLink key={f.path} path={f.path}>
              {f.label || f.path.split("/").pop()}
            </FileLink>
          ))}
        </span>
        <span className="cv-actions">
          {t.trim() && (
            <button
              className="link"
              title="Open in the preview panel"
              onClick={() => openContentPreview(title, t, fmt)}
            >
              <Icon.files /> Open in preview
            </button>
          )}
          <CopyButton text={t} format={fmt} name={name} />
        </span>
      </div>
      {!t.trim() ? (
        <p className="muted cv-empty">{empty}</p>
      ) : mode === "preview" && canPreview(fmt) ? (
        <RenderPreview text={t} format={fmt} />
      ) : (
        <pre className="code cv-pre">
          <Linkify text={t} />
        </pre>
      )}
    </div>
  );
}
