package symbol

import (
	"encoding/json"
	"testing"
)

// TestSymbolJSONRoundTrip verifies that a Symbol survives JSON encode/decode
// without loss across all supported kinds and field combinations.
func TestSymbolJSONRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		sym  Symbol
	}{
		{
			name: "function symbol",
			sym: Symbol{
				Name:    "HandleRequest",
				Kind:    KindFunction,
				File:    "internal/server/handler.go",
				Line:    42,
				Col:     6,
				Content: "func HandleRequest(w http.ResponseWriter, r *http.Request) { ... }",
			},
		},
		{
			name: "type symbol",
			sym: Symbol{
				Name: "Config",
				Kind: KindType,
				File: "internal/config/config.go",
				Line: 18,
				Col:  6,
				Content: "type Config struct {\n\tPort int\n}",
			},
		},
		{
			name: "class symbol",
			sym: Symbol{
				Name: "UserService",
				Kind: KindClass,
				File: "src/services/user.ts",
				Line: 7,
				Col:  14,
				Content: "class UserService { ... }",
			},
		},
		{
			name: "interface symbol",
			sym: Symbol{
				Name: "Store",
				Kind: KindInterface,
				File: "internal/db/store.go",
				Line: 12,
				Col:  11,
				Content: "interface Store { ... }",
			},
		},
		{
			name: "import symbol",
			sym: Symbol{
				Name: "net/http",
				Kind: KindImport,
				File: "main.go",
				Line: 3,
				Col:  2,
				Content: "\"net/http\"",
			},
		},
		{
			name: "export symbol",
			sym: Symbol{
				Name: "default",
				Kind: KindExport,
				File: "src/index.ts",
				Line: 21,
				Col:  1,
				Content: "export default main;",
			},
		},
		{
			name: "empty content and zero position",
			sym: Symbol{
				Name:    "Empty",
				Kind:    KindFunction,
				File:    "empty.go",
				Line:    0,
				Col:     0,
				Content: "",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.sym)
			if err != nil {
				t.Fatalf("marshal: unexpected error: %v", err)
			}

			var got Symbol
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: unexpected error: %v", err)
			}

			if got != tc.sym {
				t.Fatalf("round-trip mismatch:\nwant: %+v\ngot:  %+v", tc.sym, got)
			}
		})
	}
}

// TestSymbolZeroValue documents the zero value of Symbol and ensures it
// round-trips cleanly through JSON.
func TestSymbolZeroValue(t *testing.T) {
	var zero Symbol

	data, err := json.Marshal(zero)
	if err != nil {
		t.Fatalf("marshal zero: unexpected error: %v", err)
	}

	var got Symbol
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal zero: unexpected error: %v", err)
	}

	if got != zero {
		t.Fatalf("zero round-trip mismatch:\nwant: %+v\ngot:  %+v", zero, got)
	}
}

// TestSymbolKinds verifies that every declared SymbolKind constant has the
// expected string value, guarding against accidental reordering.
func TestSymbolKinds(t *testing.T) {
	cases := []struct {
		name string
		kind SymbolKind
		want string
	}{
		{"function", KindFunction, "function"},
		{"type", KindType, "type"},
		{"class", KindClass, "class"},
		{"interface", KindInterface, "interface"},
		{"import", KindImport, "import"},
		{"export", KindExport, "export"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if string(tc.kind) != tc.want {
				t.Fatalf("kind %q = %q, want %q", tc.name, tc.kind, tc.want)
			}
		})
	}
}