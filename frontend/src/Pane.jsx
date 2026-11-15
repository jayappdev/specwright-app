// Pane: the resize handle for a side pane, with widths kept in the app prefs.
import { S, savePrefsNow } from "./store.js";
import { PaneHandle } from "./ui.jsx";

export function Pane(props) { return <PaneHandle {...props} panes={S.panes} save={savePrefsNow} />; }
