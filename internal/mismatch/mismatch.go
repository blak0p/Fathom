// Package mismatch detects signature mismatches between call sites and
// definitions across files. It runs as a parallel pass to the impact
// engine: where impact computes reachability (who references a changed
// symbol), mismatch computes correctness (does each call site still match
// the declaration it targets).
//
// The engine detects four classes of mismatch:
//
//   - Arity: a call site passes fewer arguments than the declaration's
//     MinParams, or — when the declaration is not variadic — more than its
//     MaxParams.
//   - Type: a literal argument whose normalized type is concrete
//     (not "unknown" or "null") does not match the corresponding parameter's
//     declared type (also concrete). Both sides must be concrete for a
//     mismatch to be flagged; "unknown" on either side is treated as
//     compatible because the information to compare is missing.
//   - Overloading: when multiple definitions share a name (overloaded
//     methods), a call site is flagged only if it matches NONE of them.
//   - Override: a method that overrides a parent method is checked against
//     the parent's signature, walking the inheritance chain transitively.
//
// The engine is backed by a Fathom Store. All lookups are read-only, so
// Detect is safe to run concurrently with readers.
package mismatch

import (
	"fmt"
	"sort"
	"strings"

	"github.com/blak0p/Fathom/internal/db"
	"github.com/blak0p/Fathom/internal/refs"
	"github.com/blak0p/Fathom/internal/symbol"
)

// MismatchType classifies the kind of signature mismatch found.
type MismatchType string

const (
	// MismatchArity is an argument-count mismatch (too few or too many
	// arguments relative to [MinParams, MaxParams]).
	MismatchArity MismatchType = "arity"
	// MismatchTypeMismatch is a literal argument type that does not match the
	// declared parameter type.
	MismatchTypeMismatch MismatchType = "type"
	// MismatchOverride is a method override whose signature does not match
	// the parent method's signature.
	MismatchOverride MismatchType = "override"
)

// Mismatch describes a single detected signature mismatch.
type Mismatch struct {
	// Type is the mismatch class.
	Type MismatchType `json:"type"`
	// SymbolName is the name of the referenced definition whose signature
	// was violated.
	SymbolName string `json:"symbol_name"`
	// File is the source file of the call site or overriding method.
	File string `json:"file"`
	// Line is the 1-based source line of the call site or overriding method.
	Line int `json:"line"`
	// Detail is a human-readable explanation of the mismatch.
	Detail string `json:"detail"`
}

// Engine detects signature mismatches from a reference graph and symbol
// definitions stored in a Fathom DB.
type Engine struct {
	store         db.Store
	workspaceDefs map[string][]symbol.Symbol // name → workspace definitions (override store)
}

// New creates an Engine backed by the given store.
func New(store db.Store) *Engine {
	return &Engine{store: store}
}

// SetWorkspaceDefs provides workspace-side definitions for changed symbols.
// When set, definitionsByName returns workspace definitions for the given
// name instead of loading them from the store. This enables detection of new
// mismatches introduced by workspace changes: the engine compares call sites
// in the base branch's stored index against the new (workspace) definitions,
// flagging call sites whose argument metadata no longer matches.
func (e *Engine) SetWorkspaceDefs(defs map[string][]symbol.Symbol) {
	e.workspaceDefs = defs
}

// Detect walks the given changed symbols and returns every mismatch found
// at their call sites and (for methods) against their overridden parents.
// An empty changedSymbols slice returns nil with no error.
//
// The detection order is deterministic: results are sorted by
// (SymbolName, File, Line) so output is stable across runs.
func (e *Engine) Detect(changedSymbols []string) ([]Mismatch, error) {
	if len(changedSymbols) == 0 {
		return nil, nil
	}

	var out []Mismatch

	// 1. Per-symbol call-site checks (arity, type, overloading).
	for _, name := range changedSymbols {
		refsForName, err := e.store.GetReferences(name)
		if err != nil {
			return nil, fmt.Errorf("mismatch: get references for %q: %w", name, err)
		}

		// Group definitions by name to support overloading resolution.
		defs := e.definitionsByName(name)
		if len(defs) == 0 {
			// No definition recorded (e.g. external symbol). Skip call-site
			// checks; there is nothing to compare against.
			continue
		}

		for _, r := range refsForName {
			if r.Kind != refs.RefCall {
				continue
			}
			if r.ArgCount == 0 && len(r.ArgTypes) == 0 {
				// No argument metadata recorded (e.g. pre-v3 index). Skip.
				continue
			}
			out = append(out, e.checkCallSite(name, r, defs)...)
		}
	}

	// 2. Override resolution: for each changed method symbol, walk the
	//    inheritance chain and compare signatures against parent methods.
	overrideMismatches, err := e.checkOverrides(changedSymbols)
	if err != nil {
		return nil, err
	}
	out = append(out, overrideMismatches...)

	sortMismatches(out)
	return out, nil
}

// definitionsByName returns all function definitions for name. When workspace
// definitions have been set (via SetWorkspaceDefs) and the name is among them,
// those take precedence — enabling detection of new mismatches introduced by
// workspace changes against stored base-branch references. Otherwise it falls
// back to scanning the store.
//
// Scanning all symbols and filtering is acceptable because the dataset is
// bounded by a single project's index, which is Fathom's primary workload.
func (e *Engine) definitionsByName(name string) []symbol.Symbol {
	if e.workspaceDefs != nil {
		if defs, ok := e.workspaceDefs[name]; ok {
			var fns []symbol.Symbol
			for _, d := range defs {
				if d.Kind == symbol.KindFunction {
					fns = append(fns, d)
				}
			}
			return fns
		}
	}

	all, err := e.store.ListSymbols("")
	if err != nil {
		return nil
	}
	var defs []symbol.Symbol
	for _, s := range all {
		if s.Name == name && (s.Kind == symbol.KindFunction) {
			defs = append(defs, s)
		}
	}
	return defs
}

// checkCallSite compares a single call reference against the given
// definitions. For non-overloaded names (a single definition) it performs a
// direct arity+type check. For overloaded names (multiple definitions) it
// performs the check against every definition and reports a mismatch only
// if the call site matches NONE of them.
func (e *Engine) checkCallSite(name string, r refs.Reference, defs []symbol.Symbol) []Mismatch {
	if len(defs) == 1 {
		// Non-overloaded: direct comparison.
		return checkAgainstDef(r, defs[0])
	}

	// Overloaded: a mismatch is reported only if the call matches NONE.
	for _, d := range defs {
		if len(checkAgainstDef(r, d)) == 0 {
			return nil // at least one definition matches
		}
	}
	// None matched — report a single arity/type summary mismatch.
	return []Mismatch{{
		Type:       MismatchArity,
		SymbolName: name,
		File:       r.SourceFile,
		Line:       r.SourceLine,
		Detail: fmt.Sprintf("call with %d args matches no overload of %q (tried %d definitions)",
			r.ArgCount, name, len(defs)),
	}}
}

// checkAgainstDef compares a call reference against a single definition and
// returns any arity/type mismatches found. Used both for the direct
// (non-overloaded) check and as the per-definition probe in overloading
// resolution.
func checkAgainstDef(r refs.Reference, def symbol.Symbol) []Mismatch {
	var ms []Mismatch

	// Arity check. MaxParams = -1 means variadic (no upper bound).
	if r.ArgCount < def.MinParams {
		ms = append(ms, Mismatch{
			Type:       MismatchArity,
			SymbolName: def.Name,
			File:       r.SourceFile,
			Line:       r.SourceLine,
			Detail: fmt.Sprintf("call passes %d arg(s) but %q requires at least %d",
				r.ArgCount, def.Name, def.MinParams),
		})
	} else if def.MaxParams != -1 && r.ArgCount > def.MaxParams {
		ms = append(ms, Mismatch{
			Type:       MismatchArity,
			SymbolName: def.Name,
			File:       r.SourceFile,
			Line:       r.SourceLine,
			Detail: fmt.Sprintf("call passes %d arg(s) but %q accepts at most %d",
				r.ArgCount, def.Name, def.MaxParams),
		})
	}

	// Type check: compare literal arg types against declared param types
	// position-by-position. Both sides must be concrete (not "unknown") and
	// not "null" for a mismatch to count; "unknown" means we lack the info
	// to compare, "null" is assignable to any reference type.
	maxCompare := len(r.ArgTypes)
	if len(def.ParamTypes) < maxCompare {
		maxCompare = len(def.ParamTypes)
	}
	for i := 0; i < maxCompare; i++ {
		argT := r.ArgTypes[i]
		paramT := def.ParamTypes[i]
		if argT == "unknown" || argT == "null" {
			continue
		}
		if paramT == "unknown" {
			continue
		}
		if !typeCompatible(argT, paramT) {
			ms = append(ms, Mismatch{
				Type:       MismatchTypeMismatch,
				SymbolName: def.Name,
				File:       r.SourceFile,
				Line:       r.SourceLine,
				Detail: fmt.Sprintf("arg %d type %q does not match parameter type %q",
					i+1, argT, paramT),
			})
		}
	}

	return ms
}

// typeCompatible reports whether a literal argument type is compatible with
// a declared parameter type. The check is exact-match by default; the only
// widening allowed is int→float (a common implicit coercion in many
// languages) which we accept to avoid false positives on numeric literals.
// Callers that need language-specific coercion rules can extend this.
func typeCompatible(argT, paramT string) bool {
	if argT == paramT {
		return true
	}
	// int literal can satisfy a float parameter (implicit widening).
	if argT == "int" && paramT == "float" {
		return true
	}
	return false
}

// checkOverrides walks the inheritance chain for each changed method symbol
// and flags overrides whose signature differs from the parent method's
// signature.
func (e *Engine) checkOverrides(changedSymbols []string) ([]Mismatch, error) {
	// Build the class → parent map and the (class, method) → symbol index
	// once from all stored symbols. Both are derived from the same scan to
	// keep the override resolution self-contained.
	allSyms, err := e.store.ListSymbols("")
	if err != nil {
		return nil, fmt.Errorf("mismatch: list symbols for override resolution: %w", err)
	}
	classParent := make(map[string]string)
	// methodsByClass maps a class name to the methods declared directly on
	// it (ClassName == that class).
	methodsByClass := make(map[string][]symbol.Symbol)
	for _, s := range allSyms {
		if s.Kind == symbol.KindClass && s.ParentClass != "" {
			classParent[s.Name] = s.ParentClass
		}
		if s.Kind == symbol.KindFunction && s.ClassName != "" {
			methodsByClass[s.ClassName] = append(methodsByClass[s.ClassName], s)
		}
	}

	// Index changed method symbols (those with a ClassName set).
	changedSet := make(map[string]bool, len(changedSymbols))
	for _, n := range changedSymbols {
		changedSet[n] = true
	}

	var out []Mismatch
	for class, methods := range methodsByClass {
		for _, m := range methods {
			if !changedSet[m.Name] {
				continue
			}
			out = append(out, e.compareOverride(m, class, classParent, methodsByClass)...)
		}
	}
	return out, nil
}

// compareOverride walks the parent chain of class and compares method m
// against any parent method with the same name. A mismatch is reported when
// a parent defines a method of the same name whose [MinParams, MaxParams]
// range or ParamTypes differ from the overriding method.
//
// The walk is bounded by a visited set to break cycles in the inheritance
// graph (e.g. via malformed or self-referential `extends` clauses), matching
// the impact engine's cycle-detection approach.
func (e *Engine) compareOverride(m symbol.Symbol, class string, classParent map[string]string, methodsByClass map[string][]symbol.Symbol) []Mismatch {
	var out []Mismatch
	visited := make(map[string]bool)
	cur := class
	for cur != "" && !visited[cur] {
		visited[cur] = true
		parent, ok := classParent[cur]
		if !ok {
			break // no parent recorded → chain ends
		}
		// Look up the parent's method with the same name.
		for _, pm := range methodsByClass[parent] {
			if pm.Name != m.Name {
				continue
			}
			if !sameSignature(m, pm) {
				out = append(out, Mismatch{
					Type:       MismatchOverride,
					SymbolName: m.Name,
					File:       m.File,
					Line:       m.Line,
					Detail: fmt.Sprintf("override %s.%s signature differs from parent %s.%s (min %d vs %d, max %d vs %d)",
						m.ClassName, m.Name, parent, pm.Name,
						m.MinParams, pm.MinParams, m.MaxParams, pm.MaxParams),
				})
			}
		}
		cur = parent
	}
	return out
}

// sameSignature reports whether two method symbols have compatible
// signatures: identical arity bounds and identical parameter type lists
// (modulo "unknown" which is treated as compatible with anything, since a
// missing annotation carries no comparable information).
func sameSignature(a, b symbol.Symbol) bool {
	if a.MinParams != b.MinParams {
		return false
	}
	if a.MaxParams != b.MaxParams {
		return false
	}
	if len(a.ParamTypes) != len(b.ParamTypes) {
		return false
	}
	for i := range a.ParamTypes {
		if a.ParamTypes[i] == "unknown" || b.ParamTypes[i] == "unknown" {
			continue
		}
		if !typeCompatible(a.ParamTypes[i], b.ParamTypes[i]) && !typeCompatible(b.ParamTypes[i], a.ParamTypes[i]) {
			return false
		}
	}
	return true
}

// sortMismatches orders results by (SymbolName, File, Line) for stable output.
func sortMismatches(ms []Mismatch) {
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].SymbolName != ms[j].SymbolName {
			return ms[i].SymbolName < ms[j].SymbolName
		}
		if ms[i].File != ms[j].File {
			return ms[i].File < ms[j].File
		}
		return ms[i].Line < ms[j].Line
	})
}

// FormatHuman renders a slice of mismatches as a human-readable report. It is
// used by the CLI when --json is not set. The output is grouped by symbol
// name for readability.
func FormatHuman(ms []Mismatch) string {
	if len(ms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Signature mismatches:\n")
	for _, m := range ms {
		b.WriteString(fmt.Sprintf("  [%s] %s (%s:%d): %s\n", m.Type, m.SymbolName, m.File, m.Line, m.Detail))
	}
	return b.String()
}