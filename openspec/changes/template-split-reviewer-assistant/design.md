# Design: Template Split + Reviewer Assistant

## Technical Approach

Split the monolithic `internal/report/report.html` into a Go `embed.FS` template set and add a `ReviewAssistantBlock` generated inside `Compile()`. The layout file pulls in named partials via `{{ template }}`; `template.go` parses the set with `template.ParseFS`. The reviewer block is computed by reusing the already-built `[]Finding` slice and the `dead` symbols slice, so no extra database work is required.

## Architecture Decisions

| Decision | Alternatives | Rationale |
|----------|--------------|-----------|
| Use `embed.FS` + `template.ParseFS` with glob `report.html report_*.html` | Keep a single string embed and `strings` split | `ParseFS` is the idiomatic Go way to parse multiple templates from embedded files; glob keeps the directive declarative |
| Layout file `report.html` only imports partials | Each partial also self-executes | Guarantees one render entry point and makes the dependency graph explicit |
| `fileTreeDir` stays in `report_findings.html` | Move to a separate helper file | The recursive template is only used in the findings block; keeping it there minimizes file count and matches the spec |
| Reviewer generation consumes the already-built `findings` slice | Re-scan the blast result separately | Avoids duplicate cross-reference logic and keeps caller/file counting in one place |
| `ImpactTable` sorted by `CallerCount` desc, then breaking > override > internal | Sort only by symbol name | Prioritizes the highest-impact changes first, which is the core value of the assistant |

## Data Flow

```
Compile(...) ReportPayload
  ├─ findings []Finding ──┐
  │                        ├─> classifyChangeType()
  │                        ├─> deduplicateCallerFiles()
  │                        └─> buildReviewAssistantBlock()
  │                                 ├─ ImpactTable (sorted)
  │                                 ├─ Questions
  │                                 └─ Actions
  └─ dead []DeadSymbol ────┘
                           │
                           v
              template.ParseFS(embedFS, "report.html", "report_*.html")
                           │
                           v
              Render(w, payload) → single self-contained HTML
```

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/report/report.go` | Modify | Add `ImpactRow`, `ReviewerQuestion`, `RecommendedAction`, `ReviewAssistantBlock`; expand `SummaryBlock`; populate in `Compile()` |
| `internal/report/template.go` | Modify | Replace `//go:embed report.html` and `string` var with `embed.FS`; use `template.ParseFS` |
| `internal/report/report.html` | Modify | Shrink to layout shell only: head, body, container, `{{ template }}` imports |
| `internal/report/report_css.html` | Create | Inline `<style>` block (moved from `report.html`) |
| `internal/report/report_summary.html` | Create | Executive summary cards + risk tone |
| `internal/report/report_findings.html` | Create | Findings block, file tree recursive partial |
| `internal/report/report_blast.html` | Create | Blast radius block + affected files list |
| `internal/report/report_deadcode.html` | Create | Dead code analysis table |
| `internal/report/report_reviewer.html` | Create | Reviewer Assistant section |
| `internal/report/report_js.html` | Create | Inline `<script>` block (theme toggle, search, LCS diff) |
| `internal/report/report_test.go` | Modify | Add assistant/summary tests; update `TestCompileAndRender` |

## Interfaces / Contracts

### New Go types (in `internal/report/report.go`)

```go
type ImpactRow struct {
	SymbolName         string `json:"symbol_name"`
	File               string `json:"file"`
	ChangeType         string `json:"change_type"` // "breaking" | "override" | "internal"
	CallerCount        int    `json:"caller_count"`
	AffectedFilesCount int    `json:"affected_files_count"`
}

type ReviewerQuestion struct {
	Text     string `json:"text"`
	Category string `json:"category"` // "signature" | "override" | "internal" | "spread"
}

type RecommendedAction struct {
	Text     string `json:"text"`
	Category string `json:"category"` // "review" | "deadcode" | "override"
}

type ReviewAssistantBlock struct {
	ImpactTable []ImpactRow         `json:"impact_table"`
	Questions   []ReviewerQuestion  `json:"questions"`
	Actions     []RecommendedAction `json:"actions"`
}
```

### Expanded `SummaryBlock`

```go
type SummaryBlock struct {
	TotalFindings   int `json:"total_findings"`
	WarningCount    int `json:"warning_count"`
	AffectedFiles   int `json:"affected_files"`
	DeadCodeCount   int `json:"dead_code_count"`
	BreakingCount   int `json:"breaking_count"`
	OverrideCount   int `json:"override_count"`
	InternalCount   int `json:"internal_count"`
	DirectCallers   int `json:"direct_callers"`
	TransitiveCallers int `json:"transitive_callers"`
}
```

### `ReportPayload` addition

```go
type ReportPayload struct {
	Summary         SummaryBlock         `json:"summary"`
	Verdict         VerdictBlock         `json:"verdict"`
	Findings        FindingsBlock        `json:"findings"`
	BlastRadius     BlastRadiusBlock     `json:"blast_radius"`
	DeadCode        DeadCodeBlock        `json:"dead_code"`
	ReviewAssistant ReviewAssistantBlock `json:"review_assistant"`
}
```

## Compile() Logic

After the existing findings/fileGroups build, add:

```go
reviewAssistant := buildReviewAssistantBlock(findings, dead)

summary := SummaryBlock{
	TotalFindings:     len(findings),
	WarningCount:      len(findings),
	AffectedFiles:     len(blast.AffectedFiles),
	DeadCodeCount:     len(dead),
	BreakingCount:     reviewAssistant.BreakingCount,
	OverrideCount:     reviewAssistant.OverrideCount,
	InternalCount:     reviewAssistant.InternalCount,
	DirectCallers:     len(blast.DirectlyAffected),
	TransitiveCallers: len(blast.TransitivelyAffected),
}
```

`buildReviewAssistantBlock` pseudocode:

```go
func buildReviewAssistantBlock(findings []Finding, dead []deadcode.DeadSymbol) ReviewAssistantBlock {
    var rows []ImpactRow
    var questions []ReviewerQuestion
    var actions []RecommendedAction

    for _, f := range findings {
        changeType := classifyChangeType(f)
        files := dedupCallerFiles(f.AffectedCallers)
        row := ImpactRow{
            SymbolName:         f.SymbolName,
            File:               f.File,
            ChangeType:         changeType,
            CallerCount:        len(f.AffectedCallers),
            AffectedFilesCount: len(files),
        }
        rows = append(rows, row)

        // Questions
        if row.CallerCount >= 2 && changeType == "breaking" {
            questions = append(questions, ReviewerQuestion{
                Text:     fmt.Sprintf("`%s` has %d callers and changed its signature — did you verify all callers?", f.SymbolName, row.CallerCount),
                Category: "signature",
            })
        }
        if changeType == "override" {
            questions = append(questions, ReviewerQuestion{
                Text:     fmt.Sprintf("`%s` is an override — does the parent contract still match?", f.SymbolName),
                Category: "override",
            })
        }
        if row.CallerCount == 0 {
            questions = append(questions, ReviewerQuestion{
                Text:     fmt.Sprintf("`%s` has no callers in the blast radius — is it dead code or was the index stale?", f.SymbolName),
                Category: "internal",
            })
        }
        if row.AffectedFilesCount >= 3 {
            questions = append(questions, ReviewerQuestion{
                Text:     fmt.Sprintf("`%s` affects %d files — consider splitting this change", f.SymbolName, row.AffectedFilesCount),
                Category: "spread",
            })
        }

        // Actions
        if row.CallerCount >= 1 {
            actions = append(actions, RecommendedAction{
                Text:     fmt.Sprintf("Review calls to `%s` in %d files: %s", f.SymbolName, len(files), strings.Join(firstN(files, 5), ", ")),
                Category: "review",
            })
        }
        if changeType == "override" {
            actions = append(actions, RecommendedAction{
                Text:     fmt.Sprintf("Check override contract for `%s` in %s", f.SymbolName, f.File),
                Category: "override",
            })
        }
    }

    for _, d := range dead {
        actions = append(actions, RecommendedAction{
            Text:     fmt.Sprintf("Verify `%s` is no longer used (dead code, %s)", d.Symbol.Name, d.Confidence),
            Category: "deadcode",
        })
    }

    sort.Slice(rows, func(i, j int) bool {
        if rows[i].CallerCount != rows[j].CallerCount {
            return rows[i].CallerCount > rows[j].CallerCount
        }
        return changeTypeRank(rows[i].ChangeType) < changeTypeRank(rows[j].ChangeType)
    })

    return ReviewAssistantBlock{ImpactTable: rows, Questions: questions, Actions: actions}
}
```

Helpers:

```go
func classifyChangeType(f Finding) string {
    hasOverride := false
    for _, m := range f.Mismatches {
        if m.Type == mismatch.MismatchOverride {
            hasOverride = true
            break
        }
    }
    if hasOverride {
        return "override"
    }
    if len(f.AffectedCallers) == 0 {
        return "internal"
    }
    return "breaking"
}

func dedupCallerFiles(callers []impact.AffectedSymbol) []string {
    seen := map[string]bool{}
    var files []string
    for _, c := range callers {
        if !seen[c.File] {
            seen[c.File] = true
            files = append(files, c.File)
        }
    }
    return files
}

func changeTypeRank(t string) int {
    switch t {
    case "breaking": return 0
    case "override": return 1
    case "internal": return 2
    }
    return 3
}

func firstN(s []string, n int) []string {
    if len(s) <= n {
        return s
    }
    return s[:n]
}
```

## Template Structure

| File | Defines | Notes |
|------|---------|-------|
| `report.html` | `report` (root) | Only `<html>`, `<head>` meta, container, and `{{ template "..." . }}` calls |
| `report_css.html` | `css` | Entire `<style>` block from the current monolith |
| `report_summary.html` | `summary` | Executive summary cards + risk tone block |
| `report_findings.html` | `findings`, `fileTreeDir` | File tree recursive partial lives here |
| `report_blast.html` | `blast` | Blast graph container + affected files list |
| `report_deadcode.html` | `deadcode` | Dead code table |
| `report_reviewer.html` | `reviewer` | Impact table, questions, actions, empty placeholder |
| `report_js.html` | `js` | Closing `</body>` scripts |

`report.html` layout example:

```html
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>Fathom Impact &amp; Compatibility Report</title>
    {{ template "css" . }}
</head>
<body>
    <div class="container">
        {{ template "summary" . }}
        {{ template "findings" . }}
        {{ template "blast" . }}
        {{ template "deadcode" . }}
        {{ template "reviewer" . }}
    </div>
    {{ template "js" . }}
</body>
</html>
```

## Rendering Template Parsing

Update `internal/report/template.go`:

```go
import (
    "embed"
    "html/template"
    "io"
    "sort"
    "strings"
)

//go:embed report.html report_*.html
var embedFS embed.FS

func Render(w io.Writer, payload ReportPayload) error {
    t, err := template.New("report").Funcs(template.FuncMap{
        "splitPath":     splitPath,
        "buildFileTree": buildFileTree,
    }).ParseFS(embedFS, "report.html", "report_*.html")
    if err != nil {
        return err
    }
    return t.Execute(w, payload)
}
```

## Testing Strategy

| Layer | What to Test | Approach |
|-------|-------------|----------|
| Unit | `buildReviewAssistantBlock` / `Compile` impact ordering | `TestCompileReviewAssistantImpactOrder` — 2 breaking findings with 5 and 2 callers; assert desc order and breaking-first tie-break |
| Unit | Question rule thresholds | `TestCompileReviewAssistantQuestions` — signature (>=2 callers), override, no-callers, spread (>=3 files), and below-threshold skip |
| Unit | Action generation | `TestCompileReviewAssistantActions` — review-calls file list cap 5, dead symbol per-entry, override file echo |
| Unit | Empty state | `TestCompileReviewAssistantEmpty` — empty slices when no findings/dead |
| Unit | Expanded summary counts | `TestCompileExpandedSummary` — assert `BreakingCount`, `OverrideCount`, `InternalCount`, `DirectCallers`, `TransitiveCallers` |
| Integration | Full render parity + new section | Update `TestCompileAndRender` — keep all existing substring checks, add `Reviewer Assistant`, impact table, questions, actions; add empty placeholder check to empty branch |

## Migration / Rollout

No migration required. The change only affects the HTML report generation path; JSON output and CLI behavior are unchanged.

## Risks

| Risk | Mitigation |
|------|------------|
| Template glob expansion order is non-deterministic across platforms | All partials use `{{ define }}` blocks; only `report.html` executes, so parse order does not matter |
| `ParseFS` requires Go 1.16+ glob support | Fathom targets Go 1.26.4, stable |
| Byte-level parity claim in spec for unchanged sections | Keep CSS/JS verbatim during the split; add only the new reviewer block and `ReviewAssistant` field in payload |
| Question/action text drift from exact spec wording | Use the exact `fmt.Sprintf` templates listed in the Compile() logic section |

## Open Questions

None. The spec is approved and provides exact text templates and thresholds.
