# blast-radius Specification

## Purpose

Compute the transitive impact of changing a set of symbols: given changed symbols, find all symbols that reference them (directly or transitively) and the files containing those symbols.

## Requirements

### Requirement: BlastRadius.Calculate

The blast radius engine MUST provide `Calculate(changedSymbols []string) (BlastResult, error)` where:
- `BlastResult` contains `DirectlyAffected []AffectedSymbol`, `TransitivelyAffected []AffectedSymbol`, and `AffectedFiles []string`
- `AffectedSymbol` has `Name`, `File`, `Depth` (1 = direct, 2+ = transitive), `Via` (the symbol that led to this one), and `DependencyType` (one of "direct_call", "interface_call", "struct_embedding")

#### Scenario: Direct impact

- GIVEN symbol "HandleRequest" is changed
- AND "HandleRequest" is referenced by "Serve" in "server.go" via a direct function call
- WHEN `Calculate(["HandleRequest"])` is called
- THEN "Serve" MUST appear in `DirectlyAffected` with Depth 1
- AND its dependency type MUST be "direct_call"

#### Scenario: Transitive impact

- GIVEN "HandleRequest" is changed
- AND "Serve" references "HandleRequest" via a direct function call
- AND "Main" references "Serve" via a direct function call
- WHEN `Calculate(["HandleRequest"])` is called
- THEN "Serve" MUST be in `DirectlyAffected` (depth 1, dependency type "direct_call")
- AND "Main" MUST be in `TransitivelyAffected` (depth 2, via "Serve", dependency type "direct_call")

#### Scenario: Interface call dependency

- GIVEN interface method "Read" on interface "Reader" is changed
- AND "Read" is invoked by "LoadConfig" in "config.go" via an interface call
- WHEN `Calculate(["Read"])` is called
- THEN "LoadConfig" MUST appear in `DirectlyAffected` with Depth 1 and dependency type "interface_call"

#### Scenario: Struct embedding dependency

- GIVEN struct "BaseController" is changed
- AND "BaseController" is embedded in struct "UserController" in "user.go"
- WHEN `Calculate(["BaseController"])` is called
- THEN "UserController" MUST appear in `DirectlyAffected` with Depth 1 and dependency type "struct_embedding"

### Requirement: Cycle Detection

The engine MUST detect and handle cycles in the call graph. A symbol MUST NOT appear more than once in the result.

#### Scenario: Mutual recursion

- GIVEN "A" calls "B" and "B" calls "A"
- AND "A" is changed
- WHEN `Calculate(["A"])` is called
- THEN "B" MUST appear exactly once in the result
- AND the algorithm MUST terminate (no infinite loop)

### Requirement: Empty Input

If `changedSymbols` is empty, the engine MUST return an empty result with no error.

#### Scenario: No changes

- GIVEN an empty changed symbols list
- WHEN `Calculate([])` is called
- THEN it MUST return an empty BlastResult with no error

## Test Strategy

- **Direct impact**: single symbol, one level of callers
- **Transitive impact**: multi-level call chain
- **Cycle detection**: mutual recursion terminates
- **Empty input**: empty changed symbols returns empty result
- **No references**: changed symbol with no references returns empty result
