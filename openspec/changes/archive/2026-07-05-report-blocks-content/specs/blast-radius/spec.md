## MODIFIED Requirements

### Requirement: BlastRadius.Calculate

The blast radius engine MUST provide `Calculate(changedSymbols []string) (BlastResult, error)` where:
- `BlastResult` contains `DirectlyAffected []AffectedSymbol`, `TransitivelyAffected []AffectedSymbol`, and `AffectedFiles []string`
- `AffectedSymbol` has `Name`, `File`, `Depth` (1 = direct, 2+ = transitive), `Via` (the symbol that led to this one), and `DependencyType` (one of "direct_call", "interface_call", "struct_embedding")
(Previously: AffectedSymbol has Name, File, Depth, and Via, without dependency type resolution.)

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
