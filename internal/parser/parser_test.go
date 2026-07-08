package parser

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Fathom/internal/symbol"
)

// TestDetectLanguage covers the DetectLanguage wrapper for known and unknown
// paths. The tree-sitter language pack maps common extensions to language
// names; we assert the mappings Fathom relies on.
func TestDetectLanguage(t *testing.T) {
	p := New()

	known := []struct {
		path string
		want string
	}{
		{"main.go", "go"},
		{"src/app.ts", "typescript"},
		{"widget.tsx", "tsx"},
		{"script.js", "javascript"},
		{"module.mjs", "javascript"},
		{"app.py", "python"},
		{"lib.rs", "rust"},
		{"Main.java", "java"},
		{"kernel.c", "c"},
		{"engine.cpp", "cpp"},
		{"gem.rb", "ruby"},
		{"index.php", "php"},
	}
	for _, tc := range known {
		t.Run("known/"+tc.path, func(t *testing.T) {
			got, ok := p.DetectLanguage(tc.path)
			if !ok {
				t.Fatalf("DetectLanguage(%q) = ok=false, want true", tc.path)
			}
			if got != tc.want {
				t.Fatalf("DetectLanguage(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}

	unknown := []string{
		"Makefile",
		"noext",
		"weird.xyz",
		"another.qqq",
	}
	for _, path := range unknown {
		t.Run("unknown/"+path, func(t *testing.T) {
			if got, ok := p.DetectLanguage(path); ok {
				t.Fatalf("DetectLanguage(%q) = (%q, true), want ok=false", path, got)
			}
		})
	}
}

// TestDetectLanguagesFromExtensions verifies deduplication: several
// extensions that resolve to the same language collapse to one entry, and
// the result is sorted and free of unknowns.
func TestDetectLanguagesFromExtensions(t *testing.T) {
	p := New()

	exts := []string{"go", "ts", "tsx", "js", "jsx", "mjs", "py", "rs"}
	got := p.DetectLanguagesFromExtensions(exts)

	// Order is sorted; duplicates (js, jsx, mjs all → javascript) collapse.
	want := []string{"go", "javascript", "python", "rust", "tsx", "typescript"}
	if !equalStrings(got, want) {
		t.Fatalf("DetectLanguagesFromExtensions(%v) = %v, want %v", exts, got, want)
	}
}

// TestExtractSymbols runs the extractor against every fixture and asserts the
// expected symbol set by kind+name. Line/col are checked loosely (1-based,
// > 0 for real symbols) to keep the test robust against minor grammar drift.
func TestExtractSymbols(t *testing.T) {
	cases := []struct {
		name    string
		lang    string
		fixture string
		want    []symbol.Symbol
	}{
		{
			name:    "go sample",
			lang:    "go",
			fixture: "testdata/sample.go",
			want: []symbol.Symbol{
				{Kind: symbol.KindImport, Name: "fmt"},
				{Kind: symbol.KindType, Name: "Config"},
				{Kind: symbol.KindInterface, Name: "Store"},
				{Kind: symbol.KindFunction, Name: "HandleRequest"},
			},
		},
		{
			name:    "typescript sample",
			lang:    "typescript",
			fixture: "testdata/sample.ts",
			want: []symbol.Symbol{
				{Kind: symbol.KindImport, Name: "bar"},
				{Kind: symbol.KindExport, Name: "hello"},
				{Kind: symbol.KindFunction, Name: "hello"},
				{Kind: symbol.KindExport, Name: "UserService"},
				{Kind: symbol.KindClass, Name: "UserService"},
				{Kind: symbol.KindInterface, Name: "Repo"},
			},
		},
		{
			name:    "python sample",
			lang:    "python",
			fixture: "testdata/sample.py",
			want: []symbol.Symbol{
				{Kind: symbol.KindImport, Name: "os"},
				{Kind: symbol.KindFunction, Name: "hello"},
				{Kind: symbol.KindClass, Name: "User"},
				{Kind: symbol.KindFunction, Name: "__init__"},
				{Kind: symbol.KindFunction, Name: "greet"},
			},
		},
		{
			name:    "rust sample",
			lang:    "rust",
			fixture: "testdata/sample.rs",
			want: []symbol.Symbol{
				{Kind: symbol.KindImport, Name: "std::io"},
				{Kind: symbol.KindFunction, Name: "main"},
				{Kind: symbol.KindType, Name: "Point"},
				{Kind: symbol.KindInterface, Name: "Draw"},
			},
		},
		{
			name:    "javascript sample",
			lang:    "javascript",
			fixture: "testdata/sample.js",
			want: []symbol.Symbol{
				{Kind: symbol.KindImport, Name: "bar"},
				{Kind: symbol.KindExport, Name: "hello"},
				{Kind: symbol.KindFunction, Name: "hello"},
				{Kind: symbol.KindExport, Name: "UserService"},
				{Kind: symbol.KindClass, Name: "UserService"},
				{Kind: symbol.KindFunction, Name: "save"},
				{Kind: symbol.KindFunction, Name: ""}, // anonymous arrow function
			},
		},
		{
			name:    "ruby sample",
			lang:    "ruby",
			fixture: "testdata/sample.rb",
			want: []symbol.Symbol{
				{Kind: symbol.KindClass, Name: "Greeter"},
				{Kind: symbol.KindFunction, Name: "hello"},
			},
		},
		{
			name:    "php sample",
			lang:    "php",
			fixture: "testdata/sample.php",
			want: []symbol.Symbol{
				{Kind: symbol.KindClass, Name: "UserController"},
				{Kind: symbol.KindFunction, Name: "show"},
			},
		},
		{
			name:    "c sample",
			lang:    "c",
			fixture: "testdata/sample.c",
			want: []symbol.Symbol{
				// The C grammar's Structure emits a Function but does not
				// surface the declarator name, so the symbol carries an empty
				// name. The struct is not emitted by the pack's C Structure
				// (struct_specifier is not a StructureKind), and C has no
				// Symbols fallback, so we expect a single nameless function.
				{Kind: symbol.KindFunction, Name: ""},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := mustReadFixture(t, tc.fixture)
			got, err := ExtractSymbols(source, tc.lang)
			if err != nil {
				t.Fatalf("ExtractSymbols(%q): unexpected error: %v", tc.fixture, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ExtractSymbols(%q) returned %d symbols, want %d:\n%+v",
					tc.fixture, len(got), len(tc.want), got)
			}
			for i, want := range tc.want {
				g := got[i]
				if g.Kind != want.Kind {
					t.Errorf("symbol[%d] kind = %q, want %q", i, g.Kind, want.Kind)
				}
				if g.Name != want.Name {
					t.Errorf("symbol[%d] name = %q, want %q", i, g.Name, want.Name)
				}
				if g.Line <= 0 {
					t.Errorf("symbol[%d] line = %d, want > 0", i, g.Line)
				}
				if g.Col <= 0 {
					t.Errorf("symbol[%d] col = %d, want > 0", i, g.Col)
				}
				if g.Content == "" {
					t.Errorf("symbol[%d] content is empty, want non-empty source slice", i)
				}
			}
		})
	}
}

// TestExtractSymbolsEmptyFile ensures an empty (package-only) Go file yields
// zero symbols and no error.
func TestExtractSymbolsEmptyFile(t *testing.T) {
	source := mustReadFixture(t, "testdata/empty.go")
	got, err := ExtractSymbols(source, "go")
	if err != nil {
		t.Fatalf("ExtractSymbols empty: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ExtractSymbols empty: got %d symbols, want 0: %+v", len(got), got)
	}
}

// TestExtractSymbolsSyntaxError guarantees a malformed file does not panic
// and still returns whatever symbols tree-sitter could recover from the
// partial tree.
func TestExtractSymbolsSyntaxError(t *testing.T) {
	source := mustReadFixture(t, "testdata/broken.go")

	// The assertion is panic-free: if we got here, extraction succeeded.
	got, err := ExtractSymbols(source, "go")
	if err != nil {
		t.Fatalf("ExtractSymbols broken: unexpected error: %v", err)
	}

	// broken.go declares `import "fmt"` and `func good()` before the broken
	// declaration, so the recovered tree should contain at least those two.
	if len(got) < 2 {
		t.Fatalf("ExtractSymbols broken: got %d symbols, want at least 2 (import + good): %+v",
			len(got), got)
	}

	// Verify the recovered declaration is present by name.
	if !hasSymbolNamed(got, "good") {
		t.Errorf("ExtractSymbols broken: expected a function named %q, got %+v", "good", got)
	}
}

// TestExtractSymbolsUnsupportedLanguage asserts the typed error path for an
// unknown language rather than a silent zero result.
func TestExtractSymbolsUnsupportedLanguage(t *testing.T) {
	_, err := ExtractSymbols([]byte("x"), "klingon")
	if err == nil {
		t.Fatal("ExtractSymbols(unsupported): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "klingon") {
		t.Fatalf("error should mention the language, got: %v", err)
	}
}

// TestParseFile exercises the full ParseFile path: read → detect → extract →
// attach File. It asserts the symbols carry the absolute file path.
func TestParseFile(t *testing.T) {
	p := New()
	abs, err := filepath.Abs("testdata/sample.go")
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}

	got, err := p.ParseFile(abs)
	if err != nil {
		t.Fatalf("ParseFile: unexpected error: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("ParseFile sample.go: got %d symbols, want 4: %+v", len(got), got)
	}
	for _, s := range got {
		if s.File == "" {
			t.Errorf("symbol %q has empty File, want %s", s.Name, abs)
		}
		// ParseFile records the absolute path; on most platforms Abs already
		// produces a clean path, so compare against filepath.Clean to be safe.
		if filepath.Clean(s.File) != filepath.Clean(abs) {
			t.Errorf("symbol %q File = %q, want %q", s.Name, s.File, abs)
		}
	}
}

// TestParseFileUnknownExtension checks that ParseFile refuses files it cannot
// detect rather than returning a misleading empty result. The extension must be
// one the language pack does not recognize; common dev extensions (md, yaml,
// json, etc.) are now detected by the 306-language pack and are NOT suitable
// sentinels.
func TestParseFileUnknownExtension(t *testing.T) {
	p := New()
	// Write a temp file with an unsupported extension.
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.qqq")
	if err := os.WriteFile(path, []byte("# hi"), 0644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if _, err := p.ParseFile(path); err == nil {
		t.Fatal("ParseFile(.qqq): expected error for unsupported extension, got nil")
	}
}

// TestSupportedExtensions verifies the helper returns a non-empty, sorted,
// deduplicated list that includes the extensions Fathom cares about.
func TestSupportedExtensions(t *testing.T) {
	exts := SupportedExtensions()
	if len(exts) == 0 {
		t.Fatal("SupportedExtensions() returned empty list")
	}
	if !sort.StringsAreSorted(exts) {
		t.Fatalf("SupportedExtensions() not sorted: %v", exts)
	}
	want := map[string]bool{
		"go": true, "ts": true, "tsx": true, "js": true,
		"py": true, "rs": true, "java": true,
		"c": true, "cpp": true, "rb": true, "php": true,
	}
	for _, e := range exts {
		want[e] = false
	}
	for ext, missing := range want {
		if missing {
			t.Errorf("SupportedExtensions() missing expected extension %q", ext)
		}
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

// equalStrings compares two slices for element-wise equality.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mustReadFixture loads a testdata file relative to the package directory or
// fails the test. `go test` sets the working directory to the package root,
// so relative paths work directly; the fallback covers runs from the repo
// root.
func mustReadFixture(t *testing.T, rel string) []byte {
	t.Helper()
	source, err := os.ReadFile(rel)
	if err != nil {
		alt := filepath.Join("internal", "parser", rel)
		if alt2, err2 := os.ReadFile(alt); err2 == nil {
			return alt2
		}
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return source
}
