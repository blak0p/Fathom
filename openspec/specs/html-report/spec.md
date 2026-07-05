# html-report Specification

## Purpose

Render Fathom analysis results into a single, self-contained HTML page using embedded Go templates. The HTML page contains all necessary CSS and JavaScript inline to remain portable and viewable offline.

## Requirements

### Requirement: Self-Contained HTML Generation

The report engine MUST render analysis results into a single HTML file. The page design and content MUST be entirely self-contained with no external CSS, JavaScript, or font network dependencies.

#### Scenario: Generating complete HTML
- GIVEN a valid analysis output containing Verdict, Findings, Blast Radius, and Dead Code blocks
- WHEN rendering the report to HTML
- THEN a single HTML file MUST be generated
- AND the file MUST contain embedded inline CSS and JS
- AND no external assets (HTTP/HTTPS) SHALL be loaded

### Requirement: Block Rendering

The generated HTML page MUST contain four distinct blocks displaying the following information:
1. Verdict Block: CLEAN/REVIEW verdict based on mismatches & direct callers.
2. Build-Break Findings Block: Mismatches with before/after symbol source code and call sites.
3. Blast Radius Block: Transitive impacts with resolved dependency types.
4. Dead Code Block: Unused symbols with confidence levels and reasons.
All text labels and headings in the report MUST be in English.

#### Scenario: Render all four blocks
- GIVEN a successful Fathom analysis result
- WHEN rendering the HTML page
- THEN the output MUST include the Verdict, Build-Break Findings, Blast Radius, and Dead Code blocks in English

### Requirement: Fallback Placeholders

If a symbol's source code is empty or unavailable (e.g. symbol was deleted or is not indexed), the HTML code comparison drawer MUST display a fallback placeholder in English.

#### Scenario: Deleted symbol code view
- GIVEN a deleted symbol with empty content
- WHEN rendering the code comparison drawer
- THEN it MUST display the fallback text "[ Symbol Deleted ]"

#### Scenario: Unavailable code view
- GIVEN a symbol whose content is unavailable
- WHEN rendering the code comparison drawer
- THEN it MUST display the fallback text "[ Code Not Available ]"

## Test Strategy

- **Template compilation**: verify that Go templates compile successfully and use `embed` package for resources.
- **Self-contained check**: verify the output HTML has no external URLs for stylesheet or script tags.
- **Fallback texts**: verify that empty or deleted symbols output correct fallback messages.
