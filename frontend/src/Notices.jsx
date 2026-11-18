// Messages: the last 20 toasts, from a button on the status bar, so a message
// that was gone before it could be read can still be found.
import { S, update } from "./store.js";
import { Icon, usePopover } from "./ui.jsx";

const time = (t) =>
  new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });

export function Notices() {
  const p = usePopover(),
    list = S.notices || [],
    unread = S.noticesUnread || 0,
    errs = list.slice(0, unread).some((n) => n.kind === "error");
  const toggle = () => {
    p.setOpen(!p.open);
    if (!p.open) {
      S.noticesUnread = 0;
      update();
    }
  };
  return (
    <span className="notices" ref={p.wrap}>
      <button
        ref={p.btn}
        className={"status-item status-btn" + (errs ? " status-err" : "")}
        aria-haspopup="true"
        aria-expanded={p.open}
        title="Recent messages"
        aria-label={`Recent messages${unread ? `, ${unread} new` : ""}`}
        onClick={toggle}
      >
        <Icon.bell /> {unread ? <b>{unread}</b> : null}
      </button>
      {p.open && (
        <div className="notices-pop" role="dialog" aria-label="Recent messages">
          <div className="notices-head">
            <b>Recent messages</b>
            {list.length > 0 && (
              <button
                className="link"
                onClick={() => {
                  S.notices = [];
                  update();
                }}
              >
                Clear
              </button>
            )}
          </div>
          {list.length ? (
            <ul>
              {list.map((n, k) => (
                <li key={n.at + "-" + k} className={"notice notice-" + n.kind}>
                  {n.kind === "error" ? <Icon.warn /> : <Icon.check />}
                  <span>{n.msg}</span>
                  <small className="muted">{time(n.at)}</small>
                </li>
              ))}
            </ul>
          ) : (
            <p className="muted">No messages yet.</p>
          )}
        </div>
      )}
    </span>
  );
}
