import { describe, it, expect } from "vitest";
import { hasDiagrams, diagramKind, svgWidth } from "./diagrams.js";

describe("hasDiagrams", () => {
  it("finds Mermaid and PlantUML blocks, and nothing in plain pages", () => {
    expect(hasDiagrams('<pre class="mermaid">graph LR</pre>')).toBe(true);
    expect(hasDiagrams('<pre><code class="language-mermaid">graph LR</code></pre>')).toBe(true);
    expect(hasDiagrams("<pre>@startuml\nA -> B\n@enduml</pre>")).toBe(true);
    expect(hasDiagrams('<div class="plantuml">A -> B</div>')).toBe(true);
    expect(hasDiagrams("<p>Hello</p><pre>code</pre>")).toBe(false);
  });
});

describe("diagramKind", () => {
  it("reads classes first, then the text", () => {
    expect(diagramKind("mermaid", "graph LR")).toBe("mermaid");
    expect(diagramKind("language-mermaid", "anything")).toBe("mermaid");
    expect(diagramKind("language-plantuml", "A -> B")).toBe("plantuml");
    expect(diagramKind("puml", "A -> B")).toBe("plantuml");
    expect(diagramKind("", "@startuml\nA -> B\n@enduml")).toBe("plantuml");
    expect(diagramKind("", "sequenceDiagram\n A->>B: hi")).toBe("mermaid");
    expect(diagramKind("", "graphics are fun")).toBe(null);
    expect(diagramKind("language-js", "graph LR")).toBe(null);
  });
});

describe("svgWidth", () => {
  it("takes the drawn width", () => {
    expect(svgWidth('<svg width="100%" style="max-width: 412.5px;" viewBox="0 0 412 300">')).toBe(413);
    expect(svgWidth('<svg width="220px" height="90px">')).toBe(220);
    expect(svgWidth('<svg viewBox="0 0 640 480">')).toBe(640);
    expect(svgWidth("<svg>")).toBe(0);
  });
});
