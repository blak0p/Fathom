// query_extractor.go implements the generic, tags.scm-driven
// ReferenceExtractor that powers every registered language in the refs
// package. Instead of hand-writing per-language walkers (one for Go, one for
// TypeScript, etc.), this engine loads the tree-sitter `tags.scm` query that is
// already bundled in the language pack for each grammar and executes it
// against the parsed tree. The query emits two kinds of captures we care
// about:
//
//   - @reference.* captures mark a node that IS a usage of some symbol. The
//     capture's name tells us the kind (call, type use, implementation,
//     class). The symbol name is the @name capture that appears in the same
//     query match.
//   - @definition.* captures mark a declaration (function, method, class,
//     type). We ignore them as references (Phase 1 already extracts
//     symbols), but we use their names to track ContainingSymbol: the
//     declaration that lexically encloses each reference.
//
// # Integration note: tspack.Node vs tree_sitter.Node
//
// The refs.ReferenceExtractor interface accepts *tspack.Node because that is
// what the rest of Fathom (internal/parser) produces via the language pack
// parser. However, the language pack's Node type is an opaque C FFI handle
// that is NOT compatible with github.com/tree-sitter/go-tree-sitter's Node
// type, and only the latter is accepted by tree_sitter.QueryCursor.Matches.
//
// The two packages both wrap the same underlying C API, but at the Go level
// they expose different struct types with no conversion path. Rather than
// fork either binding, the query extractor re-parses source with
// go-tree-sitter's own parser to obtain a *tree_sitter.Node that the query
// cursor can run on. The cost is one extra parse per file; given Fathom
// indexes a repo once and the parse is already cheap relative to the query,
// this is acceptable and keeps the integration boundary clean.
//
// The root *tspack.Node passed to ExtractReferences is therefore ignored by
// the query extractor; only source and the extractor's configured language
// are used. The parameter is kept on the interface so non-query-based
// extractors (if any are added later) can use it directly.

package refs

import (
	"fmt"
	"sort"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tspack "github.com/xberg-io/tree-sitter-language-pack/packages/go"
)

// queryCaptureKind maps a tree-sitter tags.scm @reference.* capture name to a
// Fathom ReferenceKind. Capture names not in this map are treated as
// non-reference (e.g. @name, @definition.*, @doc) and ignored by the
// reference emitter.
var queryCaptureKind = map[string]ReferenceKind{
	// Direct call expressions.
	"reference.call": RefCall,
	// Message sends in C# and similar (obj.Method) collapsed to call.
	"reference.send": RefCall,
	// Type identifier used in a non-declaration position.
	"reference.type": RefTypeUse,
	// Implements / inherits — a type use of the implemented type.
	"reference.implementation": RefTypeUse,
	// Class reference (e.g. Ruby constant used as a class).
	"reference.class": RefTypeUse,
}

// definitionCapturePrefix is the prefix on captures that mark a declaration.
// Captures starting with this string name a definition (function, method,
// class, type, interface, module, macro, constant). We use them only for
// scope tracking.
const definitionCapturePrefix = "definition."

// queryExtractor is the generic ReferenceExtractor backed by a tags.scm
// query. One instance is created per language at package init() time and
// registered via Register.
type queryExtractor struct {
	lang string
	// querySrc is the raw tags.scm content. We hold the string (not a
	// compiled *tree_sitter.Query) because a Query is bound to a specific
	// *tree_sitter.Language instance, and languages are obtained lazily from
	// the pack per call to avoid holding C pointers across the registry's
	// lifetime.
	querySrc string
}

// newQueryExtractor builds a queryExtractor for lang by loading the bundled
// tags.scm via the language pack. If the pack has no tags.scm for lang the
// extractor is still created but ExtractReferences will return
// errNoTagsQuery when invoked; this lets the registry hold a slot for the
// language without crashing at init time.
func newQueryExtractor(lang string) *queryExtractor {
	src := ""
	if q := tspack.GetTagsQuery(lang); q != nil {
		src = *q
	}
	return &queryExtractor{lang: lang, querySrc: src}
}

// Language returns the language name.
func (e *queryExtractor) Language() string { return e.lang }

// errNoTagsQuery is returned when the language pack ships no tags.scm for
// the configured language. It is a typed error so callers can distinguish
// "unsupported" from a transient parse failure.
type errNoTagsQuery string

func (e errNoTagsQuery) Error() string {
	return "refs: no tags.scm bundled for language: " + string(e)
}

// ExtractReferences parses source with go-tree-sitter using the language
// pack's language, runs the tags.scm query, and emits one Reference per
// @reference.* capture. The @name capture in the same match supplies the
// referenced symbol's name.
//
// root is accepted to satisfy the ReferenceExtractor interface but is not
// used by the query engine (see the integration note at the top of this
// file).
func (e *queryExtractor) ExtractReferences(root *tspack.Node, source []byte) ([]Reference, error) {
	_ = root // unused — see integration note

	if e.querySrc == "" {
		return nil, errNoTagsQuery(e.lang)
	}

	// Obtain the language through the pack so the parser and the query both
	// see the same grammar pointer (mismatched languages make query
	// compilation fail with TSQueryErrorLanguage).
	lang, err := tspack.GetLanguage(e.lang)
	if err != nil {
		return nil, fmt.Errorf("refs: load language %q: %w", e.lang, err)
	}

	// Compile the tags.scm query. tree_sitter.NewQuery returns a *QueryError
	// (not an error) on failure; we convert it so the public API returns a
	// plain error.
	query, qerr := tree_sitter.NewQuery(lang, e.querySrc)
	if qerr != nil {
		return nil, fmt.Errorf("refs: compile tags query for %q: %s", e.lang, qerr.Error())
	}
	defer query.Close()

	// Re-parse source with go-tree-sitter to get a Node compatible with the
	// query cursor. The pack's parser gives us *tspack.Node which is not
	// assignable to *tree_sitter.Node.
	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang); err != nil {
		return nil, fmt.Errorf("refs: set parser language %q: %w", e.lang, err)
	}
	tree := parser.Parse(source, nil)
	if tree == nil {
		return nil, fmt.Errorf("refs: parse produced no tree for language %q", e.lang)
	}
	defer tree.Close()

	tsRoot := tree.RootNode()
	if tsRoot == nil {
		return nil, nil
	}

	// Pre-compute ContainingSymbol for every node by walking the tree once
	// and recording, for each definition capture, the name it declares. We
	// then resolve each reference's container by walking its parents until
	// we find a node that was recorded as a definition.
	defs := buildDefinitionIndex(query, tsRoot, source)

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	captureNames := query.CaptureNames()

	var refs []Reference
	matches := cursor.Matches(query, tsRoot, source)
	for {
		m := matches.Next()
		if m == nil {
			break
		}
		// A single match can carry multiple captures: one @reference.* (the
		// usage) and one @name (the identifier text). We resolve them within
		// the match so the symbol name always belongs to the right usage.
		var (
			refNode  *tree_sitter.Node
			refKind  ReferenceKind
			refFound bool
			nameNode *tree_sitter.Node
		)
		for _, c := range m.Captures {
			capName := captureNames[c.Index]
			if kind, ok := queryCaptureKind[capName]; ok {
				node := c.Node // copy so we can take the address
				refNode = &node
				refKind = kind
				refFound = true
				continue
			}
			if capName == "name" {
				node := c.Node
				nameNode = &node
			}
		}
		if !refFound {
			continue
		}
		// Without a @name capture we can't know which symbol was used; skip
		// rather than emit a nameless reference. This matches the spec's
		// requirement that every Reference has a SymbolName.
		if nameNode == nil {
			continue
		}
		name := nameNode.Utf8Text(source)
		if name == "" {
			continue
		}

		// Resolve the lexical container. We walk the reference node's
		// parents and pick the nearest node recorded as a definition. The
		// definition's own name comes from its @name capture, which
		// buildDefinitionIndex already stored.
		container := containingSymbol(defs, refNode)

		pos := refNode.StartPosition()
		r := Reference{
			SymbolName:       name,
			Kind:             refKind,
			SourceLine:       int(pos.Row) + 1,
			SourceCol:        int(pos.Column) + 1,
			ContainingSymbol: container,
		}

		// For call references, extract the argument count and normalized
		// literal types from the call node's `arguments` child. Non-call
		// references (type uses, import uses, var reads) carry no argument
		// metadata and leave the zero values in place.
		if refKind == RefCall {
			argCount, argTypes := extractArgs(refNode, source)
			r.ArgCount = argCount
			r.ArgTypes = argTypes
		}

		refs = append(refs, r)
	}

	// Sort by (line, col) so output is stable across runs and easy to assert
	// on in tests. The query cursor emits matches in tree order already, but
	// sorting defensively guards against future query API changes.
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].SourceLine != refs[j].SourceLine {
			return refs[i].SourceLine < refs[j].SourceLine
		}
		return refs[i].SourceCol < refs[j].SourceCol
	})
	return refs, nil
}

// defEntry records a single declaration discovered by the tags query: the
// node, the declared name, and the kind (function/method/class/...).
type defEntry struct {
	node *tree_sitter.Node
	name string
	kind string // the @definition.* suffix, e.g. "function"
}

// buildDefinitionIndex runs the tags query once in definition-only mode — we
// reuse the same query, we just only look at @definition.* and @name captures
// — and records every declaration node along with its name. The result is a
// slice (not a map keyed by node id) because we resolve containers by walking
// parents, which requires ordering by tree depth; a slice plus a parent-walk
// is simpler and avoids needing a stable node-id hashing scheme across the
// two bindings.
//
// The slice is sorted by node start byte so the parent-walk in
// containingSymbol can short-circuit once it passes the reference's position.
func buildDefinitionIndex(query *tree_sitter.Query, root *tree_sitter.Node, source []byte) []defEntry {
	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	captureNames := query.CaptureNames()

	var defs []defEntry
	matches := cursor.Matches(query, root, source)
	for {
		m := matches.Next()
		if m == nil {
			break
		}
		var (
			defNode  *tree_sitter.Node
			defKind  string
			nameNode *tree_sitter.Node
		)
		for _, c := range m.Captures {
			capName := captureNames[c.Index]
			if strings.HasPrefix(capName, definitionCapturePrefix) {
				node := c.Node
				defNode = &node
				defKind = strings.TrimPrefix(capName, definitionCapturePrefix)
			} else if capName == "name" {
				node := c.Node
				nameNode = &node
			}
		}
		if defNode == nil || nameNode == nil {
			continue
		}
		name := nameNode.Utf8Text(source)
		if name == "" {
			continue
		}
		defs = append(defs, defEntry{node: defNode, name: name, kind: defKind})
	}

	// Sort by start byte ascending so the parent-walk can stop early.
	sort.SliceStable(defs, func(i, j int) bool {
		return defs[i].node.StartByte() < defs[j].node.StartByte()
	})
	return defs
}

// containingSymbol returns the name of the nearest enclosing declaration, or
// "" when the reference is at file scope. It walks ref's parents and returns
// the first node that matches a defEntry by node id. Node ids are unique
// within a single tree and stable for the tree's lifetime, so comparing ids
// is the correct identity check (comparing pointers would also work since
// the nodes come from the same tree, but ids are explicit and match the
// tree-sitter contract).
func containingSymbol(defs []defEntry, ref *tree_sitter.Node) string {
	if ref == nil {
		return ""
	}
	// Build a set of definition node ids for O(1) containment checks.
	idSet := make(map[uintptr]struct{}, len(defs))
	for _, d := range defs {
		if d.node != nil {
			idSet[d.node.Id()] = struct{}{}
		}
	}
	// Walk parents from ref upward. We include ref itself so a reference that
	// coincides with a definition node (rare but possible for
	// @definition.class on a class used as a type elsewhere) resolves to its
	// own name.
	cur := ref
	for cur != nil {
		if _, ok := idSet[cur.Id()]; ok {
			if name := lookupDefName(defs, cur.Id()); name != "" {
				return name
			}
		}
		cur = cur.Parent()
	}
	return ""
}

// lookupDefName returns the declared name for the first defEntry whose node
// id matches id. defs is small (a file has at most a few hundred
// declarations) so a linear scan is fine.
func lookupDefName(defs []defEntry, id uintptr) string {
	for _, d := range defs {
		if d.node != nil && d.node.Id() == id {
			return d.name
		}
	}
	return ""
}

// extractArgs walks the `arguments` child of a call expression and returns
// (ArgCount, ArgTypes). ArgCount is the number of argument children; ArgTypes
// is the normalized literal type of each argument, one of: "string", "int",
// "float", "bool", "null", "unknown".
//
// refNode is the node captured by the @reference.call pattern. Depending on
// the grammar, this may be either the called identifier or the enclosing
// call expression. We locate the call expression by checking refNode and its
// parent for an `arguments` named child (the canonical call-expression marker
// across grammars), then walk each argument child and classify it by node
// kind.
//
// Normalization is purely syntactic (switch on node kind) so it is fast and
// does not require semantic resolution. Variables, expressions, and
// composite literals all collapse to "unknown" — only literal arguments
// receive a concrete type, matching the spec's requirement that the
// mismatch engine compare literal types against declared parameter types.
func extractArgs(refNode *tree_sitter.Node, source []byte) (int, []string) {
	if refNode == nil {
		return 0, nil
	}

	callNode := findCallExpr(refNode)
	if callNode == nil {
		return 0, nil
	}
	argsNode := callNode.ChildByFieldName("arguments")
	if argsNode == nil {
		return 0, nil
	}

	count := argsNode.NamedChildCount()
	if count == 0 {
		return 0, nil
	}

	argCount := 0
	argTypes := make([]string, 0, count)
	for i := uint(0); i < count; i++ {
		arg := argsNode.NamedChild(i)
		if arg == nil {
			continue
		}
		argCount++
		argTypes = append(argTypes, classifyArgType(arg.Kind()))
	}
	return argCount, argTypes
}

// findCallExpr locates the call expression node carrying an `arguments` named
// child, starting from refNode and walking up to its parent. tags.scm
// @reference.call captures sometimes point at the called identifier rather
// than the call expression itself, so we may need to look at the parent to
// find the node that exposes the `arguments` field.
func findCallExpr(refNode *tree_sitter.Node) *tree_sitter.Node {
	if refNode == nil {
		return nil
	}
	if refNode.ChildByFieldName("arguments") != nil {
		return refNode
	}
	if p := refNode.Parent(); p != nil && p.ChildByFieldName("arguments") != nil {
		return p
	}
	return nil
}

// classifyArgType maps a tree-sitter argument node kind to a normalized
// literal type string. The mapping covers the canonical literal kinds across
// the bundled grammars; any kind not listed is treated as "unknown" so the
// mismatch engine only flags mismatches between concrete literal types and
// declared parameter types.
func classifyArgType(kind string) string {
	switch kind {
	// String literals.
	case
		"string",                       // TS/JS/PHP/Ruby
		"string_literal",                // Java/C++
		"interpreted_string_literal",   // Go
		"raw_string_literal",            // Go backtick strings
		"string_content",                // some grammars
		"heredoc_body",                  // PHP/Ruby
		"concatenated_string":           // Python implicit concat
		return "string"

	// Integer literals.
	case
		"integer",                      // TS/JS
		"integer_literal",              // Java/C++/Python
		"int_literal",                  // Go
		"decimal_integer_literal",      // TS/JS
		"hex_integer_literal",          // TS/JS
		"octal_integer_literal",        // TS/JS
		"number":                       // some grammars use this for int
		return "int"

	// Float literals.
	case
		"float",                        // TS/JS
		"float_literal",                 // Java/C++/Python
		"float_literal_specifier",       // Go
		"decimal_floating_point_literal": // TS/JS
		return "float"

	// Boolean literals.
	case
		"true", "false",                // TS/JS/Python/Ruby
		"boolean":                       // some grammars
		return "bool"

	// Null / nil / None.
	case
		"null", "undefined",            // TS/JS
		"nil",                           // Go/Ruby
		"none",                          // Python
		"nullptr",                       // C++
		"null_literal":                  // Java
		return "null"
	}
	return "unknown"
}

// bundledQueryLanguages is the set of languages for which the language pack
// ships a tags.scm. We register a queryExtractor for each at package init()
//
// The set is intentionally curated (not derived from AvailableLanguages at
// init) because some pack languages ship no tags.scm at all, and registering
// an extractor for a language with no query would only ever produce
// errNoTagsQuery when invoked. Curating keeps the registry honest: only
// languages with a real bundled tags.scm appear in Languages().
var bundledQueryLanguages = []string{
	"go",
	"python",
	"javascript",
	"typescript",
	"tsx",
	"rust",
	"java",
	"c",
	"cpp",
	"ruby",
	"php",
}

func init() {
	for _, lang := range bundledQueryLanguages {
		// Only register when the pack actually bundles a tags.scm for the
		// language in this build. This keeps the registry accurate across
		// builds that include different grammar subsets.
		if tspack.GetTagsQuery(lang) == nil {
			continue
		}
		Register(newQueryExtractor(lang))
	}
}
