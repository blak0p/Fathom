package mismatch

import (
	"path/filepath"
	"testing"

	"github.com/blak0p/Fathom/internal/db"
	"github.com/blak0p/Fathom/internal/refs"
	"github.com/blak0p/Fathom/internal/symbol"
)

// testStore opens a fresh bolt store in t.TempDir() and returns it. It
// mirrors the helper used by the impact-engine tests so mismatch tests run
// against the same real persistence layer.
func testStore(t *testing.T) db.Store {
	t.Helper()
	s := db.New()
	path := filepath.Join(t.TempDir(), "test.bolt")
	if err := s.Open(path); err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// putSyms writes symbols in one batch and fails the test on error.
func putSyms(t *testing.T, store db.Store, syms []symbol.Symbol) {
	t.Helper()
	if err := store.PutSymbols(syms); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}
}

// putRefs writes references for a file in one call and fails the test on
// error.
func putRefs(t *testing.T, store db.Store, file string, rs []refs.Reference) {
	t.Helper()
	if err := store.PutReferences(file, rs); err != nil {
		t.Fatalf("PutReferences(%q): %v", file, err)
	}
}

// TestArityTooFew checks that a call passing fewer args than MinParams is
// flagged as an arity mismatch.
func TestArityTooFew(t *testing.T) {
	store := testStore(t)

	// Definition: foo(a, b) — MinParams=2, MaxParams=2.
	putSyms(t, store, []symbol.Symbol{
		{Name: "foo", Kind: symbol.KindFunction, File: "a.go", MinParams: 2, MaxParams: 2, ParamTypes: []string{"unknown", "unknown"}},
	})
	// Call site: foo(x) — 1 arg.
	putRefs(t, store, "b.go", []refs.Reference{
		{SymbolName: "foo", Kind: refs.RefCall, SourceFile: "b.go", SourceLine: 5, ContainingSymbol: "caller", ArgCount: 1, ArgTypes: []string{"unknown"}},
	})

	got, err := New(store).Detect([]string{"foo"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 mismatch, got %d: %+v", len(got), got)
	}
	if got[0].Type != MismatchArity {
		t.Errorf("type = %q, want %q", got[0].Type, MismatchArity)
	}
}

// TestArityTooMany checks that a non-variadic call passing more args than
// MaxParams is flagged.
func TestArityTooMany(t *testing.T) {
	store := testStore(t)

	putSyms(t, store, []symbol.Symbol{
		{Name: "bar", Kind: symbol.KindFunction, File: "a.go", MinParams: 1, MaxParams: 1, ParamTypes: []string{"string"}},
	})
	// bar("x", "y") — 2 args against a 1-arg definition.
	putRefs(t, store, "b.go", []refs.Reference{
		{SymbolName: "bar", Kind: refs.RefCall, SourceFile: "b.go", SourceLine: 3, ContainingSymbol: "c", ArgCount: 2, ArgTypes: []string{"string", "string"}},
	})

	got, err := New(store).Detect([]string{"bar"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 mismatch, got %d: %+v", len(got), got)
	}
	if got[0].Type != MismatchArity {
		t.Errorf("type = %q, want %q", got[0].Type, MismatchArity)
	}
}

// TestArityVariadicOk checks that a variadic definition (MaxParams = -1)
// accepts any number of args >= MinParams without flagging.
func TestArityVariadicOk(t *testing.T) {
	store := testStore(t)

	// Log(msg string, args ...any) — MinParams=1, MaxParams=-1.
	putSyms(t, store, []symbol.Symbol{
		{Name: "Log", Kind: symbol.KindFunction, File: "a.go", MinParams: 1, MaxParams: -1, ParamTypes: []string{"string", "unknown"}},
	})
	// Log("err", 404, true) — 3 args, within variadic bounds.
	putRefs(t, store, "b.go", []refs.Reference{
		{SymbolName: "Log", Kind: refs.RefCall, SourceFile: "b.go", SourceLine: 7, ContainingSymbol: "c", ArgCount: 3, ArgTypes: []string{"string", "int", "bool"}},
	})

	got, err := New(store).Detect([]string{"Log"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 mismatches for variadic call in bounds, got %d: %+v", len(got), got)
	}
}

// TestTypeMismatch checks that a literal arg whose concrete type does not
// match the declared param type is flagged.
func TestTypeMismatch(t *testing.T) {
	store := testStore(t)

	// render(name string) — param type "string".
	putSyms(t, store, []symbol.Symbol{
		{Name: "render", Kind: symbol.KindFunction, File: "a.go", MinParams: 1, MaxParams: 1, ParamTypes: []string{"string"}},
	})
	// render(404) — int literal against string param.
	putRefs(t, store, "b.go", []refs.Reference{
		{SymbolName: "render", Kind: refs.RefCall, SourceFile: "b.go", SourceLine: 2, ContainingSymbol: "c", ArgCount: 1, ArgTypes: []string{"int"}},
	})

	got, err := New(store).Detect([]string{"render"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 mismatch, got %d: %+v", len(got), got)
	}
	if got[0].Type != MismatchTypeMismatch {
		t.Errorf("type = %q, want %q", got[0].Type, MismatchTypeMismatch)
	}
}

// TestTypeUnknownNotFlagged checks that an "unknown" arg type (variable,
// expression) is NOT flagged against a concrete param type, since we lack
// the info to compare.
func TestTypeUnknownNotFlagged(t *testing.T) {
	store := testStore(t)

	putSyms(t, store, []symbol.Symbol{
		{Name: "render", Kind: symbol.KindFunction, File: "a.go", MinParams: 1, MaxParams: 1, ParamTypes: []string{"string"}},
	})
	// render(val) — unknown arg type.
	putRefs(t, store, "b.go", []refs.Reference{
		{SymbolName: "render", Kind: refs.RefCall, SourceFile: "b.go", SourceLine: 2, ContainingSymbol: "c", ArgCount: 1, ArgTypes: []string{"unknown"}},
	})

	got, err := New(store).Detect([]string{"render"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 mismatches for unknown arg, got %d: %+v", len(got), got)
	}
}

// TestTypeNullNotFlagged checks that a "null" literal arg is NOT flagged
// against any concrete param type (null is assignable to reference types).
func TestTypeNullNotFlagged(t *testing.T) {
	store := testStore(t)

	putSyms(t, store, []symbol.Symbol{
		{Name: "set", Kind: symbol.KindFunction, File: "a.go", MinParams: 1, MaxParams: 1, ParamTypes: []string{"string"}},
	})
	// set(nil) — null literal.
	putRefs(t, store, "b.go", []refs.Reference{
		{SymbolName: "set", Kind: refs.RefCall, SourceFile: "b.go", SourceLine: 2, ContainingSymbol: "c", ArgCount: 1, ArgTypes: []string{"null"}},
	})

	got, err := New(store).Detect([]string{"set"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 mismatches for null arg, got %d: %+v", len(got), got)
	}
}

// TestIntToFloatWidening checks that an int literal satisfies a float
// parameter (implicit widening), so no mismatch is flagged.
func TestIntToFloatWidening(t *testing.T) {
	store := testStore(t)

	putSyms(t, store, []symbol.Symbol{
		{Name: "scale", Kind: symbol.KindFunction, File: "a.go", MinParams: 1, MaxParams: 1, ParamTypes: []string{"float"}},
	})
	// scale(2) — int literal against float param.
	putRefs(t, store, "b.go", []refs.Reference{
		{SymbolName: "scale", Kind: refs.RefCall, SourceFile: "b.go", SourceLine: 2, ContainingSymbol: "c", ArgCount: 1, ArgTypes: []string{"int"}},
	})

	got, err := New(store).Detect([]string{"scale"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 mismatches for int→float widening, got %d: %+v", len(got), got)
	}
}

// TestOverloadingMatchedNotFlagged checks that an overloaded name with a
// matching definition does NOT flag a mismatch. The two overloads live in
// different files because the store keys symbols by (file, name), so two
// same-named definitions in one file would collide.
func TestOverloadingMatchedNotFlagged(t *testing.T) {
	store := testStore(t)

	// Two overloads: parse(string) in a.go and parse(int) in c.go.
	putSyms(t, store, []symbol.Symbol{
		{Name: "parse", Kind: symbol.KindFunction, File: "a.go", MinParams: 1, MaxParams: 1, ParamTypes: []string{"string"}},
		{Name: "parse", Kind: symbol.KindFunction, File: "c.go", MinParams: 1, MaxParams: 1, ParamTypes: []string{"int"}},
	})
	// parse("test") — matches the string overload.
	putRefs(t, store, "b.go", []refs.Reference{
		{SymbolName: "parse", Kind: refs.RefCall, SourceFile: "b.go", SourceLine: 4, ContainingSymbol: "c", ArgCount: 1, ArgTypes: []string{"string"}},
	})

	got, err := New(store).Detect([]string{"parse"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 mismatches when an overload matches, got %d: %+v", len(got), got)
	}
}

// TestOverloadingNoneMatched checks that an overloaded name with NO matching
// definition IS flagged (exactly one summary mismatch).
func TestOverloadingNoneMatched(t *testing.T) {
	store := testStore(t)

	// Two overloads in different files (see TestOverloadingMatchedNotFlagged
	// for why same-file overloads collide in the store).
	putSyms(t, store, []symbol.Symbol{
		{Name: "parse", Kind: symbol.KindFunction, File: "a.go", MinParams: 1, MaxParams: 1, ParamTypes: []string{"string"}},
		{Name: "parse", Kind: symbol.KindFunction, File: "c.go", MinParams: 1, MaxParams: 1, ParamTypes: []string{"int"}},
	})
	// parse(true) — bool matches neither overload.
	putRefs(t, store, "b.go", []refs.Reference{
		{SymbolName: "parse", Kind: refs.RefCall, SourceFile: "b.go", SourceLine: 4, ContainingSymbol: "c", ArgCount: 1, ArgTypes: []string{"bool"}},
	})

	got, err := New(store).Detect([]string{"parse"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 mismatch when no overload matches, got %d: %+v", len(got), got)
	}
	if got[0].Type != MismatchArity {
		t.Errorf("type = %q, want %q", got[0].Type, MismatchArity)
	}
}

// TestOverrideMismatch checks that an overriding method whose signature
// differs from the parent method is flagged as an override mismatch.
func TestOverrideMismatch(t *testing.T) {
	store := testStore(t)

	// Class A defines foo(string). Class B extends A and overrides foo(int).
	putSyms(t, store, []symbol.Symbol{
		{Name: "A", Kind: symbol.KindClass, File: "a.go", ParentClass: ""},
		{Name: "B", Kind: symbol.KindClass, File: "b.go", ParentClass: "A"},
		{Name: "foo", Kind: symbol.KindFunction, File: "a.go", ClassName: "A", MinParams: 1, MaxParams: 1, ParamTypes: []string{"string"}},
		{Name: "foo", Kind: symbol.KindFunction, File: "b.go", ClassName: "B", MinParams: 1, MaxParams: 1, ParamTypes: []string{"int"}},
	})

	got, err := New(store).Detect([]string{"foo"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	// Expect at least one override mismatch.
	found := false
	for _, m := range got {
		if m.Type == MismatchOverride {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected an override mismatch, got: %+v", got)
	}
}

// TestOverrideCompatibleNotFlagged checks that an overriding method with the
// same signature as the parent is NOT flagged.
func TestOverrideCompatibleNotFlagged(t *testing.T) {
	store := testStore(t)

	putSyms(t, store, []symbol.Symbol{
		{Name: "A", Kind: symbol.KindClass, File: "a.go"},
		{Name: "B", Kind: symbol.KindClass, File: "b.go", ParentClass: "A"},
		{Name: "foo", Kind: symbol.KindFunction, File: "a.go", ClassName: "A", MinParams: 1, MaxParams: 1, ParamTypes: []string{"string"}},
		{Name: "foo", Kind: symbol.KindFunction, File: "b.go", ClassName: "B", MinParams: 1, MaxParams: 1, ParamTypes: []string{"string"}},
	})

	got, err := New(store).Detect([]string{"foo"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	for _, m := range got {
		if m.Type == MismatchOverride {
			t.Fatalf("expected NO override mismatch, got: %+v", m)
		}
	}
}

// TestOverrideTransitiveChain checks that the engine walks the inheritance
// chain transitively: C → B → A, with foo overridden in C and defined in A.
func TestOverrideTransitiveChain(t *testing.T) {
	store := testStore(t)

	putSyms(t, store, []symbol.Symbol{
		{Name: "A", Kind: symbol.KindClass, File: "a.go"},
		{Name: "B", Kind: symbol.KindClass, File: "b.go", ParentClass: "A"},
		{Name: "C", Kind: symbol.KindClass, File: "c.go", ParentClass: "B"},
		{Name: "foo", Kind: symbol.KindFunction, File: "a.go", ClassName: "A", MinParams: 1, MaxParams: 1, ParamTypes: []string{"string"}},
		{Name: "foo", Kind: symbol.KindFunction, File: "c.go", ClassName: "C", MinParams: 2, MaxParams: 2, ParamTypes: []string{"string", "int"}},
	})

	got, err := New(store).Detect([]string{"foo"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	found := false
	for _, m := range got {
		if m.Type == MismatchOverride {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a transitive override mismatch, got: %+v", got)
	}
}

// TestEmptyInput checks that Detect on no changed symbols returns nil.
func TestEmptyInput(t *testing.T) {
	store := testStore(t)
	got, err := New(store).Detect(nil)
	if err != nil {
		t.Fatalf("Detect empty: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

// TestNonCallReferenceSkipped checks that a type-use reference (not a call)
// is ignored by the mismatch engine, even with mismatched metadata.
func TestNonCallReferenceSkipped(t *testing.T) {
	store := testStore(t)

	putSyms(t, store, []symbol.Symbol{
		{Name: "Widget", Kind: symbol.KindType, File: "a.go"},
	})
	// A type_use reference carries no arg metadata; it must be skipped.
	putRefs(t, store, "b.go", []refs.Reference{
		{SymbolName: "Widget", Kind: refs.RefTypeUse, SourceFile: "b.go", SourceLine: 1, ContainingSymbol: ""},
	})

	got, err := New(store).Detect([]string{"Widget"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 mismatches for non-call reference, got %d: %+v", len(got), got)
	}
}

// TestFormatHumanEmpty checks that FormatHuman returns "" for no mismatches.
func TestFormatHumanEmpty(t *testing.T) {
	if got := FormatHuman(nil); got != "" {
		t.Errorf("FormatHuman(nil) = %q, want %q", got, "")
	}
}

// TestFormatHumanNonEmpty checks that FormatHuman produces a non-empty
// report containing the mismatch detail.
func TestFormatHumanNonEmpty(t *testing.T) {
	ms := []Mismatch{
		{Type: MismatchArity, SymbolName: "foo", File: "b.go", Line: 3, Detail: "call passes 1 arg(s) but \"foo\" requires at least 2"},
	}
	got := FormatHuman(ms)
	if got == "" {
		t.Fatalf("FormatHuman returned empty for non-empty input")
	}
	if !contains(got, "foo") || !contains(got, "b.go") {
		t.Errorf("FormatHuman output missing expected content: %q", got)
	}
}

// contains is a small helper to avoid pulling strings.Contains into the test
// file's import list when only one check needs it.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

// indexOf returns the byte index of sub in s, or -1.
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestMismatchesSorted checks that Detect returns mismatches sorted by
// (SymbolName, File, Line) so output is stable across runs.
func TestMismatchesSorted(t *testing.T) {
	store := testStore(t)

	putSyms(t, store, []symbol.Symbol{
		{Name: "zeta", Kind: symbol.KindFunction, File: "z.go", MinParams: 2, MaxParams: 2, ParamTypes: []string{"unknown", "unknown"}},
		{Name: "alpha", Kind: symbol.KindFunction, File: "alpha.go", MinParams: 2, MaxParams: 2, ParamTypes: []string{"unknown", "unknown"}},
	})
	putRefs(t, store, "caller.go", []refs.Reference{
		{SymbolName: "zeta", Kind: refs.RefCall, SourceFile: "caller.go", SourceLine: 10, ContainingSymbol: "c", ArgCount: 1, ArgTypes: []string{"unknown"}},
		{SymbolName: "alpha", Kind: refs.RefCall, SourceFile: "caller.go", SourceLine: 5, ContainingSymbol: "c", ArgCount: 1, ArgTypes: []string{"unknown"}},
	})

	got, err := New(store).Detect([]string{"zeta", "alpha"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 mismatches, got %d: %+v", len(got), got)
	}
	if got[0].SymbolName != "alpha" {
		t.Errorf("first mismatch symbol = %q, want %q (sorted ascending)", got[0].SymbolName, "alpha")
	}
	if got[1].SymbolName != "zeta" {
		t.Errorf("second mismatch symbol = %q, want %q", got[1].SymbolName, "zeta")
	}
}

// TestSameSignatureUnit is a focused unit test on the sameSignature helper.
func TestSameSignatureUnit(t *testing.T) {
	cases := []struct {
		name string
		a, b symbol.Symbol
		want bool
	}{
		{
			name: "identical",
			a:    symbol.Symbol{MinParams: 1, MaxParams: 2, ParamTypes: []string{"string", "int"}},
			b:    symbol.Symbol{MinParams: 1, MaxParams: 2, ParamTypes: []string{"string", "int"}},
			want: true,
		},
		{
			name: "different min",
			a:    symbol.Symbol{MinParams: 1, MaxParams: 2},
			b:    symbol.Symbol{MinParams: 2, MaxParams: 2},
			want: false,
		},
		{
			name: "different max",
			a:    symbol.Symbol{MinParams: 1, MaxParams: 2},
			b:    symbol.Symbol{MinParams: 1, MaxParams: -1},
			want: false,
		},
		{
			name: "unknown compatible",
			a:    symbol.Symbol{MinParams: 1, MaxParams: 1, ParamTypes: []string{"unknown"}},
			b:    symbol.Symbol{MinParams: 1, MaxParams: 1, ParamTypes: []string{"string"}},
			want: true,
		},
		{
			name: "int float compatible via widening",
			a:    symbol.Symbol{MinParams: 1, MaxParams: 1, ParamTypes: []string{"int"}},
			b:    symbol.Symbol{MinParams: 1, MaxParams: 1, ParamTypes: []string{"float"}},
			want: true,
		},
		{
			name: "string int incompatible",
			a:    symbol.Symbol{MinParams: 1, MaxParams: 1, ParamTypes: []string{"string"}},
			b:    symbol.Symbol{MinParams: 1, MaxParams: 1, ParamTypes: []string{"int"}},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sameSignature(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("sameSignature = %v, want %v", got, tc.want)
			}
		})
	}
}