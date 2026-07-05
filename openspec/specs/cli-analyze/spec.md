# cli-analyze Specification

## Purpose

Provide a `fathom analyze` CLI command that takes modified file paths and outputs a blast radius report.

## Requirements

### Requirement: Command Signature

The command MUST be `fathom analyze <files...>` where `<files...>` is one or more file paths relative to the repo root.

#### Scenario: Basic usage

- GIVEN a Fathom-indexed repository
- WHEN running `fathom analyze main.go config.go`
- THEN it MUST output a human-readable report with changed symbols, directly affected symbols, transitively affected symbols, affected files, and a Verdict (CLEAN/REVIEW)

### Requirement: --json Flag

The command MUST support `--json` flag that outputs the report as JSON.

#### Scenario: JSON output

- GIVEN a Fathom-indexed repository
- WHEN running `fathom analyze --json main.go`
- THEN the output MUST be valid JSON with fields: `changed_symbols`, `affected_symbols` (each with name, file, depth, via, and dependency_type), `affected_files`, `verdict`, `findings`, and `dead_code`

### Requirement: --html Flag

The command MUST support `--html <file>` flag to generate a static HTML report at the specified path.

#### Scenario: HTML output generation

- GIVEN a Fathom-indexed repository
- WHEN running `fathom analyze --html report.html main.go`
- THEN a self-contained HTML report MUST be created at "report.html"
- AND the CLI MUST output a success message in English

### Requirement: Schema Check

The command MUST check the database schema version before running. If the database is v1, it MUST print an error and exit non-zero.

#### Scenario: v1 database

- GIVEN a v1 .fathom/ database
- WHEN running `fathom analyze main.go`
- THEN it MUST exit with code 1 and print the migration message

### Requirement: Non-existent File

If a specified file does not exist in the index, the command MUST print a warning and continue with the remaining files.

#### Scenario: Missing file

- GIVEN a Fathom-indexed repository without "nonexistent.go"
- WHEN running `fathom analyze nonexistent.go main.go`
- THEN it MUST print "Warning: nonexistent.go not found in index"
- AND continue analysis for "main.go"

## Test Strategy

- **Integration test**: init a fixture repo → analyze a file → verify output contains expected symbols
- **--json flag**: verify JSON output is parseable and contains expected fields
- **v1 database**: verify error message
- **Missing file**: verify warning
