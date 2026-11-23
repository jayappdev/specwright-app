// Tools: finding tools — search, #tags, grouping, and the tags bar of a tool.
import { useState } from "react";
import { backend } from "../../backend.js";
import { EmptyState } from "../../ui.jsx";
import { S, update, toast, errMsg } from "../../store.js";
import { builtinsVisible, selectBuiltin } from "../Builtins.jsx";
import { ToolTile, ToolGrid } from "../../ToolTile.jsx";
import { newTool, openTool } from "../Tools.jsx";
import {
  HeadingActions,
  addHeading,
  dragProps,
  dropProps,
  headingOf,
  headingsOf,
  loadLayout,
} from "../../listLayout.jsx";

// --- finding tools: search, tags, grouping --------------------------------------------

const tf = () => (S.tf ||= { q: "", tag: "", groupBy: "source" });
export const finding = () => {
  const f = tf();
  return !!(f.q.trim() || f.tag || f.groupBy === "tag" || f.groupBy === "heading");
};

export async function loadToolTags() {
  if (S.toolTags) return;
  S.toolTags = (await backend.ToolTags().catch(() => null)) || {};
  update();
}
const tagsOf = (key) => (S.toolTags || {})[key] || [];
export const withTags = (list) => list.map((t) => ({ ...t, tags: tagsOf(t.name) }));
const allTags = () => [...new Set(Object.values(S.toolTags || {}).flat())].sort();

export function ToolFinder() {
  const f = tf(),
    set = (k, v) => {
      f[k] = v;
      update();
    },
    tags = allTags();
  return (
    <div className="tf-bar">
      <input
        type="search"
        value={f.q}
        placeholder="Search tools: name, what it does, #tag"
        aria-label="Search tools"
        onChange={(e) => set("q", e.target.value)}
      />
      <div className="tf-row">
        <span className="muted">Group by</span>
        <div className="seg" role="group" aria-label="Group by">
          {[
            ["heading", "Heading"],
            ["source", "Source"],
            ["tag", "Tag"],
          ].map(([v, l]) => (
            <button
              key={v}
              type="button"
              aria-pressed={f.groupBy === v}
              onClick={() => {
                if (v === "heading") loadLayout();
                set("groupBy", v);
              }}
            >
              {l}
            </button>
          ))}
        </div>
        {f.groupBy === "heading" && (
          <button type="button" className="link" onClick={() => addHeading("tools")}>
            + Heading
          </button>
        )}
      </div>
      {tags.length > 0 && (
        <div className="tf-tags">
          {tags.map((t) => (
            <button
              key={t}
              type="button"
              className={"tt-tag tf-tag" + (f.tag === t ? " on" : "")}
              aria-pressed={f.tag === t}
              onClick={() => set("tag", f.tag === t ? "" : t)}
            >
              #{t}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// every tool (built-in, managed, project, global, Copilot) matching the
// search and tag, grouped by source or by tag
export function FoundTools({ exts }) {
  const f = tf(),
    q = f.q.trim().toLowerCase().replace(/^#/, "");
  const items = [
    ...(builtinsVisible() ? S.builtins || [] : []).map((t) => ({
      name: t.name,
      key: "builtin:" + t.name,
      desc: t.description,
      kind: "builtin",
      scope: "builtin",
      status: t.enabled === false ? "off" : "built-in",
      open: () => {
        if (S.cx) S.cx.sel = "";
        selectBuiltin(t.name);
      },
    })),
    ...exts.map((t) => ({
      ...t,
      key: t.name,
      open: () => {
        if (S.bt) S.bt.sel = "";
        if (S.cx) S.cx.sel = "";
        openTool(t);
      },
    })),
  ]
    .map((t) => ({ ...t, tags: tagsOf(t.key) }))
    .filter(
      (t) =>
        (!f.tag || t.tags.includes(f.tag)) &&
        (!q || (t.name + " " + (t.desc || "") + " " + t.tags.join(" ")).toLowerCase().includes(q)),
    );
  const SRC = [
    ["builtin", "Built-in"],
    ["managed", "Managed by admin"],
    ["project", "Project tools"],
    ["global", "Global tools"],
  ];
  const byHeading = f.groupBy === "heading";
  const groups = byHeading
    ? [
        ...headingsOf("tools").map((h) => [h, items.filter((x) => headingOf("tools", x.key) === h)]),
        ["Not under a heading", items.filter((x) => !headingOf("tools", x.key))],
      ]
    : f.groupBy === "tag"
      ? [
          ...allTags()
            .filter((t) => !f.tag || t === f.tag)
            .map((t) => ["#" + t, items.filter((x) => x.tags.includes(t))]),
          ["Not tagged", items.filter((x) => !x.tags.length)],
        ]
      : [
          ...SRC.map(([s, l]) => [l, items.filter((x) => x.scope === s)]),
          ["Other", items.filter((x) => !SRC.some(([s]) => s === x.scope))],
        ];
  // headings show even when empty (a place to drop tools on)
  const shown = groups.filter(
    ([l, list]) => list.length || (byHeading && (headingsOf("tools").includes(l) || !q)),
  );
  return (
    <>
      {shown.map(([l, list]) => (
        <ToolGrid
          key={l}
          title={l}
          compact
          tools={list}
          empty={byHeading ? "Drag tools here" : undefined}
          zone={byHeading ? dropProps("tools", headingsOf("tools").includes(l) ? l : "") : undefined}
          actions={
            byHeading && headingsOf("tools").includes(l) ? <HeadingActions list="tools" name={l} /> : null
          }
          render={(t) => (
            <ToolTile
              drag={byHeading ? dragProps("tools", t.key) : undefined}
              key={t.key}
              t={t}
              compact
              active={S.tb?.editing === t.name || (t.kind === "builtin" && S.bt?.sel === t.name)}
              onOpen={t.open}
            />
          )}
        />
      ))}
      {!shown.length && (
        <EmptyState
          compact
          title={`No tools match${q ? ` “${f.q}”` : ""}${f.tag ? ` with #${f.tag}` : ""}`}
          actions={[
            (q || f.tag) && [
              "Clear the search",
              () => {
                f.q = "";
                f.tag = "";
                update();
              },
            ],
            ["Create a tool", () => newTool(false)],
          ]}
        />
      )}
    </>
  );
}

// The open tool's tags.
export function ToolTagsBar({ name }) {
  const tags = tagsOf(name),
    [text, setText] = useState("");
  const save = async (next) => {
    try {
      S.toolTags = await backend.SetToolTags(name, next);
      update();
    } catch (e) {
      toast(errMsg(e), "error");
    }
  };
  const add = () => {
    const t = text
      .split(",")
      .map((x) => x.trim().replace(/^#/, ""))
      .filter(Boolean);
    if (t.length) save([...tags, ...t]);
    setText("");
  };
  return (
    <div className="tt-tagbar">
      <span className="muted">Tags</span>
      {tags.map((t) => (
        <span key={t} className="tt-tag">
          #{t}
          <button
            className="link"
            aria-label={"Remove tag " + t}
            onClick={() => save(tags.filter((x) => x !== t))}
          >
            ✕
          </button>
        </span>
      ))}
      <input
        value={text}
        list="tool-tag-list"
        placeholder={tags.length ? "add a tag" : "add tags, e.g. database, reporting"}
        aria-label="Add a tag"
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            add();
          }
        }}
        onBlur={add}
      />
      <datalist id="tool-tag-list">
        {allTags()
          .filter((t) => !tags.includes(t))
          .map((t) => (
            <option key={t} value={t} />
          ))}
      </datalist>
    </div>
  );
}
