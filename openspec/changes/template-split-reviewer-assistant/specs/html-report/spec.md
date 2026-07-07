# Delta for html-report

## ADDED Requirements

### Requirement: Composable Template Layout

The report engine MUST split the monolithic `report.html` into a template set composed of one layout file and partials. The embed directive MUST glob `report.html report_*.html` and `Render` MUST parse the embedded files as a single template set via `template.ParseFS`. Each partial MUST be wrapped in a `{{ define "name" }}...{{ end }}` block and composed through `{{ template "name" . }}`. The rendered output MUST remain a single self-contained HTML file with no external URLs.

#### Scenario: Template set composes all partials

- GIVEN the 8 template files exist: `report.html`, `report_css.html`, `report_summary.html`, `report_findings.html`, `report_blast.html`, `report_deadcode.html`, `report_reviewer.html`, `report_js.html`
- WHEN `Render` parses the embedded filesystem with `template.ParseFS(embedFS, "report.html", "report_*.html")` and executes against a `ReportPayload`
- THEN the output MUST contain the layout shell (head, body, container) AND every partial body
- AND the output MUST NOT load any `http://` or `https://` resource

#### Scenario: Recursive fileTreeDir partial survives the split

- GIVEN `report_findings.html` defines `{{ define "fileTreeDir" }}` recursively and calls `{{ template "fileTreeDir" . }}`
- WHEN the template set is parsed and rendered with `FileGroups` containing nested paths
- THEN the file tree MUST render identically to the pre-split output

#### Scenario: Layout file imports all partials

- GIVEN `report.html` contains only the head, body open/close, container, and `{{ template }}` import directives
- WHEN rendered
- THEN no CSS rules, JS scripts, or section markup SHALL live in `report.html` directly

### Requirement: Expanded Summary Block

`SummaryBlock` MUST carry classification counts derived from findings and blast radius: `BreakingCount`, `OverrideCount`, `InternalCount`, plus `DirectCallers` and `TransitiveCallers`. `Compile` MUST populate these fields without a separate counting pass.

#### Scenario: Counts reflect classified findings

- GIVEN findings where 2 changed signatures have callers and 1 is an override
- WHEN `Compile` runs
- THEN `Summary.BreakingCount` MUST equal 2
- AND `Summary.OverrideCount` MUST equal 1
- AND `Summary.InternalCount` MUST equal the count of findings with no callers in the blast radius

#### Scenario: Caller counts come from blast

- GIVEN a blast with 3 `DirectlyAffected` and 5 `TransitivelyAffected`
- WHEN `Compile` runs
- THEN `Summary.DirectCallers` MUST equal 3
- AND `Summary.TransitiveCallers` MUST equal 5

#### Scenario: Empty input yields zero counts

- GIVEN an empty blast, no mismatches, no dead symbols
- WHEN `Compile` runs
- THEN `Summary` MUST equal the zero value of `SummaryBlock` (all new fields zero)

### Requirement: Reviewer Assistant Block

`Compile` MUST produce a `ReviewAssistantBlock` containing `ImpactTable []ImpactRow`, `Questions []ReviewerQuestion`, and `Actions []RecommendedAction`. The block MUST be attached to `ReportPayload.ReviewAssistant`. When there are no findings and no dead symbols, all three slices MUST be empty (non-nil if Go requires, empty otherwise).

#### Scenario: Impact table sorted by impact

- GIVEN two findings: `Foo` with 5 callers and `Bar` with 2 callers, both breaking
- WHEN `Compile` runs
- THEN `ReviewAssistant.ImpactTable[0].SymbolName` MUST equal `Foo`
- AND within equal `CallerCount`, breaking rows MUST precede non-breaking rows

#### Scenario: Empty state

- GIVEN no mismatches and no dead symbols
- WHEN `Compile` runs
- THEN `ReviewAssistant.ImpactTable`, `Questions`, and `Actions` MUST each have length 0

### Requirement: Impact Row Classification

Each `ImpactRow` MUST be classified with `ChangeType` set to one of `breaking`, `override`, `internal`. `Compile` MUST compute `CallerCount` from `Finding.AffectedCallers` (the cross-referenced set) and `AffectedFilesCount` from the deduplicated set of caller files.

#### Scenario: Breaking classification

- GIVEN a finding with `MismatchArity` or `MismatchTypeMismatch` and 2+ callers
- WHEN `Compile` builds the impact row
- THEN `ChangeType` MUST equal `breaking`
- AND `CallerCount` MUST equal `len(Finding.AffectedCallers)`

#### Scenario: Override classification

- GIVEN a finding whose mismatches contain `MismatchOverride`
- WHEN `Compile` builds the impact row
- THEN `ChangeType` MUST equal `override`

#### Scenario: Internal classification

- GIVEN a finding with zero callers in the blast radius
- WHEN `Compile` builds the impact row
- THEN `ChangeType` MUST equal `internal`
- AND `CallerCount` MUST equal 0

### Requirement: Reviewer Question Rules

`Compile` MUST generate `ReviewerQuestion` entries from findings according to the rules below. Each question MUST carry a `Category`. Thresholds gate noise: rules SHALL NOT fire below their stated minimums.

| Rule | Condition | Category | Text template |
|------|-----------|----------|---------------|
| Signature callers | `CallerCount >= 2` AND `ChangeType == breaking` | `signature` | ``{Symbol}` has {N} callers and changed its signature — did you verify all callers?` |
| Override contract | `ChangeType == override` | `override` | ``{Symbol}` is an override — does the parent contract still match?` |
| No callers in blast | `CallerCount == 0` AND finding exists (not dead) | `internal` | ``{Symbol}` has no callers in the blast radius — is it dead code or was the index stale?` |
| Spread change | `AffectedFilesCount >= 3` | `spread` | ``{Symbol}` affects {N} files — consider splitting this change` |

`{Symbol}` is the finding `SymbolName`; `{N}` is `CallerCount` or `AffectedFilesCount` respectively. A single finding MAY produce multiple questions.

#### Scenario: Signature caller question fires

- GIVEN a breaking finding with `CallerCount == 3`
- WHEN `Compile` generates questions
- THEN `Questions` MUST contain an entry whose `Category == "signature"` and text contains `"3 callers"`
- AND the text MUST contain `"did you verify all callers?"`

#### Scenario: Override question fires

- GIVEN an override finding
- WHEN `Compile` generates questions
- THEN `Questions` MUST contain an entry with `Category == "override"` and text containing `"parent contract still match"`

#### Scenario: Below threshold does not fire

- GIVEN a breaking finding with `CallerCount == 1`
- WHEN `Compile` generates questions
- THEN `Questions` MUST NOT contain a `signature`-category question for that symbol

#### Scenario: Spread question uses affected files count

- GIVEN a finding with `AffectedFilesCount == 4`
- WHEN `Compile` generates questions
- THEN `Questions` MUST contain a `spread` entry whose text contains `"4 files"`

### Requirement: Recommended Action Rules

`Compile` MUST generate `RecommendedAction` entries from findings and dead symbols. Each action MUST carry a `Category`.

| Rule | Source | Category | Text template |
|------|--------|----------|---------------|
| Review calls | Finding with `CallerCount >= 1` | `review` | `Review calls to `{Symbol}` in {N} files: {file1}, {file2}, ...` |
| Verify dead | Dead symbol | `deadcode` | `Verify `{Symbol}` is no longer used (dead code, {confidence})` |
| Check override | Finding with `ChangeType == override` | `override` | `Check override contract for `{Symbol}` in {file}` |

For the review-calls rule, files come from the deduplicated caller files (max first 5, comma-separated). For dead symbols, `{confidence}` is `DeadSymbol.Confidence` (`High`/`Medium`/`Low`).

#### Scenario: Review-calls action lists caller files

- GIVEN a finding with `CallerCount == 2` across files `a.go`, `b.go`
- WHEN `Compile` generates actions
- THEN `Actions` MUST contain a `review` entry whose text contains both `a.go` and `b.go`

#### Scenario: Verify dead action fires for each dead symbol

- GIVEN 3 dead symbols with mixed confidence
- WHEN `Compile` generates actions
- THEN `Actions` MUST contain 3 `deadcode` entries, one per symbol, each echoing its confidence

#### Scenario: Override action includes the finding file

- GIVEN an override finding in `iface.go`
- WHEN `Compile` generates actions
- THEN `Actions` MUST contain an `override` entry whose text contains `iface.go`

### Requirement: Reviewer Assistant Rendering

The HTML MUST include a Reviewer Assistant section rendering the impact table, questions, and actions in English. The section MUST be present even when empty (rendering a "no reviewer notes" placeholder in English).

#### Scenario: Section renders populated

- GIVEN a payload with 2 impact rows, 3 questions, 1 action
- WHEN rendered
- THEN the HTML MUST contain `Reviewer Assistant`, an impact table with 2 rows, the 3 question texts, and the 1 action text

#### Scenario: Section renders empty placeholder

- GIVEN an empty `ReviewAssistantBlock`
- WHEN rendered
- THEN the HTML MUST contain `Reviewer Assistant` AND an English placeholder indicating no reviewer notes

## MODIFIED Requirements

### Requirement: Self-Contained HTML Generation

The report engine MUST render analysis results into a single HTML file from a composable template set parsed via `template.ParseFS` over the embedded `report.html report_*.html` glob. The page design and content MUST be entirely self-contained with no external CSS, JavaScript, or font network dependencies.

(Previously: single-file `report.html` parsed from a `string` embed; now an 8-file template set parsed as one set while remaining self-contained.)

#### Scenario: Generating complete HTML

- GIVEN a valid analysis output containing Verdict, Findings, Blast Radius, Dead Code, and ReviewAssistant blocks
- WHEN rendering the report to HTML via the parsed template set
- THEN a single HTML file MUST be generated
- AND the file MUST contain embedded inline CSS and JS
- AND no external assets (HTTP/HTTPS) SHALL be loaded

#### Scenario: All four original blocks still render

- GIVEN a successful Fathom analysis result
- WHEN rendering the HTML page from the template set
- THEN the output MUST include the Verdict, Build-Break Findings, Blast Radius, and Dead Code blocks in English
- AND their rendered content MUST match the pre-split output byte-for-byte for the unchanged sections

### Requirement: Block Rendering

The generated HTML page MUST contain the original four blocks plus a fifth Reviewer Assistant block:
1. Verdict Block: CLEAN/REVIEW verdict based on mismatches & direct callers.
2. Build-Break Findings Block: Mismatches with before/after symbol source code and call sites.
3. Blast Radius Block: Transitive impacts with resolved dependency types.
4. Dead Code Block: Unused symbols with confidence levels and reasons.
5. Reviewer Assistant Block: Prioritized impact table, reviewer questions, and recommended actions.
All text labels and headings in the report MUST be in English.

(Previously: four blocks; now five with the Reviewer Assistant added.)

#### Scenario: Render all five blocks

- GIVEN a successful Fathom analysis result with findings
- WHEN rendering the HTML page from the template set
- THEN the output MUST include the Verdict, Build-Break Findings, Blast Radius, Dead Code, and Reviewer Assistant blocks in English

## Test Strategy

- `TestCompileReviewAssistantImpactOrder`: 2 breaking findings with different caller counts; assert ordering (desc by `CallerCount`, breaking first) and field population.
- `TestCompileReviewAssistantQuestions`: cover each rule firing and the below-threshold non-firing case.
- `TestCompileReviewAssistantActions`: assert review-calls file listing, per-dead-symbol action count, override file echo.
- `TestCompileReviewAssistantEmpty`: empty inputs yield zero-length `ImpactTable`, `Questions`, `Actions`.
- `TestCompileExpandedSummary`: assert `BreakingCount`, `OverrideCount`, `InternalCount`, `DirectCallers`, `TransitiveCallers` for a mixed payload.
- `TestCompileAndRender` (MODIFIED): add assertions for `Reviewer Assistant` section, impact table, questions, actions; add reviewer empty-placeholder check to the empty-payload branch; keep all existing assertions for the unchanged blocks to guard byte-level parity across the split.