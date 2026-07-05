## Exploration: Beautify Fathom HTML Report

### Current State
Fathom's `--html` option generates a report using the embedded template `internal/report/report.html`.
- **Aesthetics**: The current template has a very basic design. It uses static CSS with a white background, basic tables, plain badges, and a standard `<details>` tag for findings. It lacks responsive styling, dark mode, smooth transitions, and modern coloring.
- **Interactivity**: There is no search, filtering, theme toggling, or dynamic code diff highlighting.
- **Variables Available** (from `internal/report/report.go`):
  - `.Verdict.Verdict` ("CLEAN" or "REVIEW")
  - `.Verdict.Summary` (Text summary)
  - `.Findings.Findings` — Slice of `Finding`:
    - `SymbolName`
    - `File`
    - `ChangeDescription`
    - `OldContent` / `NewContent` (accessed via `.DisplayOldContent` / `.DisplayNewContent`)
    - `Mismatches` (Slice of mismatches: `Type`, `SymbolName`, `File`, `Line`, `Detail`)
  - `.BlastRadius.DirectlyAffected` — Slice of `AffectedSymbol`:
    - `Name`, `File`, `Via`, `DependencyType`
  - `.BlastRadius.TransitivelyAffected` — Slice of `AffectedSymbol`:
    - `Name`, `File`, `Via`, `Depth`, `DependencyType`
  - `.BlastRadius.AffectedFiles` — Slice of strings
  - `.DeadCode.DeadSymbols` — Slice of `DeadSymbol`:
    - `Symbol.Name`, `Symbol.Kind`, `Symbol.File`, `Confidence`, `Reason`

### Affected Areas
- `internal/report/report.html` — The primary template. All styling and interactive JavaScript must be embedded here.
- `internal/report/template.go` — Contains helper methods for rendering findings (e.g. `DisplayOldContent`). We can add small text helpers here if needed, though most formatting and diff highlighting can be done dynamically via JavaScript in the client.

### Approaches

#### 1. Custom Self-Contained CSS & JS (Offline-First)
This approach embeds a highly optimized stylesheet and custom vanilla JavaScript directly within `<style>` and `<script>` blocks in the HTML template. It uses system font fallbacks, CSS variables for theme management, and lightweight custom diff highlighting.
- **Pros**:
  - **100% Offline-Capable**: Works perfectly on air-gapped CI/CD machines, local files (`file:///`), and behind corporate firewalls without making external network requests.
  - **Ultra-Lightweight**: Generates reports that are only ~30–50 KB, rendering instantly without wait times for CDNs to load.
  - **Sleek Light/Dark Mode**: Smooth transitions between themes using standard CSS properties (variables) toggled via a small JS theme switch.
- **Cons**:
  - Requires writing bespoke CSS rather than using utility libraries like Tailwind.
- **Effort**: Medium

#### 2. CDN-Based Tailwind CSS & Prism.js (Network-Dependent)
This approach embeds links to Tailwind CSS via Play CDN, Inter Fonts from Google Fonts, and Prism.js for syntax highlighting and diff rendering.
- **Pros**:
  - Easier to implement rich code highlighting and utility styles out-of-the-box.
- **Cons**:
  - **Breaks Offline**: The report renders as unstyled raw text when opened without internet access, which is extremely common in enterprise test runners and local development.
  - **Bloated File Size**: The browser must download several megabytes of scripts/styles before rendering, causing layout shift and delay.
- **Effort**: Low

### Recommendation
We strongly recommend **Approach 1 (Custom Self-Contained CSS & JS)**. Fathom is a developer tool, and its HTML reports are frequently generated in local offline workspaces, CI pipelines, and secure environments. Relying on external CDNs is a major architectural risk that would break reports under offline conditions.

By writing a custom stylesheet with CSS Variables and using modern vanilla JS, we can deliver a premium, dark-mode-ready interface with interactive filters and code diff highlighting, while keeping the output file self-contained, light, and completely network-independent.

### Design Plan for Premium Aesthetics & Interactivity
- **Color Palette**: Modern, premium slate/indigo color palette with emerald accents for clean checks and rose accents for warnings.
- **Typography**: Inter (via system font if available or Google Fonts as progressive enhancement, falling back gracefully to system sans-serif like San Francisco, Segoe UI, or Roboto).
- **Theme Switcher**: A header toggle for light/dark mode that automatically inherits the user's OS preference (`prefers-color-scheme`) and persists their choice in `localStorage`.
- **Search & Filters**: A centralized filter control that filters:
  - Findings by symbol name / filename.
  - Blast Radius callers (Direct/Transitive) by name / dependency type.
  - Dead Code by name / confidence level.
- **Interactive Code Diffs**: Enhance the side-by-side pre-formatted code block. A small embedded JavaScript helper will parse the lines of before/after code and apply styling classes (green highlight for lines added, red for lines deleted) to make diffs visually distinct.

### Risks
- **Large Diff Payloads**: If files contain huge symbol bodies, rendering the entire symbol content in HTML could make the file large. We should implement a max-height container on code blocks with scrolling.

### Ready for Proposal
Yes. The orchestrator should proceed to the proposal phase (`sdd-propose`), presenting the design for a self-contained, offline-first premium HTML report with custom dark mode and search features.
