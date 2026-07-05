## ADDED Requirements

### Requirement: --html Flag

The command MUST support `--html <file>` flag to generate a static HTML report at the specified path.

#### Scenario: HTML output generation

- GIVEN a Fathom-indexed repository
- WHEN running `fathom analyze --html report.html main.go`
- THEN a self-contained HTML report MUST be created at "report.html"
- AND the CLI MUST output a success message in English

## MODIFIED Requirements

### Requirement: Command Signature

The command MUST be `fathom analyze <files...>` where `<files...>` is one or more file paths relative to the repo root.
(Previously: The command runs analysis and outputs a human-readable report with changed/affected symbols and files.)

#### Scenario: Basic usage

- GIVEN a Fathom-indexed repository
- WHEN running `fathom analyze main.go config.go`
- THEN it MUST output a human-readable report with changed symbols, directly affected symbols, transitively affected symbols, affected files, and a Verdict (CLEAN/REVIEW)

### Requirement: --json Flag

The command MUST support `--json` flag that outputs the report as JSON.
(Previously: The output was valid JSON with basic changed/affected fields.)

#### Scenario: JSON output

- GIVEN a Fathom-indexed repository
- WHEN running `fathom analyze --json main.go`
- THEN the output MUST be valid JSON with fields: `changed_symbols`, `affected_symbols` (each with name, file, depth, via, and dependency_type), `affected_files`, `verdict`, `findings`, and `dead_code`
