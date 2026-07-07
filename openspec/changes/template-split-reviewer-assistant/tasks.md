# Tasks: Template Split + Reviewer Assistant

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | ~350-450 (new) + ~1126 (moved) |
| 400-line budget risk | Low (new content only) |
| Chained PRs recommended | No |
| Suggested split | Single PR |
| Delivery strategy | exception-ok |
| Chain strategy | size-exception |

Decision needed before apply: No
Chained PRs recommended: No
Chain strategy: size-exception
400-line budget risk: Low

> **Note**: ~1126 lines are moved verbatim from `report.html` to partials — mechanical split, not new content. Truly new content is ~370 lines (types, helpers, reviewer template, tests). User has accepted `size:exception` for this cycle.

## Phase 1: Data Model — New Types & Expanded Summary

- [x] 1.1 Add `ImpactRow`, `ReviewerQuestion`, `RecommendedAction`, `ReviewAssistantBlock` structs to `internal/report/report.go`
- [x] 1.2 Expand `SummaryBlock` with `BreakingCount`, `OverrideCount`, `InternalCount`, `DirectCallers`, `TransitiveCallers`
- [x] 1.3 Add `ReviewAssistant ReviewAssistantBlock` field to `ReportPayload`

## Phase 2: Compile() Logic — Reviewer Assistant Builder

- [x] 2.1 Add helpers to `internal/report/report.go`: `classifyChangeType()`, `dedupCallerFiles()`, `changeTypeRank()`, `firstN()`
- [x] 2.2 Add `buildReviewAssistantBlock()` to `internal/report/report.go` — builds ImpactTable (sorted), Questions, Actions from findings + dead symbols
- [x] 2.3 Update `Compile()` to call `buildReviewAssistantBlock()` and populate expanded `SummaryBlock` fields

## Phase 3: Template Split — Create Partials

- [x] 3.1 Create `internal/report/report_css.html` — inline `<style>` block (verbatim from current `report.html` lines 6–548)
- [x] 3.2 Create `internal/report/report_summary.html` — executive summary cards + risk score (lines 574–607)
- [x] 3.3 Create `internal/report/report_findings.html` — findings block + `fileTreeDir` recursive partial (lines 609–736, 805–818)
- [x] 3.4 Create `internal/report/report_blast.html` — blast radius block (lines 738–764)
- [x] 3.5 Create `internal/report/report_deadcode.html` — dead code block (lines 766–801)
- [x] 3.6 Create `internal/report/report_js.html` — inline `<script>` block (lines 549–560, 820–1154)
- [x] 3.7 Shrink `internal/report/report.html` to layout shell: `<!DOCTYPE>`, `<head>` meta/title, `{{ template "css" . }}`, body/container, `{{ template }}` imports for all 6 partials, closing tags

## Phase 4: Template Parsing — embed.FS + ParseFS

- [x] 4.1 Update `internal/report/template.go` — replace `//go:embed report.html` + `string` with `embed.FS` + `//go:embed report.html report_*.html`
- [x] 4.2 Update `Render()` to use `template.ParseFS(embedFS, "report.html", "report_*.html")` instead of `Parse(rawTemplate)`

## Phase 5: Reviewer Assistant Template

- [x] 5.1 Create `internal/report/report_reviewer.html` — `{{ define "reviewer" }}` with impact table, questions list, actions list, and empty-state placeholder

## Phase 6: Tests

- [x] 6.1 Add `TestCompileReviewAssistantImpactOrder` — 2 breaking findings with 5 and 2 callers; assert desc order and breaking-first tie-break
- [x] 6.2 Add `TestCompileReviewAssistantQuestions` — signature (>=2 callers), override, no-callers, spread (>=3 files), below-threshold skip
- [x] 6.3 Add `TestCompileReviewAssistantActions` — review-calls file list cap 5, per-dead-symbol action count, override file echo
- [x] 6.4 Add `TestCompileReviewAssistantEmpty` — empty inputs yield zero-length ImpactTable, Questions, Actions
- [x] 6.5 Add `TestCompileExpandedSummary` — assert BreakingCount, OverrideCount, InternalCount, DirectCallers, TransitiveCallers
- [x] 6.6 Update `TestCompileAndRender` — add `Reviewer Assistant` section assertions, impact table, questions, actions; add empty placeholder check to empty branch; keep all existing assertions for byte-level parity
