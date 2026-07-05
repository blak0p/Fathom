# Tasks: HTML Report Blocks Content

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | 250-350 |
| 400-line budget risk | Medium |
| Chained PRs recommended | No |
| Suggested split | Single PR |
| Delivery strategy | ask-on-risk |
| Chain strategy | pending |

Decision needed before apply: Yes
Chained PRs recommended: No
Chain strategy: pending
400-line budget risk: Medium

## Phase 1: Foundation & Blast Radius Extensions

- [x] 1.1 Extend `AffectedSymbol` struct in [internal/impact/engine.go](file:///home/alejandro/dev/Fathom/internal/impact/engine.go) to add `DependencyType` string field.
- [x] 1.2 Implement dependency type resolution in `Calculate()` in [internal/impact/engine.go](file:///home/alejandro/dev/Fathom/internal/impact/engine.go) mapping to `"direct_call"`, `"interface_call"`, or `"struct_embedding"`.

## Phase 2: Core Analysis & Report Packages

- [x] 2.1 Create [internal/deadcode/scanner.go](file:///home/alejandro/dev/Fathom/internal/deadcode/scanner.go) with `Scanner` interface and `Scan()` checking for unused symbols with confidence per language.
- [x] 2.2 Create [internal/report/report.go](file:///home/alejandro/dev/Fathom/internal/report/report.go) defining data structures for `ReportPayload`, `VerdictBlock`, `FindingsBlock`, `BlastRadiusBlock`, `DeadCodeBlock`, and the report compiler.
- [x] 2.3 Create [internal/report/template.go](file:///home/alejandro/dev/Fathom/internal/report/template.go) with embedded static HTML/CSS/JS template using `html/template` and `embed`.
- [x] 2.4 Add code drawer fallback strings in [internal/report/template.go](file:///home/alejandro/dev/Fathom/internal/report/template.go) for missing content (`[ Code Not Available ]` and `[ Symbol Deleted ]`).

## Phase 3: CLI Wiring

- [x] 3.1 Modify [cmd/analyze.go](file:///home/alejandro/dev/Fathom/cmd/analyze.go) to add `--html <file>` flag and compile/render the report.
- [x] 3.2 Implement verdict calculation in [cmd/analyze.go](file:///home/alejandro/dev/Fathom/cmd/analyze.go): `REVIEW` if signature mismatches or direct callers exist, else `CLEAN`.
- [x] 3.3 Update JSON report output in [cmd/analyze.go](file:///home/alejandro/dev/Fathom/cmd/analyze.go) to output new fields.

## Phase 4: Testing & Verification

- [x] 4.1 Add unit tests in [internal/impact/engine_test.go](file:///home/alejandro/dev/Fathom/internal/impact/engine_test.go) verifying Direct, Transitive, Interface, and Struct Embedding scenarios.
- [x] 4.2 Add unit tests in `internal/deadcode/scanner_test.go` verifying export resolution and confidence levels across supported languages (Go, JS/TS, Python, Rust, Java, C/C++).
- [x] 4.3 Add unit tests in `internal/report/report_test.go` to test payload building and HTML template rendering.
- [x] 4.4 Add integration tests in [cmd/analyze_test.go](file:///home/alejandro/dev/Fathom/cmd/analyze_test.go) verifying the `--html` flag and updated JSON/human outputs.
