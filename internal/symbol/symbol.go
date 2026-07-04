// Package symbol defines the core symbol model used by Fathom to represent
// code entities (functions, types, classes, interfaces, imports, exports)
// extracted from a repository.
package symbol

// SymbolKind classifies the kind of code entity a Symbol represents.
// It is a string so encoded symbols remain human-readable and stable
// across versions.
type SymbolKind string

// Kind constants enumerate the supported SymbolKind values.
const (
	KindFunction  SymbolKind = "function"
	KindType      SymbolKind = "type"
	KindClass     SymbolKind = "class"
	KindInterface SymbolKind = "interface"
	KindImport    SymbolKind = "import"
	KindExport    SymbolKind = "export"
)

// Symbol describes a single code entity extracted from a file.
//
// Field order is intentional for stable JSON encoding. JSON tags use
// lowerCamelCase to match Fathom's on-disk format conventions.
type Symbol struct {
	Name    string     `json:"name"`
	Kind    SymbolKind `json:"kind"`
	File    string     `json:"file"`
	Line    int        `json:"line"`
	Col     int        `json:"col"`
	Content string     `json:"content"`
}