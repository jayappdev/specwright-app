import { createRoot } from "react-dom/client";
import { App } from "./App.jsx";
import { ErrorBoundary } from "./ErrorBoundary.jsx";
import "./fonts.css";
import "./styles.css";

// the last line of defence: never an empty window
createRoot(document.getElementById("root")).render(
  <ErrorBoundary name="SpecWright" full>
    <App />
  </ErrorBoundary>,
);
