// Organising flows (flow/tags.go): each flow has a group (where it is
// listed) and tags (labels to find it). The Flow designer's list and Run
// flows share these pieces: a search box (name, description, group, tags,
// the steps' names and tools), tag filter chips, flows listed by group
// (groups fold away), and a tag editor for the flow's settings.
import { useState } from "react";
import { S, update } from "./store.js";

// filter state per list ("designer", "runner"), kept for the session
export const flowFilter = (area) => ((S.flowFilter ||= {})[area] ||= { q: "", tags: [], closed: {} });

const norm = (s) => String(s || "").toLowerCase();

// flowMatches: every word of the search is in the flow's text, and it has every chosen tag
export function flowMatches(x, ff) {
  if (ff.tags.length && !ff.tags.every((t) => (x.tags || []).includes(t))) return false;
  const words = norm(ff.q).split(/\s+/).filter(Boolean);
  if (!words.length) return true;
  const hay = norm(
    [
      x.name,
      x.description,
      x.group,
      ...(x.tags || []).map((t) => "#" + t + " " + t),
      ...(x.keywords || []),
    ].join(" \u0001 "),
  );
  return words.every((w) => hay.includes(w.replace(/^#/, "")));
}

// groupFlows: the flows that match, by group (named groups A–Z, then the ungrouped)
export function groupFlows(list, ff) {
  const by = new Map();
  for (const x of list || []) {
    if (!flowMatches(x, ff)) continue;
    const g = x.group || "";
    if (!by.has(g)) by.set(g, []);
    by.get(g).push(x);
  }
  return [...by.entries()].sort(([a], [b]) => (!a ? 1 : !b ? -1 : a.localeCompare(b)));
}

export const allTags = (list) => [...new Set((list || []).flatMap((x) => x.tags || []))].sort();
export const allGroups = (list) => [...new Set((list || []).map((x) => x.group).filter(Boolean))].sort();

// the search box and the tag chips above a list
export function FlowFilterBar({ area, list }) {
  const ff = flowFilter(area),
    tags = allTags(list);
  if (!(list || []).length) return null;
  const shown = (list || []).filter((x) => flowMatches(x, ff)).length;
  return (
    <div className="fls-bar">
      <input
        type="search"
        className="hk-search"
        value={ff.q}
        placeholder="Search flows, #tags, steps and tools"
        aria-label="Search flows"
        onChange={(e) => {
          ff.q = e.target.value;
          update();
        }}
      />
      {tags.length > 0 && (
        <div className="fls-tags" role="group" aria-label="Show flows with these tags">
          {tags.map((t) => {
            const on = ff.tags.includes(t);
            return (
              <button
                key={t}
                type="button"
                className={"fl-tag" + (on ? " on" : "")}
                aria-pressed={on}
                onClick={() => {
                  ff.tags = on ? ff.tags.filter((x) => x !== t) : [...ff.tags, t];
                  update();
                }}
              >
                #{t}
              </button>
            );
          })}
        </div>
      )}
      {(ff.q || ff.tags.length > 0) && (
        <small className="muted">
          {shown} of {list.length} flow{list.length === 1 ? "" : "s"}{" "}
          <button
            className="link"
            onClick={() => {
              ff.q = "";
              ff.tags = [];
              update();
            }}
          >
            Clear
          </button>
        </small>
      )}
    </div>
  );
}

// a group's heading (it folds the group away), or nothing for the ungrouped
// when there are no groups at all
export function FlowGroup({ area, name, count, children, groups }) {
  const ff = flowFilter(area),
    key = name || "\u0000",
    closed = !!ff.closed[key] && !ff.q && !ff.tags.length; // a search shows everything it found
  if (!name && groups === 1) return children;
  return (
    <div className="fls-group">
      <button
        className="tree-folder fls-group-head"
        aria-expanded={!closed}
        onClick={() => {
          ff.closed[key] = !closed;
          update();
        }}
      >
        <span className="fls-caret">{closed ? "▸" : "▾"}</span> {name || "Not in a group"} · {count}
      </button>
      {!closed && children}
    </div>
  );
}

// a flow's tags, small
export function FlowTagChips({ tags, area }) {
  if (!tags?.length) return null;
  return (
    <span className="fls-chips">
      {tags.map((t) => (
        <span
          key={t}
          className="fl-tag small"
          role={area ? "button" : undefined}
          tabIndex={area ? 0 : undefined}
          title={area ? "Show only flows tagged #" + t : undefined}
          onClick={
            area
              ? (e) => {
                  e.stopPropagation();
                  const ff = flowFilter(area);
                  if (!ff.tags.includes(t)) ff.tags = [...ff.tags, t];
                  update();
                }
              : undefined
          }
        >
          #{t}
        </span>
      ))}
    </span>
  );
}

// the tag editor in a flow's settings: chips, and a box that adds on Enter or a comma
export function TagEditor({ tags, onChange, suggestions = [] }) {
  const [text, setText] = useState("");
  const add = (s) => {
    const t = String(s)
      .toLowerCase()
      .trim()
      .replace(/^#/, "")
      .replace(/[^\p{L}\p{N}_./]+/gu, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 32);
    if (t && !tags.includes(t)) onChange([...tags, t].sort());
    setText("");
  };
  return (
    <div className="fls-editor">
      {tags.map((t) => (
        <span key={t} className="fl-tag">
          #{t}
          <button
            type="button"
            className="fls-x"
            aria-label={"Remove the tag " + t}
            onClick={() => onChange(tags.filter((x) => x !== t))}
          >
            ×
          </button>
        </span>
      ))}
      <input
        value={text}
        list="fls-tag-suggestions"
        placeholder={tags.length ? "Add a tag" : "e.g. nightly, jira, report"}
        aria-label="Add a tag"
        onChange={(e) => {
          const v = e.target.value;
          if (v.includes(",")) v.split(",").slice(0, -1).forEach(add);
          setText(v.includes(",") ? v.split(",").pop() : v);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            add(text);
          } else if (e.key === "Backspace" && !text && tags.length) onChange(tags.slice(0, -1));
        }}
        onBlur={() => text.trim() && add(text)}
      />
      <datalist id="fls-tag-suggestions">
        {suggestions
          .filter((t) => !tags.includes(t))
          .map((t) => (
            <option key={t} value={t} />
          ))}
      </datalist>
    </div>
  );
}
