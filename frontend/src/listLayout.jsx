// Headings for the Tools and Flows lists (core/layout.go): purely how the
// lists are arranged. Each list ("tools", "flows") has its own headings in
// order and the items placed under them; nothing about a tool or a flow
// changes. Items are placed by dragging them onto a heading.
import { backend } from "./backend.js";
import { S, update, toast, errMsg } from "./store.js";
import { askText, confirmDialog } from "./Dialog.jsx";
import { Icon } from "./ui.jsx";

const empty = () => ({ headings: [], items: {} });

// loadLayout reads the open project's headings (again when force).
export async function loadLayout(force) {
  if (!S.ws) return;
  if (S.lists && S.lists.root === S.ws.root && !force) return;
  const root = S.ws.root;
  const l = await backend.ListLayout().catch(() => null);
  S.lists = { root, tools: l?.tools || empty(), flows: l?.flows || empty() };
  update();
}

const sec = (list) => (S.lists?.root === S.ws?.root && S.lists?.[list]) || empty();
export const headingsOf = (list) => sec(list).headings || [];
export const headingOf = (list, id) => (sec(list).items || {})[id] || "";

async function save(list, next) {
  const before = S.lists;
  S.lists = { ...S.lists, [list]: next };
  update();
  try {
    const l = await backend.SaveListLayout({
      tools: list === "tools" ? next : sec("tools"),
      flows: list === "flows" ? next : sec("flows"),
    });
    S.lists = { root: S.ws.root, tools: l.tools, flows: l.flows };
  } catch (e) {
    S.lists = before;
    toast(errMsg(e), "error");
  }
  update();
}

// setHeading places an item under a heading ("" takes it out).
export function setHeading(list, id, heading) {
  const s = sec(list),
    items = { ...(s.items || {}) };
  if (heading) items[id] = heading;
  else delete items[id];
  return save(list, { headings: s.headings, items });
}

export async function addHeading(list) {
  const name = (await askText("New heading", { placeholder: "e.g. Reporting", ok: "Add" }))?.trim();
  if (!name) return;
  const s = sec(list);
  if (s.headings.some((h) => h.toLowerCase() === name.toLowerCase()))
    return toast(`There is a heading ${name} already.`);
  return save(list, { headings: [...s.headings, name], items: s.items });
}

export async function renameHeading(list, old) {
  const name = (await askText("Rename the heading", { value: old, ok: "Rename" }))?.trim();
  if (!name || name === old) return;
  const s = sec(list);
  if (s.headings.some((h) => h !== old && h.toLowerCase() === name.toLowerCase()))
    return toast(`There is a heading ${name} already.`);
  const items = {};
  for (const [k, v] of Object.entries(s.items || {})) items[k] = v === old ? name : v;
  return save(list, { headings: s.headings.map((h) => (h === old ? name : h)), items });
}

export async function removeHeading(list, h) {
  const s = sec(list),
    n = Object.values(s.items || {}).filter((v) => v === h).length;
  if (
    n &&
    !(await confirmDialog(
      `Its ${n} item${n === 1 ? "" : "s"} go back to where they were listed before. Nothing is deleted.`,
      {
        title: `Remove the heading ${h}?`,
        ok: "Remove",
      },
    ))
  )
    return;
  const items = {};
  for (const [k, v] of Object.entries(s.items || {})) if (v !== h) items[k] = v;
  return save(list, { headings: s.headings.filter((x) => x !== h), items });
}

export function moveHeading(list, h, by) {
  const s = sec(list),
    hs = [...s.headings],
    i = hs.indexOf(h),
    j = i + by;
  if (i < 0 || j < 0 || j >= hs.length) return;
  [hs[i], hs[j]] = [hs[j], hs[i]];
  return save(list, { headings: hs, items: s.items });
}

// --- dragging an item onto a heading ---------------------------------------------

const MIME = "application/x-specwright-item";

// dragProps: what makes a tile or row draggable as item id of list.
export const dragProps = (list, id) => ({
  draggable: true,
  onDragStart: (e) => {
    e.dataTransfer.setData(MIME, JSON.stringify({ list, id }));
    e.dataTransfer.effectAllowed = "move";
  },
});

// dropProps: what makes a section a place to drop items of list on
// (heading "" takes them out of their heading).
export const dropProps = (list, heading) => ({
  onDragOver: (e) => {
    if ([...e.dataTransfer.types].includes(MIME)) {
      e.preventDefault();
      e.currentTarget.classList.add("drop-on");
    }
  },
  onDragLeave: (e) => e.currentTarget.classList.remove("drop-on"),
  onDrop: (e) => {
    e.currentTarget.classList.remove("drop-on");
    try {
      const x = JSON.parse(e.dataTransfer.getData(MIME));
      if (x.list === list && headingOf(list, x.id) !== heading) {
        e.preventDefault();
        setHeading(list, x.id, heading);
      }
    } catch {
      /* not one of ours */
    }
  },
});

// HeadingActions: rename, move up / down, remove (on a heading's title).
export function HeadingActions({ list, name }) {
  const hs = headingsOf(list),
    i = hs.indexOf(name);
  const stop = (f) => (e) => {
    e.stopPropagation();
    f();
  };
  return (
    <span className="lh-actions">
      <button
        type="button"
        className="icon-btn"
        title="Rename the heading"
        aria-label={`Rename ${name}`}
        onClick={stop(() => renameHeading(list, name))}
      >
        <Icon.pencil />
      </button>
      <button
        type="button"
        className="icon-btn"
        title="Move up"
        aria-label={`Move ${name} up`}
        disabled={i <= 0}
        onClick={stop(() => moveHeading(list, name, -1))}
      >
        ↑
      </button>
      <button
        type="button"
        className="icon-btn"
        title="Move down"
        aria-label={`Move ${name} down`}
        disabled={i < 0 || i >= hs.length - 1}
        onClick={stop(() => moveHeading(list, name, 1))}
      >
        ↓
      </button>
      <button
        type="button"
        className="icon-btn"
        title="Remove the heading (nothing is deleted)"
        aria-label={`Remove ${name}`}
        onClick={stop(() => removeHeading(list, name))}
      >
        ×
      </button>
    </span>
  );
}

// HeadingPicker: put an item under a heading from a menu (for those who do
// not drag), with "+ New heading…".
export function HeadingPicker({ list, id, label = "Heading" }) {
  const cur = headingOf(list, id),
    hs = headingsOf(list);
  return (
    <label className="lh-picker">
      <span className="muted">{label}</span>
      <select
        value={cur}
        onChange={async (e) => {
          const v = e.target.value;
          if (v === "\u0001new") {
            const before = headingsOf(list).length;
            await addHeading(list);
            const now = headingsOf(list);
            if (now.length > before) setHeading(list, id, now[now.length - 1]);
            return;
          }
          setHeading(list, id, v);
        }}
      >
        <option value="">None</option>
        {hs.map((h) => (
          <option key={h} value={h}>
            {h}
          </option>
        ))}
        <option value={"\u0001new"}>+ New heading…</option>
      </select>
    </label>
  );
}

// flow files changed (the AI saved a version, another window): its heading
// may have been set for it, so read the arrangement again
if (typeof window !== "undefined") {
  let t = 0;
  window.addEventListener("specwright:flows-changed", () => {
    clearTimeout(t);
    t = setTimeout(() => loadLayout(true), 200);
  });
}
