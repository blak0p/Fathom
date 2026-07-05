package report

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fathom/internal/db"
	"github.com/Fathom/internal/deadcode"
	"github.com/Fathom/internal/impact"
	"github.com/Fathom/internal/mismatch"
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

func TestCompileAndRender(t *testing.T) {
	store := testStore(t)

	// Put some symbols into the database
	oldSyms := []symbol.Symbol{
		{Name: "OldFunc", Kind: symbol.KindFunction, File: "old.go", Content: "func OldFunc() {}"},
		{Name: "DeletedFunc", Kind: symbol.KindFunction, File: "deleted.go", Content: "func DeletedFunc() {}"},
	}
	if err := store.PutSymbols(oldSyms); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}

	// Prepare mock workspace defs
	workspaceDefs := map[string][]symbol.Symbol{
		"OldFunc": {
			{Name: "OldFunc", Kind: symbol.KindFunction, File: "old.go", Content: "func OldFunc(x int) {}"},
		},
		"NewFunc": {
			{Name: "NewFunc", Kind: symbol.KindFunction, File: "new.go", Content: "func NewFunc() {}"},
		},
	}

	// Prepare mock blast result
	blast := impact.BlastResult{
		DirectlyAffected: []impact.AffectedSymbol{
			{Name: "Caller", File: "caller.go", Depth: 1, Via: "OldFunc", DependencyType: "direct_call"},
		},
		TransitivelyAffected: []impact.AffectedSymbol{
			{Name: "Transitive", File: "transitive.go", Depth: 2, Via: "Caller", DependencyType: "direct_call"},
		},
		AffectedFiles: []string{"caller.go", "transitive.go"},
	}

	// Prepare mock mismatches
	mismatches := []mismatch.Mismatch{
		{
			Type:       mismatch.MismatchArity,
			SymbolName: "OldFunc",
			File:       "caller.go",
			Line:       10,
			Detail:     "call passes 0 arg(s) but OldFunc requires at least 1",
		},
	}

	// Prepare mock deadcode
	dead := []deadcode.DeadSymbol{
		{
			Symbol:     symbol.Symbol{Name: "UnusedFunc", Kind: symbol.KindFunction, File: "unused.go", Content: "func UnusedFunc() {}"},
			Confidence: deadcode.ConfidenceHigh,
			Reason:     "Private symbol with no references found in the workspace",
		},
	}

	// Compile the report
	payload, err := Compile(store, blast, mismatches, dead, workspaceDefs)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Check Verdict
	if payload.Verdict.Verdict != "REVIEW" {
		t.Errorf("Verdict = %q, want REVIEW", payload.Verdict.Verdict)
	}
	if !strings.Contains(payload.Verdict.Summary, "Review required") {
		t.Errorf("Verdict summary = %q, expected 'Review required' warning", payload.Verdict.Summary)
	}

	// Render report to a buffer
	var buf bytes.Buffer
	if err := Render(&buf, payload); err != nil {
		t.Fatalf("Render: %v", err)
	}

	htmlOutput := buf.String()

	// 1. Verify all sections exist in the HTML report
	expectedSubstrings := []string{
		"Fathom Impact & Compatibility Report",
		"Verdict",
		"Build-Break Findings",
		"Blast Radius",
		"Directly Affected",
		"Transitively Affected",
		"Dead Code Analysis",
	}
	for _, sub := range expectedSubstrings {
		if !strings.Contains(htmlOutput, sub) {
			t.Errorf("rendered HTML missing expected section: %q", sub)
		}
	}

	// 2. Verify self-contained check (no http/https references to external CSS/JS)
	if strings.Contains(htmlOutput, "http://") || strings.Contains(htmlOutput, "https://") {
		t.Errorf("rendered HTML is not self-contained, contains http/https references")
	}

	// 3. Verify fallbacks:
	// - "DeletedFunc" was deleted, so its NewContent should show "[ Symbol Deleted ]"
	// Let's compile a payload where DeletedFunc has a mismatch to trigger finding rendering.
	deletedMismatches := []mismatch.Mismatch{
		{Type: mismatch.MismatchArity, SymbolName: "DeletedFunc", File: "main.go", Line: 42, Detail: "deleted"},
	}
	payloadDel, err := Compile(store, impact.BlastResult{}, deletedMismatches, nil, workspaceDefs)
	if err != nil {
		t.Fatalf("Compile for deleted: %v", err)
	}

	var bufDel bytes.Buffer
	if err := Render(&bufDel, payloadDel); err != nil {
		t.Fatalf("Render for deleted: %v", err)
	}
	htmlDel := bufDel.String()
	if !strings.Contains(htmlDel, "[ Symbol Deleted ]") {
		t.Errorf("expected [ Symbol Deleted ] placeholder in HTML output")
	}

	// - "NewFunc" has no old definition in store, so its OldContent should show "[ Code Not Available ]"
	newMismatches := []mismatch.Mismatch{
		{Type: mismatch.MismatchArity, SymbolName: "NewFunc", File: "main.go", Line: 42, Detail: "new"},
	}
	payloadNew, err := Compile(store, impact.BlastResult{}, newMismatches, nil, workspaceDefs)
	if err != nil {
		t.Fatalf("Compile for new: %v", err)
	}

	var bufNew bytes.Buffer
	if err := Render(&bufNew, payloadNew); err != nil {
		t.Fatalf("Render for new: %v", err)
	}
	htmlNew := bufNew.String()
	if !strings.Contains(htmlNew, "[ Code Not Available ]") {
		t.Errorf("expected [ Code Not Available ] placeholder in HTML output")
	}
}
