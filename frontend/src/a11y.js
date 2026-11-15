// Keyboard focus for modal dialogs (every element with aria-modal="true"):
// - Tab and Shift+Tab stay inside the top-most open modal;
// - a click or focus that lands outside it is pulled back in;
// - when the last modal closes, focus goes back to where it was before the
//   first one opened (the button that opened it, usually).
const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"]), [contenteditable="true"]';

const topModal = () => { const all = document.querySelectorAll('[aria-modal="true"]'); return all[all.length - 1] || null; };
const focusables = (el) => [...el.querySelectorAll(FOCUSABLE)].filter((x) => x.offsetParent !== null || x === document.activeElement);

let installed = false;
export function installModalFocus() {
  if (installed) return;
  installed = true;
  let before = null, open = false;
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Tab") return;
    const m = topModal(); if (!m) return;
    const list = focusables(m); if (!list.length) { e.preventDefault(); return; }
    const first = list[0], last = list[list.length - 1], at = document.activeElement;
    if (!m.contains(at)) { e.preventDefault(); (e.shiftKey ? last : first).focus(); }
    else if (e.shiftKey && at === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && at === last) { e.preventDefault(); first.focus(); }
  }, true);
  document.addEventListener("focusin", (e) => {
    const m = topModal();
    if (m && !m.contains(e.target) && !e.target.closest?.(".toast")) focusables(m)[0]?.focus();
  });
  // remember the focus before a modal opens; give it back when none is left
  new MutationObserver(() => {
    const m = topModal();
    if (m && !open) { open = true; before = lastFocus; }
    else if (!m && open) { open = false; const b = before; before = null; if (b?.isConnected && (document.activeElement === document.body || !document.activeElement)) setTimeout(() => b.focus?.(), 0); }
  }).observe(document.body, { childList: true, subtree: true });
  let lastFocus = null;
  document.addEventListener("focusin", (e) => { if (!topModal()) lastFocus = e.target; }, true);
}

// Tooltips: any element with a title gets a styled tooltip after a short
// pause on hover, and at once when it gets keyboard focus (the browser's own
// title tooltip never shows on focus). While shown, the title moves to
// data-tip so the native one does not appear on top; it is put back after.
let tipInstalled = false;
export function installTooltips() {
  if (tipInstalled) return;
  tipInstalled = true;
  const tip = document.createElement("div");
  tip.className = "tip"; tip.setAttribute("role", "tooltip"); tip.id = "app-tip"; tip.hidden = true;
  document.body.appendChild(tip);
  let el = null, timer = 0;
  const restore = () => {
    if (!el) return;
    if (!el.hasAttribute("title") && el.dataset.tip != null) el.setAttribute("title", el.dataset.tip);
    delete el.dataset.tip;
    if (el.getAttribute("aria-describedby") === "app-tip") el.removeAttribute("aria-describedby");
    el = null;
  };
  const hide = () => { clearTimeout(timer); tip.hidden = true; restore(); };
  const show = (t) => {
    const text = t.getAttribute("title");
    if (!text?.trim() || !t.isConnected) return;
    hide();
    el = t; el.dataset.tip = text; el.removeAttribute("title");
    if (!el.hasAttribute("aria-describedby") && el.getAttribute("aria-label") !== text) el.setAttribute("aria-describedby", "app-tip");
    tip.textContent = text; tip.hidden = false;
    const r = t.getBoundingClientRect(), w = tip.offsetWidth, h = tip.offsetHeight;
    let x = Math.min(Math.max(6, r.left + r.width / 2 - w / 2), innerWidth - w - 6), y = r.bottom + 6;
    if (y + h > innerHeight - 6) y = r.top - h - 6;
    tip.style.left = x + "px"; tip.style.top = Math.max(6, y) + "px";
  };
  const target = (e) => e.target?.closest?.("[title]");
  document.addEventListener("mouseover", (e) => {
    const t = target(e);
    if (!t) { if (el && !el.contains(e.target)) hide(); return; }
    if (t === el) return;
    hide(); timer = setTimeout(() => show(t), 400);
  });
  document.addEventListener("focusin", (e) => { const t = target(e); if (t && t === e.target && t.matches(":focus-visible")) show(t); });
  document.addEventListener("focusout", hide);
  document.addEventListener("mousedown", hide, true);
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !tip.hidden) hide(); }, true);
  window.addEventListener("scroll", hide, true);
}
