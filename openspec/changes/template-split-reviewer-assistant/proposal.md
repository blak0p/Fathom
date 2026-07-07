# Proposal: Template Split + Reviewer Assistant

## Intent

Split the monolithic 1156-line HTML template into composable partials and add a Reviewer Assistant section that generates prioritized impact tables, rule-driven questions, and concrete review actions from findings + blast data.

## Scope

### In Scope
- Split `report.html` into 8 files: layout, CSS, summary, findings, blast, deadcode, reviewer, JS
- Add `ReviewAssistantBlock` with `ImpactTable`, `Questions`, `Actions` to `ReportPayload`
- Expand `SummaryBlock` with `BreakingCount`, `OverrideCount`, `InternalCount`, `DirectCallers`, `TransitiveCallers`
- Implement generation logic in `Compile()`: classify change type, count callers, sort by impact, generate questions/actions
- Update `//go:embed` to glob `report.html report_*.html` and parse as template set
- Add `TestCompileReviewAssistant` and update existing tests for new fields

### Out of Scope
- Risk score 0-100
- CI verdict behavior
- Changes to `cmd/analyze.go` or JSON output
- Existing risk-tone visual in summary

## Capabilities

### Modified Capabilities
- `html-report`: new Reviewer Assistant section, split template structure, expanded summary fields

## Approach

**Template split**: Change `//go:embed report.html` to `//go:embed report.html report_*.html`. Parse with `template.New("report").Funcs(...).ParseFS(embedFS, "report.html", "report_*.html")` — Go's `template.ParseFS` supports globs. Each partial uses `{{ define "name" }}` / `{{ template "name" . }}` for composition. The `fileTreeDir` template stays in `report_findings.html`.

**Data model**: Add types in `report.go`:

```go
type ImpactRow struct {
    SymbolName, File, ChangeType string
    CallerCount, AffectedFilesCount int
}

type ReviewerQuestion struct {
    Text, Category string
}

type RecommendedAction struct {
    Text, Category string
}

type ReviewAssistantBlock struct {
    ImpactTable []ImpactRow
    Questions   []ReviewerQuestion
    Actions     []RecommendedAction
}
```

**Generation rules in `Compile()`**:
1. Iterate findings, classify each: `breaking` (arity/type mismatch), `override` (MismatchOverride), `internal` (no callers in blast). Count callers from `AffectedCallers` + blast depth.
2. Sort `ImpactTable` by `CallerCount` desc, breaking first.
3. Generate questions from rules (see below).
4. Generate actions from rules (see below).

**Question rules**:
- `{Symbol}` has {N} callers and changed its signature → "did you verify all callers?"
- `{Symbol}` is an override → "does the parent contract still match?"
- `{Symbol}` has no callers in blast radius → "is it dead code or was the index stale?"
- `{Symbol}` affects {N} files → "consider splitting this change"

**Action rules**:
- "Review calls to `{Symbol}` in {N} files: {file1}, {file2}, ..."
- "Verify `{Symbol}` is no longer used (dead code, {confidence})"
- "Check override contract for `{Symbol}` in {file}"

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `internal/report/report.go` | Modified | New types, expanded SummaryBlock, generation logic |
| `internal/report/template.go` | Modified | Embed glob, ParseFS, template set |
| `internal/report/report.html` | Split | Becomes layout-only (head, body, imports) |
| `internal/report/report_*.html` | New | 7 partial files |
| `internal/report/report_test.go` | Modified | New tests, updated assertions |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Template glob breaks on some Go versions | Low | `ParseFS` glob support stable since Go 1.16; Fathom uses 1.26 |
| Question/action generation produces noise for small changes | Med | Gate on thresholds: skip questions when callers < 2, skip "split" when files < 3 |
| Existing `TestCompileAndRender` breaks on new template structure | Med | Update expected substrings; add reviewer section checks |

## Rollback Plan

1. Revert `template.go` to single `//go:embed report.html` + `Parse(rawTemplate)`
2. Delete `report_*.html` files, restore original `report.html`
3. Remove `ReviewAssistantBlock` from `ReportPayload`, revert `SummaryBlock` fields
4. Remove reviewer generation from `Compile()`
5. Revert test changes

## Dependencies

- PR 1 and PR 2 merged to main (confirmed)

## Success Criteria

- [ ] Template renders all 5 existing sections identically to pre-split output
- [ ] Reviewer Assistant section appears with impact table, questions, and actions
- [ ] `TestCompileAndRender` passes with new section checks
- [ ] `TestCompileReviewAssistant` covers question generation, impact ordering, empty state
- [ ] HTML remains self-contained (no external URLs)
