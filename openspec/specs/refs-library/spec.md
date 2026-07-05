# refs-library Specification

## Purpose

Provide a public, importable Go package (`github.com/Fathom/refs`) that extracts cross-file references from tree-sitter ASTs. Any Go project can use it independently of Fathom CLI.

## Requirements

### Requirement: ReferenceExtractor Interface

The package MUST define a `ReferenceExtractor` interface with:
- `Language() string` — returns the language name (e.g. "go", "typescript")
- `ExtractReferences(root *tspack.Node, source []byte) ([]symbol.Reference, error)` — walks the CST and returns all references found

#### Scenario: Interface contract

- GIVEN a valid tree-sitter CST root for a Go source file
- WHEN `ExtractReferences` is called
- THEN it MUST return all call, type-use, var-read, and import-use references in the file
- AND it MUST NOT return declaration names as references

### Requirement: Registry Pattern

The package MUST implement a registry where each language self-registers via `init()`:
- `Register(e ReferenceExtractor)` — registers an extractor
- `Get(lang string) (ReferenceExtractor, bool)` — retrieves by language name
- `Languages() []string` — returns all registered language names
- `ExtractAll(root *tspack.Node, source []byte, langs []string) (map[string][]symbol.Reference, error)` — runs all registered extractors for the given languages

#### Scenario: Self-registration

- GIVEN a file `go_extractor.go` with `init() { Register(&goExtractor{}) }`
- WHEN the package is imported
- THEN `Languages()` MUST include "go"
- AND `Get("go")` MUST return the Go extractor

#### Scenario: Unknown language

- GIVEN a call to `Get("kotlin")`
- WHEN no extractor has registered for "kotlin"
- THEN it MUST return `(nil, false)`

### Requirement: Extraction Heuristic

The extractor MUST use the following heuristic to distinguish references from definitions:
- Any identifier/type_identifier/call_expression node that IS a `ChildByFieldName("name")` of a declaration node → SKIP (definition)
- Any identifier/type_identifier/call_expression node that is NOT a `ChildByFieldName("name")` of a declaration → EMIT (reference)

#### Scenario: Go function call vs definition

- GIVEN Go source: `func foo() { bar() }`
- WHEN extracting references
- THEN `foo` MUST NOT appear as a reference (it's a definition name)
- AND `bar` MUST appear as a reference (it's a call)

### Requirement: Go Reference Extraction

The Go extractor MUST capture:
- `call_expression` → `RefCall` where the function identifier is the target
- `selector_expression` → `RefCall` for method calls (`obj.Method`)
- `type_identifier` used in type declarations, function signatures, and struct fields → `RefTypeUse`
- `identifier` used as a variable read (not in declaration position) → `RefVarRead`

#### Scenario: Go function calls

- GIVEN `result := process(data)`
- WHEN extracting
- THEN `process` MUST be a `RefCall` reference

#### Scenario: Go method calls

- GIVEN `client.Get(url)`
- WHEN extracting
- THEN `Get` MUST be a `RefCall` reference with SymbolName "Get"

#### Scenario: Go type references

- GIVEN `var cfg Config`
- WHEN extracting
- THEN `Config` MUST be a `RefTypeUse` reference

### Requirement: TypeScript/JavaScript Reference Extraction

The TS/JS extractor MUST capture:
- `call_expression` where `function` field is an identifier → `RefCall`
- `call_expression` where `function` is a `property_access_expression` → `RefCall` (method call)
- `type_annotation` children → `RefTypeUse`
- `import_statement` source → `RefImportUse`

#### Scenario: TS function call

- GIVEN `parse(input)`
- WHEN extracting
- THEN `parse` MUST be a `RefCall` reference

#### Scenario: TS method call

- GIVEN `obj.method()`
- WHEN extracting
- THEN `method` MUST be a `RefCall` reference

### Requirement: Python Reference Extraction

The Python extractor MUST capture:
- `call` node where `function` is an identifier → `RefCall`
- `attribute` node used as function call → `RefCall` (method call)
- `type` in function annotations → `RefTypeUse`
- `import_statement` / `import_from_statement` names → `RefImportUse`

### Requirement: Ruby Reference Extraction

The Ruby extractor MUST capture:
- `call` node where `method` is an identifier → `RefCall`
- `method_call` with receiver → `RefCall`
- `constant` reference (not in class/module definition position) → `RefTypeUse`
- `require` / `require_relative` arguments → `RefImportUse`

## Test Strategy

- **Go extractor**: table-driven tests with inline Go source snippets, expected references list
- **TS/JS extractor**: same pattern with TypeScript/JavaScript snippets
- **Python extractor**: same pattern with Python snippets
- **Ruby extractor**: same pattern with Ruby snippets
- **Registry tests**: Register/Get/Languages/ExtractAll with mock extractors
- **Heuristic tests**: verify that definition names are excluded, call names are included
