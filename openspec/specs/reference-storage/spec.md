# reference-storage Specification

## Purpose

Extend the existing bbolt Store to persist and query references.

## Requirements

### Requirement: PutReferences

The Store MUST provide `PutReferences(filePath string, refs []symbol.Reference) error` that atomically writes all references for a file in a single bbolt transaction.

#### Scenario: Write references

- GIVEN a file "main.go" with 3 references
- WHEN `PutReferences("main.go", refs)` is called
- THEN all 3 references MUST be persisted in the References bucket
- AND a subsequent `GetReferences("HandleRequest")` MUST return them

### Requirement: GetReferences

The Store MUST provide `GetReferences(symbolName string) ([]symbol.Reference, error)` that returns all references to a given symbol name via prefix scan.

#### Scenario: Query by symbol name

- GIVEN references to "HandleRequest" in 2 files
- WHEN `GetReferences("HandleRequest")` is called
- THEN it MUST return all references with SymbolName "HandleRequest"
- AND the results MUST be sorted by (sourceFile, sourceLine)

### Requirement: ListReferencesByFile

The Store MUST provide `ListReferencesByFile(filePath string) ([]symbol.Reference, error)` that returns all references originating from a given file.

#### Scenario: Query by source file

- GIVEN references from "main.go" to 5 different symbols
- WHEN `ListReferencesByFile("main.go")` is called
- THEN it MUST return all 5 references

### Requirement: Key Format

The References bucket key MUST use the format `{targetSymbolName}#{sourceFile}#{sourceLine}`.

#### Scenario: Key uniqueness

- GIVEN two references to "Parse" on lines 10 and 15 of "main.go"
- WHEN both are stored
- THEN both MUST be present (line number in key prevents overwrite)

### Requirement: Schema Version Check

The Store MUST check `schema_version` on Open. If version is "1", it MUST return an error: "index was built with Fathom v1. Please re-run `fathom init` to enable impact analysis."

#### Scenario: v1 database

- GIVEN a .fathom/ database with schema_version "1"
- WHEN Store.Open() is called
- THEN it MUST return an error with the migration message

## Test Strategy

- **PutReferences + GetReferences**: write references, read them back, verify all fields
- **ListReferencesByFile**: write references from multiple files, query by file
- **Key collision**: same symbol on different lines in same file → both stored
- **Schema version check**: v1 database returns error

## Migration Path

### v1 → v2

1. Bump `schema_version` meta key from `"1"` to `"2"` after a successful init that includes reference extraction
2. No data migration needed — v1 databases have empty reference buckets
3. `fathom analyze` checks schema_version at startup
4. If v1 → error: "index was built with Fathom v1. Please re-run `fathom init` to enable impact analysis."
