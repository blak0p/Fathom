package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Fathom/internal/db"
	"github.com/Fathom/internal/symbol"
)

// writeFixture writes a file at relPath under dir with the given content,
// creating parent directories as needed. It fails the test on any I/O error.
func writeFixture(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// gitInit runs "git init" + a first commit so rev-parse HEAD succeeds. It
// fails (and skips) the test when git is unavailable.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@fathom.local"},
		{"config", "user.name", "Fathom Test"},
		{"add", "-A"},
		{"commit", "-m", "fixture"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed in %s: %v\n%s", args, dir, err, out)
		}
	}
}

// openStoreForRead opens a read-only handle to the bolt store at path and
// registers Close with t.Cleanup. Used by tests to inspect the index without
// re-running init.
func openStoreForRead(t *testing.T, path string) db.Store {
	t.Helper()
	s := db.New()
	if err := s.Open(path); err != nil {
		t.Fatalf("open store for read: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// goodGoSource is a minimal, valid Go file with one import, one type, one
// interface, and one function — enough for the extractor to emit 4 symbols.
const goodGoSource = `package fixture

import "fmt"

type Config struct {
	Path string
}

type Store interface {
	Get(string) (string, error)
}

func HandleRequest(s Store, key string) {
	fmt.Println(s.Get(key))
}
`

// brokenGoSource is a syntactically malformed Go file that still has a valid
// import and a valid function declaration before the break, so tree-sitter's
// error recovery yields some symbols. The point is the WALK must not abort.
const brokenGoSource = `package fixture

import "os"

func good() {
	os.Exit(0)
}

func broken(
`

// TestInitOnFixtureRepo builds a tiny repo in a temp dir, runs fathom init
// against it, and verifies the .fathom/index.bolt store exists and holds the
// expected symbols. Requires git, so it is skipped under -short.
func TestInitOnFixtureRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping git-backed integration test in -short mode")
	}

	dir := t.TempDir()
	writeFixture(t, dir, "main.go", goodGoSource)
	writeFixture(t, dir, "sub/sub.go", `package sub

type Point struct{ X, Y int }
`)
	gitInit(t, dir)

	if err := runInit(dir); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	indexPath := filepath.Join(dir, ".fathom", "index.bolt")
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("index.bolt missing after init: %v", err)
	}

	store := openStoreForRead(t, indexPath)
	all, err := store.ListSymbols("")
	if err != nil {
		t.Fatalf("ListSymbols: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no symbols indexed; expected at least the main.go declarations")
	}

	// HandleRequest is declared in main.go; it must be present by name.
	found := false
	for _, s := range all {
		if s.Name == "HandleRequest" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected symbol HandleRequest in index, got %d symbols", len(all))
	}

	// Metadata: schema_version must be the current version and commit_hash must be set (git
	// committed). Verify via the meta bucket.
	if v, err := store.GetMeta("schema_version"); err != nil || v != db.CurrentSchemaVersion {
		t.Errorf("schema_version meta = %q, err=%v; want %q", v, err, "2")
	}
	if v, err := store.GetMeta("commit_hash"); err != nil || v == "" {
		t.Errorf("commit_hash meta = %q, err=%v; want non-empty (git repo)", v, err)
	}
}

// TestInitOnEmptyDir runs init against an empty directory. It must succeed
// and still create .fathom/index.bolt (with zero symbols). No git is needed.
func TestInitOnEmptyDir(t *testing.T) {
	dir := t.TempDir()

	if err := runInit(dir); err != nil {
		t.Fatalf("runInit on empty dir: %v", err)
	}

	indexPath := filepath.Join(dir, ".fathom", "index.bolt")
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("index.bolt missing after init on empty dir: %v", err)
	}

	store := openStoreForRead(t, indexPath)
	all, err := store.ListSymbols("")
	if err != nil {
		t.Fatalf("ListSymbols: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("empty repo indexed %d symbols, want 0", len(all))
	}

	// schema_version is written regardless of how many files were indexed.
	if v, err := store.GetMeta("schema_version"); err != nil || v != db.CurrentSchemaVersion {
		t.Errorf("schema_version meta = %q, err=%v; want %q", v, err, "2")
	}
}

// TestInitIdempotency runs init twice on the same repo and asserts the symbol
// set is identical (no duplicates). PutSymbols keys by {file}#{name}, so a
// second run overwrites in place rather than appending.
func TestInitIdempotency(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "main.go", goodGoSource)

	if err := runInit(dir); err != nil {
		t.Fatalf("runInit #1: %v", err)
	}
	indexPath := filepath.Join(dir, ".fathom", "index.bolt")
	store1 := openStoreForRead(t, indexPath)
	first, err := store1.ListSymbols("")
	if err != nil {
		t.Fatalf("ListSymbols #1: %v", err)
	}
	// Close the first handle so bbolt releases the file lock before the
	// second run reopens the same path.
	_ = store1.Close()

	if err := runInit(dir); err != nil {
		t.Fatalf("runInit #2: %v", err)
	}
	store2 := openStoreForRead(t, indexPath)
	second, err := store2.ListSymbols("")
	if err != nil {
		t.Fatalf("ListSymbols #2: %v", err)
	}

	if len(second) != len(first) {
		t.Fatalf("idempotency broken: first run=%d symbols, second run=%d",
			len(first), len(second))
	}
	// Name-kind fingerprint comparison: order-independent.
	if !sameSymbolSet(first, second) {
		t.Fatalf("idempotency broken: symbol sets differ after second run\nfirst=%v\nsecond=%v",
			first, second)
	}
}

// TestInitErrorIsolation verifies that a malformed source file between good
// ones does not abort the index walk: the good files' symbols still land in
// the store. tree-sitter is error-tolerant so broken.go may itself emit a few
// recovered symbols; the assertion is only that the GOOD file's symbols are
// present.
func TestInitErrorIsolation(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "good.go", goodGoSource)
	writeFixture(t, dir, "broken.go", brokenGoSource)
	writeFixture(t, dir, "also_good.go", `package fixture

type Extra struct{ N int }
`)

	if err := runInit(dir); err != nil {
		t.Fatalf("runInit with a broken file: %v", err)
	}

	indexPath := filepath.Join(dir, ".fathom", "index.bolt")
	store := openStoreForRead(t, indexPath)
	all, err := store.ListSymbols("")
	if err != nil {
		t.Fatalf("ListSymbols: %v", err)
	}

	// The good file's function must be indexed despite the broken sibling.
	if !hasSymbolNamed(all, "HandleRequest") {
		t.Errorf("good.go's HandleRequest must be indexed even with a broken sibling; got %d symbols", len(all))
	}
	// The also_good file's type must also be present.
	if !hasSymbolNamed(all, "Extra") {
		t.Errorf("also_good.go's Extra must be indexed even with a broken sibling; got %d symbols", len(all))
	}

	// Sanity: the index is non-empty overall.
	if len(all) == 0 {
		t.Fatal("no symbols indexed at all; error isolation failed completely")
	}
}

// hasSymbolNamed reports whether any symbol in syms has the given name.
func hasSymbolNamed(syms []symbol.Symbol, name string) bool {
	for _, s := range syms {
		if s.Name == name {
			return true
		}
	}
	return false
}

// TestInitWithRefs runs init on a multi-file Go fixture and verifies that
// references are stored in the database. Requires git, so it is skipped
// under -short.
func TestInitWithRefs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping git-backed integration test in -short mode")
	}

	dir := t.TempDir()
	// main.go calls NewServer() and s.HandleRequest()
	writeFixture(t, dir, "main.go", `package fixture

import "fmt"

func main() {
	s := NewServer()
	s.HandleRequest()
	fmt.Println("done")
}
`)
	// server.go defines NewServer and HandleRequest
	writeFixture(t, dir, "server.go", `package fixture

type Server struct{}

func NewServer() *Server {
	return &Server{}
}

func (s *Server) HandleRequest() {
	// handle it
}
`)
	gitInit(t, dir)

	if err := runInit(dir); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	indexPath := filepath.Join(dir, ".fathom", "index.bolt")
	store := openStoreForRead(t, indexPath)

	// Verify references exist for "NewServer" (called from main.go)
	refsToNewServer, err := store.GetReferences("NewServer")
	if err != nil {
		t.Fatalf("GetReferences(NewServer): %v", err)
	}
	if len(refsToNewServer) == 0 {
		t.Fatal("expected at least one reference to NewServer, got 0")
	}

	// Verify references exist for "HandleRequest" (called from main.go)
	refsToHandle, err := store.GetReferences("HandleRequest")
	if err != nil {
		t.Fatalf("GetReferences(HandleRequest): %v", err)
	}
	if len(refsToHandle) == 0 {
		t.Fatal("expected at least one reference to HandleRequest, got 0")
	}

	// Verify ListReferencesByFile works for main.go
	mainRefs, err := store.ListReferencesByFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("ListReferencesByFile(main.go): %v", err)
	}
	if len(mainRefs) == 0 {
		t.Fatal("expected references from main.go, got 0")
	}

	// Verify schema_version is "2"
	if v, err := store.GetMeta("schema_version"); err != nil || v != db.CurrentSchemaVersion {
		t.Errorf("schema_version meta = %q, err=%v; want %q", v, err, "2")
	}
}

// sameSymbolSet reports whether a and b contain the same (name, kind, file
// basename) fingerprint set, ignoring order and line/column drift.
func sameSymbolSet(a, b []symbol.Symbol) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(s symbol.Symbol) string {
		return string(s.Kind) + "|" + s.Name + "|" + filepath.Base(s.File)
	}
	set := make(map[string]int, len(a))
	for _, s := range a {
		set[key(s)]++
	}
	for _, s := range b {
		k := key(s)
		if set[k] == 0 {
			return false
		}
		set[k]--
	}
	return true
}