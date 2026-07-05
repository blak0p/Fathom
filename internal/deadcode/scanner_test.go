package deadcode

import (
	"path/filepath"
	"testing"

	"github.com/Fathom/internal/db"
	"github.com/Fathom/internal/refs"
	"github.com/Fathom/internal/symbol"
)

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

func TestScanNoReferences(t *testing.T) {
	store := testStore(t)
	scanner := New(store)

	tests := []struct {
		name       string
		symbol     symbol.Symbol
		wantConf   Confidence
		wantReason string
	}{
		// Go
		{
			name: "Go Public",
			symbol: symbol.Symbol{
				Name:    "ExportedFunc",
				Kind:    symbol.KindFunction,
				File:    "test.go",
				Content: "func ExportedFunc() {}",
			},
			wantConf:   ConfidenceMedium,
			wantReason: "Public symbol with no references found in the workspace",
		},
		{
			name: "Go Private",
			symbol: symbol.Symbol{
				Name:    "privateFunc",
				Kind:    symbol.KindFunction,
				File:    "test.go",
				Content: "func privateFunc() {}",
			},
			wantConf:   ConfidenceHigh,
			wantReason: "Private symbol with no references found in the workspace",
		},
		// JS/TS
		{
			name: "JS Exported by Content",
			symbol: symbol.Symbol{
				Name:    "myExport",
				Kind:    symbol.KindFunction,
				File:    "test.js",
				Content: "export function myExport() {}",
			},
			wantConf:   ConfidenceMedium,
			wantReason: "Public symbol with no references found in the workspace",
		},
		{
			name: "JS Private",
			symbol: symbol.Symbol{
				Name:    "myPrivate",
				Kind:    symbol.KindFunction,
				File:    "test.js",
				Content: "function myPrivate() {}",
			},
			wantConf:   ConfidenceHigh,
			wantReason: "Private symbol with no references found in the workspace",
		},
		// Python
		{
			name: "Python Public",
			symbol: symbol.Symbol{
				Name:    "public_api",
				Kind:    symbol.KindFunction,
				File:    "test.py",
				Content: "def public_api(): pass",
			},
			wantConf:   ConfidenceMedium,
			wantReason: "Public symbol with no references found in the workspace",
		},
		{
			name: "Python Private",
			symbol: symbol.Symbol{
				Name:    "_private_api",
				Kind:    symbol.KindFunction,
				File:    "test.py",
				Content: "def _private_api(): pass",
			},
			wantConf:   ConfidenceHigh,
			wantReason: "Private symbol with no references found in the workspace",
		},
		// Rust
		{
			name: "Rust Public",
			symbol: symbol.Symbol{
				Name:    "rust_pub",
				Kind:    symbol.KindFunction,
				File:    "test.rs",
				Content: "pub fn rust_pub() {}",
			},
			wantConf:   ConfidenceMedium,
			wantReason: "Public symbol with no references found in the workspace",
		},
		{
			name: "Rust Private",
			symbol: symbol.Symbol{
				Name:    "rust_priv",
				Kind:    symbol.KindFunction,
				File:    "test.rs",
				Content: "fn rust_priv() {}",
			},
			wantConf:   ConfidenceHigh,
			wantReason: "Private symbol with no references found in the workspace",
		},
		// Java
		{
			name: "Java Public",
			symbol: symbol.Symbol{
				Name:    "javaPub",
				Kind:    symbol.KindFunction,
				File:    "Test.java",
				Content: "public void javaPub() {}",
			},
			wantConf:   ConfidenceMedium,
			wantReason: "Public symbol with no references found in the workspace",
		},
		{
			name: "Java Private",
			symbol: symbol.Symbol{
				Name:    "javaPriv",
				Kind:    symbol.KindFunction,
				File:    "Test.java",
				Content: "void javaPriv() {}",
			},
			wantConf:   ConfidenceHigh,
			wantReason: "Private symbol with no references found in the workspace",
		},
		// C/C++
		{
			name: "C++ Public",
			symbol: symbol.Symbol{
				Name:    "cppPub",
				Kind:    symbol.KindFunction,
				File:    "test.cpp",
				Content: "void cppPub() {}",
			},
			wantConf:   ConfidenceMedium,
			wantReason: "Public symbol with no references found in the workspace",
		},
		{
			name: "C++ Private",
			symbol: symbol.Symbol{
				Name:    "cppPriv",
				Kind:    symbol.KindFunction,
				File:    "test.cpp",
				Content: "static void cppPriv() {}",
			},
			wantConf:   ConfidenceHigh,
			wantReason: "Private symbol with no references found in the workspace",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := scanner.Scan([]symbol.Symbol{tc.symbol})
			if err != nil {
				t.Fatalf("Scan: %v", err)
			}
			if len(res) != 1 {
				t.Fatalf("expected 1 dead symbol, got %d", len(res))
			}
			if res[0].Symbol.Name != tc.symbol.Name {
				t.Errorf("name = %q, want %q", res[0].Symbol.Name, tc.symbol.Name)
			}
			if res[0].Confidence != tc.wantConf {
				t.Errorf("confidence = %s, want %s", res[0].Confidence, tc.wantConf)
			}
			if res[0].Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", res[0].Reason, tc.wantReason)
			}
		})
	}
}

func TestScanWithReferences(t *testing.T) {
	store := testStore(t)
	scanner := New(store)

	// Add references to "ActiveFunc"
	err := store.PutReferences("caller.go", []refs.Reference{
		{SymbolName: "ActiveFunc", Kind: refs.RefCall, SourceFile: "caller.go", SourceLine: 10, ContainingSymbol: "Run"},
	})
	if err != nil {
		t.Fatalf("PutReferences: %v", err)
	}

	// Add self reference (recursion) to "RecursiveFunc"
	err = store.PutReferences("rec.go", []refs.Reference{
		{SymbolName: "RecursiveFunc", Kind: refs.RefCall, SourceFile: "rec.go", SourceLine: 5, ContainingSymbol: "RecursiveFunc"},
	})
	if err != nil {
		t.Fatalf("PutReferences: %v", err)
	}

	syms := []symbol.Symbol{
		{Name: "ActiveFunc", Kind: symbol.KindFunction, File: "main.go", Content: "func ActiveFunc() {}"},
		{Name: "RecursiveFunc", Kind: symbol.KindFunction, File: "rec.go", Content: "func RecursiveFunc() { RecursiveFunc() }"},
	}

	dead, err := scanner.Scan(syms)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// "ActiveFunc" should not be dead (has reference in caller.go)
	// "RecursiveFunc" should be dead (only references itself)
	if len(dead) != 1 {
		t.Fatalf("expected 1 dead symbol, got %d: %+v", len(dead), dead)
	}
	if dead[0].Symbol.Name != "RecursiveFunc" {
		t.Errorf("expected dead symbol to be 'RecursiveFunc', got %q", dead[0].Symbol.Name)
	}
}
