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

	return extractSymbolsFromRoot(root, source, lang, kindMap), nil
}

// extractSymbolsFromRoot walks an already-parsed CST root and returns the
// symbols found. It is the shared implementation used by both ExtractSymbols
// (which parses internally) and ParseFileWithRefs (which passes a pre-parsed
// root to avoid a second parse).
func extractSymbolsFromRoot(root *tspack.Node, source []byte, lang string, kindMap map[string]symbol.SymbolKind) []symbol.Symbol {
	var symbols []symbol.Symbol
	walkNode(root, source, lang, kindMap, &symbols)
	return symbols
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
		emitSymbol(n, source, lang, sk, out)
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
// lang drives the language-specific parameter and inheritance extraction.
func emitSymbol(n *tspack.Node, source []byte, lang string, kind symbol.SymbolKind, out *[]symbol.Symbol) {
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

	// Signature metadata: only function/method and class declarations carry
	// parameter/inheritance info. extractParams and extractParentClass are
	// no-ops on declarations without the relevant named children, leaving
	// the zero values in place.
	if kind == symbol.KindFunction {
		minP, maxP, types := extractParams(n, source, lang)
		s.MinParams = minP
		s.MaxParams = maxP
		s.ParamTypes = types
		// Methods carry the enclosing class name. Walk parents to find a
		// class declaration node and reuse its name. The enclosing class is
		// available only when the function node's parent chain includes a
		// class-declaration kind for the current language.
		if cn := enclosingClassName(n, source, lang); cn != "" {
			s.ClassName = cn
		}
	}
	if kind == symbol.KindClass {
		s.ParentClass = extractParentClass(n, source, lang)
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

// extractParams walks the `parameters` named child of a function/method
// declaration node n and returns (MinParams, MaxParams, ParamTypes):
//
//   - MinParams is the number of required parameters (those without a
//     default value). Parameters with defaults count toward MaxParams only.
//   - MaxParams is the total number of positional parameters, or -1 when the
//     declaration is variadic (has a rest/variadic parameter).
//   - ParamTypes is the type annotation string of each parameter in
//     declaration order, or "unknown" when the parameter is unannotated.
//
// The walk is language-aware: the parameters container is a named child
// called "parameters" in Go/TS/JS/Java/Rust/PHP and "parameters" in Python
// (Python uses a `parameters` named child too). When n has no `parameters`
// child, extractParams returns (0, 0, nil) and the caller leaves the zero
// values in place.
//
// Variadic detection is language-specific:
//
//   - Go: a parameter whose kind is `variadic_parameter_declaration` (or whose
//     type is `variadic_type`).
//   - TS/JS: `rest_pattern`.
//   - Python: `list_splat_pattern` / `dictionary_splat_pattern`.
//   - Java/PHP: `spread_parameter` / `variadic_parameter`.
//   - Rust: `variadic_parameter`.
//
// A parameter with a default value is detected via the `default_value`
// named child (TS/JS/Python/Java) or a `= value` sibling pattern; this
// implementation uses the presence of a named child whose kind contains
// "default" as a heuristic that works across grammars.
func extractParams(n *tspack.Node, source []byte, lang string) (minParams, maxParams int, paramTypes []string) {
	if n == nil {
		return 0, 0, nil
	}
	paramsNode := n.ChildByFieldName("parameters")
	if paramsNode == nil {
		return 0, 0, nil
	}

	count := paramsNode.NamedChildCount()
	if count == 0 {
		return 0, 0, nil
	}

	variadic := false
	required := 0
	types := make([]string, 0, count)

	for i := uint(0); i < count; i++ {
		p := paramsNode.NamedChild(uint32(i))
		if p == nil {
			continue
		}
		kind := p.Kind()

		// Variadic/rest parameter → set the variadic flag and still record
		// its type (the element type for Go's `...T`, the wrapped type for
		// spread patterns). The parameter itself is NOT counted toward
		// MaxParams (the total positional count) because MaxParams = -1.
		if isVariadicParam(kind, lang) {
			variadic = true
			types = append(types, paramType(p, source, lang))
			continue
		}

		// A parameter has a default value when it carries a named child
		// whose kind contains "default" (covers TS/JS `default_value`,
		// Python `default_parameter`, Java `default_value`). Required
		// params have no such child.
		if !hasDefault(p) {
			required++
		}
		maxParams++
		types = append(types, paramType(p, source, lang))
	}

	if variadic {
		maxParams = -1
	}
	return required, maxParams, types
}

// isVariadicParam reports whether a parameter node kind represents a
// variadic/rest parameter in the given language. The check is on kind names
// because tree-sitter grammars use distinct kinds for variadic parameters
// (rather than a field), so a substring match on the canonical names is the
// most robust cross-language test.
func isVariadicParam(kind, lang string) bool {
	switch lang {
	case "go":
		// `variadic_parameter_declaration` is the Go kind for `args ...T`.
		return kind == "variadic_parameter_declaration" || kind == "variadic_type"
	case "javascript", "typescript", "tsx":
		return kind == "rest_pattern"
	case "python":
		return kind == "list_splat_pattern" || kind == "dictionary_splat_pattern" || kind == "list_splat" || kind == "dictionary_splat"
	case "java":
		return kind == "spread_parameter" || kind == "variadic_parameter"
	case "php":
		return kind == "variadic_parameter" || kind == "rest_argument"
	case "rust":
		return kind == "variadic_parameter"
	case "ruby":
		return kind == "rest_parameter" || kind == "keyword_rest_parameter" || kind == "block_parameter"
	case "c", "cpp":
		return kind == "variadic_parameter" || kind == "parameter_declaration" && false // C/C++ use `...` tokens, handled below
	}
	return false
}

// hasDefault reports whether parameter node p carries a default value. We
// detect this generically by looking for a named child whose kind contains
// the substring "default". This works across TS/JS (`default_value`),
// Python (`default_parameter` wraps the value), Java (`default_value`), and
// PHP. Go and Rust have no default parameters so the check returns false,
// which is correct (all their parameters are required).
func hasDefault(p *tspack.Node) bool {
	if p == nil {
		return false
	}
	count := p.NamedChildCount()
	for i := uint(0); i < count; i++ {
		c := p.NamedChild(uint32(i))
		if c == nil {
			continue
		}
		if strings.Contains(c.Kind(), "default") {
			return true
		}
	}
	return false
}

// paramType returns the type-annotation string of a parameter node, or
// "unknown" when the parameter is unannotated. The annotation lives in a
// `type` field child (Go, TS, Java, Rust, PHP) or in a `type_annotation`
// named child (Python). The text is trimmed of surrounding whitespace;
// composite/anonymous types collapse to "unknown" only when no `type` field
// or annotation child exists at all.
func paramType(p *tspack.Node, source []byte, lang string) string {
	if p == nil {
		return "unknown"
	}

	// 1. Grammar-defined "type" field — Go (`type`), TS (`type_annotation`
	//    exposed as a field in some grammars), Java (`type`), Rust (`type`),
	//    PHP (`type`).
	if tNode := p.ChildByFieldName("type"); tNode != nil {
		if s := strings.TrimSpace(nodeText(tNode, source)); s != "" {
			return s
		}
	}

	// 2. Python: the annotation is a `type_annotation` (or bare `type`)
	//    named child, or appears as the second named child after the
	//    identifier.
	count := p.NamedChildCount()
	for i := uint(0); i < count; i++ {
		c := p.NamedChild(uint32(i))
		if c == nil {
			continue
		}
		k := c.Kind()
		if k == "type_annotation" || k == "type" || strings.HasSuffix(k, "type") {
			if s := strings.TrimSpace(nodeText(c, source)); s != "" {
				return s
			}
		}
	}

	// 3. TS/JS: the parameter type may be nested under a `type_annotation`
	//    child even when no field is named.
	for i := uint(0); i < count; i++ {
		c := p.NamedChild(uint32(i))
		if c == nil {
			continue
		}
		if c.Kind() == "type_annotation" {
			if s := strings.TrimSpace(nodeText(c, source)); s != "" {
				return s
			}
		}
	}

	return "unknown"
}

// extractParentClass returns the superclass name of a class declaration n, or
// "" when the class declares no superclass. The superclass lives in a
// `superclass` named child (TS/JS, Java via `superclass`, Python via
// `superclasses`/`argument_list`), in a `base_class` (Ruby), or in an
// `extends` clause. We try the common named children and fall back to a
// substring scan of the source for an `extends`/`inherits` clause when the
// grammar does not expose a named field.
func extractParentClass(n *tspack.Node, source []byte, lang string) string {
	if n == nil {
		return ""
	}

	// 1. `superclass` field — TS/JS/Java grammars expose this.
	if sc := n.ChildByFieldName("superclass"); sc != nil {
		if s := strings.TrimSpace(nodeText(sc, source)); s != "" {
			return stripGenericArgs(s)
		}
	}

	// 2. Named children with kind `superclass` / `superclasses` / `base_class`.
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		c := n.NamedChild(uint32(i))
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "superclass", "superclasses", "base_class", "extends_clause", "inheritance_specifier":
			if s := strings.TrimSpace(nodeText(c, source)); s != "" {
				return stripGenericArgs(firstIdentifier(s))
			}
		}
	}

	return ""
}

// enclosingClassName walks n's parent chain and returns the name of the
// nearest enclosing class declaration, or "" when n is not inside a class.
// The class kinds are language-specific (see kindMaps); we match by the
// SymbolKind the parent would emit rather than by raw node kind so this works
// uniformly across grammars.
//
// The walk is depth-bounded (maxParentDepth) as a defensive guard: the
// tspack Node.Parent() binding returns a non-nil pointer for the root's
// parent rather than nil, so a naive `for cur != nil` loop would never
// terminate. The depth cap is well above any realistic nesting depth and
// breaks the loop safely when the chain reaches the root.
func enclosingClassName(n *tspack.Node, source []byte, lang string) string {
	if n == nil {
		return ""
	}
	kindMap, ok := kindMaps[lang]
	if !ok {
		return ""
	}
	// visited guards against malformed self-referential parent pointers
	// by breaking cycles. We key on (StartByte, EndByte) which uniquely
	// identifies a node within a single tree.
	visited := make(map[uint]struct{})
	cur := n.Parent()
	for depth := 0; cur != nil && depth < maxParentDepth; depth++ {
		start, end := cur.StartByte(), cur.EndByte()
		key := start*31 + end
		if _, seen := visited[key]; seen {
			break
		}
		visited[key] = struct{}{}
		// A null/invalid parent (tspack returns a zeroed node for the root's
		// parent) is detected by a zero byte range; stop the walk there.
		if start == 0 && end == 0 {
			break
		}
		if sk, isDecl := kindMap[cur.Kind()]; isDecl && sk == symbol.KindClass {
			if name := extractName(cur, source, symbol.KindClass); name != "" {
				return name
			}
		}
		cur = cur.Parent()
	}
	return ""
}

// maxParentDepth caps how far up the parent chain enclosingClassName walks.
// It is a defensive bound, not a meaningful limit: real nesting depths are in
// the dozens at most, so 1000 is effectively unbounded while guaranteeing
// termination even when the binding returns a non-nil null parent.
const maxParentDepth = 1000

// stripGenericArgs strips a trailing `<...>` generic-argument list from a
// type name so `List<int>` is recorded as `List`. The mismatch engine
// compares parameter types lexically; collapsing generics keeps the
// comparison meaningful across declarations that vary only in type
// arguments.
func stripGenericArgs(s string) string {
	if i := strings.IndexByte(s, '<'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// firstIdentifier returns the first whitespace-or-comma-separated token of
// s, so a multi-parent `extends A, B` clause is reduced to its first
// identifier. Callers that need all parents can extend this later; the
// mismatch engine starts with the first parent per the design's open
// question on C++ multiple inheritance.
func firstIdentifier(s string) string {
	for _, sep := range []string{",", " ", "\t", "\n"} {
		if i := strings.IndexByte(s, sep[0]); i >= 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
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
