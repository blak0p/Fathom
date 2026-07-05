# Design: HTML Report Blocks Content

## Technical Approach
We will implement HTML report generation and new analysis blocks directly inside the Fathom CLI. We will add a new `--html <file>` flag to `fathom analyze` that generates a self-contained HTML page using Go `html/template` and `embed`. This report aggregates:
1. **Verdict Block**: Overall safety verdict.
2. **Build-Break Findings Block**: Signature mismatches with before/after code drawers.
3. **Blast Radius Block**: Callers enriched with dependency types.
4. **Dead Code Block**: Added/modified symbols with zero references and language-specific confidence levels.

---

## Architecture Decisions

### Standalone HTML Generation
| Option | Tradeoff | Decision |
|--------|----------|----------|
| Embedded Go `html/template` + inline CSS/JS | **Pros**: Self-contained, single-file generation, zero dependencies.<br>**Cons**: Requires rebuilding CLI for UI changes. | **Chosen**. Provides the cleanest local developer experience and simplest CI integration. |
| External CLI/Web App | **Pros**: Independent UI iteration.<br>**Cons**: Multi-tool installation and execution overhead for users. | **Rejected**. |

### Language-Specific Export Resolution
| Option | Tradeoff | Decision |
|--------|----------|----------|
| Heuristic per-language parser/store checks | **Pros**: Fast and fits existing database models.<br>**Cons**: May require custom heuristics for edge cases. | **Chosen**. We resolve export status per language:<br>- **Go**: Check if first rune of symbol name is uppercase.<br>- **JS/TS**: Query database for export statement with same name/file.<br>- **Python**: Check if name starts with `_`. (If not, public).<br>- **Rust**: Check for `pub ` prefix in content.<br>- **Java**: Check for `public ` modifier in content.<br>- **C/C++**: Check if content does not start with `static `.<br>- **PHP/Ruby**: Public by default unless marked private. |
| Full static scope analysis | **Pros**: High precision.<br>**Cons**: Extremely high implementation complexity. | **Rejected**. |

### Verdict Block Derivation Logic
| Option | Tradeoff | Decision |
|--------|----------|----------|
| CLEAN iff 0 mismatches AND 0 direct callers | **Pros**: High safety; catches changes that might not have syntax mismatches but still need review.<br>**Cons**: More conservative. | **Chosen**. If either signature mismatches exist or direct callers are affected, the verdict is `REVIEW`. Otherwise, it is `CLEAN`. |
| CLEAN iff 0 mismatches | **Pros**: Less noisy.<br>**Cons**: Misses high-impact changes that compile but alter behavior. | **Rejected**. |

---

## Data Flow

```
  [CLI Flags] (--base, --json, --html, files...)
       │
       ▼
  [diff.AlignSymbols / parser.ParseFile] ──► Extract Changed Symbols
       │
       ├─────────────────────────┼─────────────────────────┐
       ▼                         ▼                         ▼
 [impact.Engine]        [mismatch.Engine]        [deadcode.Scanner]
 (Blast Radius)         (Sig Mismatches)          (Unused Symbols)
       │                         │                         │
       └─────────────────────────┼─────────────────────────┘
                                 ▼
                       [report.GeneratePayload]
                                 │
                       ┌─────────┴─────────┐
                       ▼                   ▼
                 [JSON Output]       [HTML Template]
                                           │ (embed)
                                           ▼
                                     [Static HTML File]
```

---

## Interfaces / Contracts

### `internal/deadcode` Scanner Interface
[internal/deadcode/scanner.go](file:///home/alejandro/dev/Fathom/internal/deadcode/scanner.go)
```go
package deadcode

import "github.com/Fathom/internal/symbol"

type Confidence string

const (
	ConfidenceHigh   Confidence = "High"
	ConfidenceMedium Confidence = "Medium"
	ConfidenceLow    Confidence = "Low"
)

type DeadSymbol struct {
	Symbol     symbol.Symbol `json:"symbol"`
	Confidence Confidence    `json:"confidence"`
	Reason     string        `json:"reason"`
}

type Scanner interface {
	Scan(changedSymbols []symbol.Symbol) ([]DeadSymbol, error)
}
```

### `internal/report` Data Models
[internal/report/report.go](file:///home/alejandro/dev/Fathom/internal/report/report.go)
```go
package report

import (
	"github.com/Fathom/internal/impact"
	"github.com/Fathom/internal/mismatch"
	"github.com/Fathom/internal/symbol"
)

type ReportPayload struct {
	Verdict     VerdictBlock     `json:"verdict"`
	Findings    FindingsBlock    `json:"findings"`
	BlastRadius BlastRadiusBlock `json:"blast_radius"`
	DeadCode    DeadCodeBlock    `json:"dead_code"`
}

type VerdictBlock struct {
	Verdict string `json:"verdict"` // "CLEAN" or "REVIEW"
	Summary string `json:"summary"`
}

type Finding struct {
	SymbolName        string              `json:"symbol_name"`
	File              string              `json:"file"`
	ChangeDescription string              `json:"change_description"`
	OldContent        string              `json:"old_content"`
	NewContent        string              `json:"new_content"`
	Mismatches        []mismatch.Mismatch `json:"mismatches"`
}

type FindingsBlock struct {
	Findings []Finding `json:"findings"`
}

type BlastRadiusBlock struct {
	DirectlyAffected     []impact.AffectedSymbol `json:"directly_affected"`
	TransitivelyAffected []impact.AffectedSymbol `json:"transitively_affected"`
	AffectedFiles        []string                `json:"affected_files"`
}

type DeadCodeBlock struct {
	DeadSymbols []DeadSymbol `json:"dead_symbols"`
}
```

### Code Drawer Fallback UI Representation
When rendering the collapsible code drawers in HTML, fallback strings must be in English:
- If a symbol is newly added (e.g. no database record exists for the old symbol): `OldContent` is empty → display `[ Code Not Available ]` in the "Antes" drawer.
- If a symbol has been deleted: `NewContent` is empty → display `[ Symbol Deleted ]` in the "Después" drawer.
- If symbol content is missing for any other reason: display `[ Code Not Available ]`.

---

## File Changes

| File | Action | Description |
|------|--------|-------------|
| [cmd/analyze.go](file:///home/alejandro/dev/Fathom/cmd/analyze.go) | Modify | Add `--html <file>` flag. Aggregate all blocks into `ReportPayload` and route to JSON/HTML output. |
| [internal/impact/engine.go](file:///home/alejandro/dev/Fathom/internal/impact/engine.go) | Modify | Update `AffectedSymbol` with `DependencyType`. Resolve to `"direct_call"`, `"interface_call"`, or `"struct_embedding"`. |
| `internal/deadcode/scanner.go` | Create | Implement `Scanner` to check for unused modified symbols and assign confidence/reason per language. |
| `internal/report/report.go` | Create | Define report models and aggregate mismatches, blast radius, and dead code. |
| `internal/report/template.go` | Create | Embed HTML/CSS/JS template for the static report. |

---

## Testing Strategy

| Layer | What to Test | Approach |
|-------|-------------|----------|
| Unit | `deadcode.Scanner` export resolution | Mock store & symbols for Go, JS, Rust, Python, etc. Verify confidence ratings. |
| Unit | `impact.Engine` dependency types | Mock reference kinds and symbol kinds. Verify direct/interface call and struct embedding calculation. |
| Unit | `report.Generate` & HTML render | Verify payload generation, verdict logic, and template output validation. |
| Integration | `fathom analyze --html` flag | Run CLI on test repositories and assert output HTML file is written and contains core blocks. |
