// Package refs extracts cross-file references (calls, type uses, variable
// reads, import uses) from tree-sitter concrete syntax trees.
//
// The package is internal to Fathom: it provides a registry of
// ReferenceExtractor implementations, one per supported language, and a
// generic tags.scm-driven engine (see query_extractor.go) that powers them.
//
// The registry is concurrency-safe. Extractors self-register from their
// package init() so importing `internal/refs` is enough to make every bundled
// language available.
package refs

import (
	"sort"
	"sync"

	tspack "github.com/xberg-io/tree-sitter-language-pack/packages/go"
)

// ReferenceKind classifies the kind of usage a Reference represents. It is a
// string so encoded references remain human-readable and stable across
// versions, matching the convention used by internal/symbol.
type ReferenceKind string

// Kind constants enumerate the supported ReferenceKind values. They align
// with the reference capture classes emitted by tree-sitter tags.scm queries
// (collapsed to Fathom's four reference categories).
const (
	// RefCall is a function/method invocation.
	RefCall ReferenceKind = "call"
	// RefTypeUse is a use of a type/class/interface in a position other than
	// its declaration (signatures, variable types, generic constraints,
	// implementations, etc.).
	RefTypeUse ReferenceKind = "type_use"
	// RefVarRead is a read of a variable binding in a non-declaration
	// position.
	RefVarRead ReferenceKind = "var_read"
	// RefImportUse is a use of an imported module path.
	RefImportUse ReferenceKind = "import_use"
)

// Reference describes a single usage of a symbol found in a source file.
//
// Field order is intentional for stable JSON encoding. JSON tags use
// lowerCamelCase to match Fathom's on-disk format conventions, mirroring
// internal/symbol.Symbol.
type Reference struct {
	// SymbolName is the name of the referenced symbol (the identifier text
	// captured by the @name capture inside a @reference.* pattern).
	SymbolName string `json:"symbol_name"`
	// Kind classifies the reference.
	Kind ReferenceKind `json:"kind"`
	// SourceFile is the file path the reference appears in. The extractor
	// leaves it blank; the caller (the indexer) sets it so the extractor stays
	// free of I/O, mirroring internal/symbol.Symbol.File.
	SourceFile string `json:"source_file"`
	// SourceLine is the 1-based line of the reference.
	SourceLine int `json:"source_line"`
	// SourceCol is the 1-based column of the reference.
	SourceCol int `json:"source_col"`
	// ContainingSymbol is the name of the declaration (function/method/class)
	// that lexically encloses the reference, or empty when the reference is at
	// file scope.
	ContainingSymbol string `json:"containing_symbol"`

	// Call-site signature metadata — additive, zero-value defaults preserve
	// backward compatibility with v2 indexes (all tags use omitempty).
	// ArgCount is the number of arguments at a call site; ArgTypes lists the
	// normalized literal type of each argument ("string", "int", "float",
	// "bool", "null", "unknown"). Populated only for RefCall references.
	ArgCount int      `json:"arg_count,omitempty"`
	ArgTypes []string `json:"arg_types,omitempty"`
}

// ReferenceExtractor extracts references for a single language.
//
// Implementations register themselves via Register, usually from a package
// init(), so callers only need to import the refs package.
type ReferenceExtractor interface {
	// Language returns the language name this extractor handles (e.g. "go",
	// "typescript"). Names match the keys used by tspack.GetLanguage and
	// tspack.GetTagsQuery.
	Language() string
	// ExtractReferences walks root and returns every reference found in it.
	// root is the language pack's tree-sitter node; source is the file bytes
	// the tree was parsed from (used for identifier text extraction and, for
	// the generic query engine, re-parsed with go-tree-sitter to obtain a
	// query-compatible node — see query_extractor.go for the integration
	// note).
	ExtractReferences(root *tspack.Node, source []byte) ([]Reference, error)
}

// registry holds the registered extractors keyed by Language(). It is
// guarded by mu so Register/Get/Languages/ExtractAll are safe for concurrent
// use. Reads dominate, so an RWMutex is used instead of a plain Mutex.
var (
	mu         sync.RWMutex
	extractors = make(map[string]ReferenceExtractor)
)

// Register adds e to the registry under e.Language(). Registering the same
// language twice replaces the previous extractor; the last registration
// wins. This keeps self-registration from init() idempotent across test
// re-runs.
func Register(e ReferenceExtractor) {
	if e == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	extractors[e.Language()] = e
}

// Get returns the extractor registered for lang, or (nil, false) when no
// extractor has registered for that language.
func Get(lang string) (ReferenceExtractor, bool) {
	mu.RLock()
	defer mu.RUnlock()
	e, ok := extractors[lang]
	return e, ok
}

// Languages returns every registered language name, sorted. The result is a
// fresh slice so callers can mutate it freely.
func Languages() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(extractors))
	for lang := range extractors {
		out = append(out, lang)
	}
	sort.Strings(out)
	return out
}

// ExtractAll runs the registered extractor for each language in langs against
// the given tree and source. The result maps each requested language to the
// references its extractor produced. Languages with no registered extractor
// are skipped silently (the key is absent from the map).
//
// root is the tspack tree root. As with ExtractReferences, the generic query
// extractor re-parses source with go-tree-sitter internally because the
// tspack.Node type is not directly usable with tree_sitter.QueryCursor; see
// query_extractor.go for details.
func ExtractAll(root *tspack.Node, source []byte, langs []string) (map[string][]Reference, error) {
	out := make(map[string][]Reference, len(langs))
	for _, lang := range langs {
		mu.RLock()
		e, ok := extractors[lang]
		mu.RUnlock()
		if !ok {
			continue
		}
		refs, err := e.ExtractReferences(root, source)
		if err != nil {
			return nil, err
		}
		out[lang] = refs
	}
	return out, nil
}
