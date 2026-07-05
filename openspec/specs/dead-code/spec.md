# dead-code Specification

## Purpose

Identify unused symbols that were modified or added in a diff. By scanning for reference counts of these symbols, the system flags potentially dead code and determines a confidence level for each detection.

## Requirements

### Requirement: Dead Code Scan

The dead code engine MUST scan all added or modified symbols in a given diff. If a symbol has 0 references in the index (excluding its own definition), it MUST be flagged as dead code.

#### Scenario: Unused private symbol (High confidence)

- GIVEN a modified private (unexported) symbol "helperFunc"
- AND the index contains 0 references to "helperFunc" (excluding its own definition)
- WHEN checking for dead code
- THEN "helperFunc" MUST be flagged as dead code
- AND its confidence level MUST be HIGH
- AND the reason MUST state "Private symbol with no references found in the workspace"

#### Scenario: Unused public symbol (Medium confidence)

- GIVEN a modified public (exported) symbol "ExportedAPI"
- AND the index contains 0 references to "ExportedAPI" (excluding its own definition)
- WHEN checking for dead code
- THEN "ExportedAPI" MUST be flagged as dead code
- AND its confidence level MUST be MEDIUM
- AND the reason MUST state "Public symbol with no references found in the workspace"

#### Scenario: Active symbol (Not dead code)

- GIVEN a modified symbol "activeFunc"
- AND the index contains 1 or more references to "activeFunc" (excluding its own definition)
- WHEN checking for dead code
- THEN "activeFunc" MUST NOT be flagged as dead code

### Requirement: Language-Specific Export Resolution

The engine MUST resolve the exported status of a symbol based on the source language conventions:
- For Go, a symbol starting with an uppercase letter is exported (public), otherwise unexported (private).
- For JavaScript/TypeScript, a symbol defined with the `export` keyword is exported (public), otherwise unexported (private).

#### Scenario: Go symbol export check
- GIVEN a Go symbol named "processData" (unexported) and "ProcessData" (exported)
- WHEN resolving export status
- THEN "processData" MUST be resolved as private (unexported)
- AND "ProcessData" MUST be resolved as public (exported)

#### Scenario: JS/TS symbol export check
- GIVEN a TypeScript symbol defined as `export function processData()` (exported) and `function helper()` (unexported)
- WHEN resolving export status
- THEN "processData" MUST be resolved as public (exported)
- AND "helper" MUST be resolved as private (unexported)

## Test Strategy

- **Private Symbol**: verify that an unexported function with 0 references is flagged with HIGH confidence.
- **Public Symbol**: verify that an exported function with 0 references is flagged with MEDIUM confidence.
- **Referenced Symbol**: verify that a function with active references is not flagged.
