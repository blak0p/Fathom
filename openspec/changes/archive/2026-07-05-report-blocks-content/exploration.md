## Exploration: Fathom HTML Report Blocks Content

### Current State
Currently, Fathom computes:
1. **Blast Radius** ([internal/impact/engine.go](file:///home/alejandro/dev/Fathom/internal/impact/engine.go)): Transitive impact analysis using BFS to find direct/transitive callers and affected files.
2. **Signature Mismatch Detection** ([internal/mismatch/mismatch.go](file:///home/alejandro/dev/Fathom/internal/mismatch/mismatch.go)): Detects arity mismatches, type mismatches (when comparing concrete types), and class override signature deviations.
3. **CLI Output** ([cmd/analyze.go](file:///home/alejandro/dev/Fathom/cmd/analyze.go)): Emits a human-readable text output or a JSON report (`--json`).

Currently, Fathom does **not** generate HTML reports, track dead code, compare before/after symbol contents side-by-side, or group mismatches by their definitions with detailed descriptions of changes.

---

### Affected Areas
To implement the 4 report blocks, the following files will be affected or created:
- [cmd/analyze.go](file:///home/alejandro/dev/Fathom/cmd/analyze.go) — Needs to support HTML report output generation (e.g. `--html` flag) and orchestrate the aggregation of data for the 4 blocks.
- [internal/mismatch/mismatch.go](file:///home/alejandro/dev/Fathom/internal/mismatch/mismatch.go) — Needs extension to extract old vs. new signatures, generate a description of changes (e.g., parameter added/removed), and package before/after source contents.
- [internal/impact/engine.go](file:///home/alejandro/dev/Fathom/internal/impact/engine.go) — Needs option to resolve dependency types (direct call, interface call, struct embedding) to satisfy the blast radius block.
- `internal/deadcode/` (New package) — Needs a scanner that walks changed symbols in the diff, queries the database for references, and determines the dead code confidence (High/Medium/Low) based on visibility/export status.
- `internal/report/` (New package) — Aggregates data from impact, mismatch, and deadcode engines to build the final structured report payload, and handles HTML rendering via Go's `html/template`.

---

### Approaches

#### Approach 1: Rich JSON Generation + Standalone CLI HTML Generator (Hybrid)
Compute all 4 report blocks entirely inside Fathom CLI. Provide a clean Go struct model representing the final report. Support both a rich `--json` payload and a styled `--html` output powered by an embedded Go `html/template`.
- **Pros**:
  - Extremely convenient: Developers and CI pipelines can generate the HTML report directly with a single flag (e.g., `fathom analyze --html report.html`).
  - Highly structured: The JSON output can be easily parsed by custom CI integrations or dashboards without parsing HTML.
  - Reuses existing AST extraction: Fathom already stores the full source code content of each symbol in `symbol.Symbol.Content`, making before/after collapsible code rendering straightforward.
- **Cons**:
  - Requires embedding HTML/CSS/JS templates into the Go CLI binary.
  - Modifying the styling or interactivity of the report requires rebuilding the CLI.
- **Effort**: Medium

#### Approach 2: JSON Output Only + External UI Web App/Script
Fathom CLI only focuses on producing the structured JSON payload containing the 4 report blocks. An external npm package, GitHub Action, or static web application is used to ingest this JSON and render the visual HTML report.
- **Pros**:
  - Keeps Fathom CLI small, clean, and focused solely on backend analysis.
  - Allows rapid iterations on UI styling, collapsible components, and UX interactivity without releasing new CLI binaries.
- **Cons**:
  - Complicates local usage: Developers must run two tools (or pipeline commands) to view the HTML report locally.
- **Effort**: Low (for the CLI), but High (overall system complexity due to distribution of multiple tools).

---

### Recommendation
We recommend **Approach 1 (Hybrid)**. Providing a built-in `--html` flag makes the local developer experience seamless, while also keeping the `--json` option robust for CI integration. This can be clean by embedding the HTML template inside the CLI using Go's `embed` package.

Here is the proposed design for the 4 HTML report blocks:

#### 1. Verdict Block
A derived boolean indicating whether the pull request / workspace changes are structurally "clean".
- **Condition**: `len(BuildBreakFindings) == 0 && len(BlastRadiusDirect) == 0`
- **Output States**:
  - **CLEAN**: *"Limpio, puedes aprobar sin mirar más"* (No signature mismatches and no direct callers affected).
  - **REVIEW**: *"Hay algo, revisa los bloques de abajo"* (Mismatches detected or symbols are referenced elsewhere).

#### 2. Build-Break Findings Block
Groups signature mismatches and overrides by their changed definition.
- **Definition Change Description**: Compare `oldSymbol` (loaded from database) and `newSymbol` (parsed from workspace) parameter bounds:
  - If parameter count decreased: *"parámetro eliminado"*
  - If parameter count increased: *"parámetro añadido"*
  - If parameter types changed: *"tipo de parámetro cambiado"*
- **Collapsible Code**: Display `oldSymbol.Content` ("Antes") and `newSymbol.Content` ("Después") in collapsible code drawers.
- **Call Sites**: List all references where the old signature is still invoked (`mismatch.Mismatch` occurrences with file, line, and code snippet).

#### 3. Blast Radius Block
Traces the transitive callers of any exported symbols touched in the diff.
- **Dependency Type Resolution**:
  - We can identify the dependency type by looking at the `refs.ReferenceKind`:
    - `RefCall`: Direct Call (labeled as `"direct_call"` or `"interface_call"` if target is an interface).
    - `RefTypeUse` (within struct body): Struct Embedding / Composition (labeled as `"struct_embedding"`).
  - Highlighting dependency types helps distinguish high-risk changes from indirect references.

#### 4. Dead Code Block
Identifies symbols touched in the diff that have zero references in the codebase.
- **Scanning**: For each added/modified symbol in the diff, run `store.GetReferences(symbolName)`.
- **Confidence Rating**:
  - **High**: Non-exported symbol (e.g. starting with lowercase in Go, or not exported in JS/TS) has 0 references.
  - **Medium/Low**: Exported symbol (e.g. uppercase in Go, or exported in JS/TS) has 0 references (as it could be accessed by external consumers or reflection).
- **Motivo (Reason)**: E.g., *"sin referencias encontradas"* or *"sin referencias directas, podría llamarse por reflection/tag"*.

---

### Risks
- **False Positives in Dead Code**: Exported symbols in libraries/APIs will be flagged as dead code (Low/Medium confidence) even if they are required public interfaces. A warning indicator should clarify that exported symbols may be consumed externally.
- **Performance Overhead**: Fetching before/after symbol contents and running full-database reference checks on all modified symbols in a large PR might increase analysis time. However, since the entry point is limited to the diff, the overhead is bounded by the size of the PR.

---

### Ready for Proposal
**Yes**. The requirements are clear, and the codebase has all the necessary primitives (`symbol.Symbol.Content`, `mismatch.Engine`, and `impact.Engine`) to produce this structured report content. The orchestrator should propose proceeding with the creation of the specification and task list for the HTML report block content implementation.
