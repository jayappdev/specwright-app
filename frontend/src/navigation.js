// Back and Forward between views (nav.js keeps the list). Every update, the
// place you are at — the view, and what is selected in it — is recorded
// when it changed, whichever way you got there (the sidebar, a link, a
// button). Views tell how to read and restore their selection with
// registerPlace; a view that does not is restored as a view only. Going
// back is like clicking the view in the sidebar, so a view that saves when
// you leave it (the Flow designer) still does.
import { S, update, afterUpdate } from "./store.js";
import { createHistory } from "./nav.js";

const hist = createHistory(50);
const places = {}; // view id -> { key: () => string, restore: (key) => maybe a promise }
let restoring = 0;

// registerPlace says how a view's selection is read and restored.
export function registerPlace(view, p) {
  places[view] = p;
}

function here() {
  let key = "";
  try {
    key = String(places[S.view]?.key?.() || "");
  } catch {
    /* a view not set up yet: no selection */
  }
  return { view: S.view, key };
}

function track() {
  if (!restoring && S.view) hist.push(here(), { keepEmpty: !!places[S.view]?.restoreEmpty });
}
afterUpdate.add(track);

export const canBack = () => hist.canBack();
export const canForward = () => hist.canForward();
export const peek = (dir) => (dir < 0 ? hist.peekBack() : hist.peekForward());

// go moves one place back (dir -1) or forward (dir 1).
export async function go(dir) {
  const e = dir < 0 ? hist.back() : hist.forward();
  if (!e) return;
  restoring++;
  try {
    S.view = e.view;
    update();
    const p = places[e.view];
    if ((e.key || p?.restoreEmpty) && p?.restore && here().key !== e.key) await p.restore(e.key);
  } catch {
    /* the item is gone (a deleted flow, an archived run): the view is enough */
  } finally {
    // the restored view's own loading settles first, then recording resumes
    setTimeout(() => {
      restoring--;
      update();
    }, 50);
  }
}
