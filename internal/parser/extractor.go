package parser

import (
	"fmt"
	"strings"

	tspack "github.com/xberg-io/tree-sitter-language-pack/packages/go"

	"github.com/Fathom/internal/symbol"
)

// ExtractSymbols parses source via the language pack's high-level Process()
// API and returns the Fathom symbols found. It works for any language the pack
// recognizes (306 grammars), not just a curated subset.
//
// Process() returns Structure, Symbols, Imports, and Exports views of the file.
// processResultToSymbols merges and deduplicates them into a single symbol
// slice. Function parameter metadata (MinParams/MaxParams/ParamTypes) is not
// exposed by Process() — StructureItem.Signature is nil across grammars — so a
// second tree-sitter parse runs enrichFunctionSymbols to populate those fields
// via the existing extractParams helper.
//
// Parse errors (syntax errors in the source) are NOT errors here: the pack
// parses best-effort and returns diagnostics alongside whatever partial
// structure it recovered, so callers still get a usable symbol list.
func ExtractSymbols(source []byte, lang string) ([]symbol.Symbol, error) {
	config := tspack.ProcessConfig{
		Language: lang,
		Symbols:  true,
	}
	result, err := tspack.Process(string(source), config)
	if err != nil {
		return nil, fmt.Errorf("parser: process %s: %w", lang, err)
	}

	symbols := processResultToSymbols(result, source, lang)

	// Second parse for function parameter metadata. Skipped when there are no
	// function symbols (non-fatal on failure: symbols are returned without
	// params rather than dropping the whole extraction).
	enrichFunctionSymbols(source, lang, symbols)

	return symbols, nil
}

// enrichFunctionSymbols walks a second tree-sitter parse to populate
// MinParams, MaxParams, and ParamTypes on every KindFunction symbol. The pack's
// Process() does not expose parameter metadata, so we parse the source again
// and match function/method declaration nodes to symbols by line number.
//
// The second parse is skipped when no function symbols exist (RNF1). Failures
// (unknown language, nil tree) are non-fatal: the symbols are returned with
// zero-valued signature fields rather than dropping the whole extraction.
func enrichFunctionSymbols(source []byte, lang string, symbols []symbol.Symbol) {
	hasFuncs := false
	for _, s := range symbols {
		if s.Kind == symbol.KindFunction {
			hasFuncs = true
			break
		}
	}
	if !hasFuncs {
		return
	}

	p, err := tspack.GetParser(lang)
	if err != nil {
		return // non-fatal
	}
	defer p.Free()

	tree := p.ParseBytes(source)
	if tree == nil {
		return
	}
	defer tree.Free()

	root := tree.RootNode()
	if root == nil {
		return
	}

	enrichWalk(root, source, lang, symbols)
}

// functionDeclKinds lists the tree-sitter node kinds that declare a
// function/method across the grammars Fathom targets. Matching any of these
// triggers parameter extraction for the symbol on the same line.
var functionDeclKinds = map[string]struct{}{
	"function_declaration":  {},
	"method_declaration":    {},
	"function_definition":   {},
	"function_item":         {},
	"method_definition":     {},
	"arrow_function":         {},
	"generator_function_declaration": {},
	"constructor_declaration":        {},
	"operator_function_declaration":  {},
}

// enrichWalk recursively visits named children, and for each function/method
// declaration node it looks up the symbol on the same source line and fills
// in MinParams/MaxParams/ParamTypes via extractParams.
func enrichWalk(n *tspack.Node, source []byte, lang string, symbols []symbol.Symbol) {
	if n == nil {
		return
	}

	kind := n.Kind()
	if _, isFunc := functionDeclKinds[kind]; isFunc {
		line := nodeLine(n)
		for i := range symbols {
			s := &symbols[i]
			if s.Kind == symbol.KindFunction && s.Line == line && s.MinParams == 0 && s.MaxParams == 0 && s.ParamTypes == nil {
				minP, maxP, types := extractParams(n, source, lang)
				s.MinParams = minP
				s.MaxParams = maxP
				s.ParamTypes = types
				break
			}
		}
	}

	for i := uint(0); i < n.NamedChildCount(); i++ {
		enrichWalk(n.NamedChild(uint32(i)), source, lang, symbols)
	}
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
		return kind == "variadic_parameter"
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

// errUnsupportedLanguage is returned by callers that need to distinguish
// "unsupported" from "parser load failed". The pack itself returns a language
// error from Process(); this type is kept for compatibility with existing
// call sites and tests.
type errUnsupportedLanguage string

func (e errUnsupportedLanguage) Error() string { return "parser: unsupported language: " + string(e) }

// errParseFailed is returned when the tree-sitter parser produces no tree at
// all (distinct from a tree containing error nodes, which is still useful).
type errParseFailed string

func (e errParseFailed) Error() string {
	return "parser: parse produced no tree for language: " + string(e)
}