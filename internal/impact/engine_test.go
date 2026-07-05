package impact

import (
	"path/filepath"
	"testing"

	"github.com/Fathom/internal/db"
	"github.com/Fathom/internal/refs"
	"github.com/Fathom/internal/symbol"
)

// testStore opens a fresh bolt store in t.TempDir() and returns it.
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

// putRefs is a helper that writes references for a file in one call.
func putRefs(t *testing.T, store db.Store, file string, refs []refs.Reference) {
	t.Helper()
	if err := store.PutReferences(file, refs); err != nil {
		t.Fatalf("PutReferences(%q): %v", file, err)
	}
}

func TestDirectImpact(t *testing.T) {
	store := testStore(t)

	// A is referenced by B (B calls A).
	putRefs(t, store, "b.go", []refs.Reference{
		{SymbolName: "A", Kind: refs.RefCall, SourceFile: "b.go", SourceLine: 5, ContainingSymbol: "B"},
	})

	engine := New(store)
	result, err := engine.Calculate([]string{"A"})
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}

	if len(result.DirectlyAffected) != 1 {
		t.Fatalf("expected 1 directly affected, got %d: %+v", len(result.DirectlyAffected), result.DirectlyAffected)
	}
	if result.DirectlyAffected[0].Name != "B" {
		t.Errorf("directly affected name = %q, want %q", result.DirectlyAffected[0].Name, "B")
	}
	if result.DirectlyAffected[0].Depth != 1 {
		t.Errorf("directly affected depth = %d, want 1", result.DirectlyAffected[0].Depth)
	}
	if result.DirectlyAffected[0].Via != "A" {
		t.Errorf("directly affected via = %q, want %q", result.DirectlyAffected[0].Via, "A")
	}
	if len(result.TransitivelyAffected) != 0 {
		t.Errorf("expected 0 transitively affected, got %d", len(result.TransitivelyAffected))
	}
}

func TestTransitiveImpact(t *testing.T) {
	store := testStore(t)

	// A ← B ← C (B calls A, C calls B)
	putRefs(t, store, "b.go", []refs.Reference{
		{SymbolName: "A", Kind: refs.RefCall, SourceFile: "b.go", SourceLine: 5, ContainingSymbol: "B"},
	})
	putRefs(t, store, "c.go", []refs.Reference{
		{SymbolName: "B", Kind: refs.RefCall, SourceFile: "c.go", SourceLine: 10, ContainingSymbol: "C"},
	})

	engine := New(store)
	result, err := engine.Calculate([]string{"A"})
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}

	if len(result.DirectlyAffected) != 1 {
		t.Fatalf("expected 1 directly affected, got %d: %+v", len(result.DirectlyAffected), result.DirectlyAffected)
	}
	if result.DirectlyAffected[0].Name != "B" {
		t.Errorf("directly affected name = %q, want %q", result.DirectlyAffected[0].Name, "B")
	}

	if len(result.TransitivelyAffected) != 1 {
		t.Fatalf("expected 1 transitively affected, got %d: %+v", len(result.TransitivelyAffected), result.TransitivelyAffected)
	}
	if result.TransitivelyAffected[0].Name != "C" {
		t.Errorf("transitively affected name = %q, want %q", result.TransitivelyAffected[0].Name, "C")
	}
	if result.TransitivelyAffected[0].Depth != 2 {
		t.Errorf("transitively affected depth = %d, want 2", result.TransitivelyAffected[0].Depth)
	}
	if result.TransitivelyAffected[0].Via != "B" {
		t.Errorf("transitively affected via = %q, want %q", result.TransitivelyAffected[0].Via, "B")
	}
}

func TestCycleDetection(t *testing.T) {
	store := testStore(t)

	// A ← B, B ← A (mutual recursion)
	putRefs(t, store, "a.go", []refs.Reference{
		{SymbolName: "B", Kind: refs.RefCall, SourceFile: "a.go", SourceLine: 3, ContainingSymbol: "A"},
	})
	putRefs(t, store, "b.go", []refs.Reference{
		{SymbolName: "A", Kind: refs.RefCall, SourceFile: "b.go", SourceLine: 3, ContainingSymbol: "B"},
	})

	engine := New(store)
	result, err := engine.Calculate([]string{"A"})
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}

	// B should appear exactly once (directly affected).
	if len(result.DirectlyAffected) != 1 {
		t.Fatalf("expected 1 directly affected (B), got %d: %+v", len(result.DirectlyAffected), result.DirectlyAffected)
	}
	if result.DirectlyAffected[0].Name != "B" {
		t.Errorf("directly affected name = %q, want %q", result.DirectlyAffected[0].Name, "B")
	}
	// No transitive — the cycle is broken by the visited set.
	if len(result.TransitivelyAffected) != 0 {
		t.Errorf("expected 0 transitively affected (cycle broken), got %d", len(result.TransitivelyAffected))
	}
}

func TestEmptyInput(t *testing.T) {
	store := testStore(t)
	engine := New(store)

	result, err := engine.Calculate([]string{})
	if err != nil {
		t.Fatalf("Calculate empty: %v", err)
	}
	if len(result.DirectlyAffected) != 0 {
		t.Errorf("expected 0 directly affected, got %d", len(result.DirectlyAffected))
	}
	if len(result.TransitivelyAffected) != 0 {
		t.Errorf("expected 0 transitively affected, got %d", len(result.TransitivelyAffected))
	}
	if len(result.AffectedFiles) != 0 {
		t.Errorf("expected 0 affected files, got %d", len(result.AffectedFiles))
	}
}

func TestNoReferences(t *testing.T) {
	store := testStore(t)
	engine := New(store)

	// Symbol with no references at all.
	result, err := engine.Calculate([]string{"Orphan"})
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}
	if len(result.DirectlyAffected) != 0 {
		t.Errorf("expected 0 directly affected, got %d", len(result.DirectlyAffected))
	}
	if len(result.TransitivelyAffected) != 0 {
		t.Errorf("expected 0 transitively affected, got %d", len(result.TransitivelyAffected))
	}
}

func TestMultipleChangedSymbols(t *testing.T) {
	store := testStore(t)

	// A ← C, B ← C (C references both A and B)
	putRefs(t, store, "c.go", []refs.Reference{
		{SymbolName: "A", Kind: refs.RefCall, SourceFile: "c.go", SourceLine: 5, ContainingSymbol: "C"},
		{SymbolName: "B", Kind: refs.RefCall, SourceFile: "c.go", SourceLine: 10, ContainingSymbol: "C"},
	})

	engine := New(store)
	result, err := engine.Calculate([]string{"A", "B"})
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}

	// C should appear exactly once (deduplicated across A and B).
	if len(result.DirectlyAffected) != 1 {
		t.Fatalf("expected 1 directly affected (C), got %d: %+v", len(result.DirectlyAffected), result.DirectlyAffected)
	}
	if result.DirectlyAffected[0].Name != "C" {
		t.Errorf("directly affected name = %q, want %q", result.DirectlyAffected[0].Name, "C")
	}
}

func TestAffectedFiles(t *testing.T) {
	store := testStore(t)

	// A referenced by B in x.go and by C in y.go
	putRefs(t, store, "x.go", []refs.Reference{
		{SymbolName: "A", Kind: refs.RefCall, SourceFile: "x.go", SourceLine: 5, ContainingSymbol: "B"},
	})
	putRefs(t, store, "y.go", []refs.Reference{
		{SymbolName: "A", Kind: refs.RefCall, SourceFile: "y.go", SourceLine: 10, ContainingSymbol: "C"},
	})

	engine := New(store)
	result, err := engine.Calculate([]string{"A"})
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}

	if len(result.AffectedFiles) != 2 {
		t.Fatalf("expected 2 affected files, got %d: %v", len(result.AffectedFiles), result.AffectedFiles)
	}
	if result.AffectedFiles[0] != "x.go" {
		t.Errorf("affected file[0] = %q, want %q", result.AffectedFiles[0], "x.go")
	}
	if result.AffectedFiles[1] != "y.go" {
		t.Errorf("affected file[1] = %q, want %q", result.AffectedFiles[1], "y.go")
	}
}

func TestTopLevelReference(t *testing.T) {
	store := testStore(t)

	// A referenced at top level (ContainingSymbol = ""). Engine should use
	// SourceFile as the identifier.
	putRefs(t, store, "script.py", []refs.Reference{
		{SymbolName: "A", Kind: refs.RefCall, SourceFile: "script.py", SourceLine: 1, ContainingSymbol: ""},
	})

	engine := New(store)
	result, err := engine.Calculate([]string{"A"})
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}

	if len(result.DirectlyAffected) != 1 {
		t.Fatalf("expected 1 directly affected, got %d: %+v", len(result.DirectlyAffected), result.DirectlyAffected)
	}
	// The file path is used as the identifier when ContainingSymbol is empty.
	if result.DirectlyAffected[0].Name != "script.py" {
		t.Errorf("directly affected name = %q, want %q", result.DirectlyAffected[0].Name, "script.py")
	}
	if result.DirectlyAffected[0].Depth != 1 {
		t.Errorf("directly affected depth = %d, want 1", result.DirectlyAffected[0].Depth)
	}
}

func TestDependencyTypes(t *testing.T) {
	store := testStore(t)

	// 1. Direct function call
	putRefs(t, store, "server.go", []refs.Reference{
		{SymbolName: "HandleRequest", Kind: refs.RefCall, SourceFile: "server.go", SourceLine: 5, ContainingSymbol: "Serve"},
	})

	// 2. Interface call
	// Reader interface contains Read method
	if err := store.PutSymbols([]symbol.Symbol{
		{Name: "Reader", Kind: "interface", Content: "type Reader interface { Read(p []byte) }", File: "reader.go"},
	}); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}
	putRefs(t, store, "config.go", []refs.Reference{
		{SymbolName: "Read", Kind: refs.RefCall, SourceFile: "config.go", SourceLine: 10, ContainingSymbol: "LoadConfig"},
	})

	// 3. Struct embedding
	// UserController is a type embedding BaseController
	if err := store.PutSymbols([]symbol.Symbol{
		{Name: "UserController", Kind: "type", Content: "type UserController struct { BaseController }", File: "user.go"},
	}); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}
	putRefs(t, store, "user.go", []refs.Reference{
		{SymbolName: "BaseController", Kind: refs.RefTypeUse, SourceFile: "user.go", SourceLine: 2, ContainingSymbol: "UserController"},
	})

	engine := New(store)

	// Test direct call
	res1, err := engine.Calculate([]string{"HandleRequest"})
	if err != nil {
		t.Fatalf("Calculate direct: %v", err)
	}
	if len(res1.DirectlyAffected) != 1 || res1.DirectlyAffected[0].DependencyType != "direct_call" {
		t.Errorf("expected direct_call, got %+v", res1.DirectlyAffected)
	}

	// Test interface call
	res2, err := engine.Calculate([]string{"Read"})
	if err != nil {
		t.Fatalf("Calculate interface: %v", err)
	}
	if len(res2.DirectlyAffected) != 1 || res2.DirectlyAffected[0].DependencyType != "interface_call" {
		t.Errorf("expected interface_call, got %+v", res2.DirectlyAffected)
	}

	// Test struct embedding
	res3, err := engine.Calculate([]string{"BaseController"})
	if err != nil {
		t.Fatalf("Calculate embedding: %v", err)
	}
	if len(res3.DirectlyAffected) != 1 || res3.DirectlyAffected[0].DependencyType != "struct_embedding" {
		t.Errorf("expected struct_embedding, got %+v", res3.DirectlyAffected)
	}
}

