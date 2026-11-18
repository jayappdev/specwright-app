// ErrorBoundary keeps one failing part of the app from taking down the
// rest: without it, an error while rendering (say, a file the preview could
// not handle) unmounts the whole React tree and leaves an empty window. The
// part that failed shows what went wrong, with Try again (and, for a panel,
// Close); the rest of the app keeps working.
import { Component } from "react";

export class ErrorBoundary extends Component {
  constructor(props) {
    super(props);
    this.state = { error: null };
  }
  static getDerivedStateFromError(error) {
    return { error };
  }
  componentDidCatch(error, info) {
    console.error(`[${this.props.name || "app"}] ${error?.message || error}`, info?.componentStack || "");
  }
  componentDidUpdate(prev) {
    // a new view (or file) gets a fresh start
    if (this.state.error && prev.resetKey !== this.props.resetKey) this.setState({ error: null });
  }
  render() {
    const { error } = this.state;
    if (!error) return this.props.children;
    const { name = "This part of the app", onClose, full, floating } = this.props;
    return (
      <div
        className={"card err-boundary" + (full ? " full" : "") + (floating ? " floating" : "")}
        role="alert"
      >
        <h3>{name} ran into a problem</h3>
        <p className="wf-desc">
          The rest of the app still works.{" "}
          {onClose ? "Close this panel, or try again." : "Try again, or switch to another view."}
        </p>
        <pre className="code err-boundary-msg">{String(error?.message || error)}</pre>
        <div className="row-actions">
          <button className="btn primary" onClick={() => this.setState({ error: null })}>
            Try again
          </button>
          {onClose && (
            <button
              className="btn secondary"
              onClick={() => {
                this.setState({ error: null });
                onClose();
              }}
            >
              Close
            </button>
          )}
          {full && (
            <button className="btn secondary" onClick={() => window.location.reload()}>
              Reload the app
            </button>
          )}
        </div>
      </div>
    );
  }
}
