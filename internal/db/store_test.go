package db

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Fathom/internal/symbol"
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
		if got != want {
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
		if got != want {
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