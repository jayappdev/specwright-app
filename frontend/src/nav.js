// Back and forward through the places you went (pure, no app state): a list
// of entries and where you are in it, like a browser's history. An entry is
// { view, key } — the view and what is selected in it ("" when nothing).
// Going somewhere new drops the entries ahead of you; the same place twice
// in a row is one entry; at most `cap` entries are kept (the oldest go).

export const sameEntry = (a, b) => !!a && !!b && a.view === b.view && (a.key || "") === (b.key || "");

export function createHistory(cap = 50) {
  let list = [],
    at = -1;
  return {
    current: () => list[at] || null,
    // push records a new place; a change of selection with nothing selected
    // before, in the same view, completes the current entry instead
    push(e) {
      const cur = list[at];
      if (sameEntry(cur, e)) return false;
      if (cur && cur.view === e.view && !cur.key && e.key) {
        list[at] = { ...e };
        return false;
      }
      list = list.slice(0, at + 1);
      list.push({ ...e });
      if (list.length > cap) list = list.slice(list.length - cap);
      at = list.length - 1;
      return true;
    },
    canBack: () => at > 0,
    canForward: () => at < list.length - 1,
    peekBack: () => (at > 0 ? list[at - 1] : null),
    peekForward: () => (at < list.length - 1 ? list[at + 1] : null),
    back() {
      if (at <= 0) return null;
      at--;
      return list[at];
    },
    forward() {
      if (at >= list.length - 1) return null;
      at++;
      return list[at];
    },
    size: () => list.length,
    index: () => at,
  };
}
