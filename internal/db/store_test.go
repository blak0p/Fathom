package db

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/blak0p/Fathom/internal/refs"
	"github.com/blak0p/Fathom/internal/symbol"
	"go.etcd.io/bbolt"
)

// newTestStore opens a fresh boltStore backed by a temp-dir database and
// returns it along with its on-disk path. Close is registered with t.Cleanup.
func newTestStore(t *testing.T) (Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fathom.db")

	s := New()
	if err := s.Open(path); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func fileASymbols() []symbol.Symbol {
	return []symbol.Symbol{
		{Name: "FuncA", Kind: symbol.KindFunction, File: "a.go", Line: 1, Col: 1, Content: "func FuncA() {}"},
		{Name: "TypeA", Kind: symbol.KindType, File: "a.go", Line: 5, Col: 6, Content: "type TypeA struct{}"},
	}
}

func fileBSymbols() []symbol.Symbol {
	return []symbol.Symbol{
		{Name: "FuncB", Kind: symbol.KindFunction, File: "b.go", Line: 10, Col: 1, Content: "func FuncB() {}"},
		{Name: "TypeB", Kind: symbol.KindType, File: "b.go", Line: 14, Col: 6, Content: "type TypeB struct{}"},
	}
}

// TestStoreOpenClose verifies Open/Close and that Close is idempotent and
// leaves the db file on disk.
func TestStoreOpenClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fathom.db")

	s := New()
	if err := s.Open(path); err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close should be no-op, got %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("db file should exist after close: %v", err)
	}
}

// TestPutGetSymbolRoundTrip writes one file's symbols and reads each back.
func TestPutGetSymbolRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	syms := fileASymbols()

	if err := s.PutSymbols(syms); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}
	for _, want := range syms {
		got, err := s.GetSymbol(want.File, want.Name)
		if err != nil {
			t.Fatalf("GetSymbol(%q, %q): %v", want.File, want.Name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("GetSymbol mismatch for %q:\nwant: %+v\ngot:  %+v", want.Name, want, got)
		}
	}
}

// TestListSymbolsByFilePrefix verifies prefix scanning by file and by
// directory.
func TestListSymbolsByFilePrefix(t *testing.T) {
	s, _ := newTestStore(t)

	all := append(append([]symbol.Symbol{}, fileASymbols()...), fileBSymbols()...)
	if err := s.PutSymbols(all); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}

	got, err := s.ListSymbols("a.go#")
	if err != nil {
		t.Fatalf("ListSymbols a.go#: %v", err)
	}
	if len(got) != len(fileASymbols()) {
		t.Fatalf("ListSymbols a.go# = %d, want %d", len(got), len(fileASymbols()))
	}

	got, err = s.ListSymbols("b.go#")
	if err != nil {
		t.Fatalf("ListSymbols b.go#: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListSymbols b.go# = %d, want 2", len(got))
	}

	// Directory-style prefix "a" matches a.go keys but not b.go keys.
	got, err = s.ListSymbols("a")
	if err != nil {
		t.Fatalf("ListSymbols a: %v", err)
	}
	if len(got) != len(fileASymbols()) {
		t.Fatalf("ListSymbols a = %d, want %d", len(got), len(fileASymbols()))
	}
}

// TestListSymbolsEmptyPrefix verifies that an empty prefix returns all
// symbols.
func TestListSymbolsEmptyPrefix(t *testing.T) {
	s, _ := newTestStore(t)

	all := append(append([]symbol.Symbol{}, fileASymbols()...), fileBSymbols()...)
	if err := s.PutSymbols(all); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}
	got, err := s.ListSymbols("")
	if err != nil {
		t.Fatalf("ListSymbols empty: %v", err)
	}
	if len(got) != len(all) {
		t.Fatalf("ListSymbols empty = %d, want %d", len(got), len(all))
	}
}

// TestMetaRoundTrip verifies PutMeta/GetMeta persistence.
func TestMetaRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)

	cases := []struct{ key, value string }{
		{"schema_version", "1"},
		{"last_index", "2026-07-04T10:00:00Z"},
		{"repo_root", "/Fathom/Fathom"},
	}
	for _, c := range cases {
		if err := s.PutMeta(c.key, c.value); err != nil {
			t.Fatalf("PutMeta(%q): %v", c.key, err)
		}
	}
	for _, c := range cases {
		got, err := s.GetMeta(c.key)
		if err != nil {
			t.Fatalf("GetMeta(%q): %v", c.key, err)
		}
		if got != c.value {
			t.Fatalf("GetMeta(%q) = %q, want %q", c.key, got, c.value)
		}
	}
}

// TestGetSymbolNotFound asserts GetSymbol on a missing key returns
// ErrNotFound.
func TestGetSymbolNotFound(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.GetSymbol("missing.go", "Nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSymbol missing: err = %v, want ErrNotFound", err)
	}
}

// TestGetMetaNotFound asserts GetMeta on a missing key returns ErrNotFound.
func TestGetMetaNotFound(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.GetMeta("nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMeta missing: err = %v, want ErrNotFound", err)
	}
}

// TestPutSymbolsAtomicity verifies two atomicity guarantees:
//   - Per-call atomicity: when a PutSymbols transaction fails mid-batch, none
//     of that batch's symbols are committed (bbolt rolls back the Update).
//   - Per-file isolation: a failed write for file B does not affect the
//     already-committed symbols of file A.
//
// bbolt holds an exclusive file lock while open, so we must close the first
// handle before reopening the same file. The flow is: write A → close →
// reopen → run a failing B transaction (rolled back) → close → reopen →
// verify A is intact and B is absent.
func TestPutSymbolsAtomicity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fathom.db")

	// 1. Open, write A, close.
	s1 := New()
	if err := s1.Open(path); err != nil {
		t.Fatalf("open s1: %v", err)
	}
	aSyms := fileASymbols()
	if err := s1.PutSymbols(aSyms); err != nil {
		t.Fatalf("PutSymbols A: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close s1: %v", err)
	}

	// 2. Reopen the same file directly with bbolt and run a PutSymbols-shaped
	//    transaction that writes the first B symbol, then returns a sentinel
	//    error. bbolt must roll back the entire transaction.
	other, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	bSyms := fileBSymbols()
	wantErr := errors.New("simulated mid-batch failure")
	putErr := other.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketSymbols))
		if bucket == nil {
			return errors.New("symbols bucket missing")
		}
		data, mErr := json.Marshal(bSyms[0])
		if mErr != nil {
			return mErr
		}
		if pErr := bucket.Put(symbolKey(bSyms[0].File, bSyms[0].Name), data); pErr != nil {
			return pErr
		}
		// Fail before writing the second B symbol. bbolt discards the whole tx.
		return wantErr
	})
	if !errors.Is(putErr, wantErr) {
		t.Fatalf("expected simulated failure, got %v", putErr)
	}
	if err := other.Close(); err != nil {
		t.Fatalf("close other: %v", err)
	}

	// 3. Reopen via the Store interface and verify A is intact and B is gone.
	s2 := New()
	if err := s2.Open(path); err != nil {
		t.Fatalf("open s2: %v", err)
	}
	defer s2.Close()

	for _, want := range aSyms {
		got, gErr := s2.GetSymbol(want.File, want.Name)
		if gErr != nil {
			t.Fatalf("A symbol %q should survive failed B write: %v", want.Name, gErr)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("A symbol %q mismatch:\nwant: %+v\ngot:  %+v", want.Name, want, got)
		}
	}
	for _, b := range bSyms {
		_, gErr := s2.GetSymbol(b.File, b.Name)
		if !errors.Is(gErr, ErrNotFound) {
			t.Fatalf("B symbol %q must NOT exist after rolled-back tx: err = %v, want ErrNotFound", b.Name, gErr)
		}
	}
}

// ---------- References (Phase 2) ----------

// fileAReferences returns a small set of references for "a.go" used by the
// reference-storage tests. The SourceFile field is intentionally left blank
// to verify PutReferences stamps it from the filePath argument.
func fileAReferences() []refs.Reference {
	return []refs.Reference{
		{SymbolName: "HandleRequest", Kind: refs.RefCall, SourceLine: 12, SourceCol: 3, ContainingSymbol: "Serve"},
		{SymbolName: "Config", Kind: refs.RefTypeUse, SourceLine: 14, SourceCol: 8},
		{SymbolName: "Parse", Kind: refs.RefCall, SourceLine: 20, SourceCol: 3},
	}
}

// fileBReferences returns references for "b.go" so ListReferencesByFile and
// GetReferences can verify filtering by source file and target symbol across
// multiple files.
func fileBReferences() []refs.Reference {
	return []refs.Reference{
		{SymbolName: "HandleRequest", Kind: refs.RefCall, SourceLine: 5, SourceCol: 3, ContainingSymbol: "Main"},
		{SymbolName: "Serve", Kind: refs.RefCall, SourceLine: 9, SourceCol: 3},
	}
}

// TestPutGetReferencesRoundTrip writes one file's references and reads them
// back via GetReferences for each target symbol. Verifies the full Reference
// struct survives JSON encode/decode and that PutReferences stamps
// SourceFile onto each reference.
func TestPutGetReferencesRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	want := fileAReferences()

	if err := s.PutReferences("a.go", want); err != nil {
		t.Fatalf("PutReferences: %v", err)
	}

	// Group by target symbol to assert each round-trips with its fields
	// intact.
	bySymbol := map[string][]refs.Reference{}
	for _, r := range want {
		bySymbol[r.SymbolName] = append(bySymbol[r.SymbolName], r)
	}

	for sym, wantRefs := range bySymbol {
		got, err := s.GetReferences(sym)
		if err != nil {
			t.Fatalf("GetReferences(%q): %v", sym, err)
		}
		if len(got) != len(wantRefs) {
			t.Fatalf("GetReferences(%q) = %d refs, want %d", sym, len(got), len(wantRefs))
		}
		// PutReferences stamps SourceFile; the want refs above have it
		// blank, so set it on want before comparing.
		for i := range wantRefs {
			wantRefs[i].SourceFile = "a.go"
		}
		// Sort got by (file, line) to match the store's sortReferences.
		// (GetReferences already sorts; we just compare element-wise.)
		for i, g := range got {
			if !reflect.DeepEqual(g, wantRefs[i]) {
				t.Errorf("GetReferences(%q)[%d] = %+v, want %+v", sym, i, g, wantRefs[i])
			}
		}
	}
}

// TestGetReferencesNotFound verifies that querying a symbol with no stored
// references returns an empty result and no error, NOT ErrNotFound.
// GetReferences is a prefix scan, so the absence of matching keys is a normal
// empty result, not a missing-key error. We accept either a nil or
// zero-length slice — both represent "no references" — and only assert no
// error and len 0.
func TestGetReferencesNotFound(t *testing.T) {
	s, _ := newTestStore(t)
	got, err := s.GetReferences("NoSuchSymbol")
	if err != nil {
		t.Fatalf("GetReferences on empty bucket: err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Fatalf("GetReferences on empty bucket: got %d refs, want 0", len(got))
	}
}

// TestListReferencesByFile verifies that ListReferencesByFile returns only the
// references whose SourceFile matches the given path, across multiple files.
func TestListReferencesByFile(t *testing.T) {
	s, _ := newTestStore(t)

	// Write references for two files.
	if err := s.PutReferences("a.go", fileAReferences()); err != nil {
		t.Fatalf("PutReferences a.go: %v", err)
	}
	if err := s.PutReferences("b.go", fileBReferences()); err != nil {
		t.Fatalf("PutReferences b.go: %v", err)
	}

	got, err := s.ListReferencesByFile("a.go")
	if err != nil {
		t.Fatalf("ListReferencesByFile(a.go): %v", err)
	}
	wantCount := len(fileAReferences())
	if len(got) != wantCount {
		t.Fatalf("ListReferencesByFile(a.go) = %d refs, want %d", len(got), wantCount)
	}
	// All returned references must belong to a.go.
	for _, r := range got {
		if r.SourceFile != "a.go" {
			t.Errorf("ListReferencesByFile(a.go) returned ref with SourceFile %q", r.SourceFile)
		}
	}

	got, err = s.ListReferencesByFile("b.go")
	if err != nil {
		t.Fatalf("ListReferencesByFile(b.go): %v", err)
	}
	if len(got) != len(fileBReferences()) {
		t.Fatalf("ListReferencesByFile(b.go) = %d refs, want %d", len(got), len(fileBReferences()))
	}

	// A file with no references returns an empty result (nil or
	// zero-length slice) with no error.
	got, err = s.ListReferencesByFile("empty.go")
	if err != nil {
		t.Fatalf("ListReferencesByFile(empty.go): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListReferencesByFile(empty.go) = %d refs, want 0", len(got))
	}
}

// TestPutReferencesOverwritesPrevious verifies that re-indexing a file
// replaces its previous references rather than accumulating them. This is
// the reference analog of PutSymbols' per-file atomicity.
func TestPutReferencesOverwritesPrevious(t *testing.T) {
	s, _ := newTestStore(t)

	first := []refs.Reference{
		{SymbolName: "Old", Kind: refs.RefCall, SourceLine: 1, SourceCol: 1},
		{SymbolName: "Keep", Kind: refs.RefCall, SourceLine: 2, SourceCol: 1},
	}
	if err := s.PutReferences("a.go", first); err != nil {
		t.Fatalf("PutReferences first: %v", err)
	}

	second := []refs.Reference{
		{SymbolName: "New", Kind: refs.RefCall, SourceLine: 3, SourceCol: 1},
		{SymbolName: "Keep", Kind: refs.RefCall, SourceLine: 4, SourceCol: 1},
	}
	if err := s.PutReferences("a.go", second); err != nil {
		t.Fatalf("PutReferences second: %v", err)
	}

	got, err := s.ListReferencesByFile("a.go")
	if err != nil {
		t.Fatalf("ListReferencesByFile: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("after re-index, ListReferencesByFile(a.go) = %d refs, want 2 (stale Old must be gone)", len(got))
	}
	// "Old" must be gone; "New" and the new "Keep" must be present.
	names := map[string]bool{}
	for _, r := range got {
		names[r.SymbolName] = true
	}
	if names["Old"] {
		t.Errorf("stale reference to \"Old\" survived re-index; got names %v", names)
	}
	if !names["New"] {
		t.Errorf("new reference to \"New\" missing after re-index; got names %v", names)
	}
	if !names["Keep"] {
		t.Errorf("reference to \"Keep\" missing after re-index; got names %v", names)
	}
}

// TestPutReferencesKeyCollision verifies the spec's key-uniqueness scenario:
// two references to the same symbol on different lines of the same file
// must both be stored (the line number in the key prevents overwrite).
func TestPutReferencesKeyCollision(t *testing.T) {
	s, _ := newTestStore(t)

	refs := []refs.Reference{
		{SymbolName: "Parse", Kind: refs.RefCall, SourceLine: 10, SourceCol: 3},
		{SymbolName: "Parse", Kind: refs.RefCall, SourceLine: 15, SourceCol: 3},
	}
	if err := s.PutReferences("main.go", refs); err != nil {
		t.Fatalf("PutReferences: %v", err)
	}

	got, err := s.GetReferences("Parse")
	if err != nil {
		t.Fatalf("GetReferences(Parse): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetReferences(Parse) = %d refs, want 2 (line in key must prevent overwrite)", len(got))
	}
	// Both lines must be represented.
	lines := map[int]bool{}
	for _, r := range got {
		lines[r.SourceLine] = true
	}
	if !lines[10] || !lines[15] {
		t.Errorf("expected refs on lines 10 and 15; got lines %v", lines)
	}
}

// TestGetReferencesSorted verifies GetReferences returns results sorted by
// (sourceFile, sourceLine) so output is deterministic across runs.
func TestGetReferencesSorted(t *testing.T) {
	s, _ := newTestStore(t)

	// Insert references to the same symbol across two files, out of order.
	if err := s.PutReferences("b.go", []refs.Reference{
		{SymbolName: "X", Kind: refs.RefCall, SourceLine: 9},
		{SymbolName: "X", Kind: refs.RefCall, SourceLine: 1},
	}); err != nil {
		t.Fatalf("PutReferences b.go: %v", err)
	}
	if err := s.PutReferences("a.go", []refs.Reference{
		{SymbolName: "X", Kind: refs.RefCall, SourceLine: 5},
	}); err != nil {
		t.Fatalf("PutReferences a.go: %v", err)
	}

	got, err := s.GetReferences("X")
	if err != nil {
		t.Fatalf("GetReferences(X): %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("GetReferences(X) = %d refs, want 3", len(got))
	}
	// Expected order: a.go:5, b.go:1, b.go:9.
	want := []struct {
		file string
		line int
	}{
		{"a.go", 5},
		{"b.go", 1},
		{"b.go", 9},
	}
	for i, w := range want {
		if got[i].SourceFile != w.file || got[i].SourceLine != w.line {
			t.Errorf("GetReferences(X)[%d] = {file=%q line=%d}, want {file=%q line=%d}", i, got[i].SourceFile, got[i].SourceLine, w.file, w.line)
		}
	}
}

// ---------- Schema version check ----------

// TestCheckSchemaVersionMissing verifies that a fresh database with no
// schema_version meta key returns an error (wrapping ErrSchemaVersion)
// rather than nil, so `fathom analyze` can prompt the user to run init.
func TestCheckSchemaVersionMissing(t *testing.T) {
	s, _ := newTestStore(t)
	// Do NOT write schema_version.
	err := s.CheckSchemaVersion()
	if err == nil {
		t.Fatalf("CheckSchemaVersion on fresh db: err = nil, want error wrapping ErrSchemaVersion")
	}
	if !errors.Is(err, ErrSchemaVersion) {
		t.Fatalf("CheckSchemaVersion on fresh db: err = %v, want it to wrap ErrSchemaVersion", err)
	}
}

// TestCheckSchemaVersionV1 verifies that a database stamped with schema
// version "1" (Phase 1) returns an error pointing the user at `fathom init`
// to migrate, NOT nil.
func TestCheckSchemaVersionV1(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.PutMeta(schemaVersionKey, "1"); err != nil {
		t.Fatalf("PutMeta(schema_version, 1): %v", err)
	}
	err := s.CheckSchemaVersion()
	if err == nil {
		t.Fatalf("CheckSchemaVersion on v1 db: err = nil, want error wrapping ErrSchemaVersion")
	}
	if !errors.Is(err, ErrSchemaVersion) {
		t.Fatalf("CheckSchemaVersion on v1 db: err = %v, want it to wrap ErrSchemaVersion", err)
	}
}

// TestCheckSchemaVersionV2 verifies that a database stamped with the current
// schema version ("2") passes the check with nil error.
func TestCheckSchemaVersionV2(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.PutMeta(schemaVersionKey, CurrentSchemaVersion); err != nil {
		t.Fatalf("PutMeta(schema_version, %q): %v", CurrentSchemaVersion, err)
	}
	if err := s.CheckSchemaVersion(); err != nil {
		t.Fatalf("CheckSchemaVersion on v2 db: err = %v, want nil", err)
	}
}

// TestCheckSchemaVersionClosed verifies CheckSchemaVersion returns
// ErrStoreClosed when called on a closed store, consistent with the other
// Store methods.
func TestCheckSchemaVersionClosed(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	err := s.CheckSchemaVersion()
	if !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("CheckSchemaVersion on closed store: err = %v, want ErrStoreClosed", err)
	}
}

// TestPutReferencesClosed verifies PutReferences returns ErrStoreClosed when
// called on a closed store, matching the existing methods' contract.
func TestPutReferencesClosed(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	err := s.PutReferences("a.go", fileAReferences())
	if !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("PutReferences on closed store: err = %v, want ErrStoreClosed", err)
	}
}

// TestGetReferencesClosed verifies GetReferences returns ErrStoreClosed when
// called on a closed store.
func TestGetReferencesClosed(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := s.GetReferences("X")
	if !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("GetReferences on closed store: err = %v, want ErrStoreClosed", err)
	}
}

// TestListReferencesByFileClosed verifies ListReferencesByFile returns
// ErrStoreClosed when called on a closed store.
func TestListReferencesByFileClosed(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := s.ListReferencesByFile("a.go")
	if !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("ListReferencesByFile on closed store: err = %v, want ErrStoreClosed", err)
	}
}

// TestDeleteSymbolsForFile verifies that DeleteSymbolsForFile removes all
// symbols and references for a specific file while leaving other files'
// data untouched.
func TestDeleteSymbolsForFile(t *testing.T) {
	s, _ := newTestStore(t)

	// Put symbols and references for two files
	symsA := fileASymbols()
	symsB := fileBSymbols()
	if err := s.PutSymbols(append(append([]symbol.Symbol{}, symsA...), symsB...)); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}

	refsA := fileAReferences()
	refsB := fileBReferences()
	if err := s.PutReferences("a.go", refsA); err != nil {
		t.Fatalf("PutReferences A: %v", err)
	}
	if err := s.PutReferences("b.go", refsB); err != nil {
		t.Fatalf("PutReferences B: %v", err)
	}

	// Delete symbols for "a.go"
	if err := s.DeleteSymbolsForFile("a.go"); err != nil {
		t.Fatalf("DeleteSymbolsForFile: %v", err)
	}

	// Verify "a.go" symbols are gone
	for _, sym := range symsA {
		_, err := s.GetSymbol(sym.File, sym.Name)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("GetSymbol(%q, %q) = %v, want ErrNotFound", sym.File, sym.Name, err)
		}
	}

	// Verify "b.go" symbols are intact
	for _, sym := range symsB {
		got, err := s.GetSymbol(sym.File, sym.Name)
		if err != nil {
			t.Errorf("GetSymbol(%q, %q) failed: %v", sym.File, sym.Name, err)
		}
		if got.Name != sym.Name {
			t.Errorf("GetSymbol got %q, want %q", got.Name, sym.Name)
		}
	}

	// Verify "a.go" references are gone
	gotRefsA, err := s.ListReferencesByFile("a.go")
	if err != nil {
		t.Fatalf("ListReferencesByFile(a.go): %v", err)
	}
	if len(gotRefsA) != 0 {
		t.Errorf("ListReferencesByFile(a.go) got %d references, want 0", len(gotRefsA))
	}

	// Verify "b.go" references are intact
	gotRefsB, err := s.ListReferencesByFile("b.go")
	if err != nil {
		t.Fatalf("ListReferencesByFile(b.go): %v", err)
	}
	if len(gotRefsB) != len(refsB) {
		t.Errorf("ListReferencesByFile(b.go) got %d references, want %d", len(gotRefsB), len(refsB))
	}
}
