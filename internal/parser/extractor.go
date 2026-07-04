package parser

import (
	"strings"

	tspack "github.com/xberg-io/tree-sitter-language-pack/packages/go"

	"github.com/Fathom/internal/symbol"
)

// kindMaps maps a language name to a map of tree-sitter node kinds → Fathom
// symbol kind. A node kind present in the map is emitted as a symbol; the
// walker also uses noDescend to decide whether to descend into a matched
// node's children (to avoid double-emitting nested declarations).
var kindMaps = map[string]map[string]symbol.SymbolKind{
	"go": {
		"function_declaration": symbol.KindFunction,
		"method_declaration":   symbol.KindFunction,
		"type_spec":            symbol.KindType,
		"interface_type":       symbol.KindInterface,
		"import_declaration":   symbol.KindImport,
	},
	"javascript": {
		"function_declaration": symbol.KindFunction,
		"class_declaration":    symbol.KindClass,
		"method_definition":    symbol.KindFunction,
		"arrow_function":       symbol.KindFunction,
		"import_statement":     symbol.KindImport,
		"export_statement":     symbol.KindExport,
	},
	"typescript": {
		"function_declaration":  symbol.KindFunction,
		"class_declaration":     symbol.KindClass,
		"interface_declaration": symbol.KindInterface,
		"method_definition":     symbol.KindFunction,
		"arrow_function":        symbol.KindFunction,
		"import_statement":      symbol.KindImport,
		"export_statement":      symbol.KindExport,
	},
	"tsx": {
		"function_declaration":  symbol.KindFunction,
		"class_declaration":     symbol.KindClass,
		"interface_declaration": symbol.KindInterface,
		"method_definition":     symbol.KindFunction,
		"arrow_function":        symbol.KindFunction,
		"import_statement":      symbol.KindImport,
		"export_statement":      symbol.KindExport,
	},
	"python": {
		"function_definition":   symbol.KindFunction,
		"class_definition":      symbol.KindClass,
		"import_statement":      symbol.KindImport,
		"import_from_statement": symbol.KindImport,
	},
	"rust": {
		"function_item":   symbol.KindFunction,
		"struct_item":     symbol.KindType,
		"enum_item":       symbol.KindType,
		"impl_item":       symbol.KindType,
		"trait_item":      symbol.KindInterface,
		"use_declaration": symbol.KindImport,
	},
	"java": {
		"method_declaration":    symbol.KindFunction,
		"class_declaration":     symbol.KindClass,
		"interface_declaration": symbol.KindInterface,
		"import_declaration":    symbol.KindImport,
	},
	"c": {
		"function_definition": symbol.KindFunction,
		"struct_specifier":    symbol.KindType,
	},
	"cpp": {
		"function_definition": symbol.KindFunction,
		"struct_specifier":    symbol.KindType,
		"class_specifier":     symbol.KindClass,
	},
	// NOTE: the tree-sitter Ruby grammar uses `class`, `method`, and `module`
	// (NOT `class_definition`/`method_definition`/`module_definition`). The
	// naming here reflects the actual grammar so extraction works against
	// real Ruby sources.
	"ruby": {
		"class":  symbol.KindClass,
		"method": symbol.KindFunction,
		"module": symbol.KindType,
	},
	"php": {
		"function_definition":   symbol.KindFunction,
		"method_declaration":    symbol.KindFunction,
		"class_declaration":     symbol.KindClass,
		"interface_declaration": symbol.KindInterface,
	},
}

// noDescend lists node kinds whose children should NOT be walked once the
// node itself is emitted. This prevents duplicate symbols when a declaration
// wrapper contains the real name+body as children that would also match.
//
// Go `type_spec` is the canonical case: a `type_spec` wraps either a
// `struct_type` or an `interface_type`; emitting both the spec and the nested
// interface_type would double-count every interface.
var noDescend = map[string]map[string]struct{}{
	"go": {"type_spec": {}},
}

// identifierKinds are node kinds that carry a declaration name as their
// source text. Used as a fallback when ChildByFieldName("name") is empty.
var identifierKinds = map[string]struct{}{
	"identifier":        {},
	"type_identifier":   {},
	"field_identifier":  {},
	"constant":          {}, // Ruby class/module names
	"name":              {}, // PHP
	"scoped_identifier": {}, // Rust use, Java import
	"dotted_name":       {}, // Python import
}

// stringKinds are node kinds whose source text is a string literal carrying
// the imported path for import declarations.
var stringKinds = map[string]struct{}{
	"interpreted_string_literal": {}, // Go
	"string":                     {}, // TS/JS/PHP/Ruby
	"string_literal":             {}, // Java/C++
	"system_lib_string":          {}, // C/C++ #include
}

// ExtractSymbols parses source with the tree-sitter parser for lang and
// returns the symbols found in the concrete syntax tree.
//
// The function does not download parsers; it relies on the language pack's
// static bundle for the curated languages. If the language is unknown or the
// parser cannot be loaded, an error is returned. Parse errors (syntax errors
// in the source) are NOT errors here: a best-effort symbol list is returned
// alongside any partial tree the parser produced.
func ExtractSymbols(source []byte, lang string) ([]symbol.Symbol, error) {
	kindMap, ok := kindMaps[lang]
	if !ok {
		return nil, errUnsupportedLanguage(lang)
	}

	p, err := tspack.GetParser(lang)
	if err != nil {
		return nil, err
	}
	defer p.Free()

	tree := p.ParseBytes(source)
	if tree == nil {
		return nil, errParseFailed(lang)
	}
	defer tree.Free()

	root := tree.RootNode()
	if root == nil {
		return nil, nil
	}

	var symbols []symbol.Symbol
	walkNode(root, source, lang, kindMap, &symbols)
	return symbols, nil
}

// walkNode recursively walks the named children of n, emitting a symbol for
// any node whose kind matches the language's kind map. When a matched kind is
// in the language's noDescend set, its children are skipped to avoid
// duplicate emission.
func walkNode(n *tspack.Node, source []byte, lang string, kindMap map[string]symbol.SymbolKind, out *[]symbol.Symbol) {
	if n == nil {
		return
	}

	kind := n.Kind()
	stop := false
	if sk, ok := kindMap[kind]; ok {
		// Go interface detection: a `type_spec` wrapping an `interface_type`
		// is classified as an Interface rather than a plain Type. The
		// noDescend rule on type_spec means the nested interface_type would
		// never be visited, so we resolve the kind here.
		if lang == "go" && kind == "type_spec" && containsKind(n, "interface_type") {
			sk = symbol.KindInterface
		}
		emitSymbol(n, source, sk, out)
		if _, stop = noDescend[lang][kind]; stop {
			return
		}
	}

	if stop {
		return
	}

	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		walkNode(n.NamedChild(uint32(i)), source, lang, kindMap, out)
	}
}

// emitSymbol builds a symbol from n and appends it. The File field is left
// blank; the caller (ParseFile) sets it so the extractor stays free of I/O.
func emitSymbol(n *tspack.Node, source []byte, kind symbol.SymbolKind, out *[]symbol.Symbol) {
	start, end := n.StartByte(), n.EndByte()
	var content string
	if end <= uint(len(source)) {
		content = string(source[start:end])
	}

	name := extractName(n, source, kind)
	// Exports wrap an inner declaration; name the export after what it
	// exports (e.g. `export function hello` → export named "hello"). A bare
	// `export default x` is named "default".
	if kind == symbol.KindExport {
		name = exportName(n, source, name)
	}

	s := symbol.Symbol{
		Name:    name,
		Kind:    kind,
		Line:    nodeLine(n),
		Col:     nodeCol(n),
		Content: content,
	}
	*out = append(*out, s)
}

// nodeLine/nodeCol return the 1-based line and column of n's start position.
// tree-sitter positions are zero-indexed; Fathom uses 1-based to match the
// convention of editors and most CLI tools.
func nodeLine(n *tspack.Node) int {
	p := n.StartPosition()
	if p == nil {
		return 0
	}
	return int(p.Row) + 1
}

func nodeCol(n *tspack.Node) int {
	p := n.StartPosition()
	if p == nil {
		return 0
	}
	return int(p.Column) + 1
}

// extractName resolves a declaration's name. It tries the grammar-defined
// "name" field first (most reliable across grammars), then falls back to the
// first named identifier child, then to import-specific string extraction.
func extractName(n *tspack.Node, source []byte, kind symbol.SymbolKind) string {
	// 1. Grammar "name" field — works for Go type_spec, Java/TS class & method,
	//    Ruby class/module, PHP class/method, etc.
	if nameNode := n.ChildByFieldName("name"); nameNode != nil {
		if s := nodeText(nameNode, source); s != "" {
			return s
		}
	}

	// 2. Imports: pull the imported path from the string / scoped identifier
	//    child so the symbol name is the module, not a brace list.
	if kind == symbol.KindImport {
		if s := extractImportName(n, source); s != "" {
			return s
		}
	}

	// 3. First named identifier child (covers C/C++ where the name lives in a
	//    declarator, and Python/Rust simple imports).
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(uint32(i))
		if c == nil {
			continue
		}
		if _, ok := identifierKinds[c.Kind()]; ok {
			if s := nodeText(c, source); s != "" {
				return s
			}
		}
	}

	// 4. Final fallback: empty name. Better than a wrong one.
	return ""
}

// extractImportName searches n's subtree for the imported path and returns
// it with surrounding quotes/braces trimmed. Strings are preferred over
// identifiers because, for languages with both a local binding and a source
// path (e.g. `import { foo } from 'bar'`), the path is the dependency
// identifier Fathom cares about. Languages where the path IS an identifier
// (Python `import os`, Rust `use std::io`, Java `import java.util.List`)
// fall back to the identifier branch.
func extractImportName(n *tspack.Node, source []byte) string {
	// 1. Prefer string-literal paths (Go, TS/JS, C/C++ #include, PHP require).
	var found string
	var walkStrings func(*tspack.Node) bool
	walkStrings = func(node *tspack.Node) bool {
		if node == nil {
			return false
		}
		if _, ok := stringKinds[node.Kind()]; ok {
			found = stripQuotes(nodeText(node, source))
			return true
		}
		for i := uint(0); i < node.NamedChildCount(); i++ {
			if walkStrings(node.NamedChild(uint32(i))) {
				return true
			}
		}
		return false
	}
	if walkStrings(n) {
		return found
	}

	// 2. Fall back to the first path-like identifier (Python dotted_name,
	//    Rust scoped_identifier, Java scoped_identifier).
	var walkIds func(*tspack.Node) bool
	walkIds = func(node *tspack.Node) bool {
		if node == nil {
			return false
		}
		if _, ok := identifierKinds[node.Kind()]; ok {
			found = nodeText(node, source)
			return true
		}
		for i := uint(0); i < node.NamedChildCount(); i++ {
			if walkIds(node.NamedChild(uint32(i))) {
				return true
			}
		}
		return false
	}
	walkIds(n)
	return found
}

// nodeText returns the source slice of n, or "" if the byte range is out of
// bounds (which should not happen for valid trees, but guards against
// malformed input).
func nodeText(n *tspack.Node, source []byte) string {
	if n == nil {
		return ""
	}
	start, end := n.StartByte(), n.EndByte()
	if end > uint(len(source)) || start > end {
		return ""
	}
	return string(source[start:end])
}

// stripQuotes removes matching surrounding quotes and import braces from a
// path string so "fmt" → fmt and { foo } → foo.
func stripQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') ||
			(first == '\'' && last == '\'') ||
			(first == '<' && last == '>') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// errUnsupportedLanguage is returned by ExtractSymbols when no kind map is
// registered for the requested language. It is a typed error so callers can
// distinguish "unsupported" from "parser load failed".
type errUnsupportedLanguage string

func (e errUnsupportedLanguage) Error() string { return "parser: unsupported language: " + string(e) }

// errParseFailed is returned when the tree-sitter parser produces no tree at
// all (distinct from a tree containing error nodes, which is still useful).
type errParseFailed string

func (e errParseFailed) Error() string {
	return "parser: parse produced no tree for language: " + string(e)
}

// containsKind reports whether n has any direct named child whose kind
// matches. It is a shallow check used to classify wrapper declarations
// (e.g. Go type_spec → interface_type) without descending into a full walk.
func containsKind(n *tspack.Node, kind string) bool {
	if n == nil {
		return false
	}
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		if c := n.NamedChild(uint32(i)); c != nil && c.Kind() == kind {
			return true
		}
	}
	return false
}

// exportName derives a name for an export_statement. It prefers the name of
// the first nested declaration (function/class/etc.); falls back to
// "default" when the export is a default export; otherwise returns the
// fallback provided by the caller (usually empty).
func exportName(n *tspack.Node, source []byte, fallback string) string {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(uint32(i))
		if c == nil {
			continue
		}
		// The first named child of an export_statement is the exported
		// declaration (function_declaration, class_declaration, etc.).
		if name := extractName(c, source, kindForNode(c)); name != "" {
			return name
		}
	}
	if hasNamedChild(n, "default") || fallback != "" {
		if fallback == "" {
			return "default"
		}
		return fallback
	}
	return ""
}

// hasNamedChild reports whether n has a direct named child with the given
// kind. Used to detect `default` exports whose child kind is literally
// "default".
func hasNamedChild(n *tspack.Node, kind string) bool {
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		if c := n.NamedChild(uint32(i)); c != nil && c.Kind() == kind {
			return true
		}
	}
	return false
}

// kindForNode returns the Fathom symbol kind a node would be mapped to in its
// language, or the empty string when the node is not a declaration. It is a
// read-only lookup used by exportName to classify the inner declaration of
// an export_statement without emitting it.
func kindForNode(n *tspack.Node) symbol.SymbolKind {
	// We do not know the language here; search every kind map for the node
	// kind. This is O(languages) but only runs on export children, which are
	// few, so the cost is negligible.
	k := n.Kind()
	for _, m := range kindMaps {
		if sk, ok := m[k]; ok {
			return sk
		}
	}
	return ""
}
