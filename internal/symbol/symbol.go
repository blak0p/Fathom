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
//
// The signature-mismatch fields (MinParams, MaxParams, ParamTypes,
// ParentClass, ClassName) are additive: zero values are the default and
// older v2 indexes deserialize cleanly because every new tag uses
// `omitempty`. MinParams/MaxParams describe the parameter arity range of
// a function/method declaration; MaxParams = -1 marks a variadic/rest
// parameter. ParamTypes holds the declared type annotation of each
// parameter as a string, or "unknown" when the parameter is unannotated.
// ParentClass records the superclass a class declaration extends (used to
// build the inheritance graph for override resolution). ClassName records
// the enclosing class name on method symbols so the mismatch engine can
// resolve overrides.
type Symbol struct {
	Name    string     `json:"name"`
	Kind    SymbolKind `json:"kind"`
	File    string     `json:"file"`
	Line    int        `json:"line"`
	Col     int        `json:"col"`
	Content string     `json:"content"`

	// Signature metadata — additive, zero-value defaults preserve backward
	// compatibility with v2 indexes (all tags use omitempty).
	MinParams   int      `json:"min_params,omitempty"`
	MaxParams   int      `json:"max_params,omitempty"`   // -1 = variadic
	ParamTypes  []string `json:"param_types,omitempty"` // "unknown" when unannotated
	ParentClass string   `json:"parent_class,omitempty"` // superclass of a class declaration
	ClassName   string   `json:"class_name,omitempty"`   // enclosing class on method symbols
}