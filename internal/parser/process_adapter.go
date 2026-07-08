package parser

import (
	"regexp"
	"sort"
	"strings"

	tspack "github.com/xberg-io/tree-sitter-language-pack/packages/go"

	"github.com/blak0p/Fathom/internal/symbol"
)

// dedupKey identifies a symbol already emitted so the Structure, Symbols,
// Imports, and Exports passes do not double-count a declaration that appears
// in more than one view (e.g. Go functions appear in both Structure and
// Symbols; Go imports are duplicated within Imports itself).
type dedupKey struct {
	kind symbol.SymbolKind
	name string
	line int
}

// preSym pairs a symbol with its source StartByte so the final slice can be
// sorted in source order, matching the old walker's depth-first emission.
type preSym struct {
	sym   symbol.Symbol
	order uint
}

// processResultToSymbols maps a tspack.ProcessResult into Fathom symbols.
//
// The language pack exposes three parallel views of a source file:
//   - Structure: top-level and nested declarations (functions, classes, etc.)
//     with parent/child relationships. Some languages only populate Structure
//     partially (e.g. Go emits functions only; Rust emits structs/traits but
//     not enums/impls).
//   - Symbols: flat symbol list with kinds (Variable, Type, Function, ...).
//     Languages that miss items in Structure often fill them here (Go types and
//     interfaces are Symbols only; Rust enums are Symbols only).
//   - Imports / Exports: import paths and export declarations.
//
// To get a complete view across all 306 pack languages we merge all three and
// deduplicate by (Kind, Name, Line): the same declaration often appears in both
// Structure and Symbols, and Go even duplicates Imports. Line is 1-based in the
// Fathom model, so Structure/Symbol spans (0-indexed) are offset by +1.
//
// Function parameter metadata (MinParams/MaxParams/ParamTypes) is NOT available
// from ProcessResult — StructureItem.Signature is nil for every grammar we
// tested and SymbolInfo has no parameter field. The caller (ExtractSymbols)
// runs a second tree-sitter parse to populate those fields; see
// enrichFunctionSymbols in extractor.go.
func processResultToSymbols(result *tspack.ProcessResult, source []byte, lang string) []symbol.Symbol {
	if result == nil {
		return nil
	}

	seen := make(map[dedupKey]struct{}, len(result.Structure)+len(result.Symbols)+len(result.Imports)+len(result.Exports))
	out := make([]preSym, 0, len(result.Structure)+len(result.Symbols)+len(result.Imports)+len(result.Exports))

	add := func(s symbol.Symbol, startByte uint) {
		k := dedupKey{kind: s.Kind, name: s.Name, line: s.Line}
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, preSym{sym: s, order: startByte})
	}

	// 1. Structure: top-level items + flattened children (methods become their
	//    own KindFunction symbols with ClassName set to the parent name).
	for _, item := range result.Structure {
		emitStructureItem(item, source, lang, "", &out, seen)
	}

	// 2. Symbols: fill gaps left by Structure (Go types/interfaces, Rust enums,
	//    etc.). Only add entries not already produced from Structure.
	for _, si := range result.Symbols {
		sk, ok := symbolKindMap[si.Kind]
		if !ok {
			continue
		}
		// Variables/Constants are local bindings and produce noise in the
		// symbol table (every local variable would become a symbol). Skip them
		// entirely; type aliases (Kind=Type), functions, classes, interfaces,
		// and enums are kept.
		if si.Kind == tspack.SymbolKindVariable || si.Kind == tspack.SymbolKindConstant {
			continue
		}
		s := symbol.Symbol{
			Name:    si.Name,
			Kind:    sk,
			Line:    int(si.Span.StartLine) + 1,
			Col:     int(si.Span.StartColumn) + 1,
			Content: spanContent(si.Span, source),
		}
		add(s, si.Span.StartByte)
	}

	// 3. Imports → KindImport. The pack's ImportInfo.Source is the raw statement
	//    text, not the cleaned path, so we normalize per language.
	for _, imp := range result.Imports {
		name := cleanImportSource(imp.Source, lang)
		if name == "" {
			continue
		}
		s := symbol.Symbol{
			Name:    name,
			Kind:    symbol.KindImport,
			Line:    int(imp.Span.StartLine) + 1,
			Col:     int(imp.Span.StartColumn) + 1,
			Content: imp.Source,
		}
		add(s, imp.Span.StartByte)
	}

	// 4. Exports → KindExport. ExportInfo.Name is the raw declaration header
	//    (e.g. "export function hello(): void {"), so we extract the identifier.
	for _, exp := range result.Exports {
		name := cleanExportName(exp.Name)
		s := symbol.Symbol{
			Name:    name,
			Kind:    symbol.KindExport,
			Line:    int(exp.Span.StartLine) + 1,
			Col:     int(exp.Span.StartColumn) + 1,
			Content: exp.Name,
		}
		add(s, exp.Span.StartByte)
	}

	// Sort by StartByte so symbols appear in source order, matching the old
	// walker's depth-first emission.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].order < out[j].order
	})

	symbols := make([]symbol.Symbol, len(out))
	for i, p := range out {
		symbols[i] = p.sym
	}
	return symbols
}

// emitStructureItem appends a StructureItem (and recursively its children) to
// out, marking seen entries to prevent the Symbols pass from re-adding them.
// parentName is the enclosing class/struct name; it is propagated as ClassName
// onto method children.
func emitStructureItem(item tspack.StructureItem, source []byte, lang, parentName string,
	out *[]preSym, seen map[dedupKey]struct{}) {

	sk, ok := structureKindMap[item.Kind]
	if !ok {
		// Unknown structure kind (e.g. "Other"): skip the item itself but still
		// descend into children so we don't lose nested declarations.
		for _, c := range item.Children {
			emitStructureItem(c, source, lang, parentName, out, seen)
		}
		return
	}

	name := ""
	if item.Name != nil {
		name = *item.Name
	}

	s := symbol.Symbol{
		Name:      name,
		Kind:      sk,
		Line:      int(item.Span.StartLine) + 1,
		Col:       int(item.Span.StartColumn) + 1,
		Content:   spanContent(item.Span, source),
		ClassName: parentName,
	}
	if sk == symbol.KindClass {
		s.ParentClass = extractParentClassFromStructure(item, source)
	}
	key := dedupKey{kind: s.Kind, name: s.Name, line: s.Line}
	if _, dup := seen[key]; !dup {
		seen[key] = struct{}{}
		*out = append(*out, preSym{sym: s, order: item.Span.StartByte})
	}

	// Flatten children: methods inside a class become standalone KindFunction
	// symbols with ClassName set to the parent's name.
	childParent := parentName
	if sk == symbol.KindClass || sk == symbol.KindType || sk == symbol.KindInterface {
		// Methods inside a struct/interface/trait carry the enclosing name.
		childParent = name
	}
	for _, c := range item.Children {
		emitStructureItem(c, source, lang, childParent, out, seen)
	}
}

// extractParentClassFromStructure tries to recover a superclass from the
// StructureItem source text. The pack does not expose inheritance on
// StructureItem, so we scan the declaration's source for an extends/implements
// clause. This is best-effort; "" means no superclass found.
func extractParentClassFromStructure(item tspack.StructureItem, source []byte) string {
	src := spanContent(item.Span, source)
	if src == "" {
		return ""
	}
	// Common patterns: `extends Foo`, `implements Foo` (Java/TS/PHP).
	for _, kw := range []string{"extends ", "implements "} {
		if i := strings.Index(src, kw); i >= 0 {
			rest := strings.TrimSpace(src[i+len(kw):])
			// Take up to the next `{`, `,`, `;`, or newline.
			for _, cut := range []string{"{", ",", ";", "\n"} {
				if j := strings.Index(rest, cut); j >= 0 {
					rest = rest[:j]
				}
			}
			rest = strings.TrimSpace(rest)
			if rest != "" {
				return stripGenericArgs(firstIdentifier(rest))
			}
		}
	}
	return ""
}

// spanContent returns the source slice for a Span, or "" when the range is out
// of bounds. Safe to call with a nil source (returns "").
func spanContent(span tspack.Span, source []byte) string {
	if source == nil {
		return ""
	}
	start, end := span.StartByte, span.EndByte
	if end > uint(len(source)) || start > end {
		return ""
	}
	return string(source[start:end])
}

// structureKindMap maps the pack's StructureKind to Fathom's SymbolKind.
var structureKindMap = map[tspack.StructureKind]symbol.SymbolKind{
	tspack.StructureKindFunction:   symbol.KindFunction,
	tspack.StructureKindMethod:     symbol.KindFunction,
	tspack.StructureKindClass:      symbol.KindClass,
	tspack.StructureKindStruct:     symbol.KindType,
	tspack.StructureKindInterface:  symbol.KindInterface,
	tspack.StructureKindEnum:       symbol.KindType,
	tspack.StructureKindModule:     symbol.KindImport,
	tspack.StructureKindTrait:      symbol.KindInterface,
	tspack.StructureKindImpl:       symbol.KindType,
	tspack.StructureKindNamespace:  symbol.KindType,
}

// symbolKindMap maps the pack's SymbolKind to Fathom's SymbolKind. Fathom has
// no KindEnum/KindModule/KindVariable/KindConstant, so those collapse to the
// nearest existing kind. Variables/Constants are filtered out in
// processResultToSymbols (local bindings produce noise).
var symbolKindMap = map[tspack.SymbolKind]symbol.SymbolKind{
	tspack.SymbolKindVariable:   symbol.KindType,
	tspack.SymbolKindConstant:   symbol.KindType,
	tspack.SymbolKindFunction:   symbol.KindFunction,
	tspack.SymbolKindClass:      symbol.KindClass,
	tspack.SymbolKindType:       symbol.KindType,
	tspack.SymbolKindInterface: symbol.KindInterface,
	tspack.SymbolKindEnum:       symbol.KindType,
	tspack.SymbolKindModule:     symbol.KindImport,
}

// importFromRe captures the module path inside a TS/JS import statement:
// `import { foo } from 'bar';` → "bar".
var importFromRe = regexp.MustCompile(`from\s+['"]([^'"]+)['"]`)

// exportNameRe extracts the declared identifier from a raw export header:
// `export function hello(): void {` → "hello".
var exportNameRe = regexp.MustCompile(`export\s+(?:default\s+)?(?:async\s+)?(?:function|class|interface|const|let|var|enum|type)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)

// cleanImportSource normalizes the raw import statement text into the imported
// module path. The pack emits the full statement (e.g. `import "fmt"` for Go,
// `import { foo } from 'bar';` for TS) rather than just the path, so each
// language family needs a different cleanup.
func cleanImportSource(raw, lang string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	switch lang {
	case "go":
		// Go emits both `import "fmt"` and `"fmt"` for the same import; both
		// reduce to `fmt`. The dedup in processResultToSymbols handles the
		// double emission.
		s := strings.TrimPrefix(raw, "import ")
		s = strings.Trim(s, "\"")
		return s
	case "javascript", "typescript", "tsx":
		// `import { foo } from 'bar';` → "bar". Side-effect imports
		// (`import 'bar';`) fall back to the quoted string.
		if m := importFromRe.FindStringSubmatch(raw); m != nil {
			return m[1]
		}
		// Bare side-effect import: `import 'bar';`
		if strings.HasPrefix(raw, "import ") {
			rest := strings.TrimPrefix(raw, "import ")
			rest = strings.Trim(strings.TrimSuffix(rest, ";"), " \"'")
			return rest
		}
		return raw
	case "rust":
		// `use std::io;` → "std::io"
		s := strings.TrimPrefix(raw, "use ")
		s = strings.TrimSuffix(s, ";")
		return strings.TrimSpace(s)
	case "python":
		// `import os` → "os"; `from sys import argv` → "sys"
		if strings.HasPrefix(raw, "from ") || strings.HasPrefix(raw, "import ") {
			parts := strings.Fields(raw)
			if len(parts) >= 2 {
				return parts[1]
			}
		}
		return raw
	case "java":
		// `import java.util.List;` → "java.util.List"
		s := strings.TrimPrefix(raw, "import ")
		s = strings.TrimSuffix(s, ";")
		return strings.TrimSpace(s)
	case "c", "cpp":
		// `#include <stdio.h>` → "stdio.h"; `#include "foo.h"` → "foo.h"
		s := strings.TrimPrefix(raw, "#include ")
		s = strings.Trim(s, "<>\"")
		return s
	default:
		// Fallback: return the raw text trimmed; better than nothing.
		return raw
	}
}

// cleanExportName extracts the declared identifier from a raw export header
// like `export function hello(): void {`. When no identifier can be parsed, the
// raw text is returned so the symbol is still emitted with some name.
func cleanExportName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if m := exportNameRe.FindStringSubmatch(raw); m != nil {
		return m[1]
	}
	// `export default <expr>;` → "default"
	if strings.HasPrefix(raw, "export default") {
		return "default"
	}
	return raw
}