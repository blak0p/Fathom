# Proposal: HTML Report Blocks Content

## Intent
Provide rich, visual HTML reports and detailed analysis blocks (Verdict, Findings, Blast Radius, Dead Code) directly from the Fathom CLI to make pull request code reviews safer and faster.

## Scope

### In Scope
- Implement `--html <file>` flag for `fathom analyze`.
- **Verdict Block**: CLEAN/REVIEW based on mismatches & direct callers.
- **Build-Break Findings Block**: Group mismatches with before/after symbol source code and call sites.
- **Blast Radius Block**: Resolve dependency types (`direct_call`, `interface_call`, `struct_embedding`).
- **Dead Code Block**: Flag changed symbols with 0 references and assign confidence (High/Medium/Low).

### Out of Scope
- Interactive code comments/editing via the HTML page.
- Direct integration with third-party hosting services (e.g. S3 uploads) from the CLI.

## Capabilities

### New Capabilities
- `dead-code`: Identify modified/added symbols with zero references and assign confidence.
- `html-report`: Render the 4 blocks into a static HTML page using Go `html/template`.

### Modified Capabilities
- `cli-analyze`: Support `--html <file>` flag and update JSON/stdout report structure.
- `blast-radius`: Calculate and expose dependency types (`direct_call`, `interface_call`, `struct_embedding`).

## Approach
Implement all logic in Go. Extend `mismatch` and `blast-radius` packages. Add a new `deadcode` package for reference checking. Add a `report` package to define the visual data models and render static HTML via embedded Go templates (`embed`). The generated HTML page will be 100% self-contained (all CSS/JS embedded) for easy sharing and CI integration.

Exported status for dead code check will be resolved per language by each language extractor (e.g. uppercase for Go, `export` keyword for JS/TS).
If a symbol's content is empty, the HTML code comparison drawer will display `[ Symbol Deleted ]` or `[ Code Not Available ]` as fallback placeholders in English.

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `cmd/analyze.go` | Modified | Support `--html <file>` flag and call report engine |
| `internal/mismatch/` | Modified | Extract signatures, descriptions, and source contents |
| `internal/impact/` | Modified | Add dependency type resolution to Blast Radius calculation |
| `internal/deadcode/` | New | Scanner for unused modified/added symbols |
| `internal/report/` | New | Model definition and HTML template rendering |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| False positives in public API dead code | Med | Mark public symbols as Low/Medium confidence |
| Memory/performance overhead on large diffs | Low | Limit database lookups strictly to the changed diff set |

## Rollback Plan
Revert git commits back to the previous stable release. The CLI does not perform any database schema writes during analysis, making it stateless and safe to rollback immediately.

## Dependencies
- Go `embed` package (standard library).

## Success Criteria
- [ ] `fathom analyze --html report.html` runs without error and generates a valid HTML file.
- [ ] Generated HTML contains Verdict (CLEAN/REVIEW), Build-Break Findings (with collapsible code drawers), Blast Radius (with dependency types), and Dead Code blocks. All labels and content must be in English.
- [ ] JSON output includes the new metrics/blocks.
