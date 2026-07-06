package report

import (
	"bytes"
	"path/filepath"
	"sort"
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

// --- TestCompileSummary -------------------------------------------------------

func TestCompileSummary(t *testing.T) {
	store := testStore(t)

	oldSyms := []symbol.Symbol{
		{Name: "OldFunc", Kind: symbol.KindFunction, File: "old.go", Content: "func OldFunc() {}"},
	}
	if err := store.PutSymbols(oldSyms); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}

	workspaceDefs := map[string][]symbol.Symbol{
		"OldFunc": {
			{Name: "OldFunc", Kind: symbol.KindFunction, File: "old.go", Content: "func OldFunc(x int) {}"},
		},
		"OtherFunc": {
			{Name: "OtherFunc", Kind: symbol.KindFunction, File: "other.go", Content: "func OtherFunc() {}"},
		},
	}

	mismatches := []mismatch.Mismatch{
		{Type: mismatch.MismatchArity, SymbolName: "OldFunc", File: "caller.go", Line: 10, Detail: "arity mismatch"},
		{Type: mismatch.MismatchArity, SymbolName: "OtherFunc", File: "caller.go", Line: 20, Detail: "arity mismatch"},
	}

	blast := impact.BlastResult{
		AffectedFiles: []string{"caller.go", "other.go", "transitive.go", "extra.go", "fifth.go"},
	}

	dead := []deadcode.DeadSymbol{
		{Symbol: symbol.Symbol{Name: "Unused1", Kind: symbol.KindFunction, File: "u1.go"}, Confidence: deadcode.ConfidenceHigh, Reason: "no refs"},
		{Symbol: symbol.Symbol{Name: "Unused2", Kind: symbol.KindFunction, File: "u2.go"}, Confidence: deadcode.ConfidenceHigh, Reason: "no refs"},
	}

	payload, err := Compile(store, blast, mismatches, dead, workspaceDefs)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if payload.Summary.TotalFindings != 2 {
		t.Errorf("Summary.TotalFindings = %d, want 2", payload.Summary.TotalFindings)
	}
	if payload.Summary.WarningCount != 2 {
		t.Errorf("Summary.WarningCount = %d, want 2", payload.Summary.WarningCount)
	}
	if payload.Summary.AffectedFiles != 5 {
		t.Errorf("Summary.AffectedFiles = %d, want 5", payload.Summary.AffectedFiles)
	}
	if payload.Summary.DeadCodeCount != 2 {
		t.Errorf("Summary.DeadCodeCount = %d, want 2", payload.Summary.DeadCodeCount)
	}
}

func TestCompileSummaryEmpty(t *testing.T) {
	store := testStore(t)
	payload, err := Compile(store, impact.BlastResult{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if payload.Summary != (SummaryBlock{}) {
		t.Errorf("empty summary = %+v, want zero SummaryBlock", payload.Summary)
	}
}

func TestCompileSummaryDeadOnly(t *testing.T) {
	store := testStore(t)
	dead := []deadcode.DeadSymbol{
		{Symbol: symbol.Symbol{Name: "Dead1"}, Confidence: deadcode.ConfidenceHigh, Reason: "x"},
		{Symbol: symbol.Symbol{Name: "Dead2"}, Confidence: deadcode.ConfidenceHigh, Reason: "x"},
		{Symbol: symbol.Symbol{Name: "Dead3"}, Confidence: deadcode.ConfidenceHigh, Reason: "x"},
		{Symbol: symbol.Symbol{Name: "Dead4"}, Confidence: deadcode.ConfidenceHigh, Reason: "x"},
	}
	payload, err := Compile(store, impact.BlastResult{}, nil, dead, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if payload.Summary.DeadCodeCount != 4 {
		t.Errorf("DeadCodeCount = %d, want 4", payload.Summary.DeadCodeCount)
	}
	if payload.Summary.WarningCount != 0 {
		t.Errorf("WarningCount = %d, want 0", payload.Summary.WarningCount)
	}
	if payload.Summary.TotalFindings != 0 {
		t.Errorf("TotalFindings = %d, want 0", payload.Summary.TotalFindings)
	}
}

func TestCompileSummaryBlastOnly(t *testing.T) {
	store := testStore(t)
	blast := impact.BlastResult{
		DirectlyAffected: []impact.AffectedSymbol{
			{Name: "CallerA", File: "a.go", Depth: 1, Via: "Changed"},
		},
		AffectedFiles: []string{"a.go", "b.go"},
	}
	payload, err := Compile(store, blast, nil, nil, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if payload.Summary.AffectedFiles != 2 {
		t.Errorf("AffectedFiles = %d, want 2", payload.Summary.AffectedFiles)
	}
	if payload.Summary.TotalFindings != 0 {
		t.Errorf("TotalFindings = %d, want 0", payload.Summary.TotalFindings)
	}
	if payload.Summary.WarningCount != 0 {
		t.Errorf("WarningCount = %d, want 0", payload.Summary.WarningCount)
	}
}

// --- TestCompileFileGroups ----------------------------------------------------

func TestCompileFileGroups(t *testing.T) {
	store := testStore(t)

	oldSyms := []symbol.Symbol{
		{Name: "FuncA", Kind: symbol.KindFunction, File: "z.go", Content: "func FuncA() {}"},
		{Name: "FuncB", Kind: symbol.KindFunction, File: "a.go", Content: "func FuncB() {}"},
		{Name: "FuncC", Kind: symbol.KindFunction, File: "m.go", Content: "func FuncC() {}"},
		{Name: "FuncA2", Kind: symbol.KindFunction, File: "a.go", Content: "func FuncA2() {}"},
	}
	if err := store.PutSymbols(oldSyms); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}

	workspaceDefs := map[string][]symbol.Symbol{
		"FuncA":  {{Name: "FuncA", Kind: symbol.KindFunction, File: "z.go", Content: "func FuncA(x int) {}"}},
		"FuncB":  {{Name: "FuncB", Kind: symbol.KindFunction, File: "a.go", Content: "func FuncB(x int) {}"}},
		"FuncC":  {{Name: "FuncC", Kind: symbol.KindFunction, File: "m.go", Content: "func FuncC(x int) {}"}},
		"FuncA2": {{Name: "FuncA2", Kind: symbol.KindFunction, File: "a.go", Content: "func FuncA2(x int) {}"}},
	}

	mismatches := []mismatch.Mismatch{
		{Type: mismatch.MismatchArity, SymbolName: "FuncA", File: "caller.go", Line: 1, Detail: "x"},
		{Type: mismatch.MismatchArity, SymbolName: "FuncB", File: "caller.go", Line: 2, Detail: "x"},
		{Type: mismatch.MismatchArity, SymbolName: "FuncC", File: "caller.go", Line: 3, Detail: "x"},
		{Type: mismatch.MismatchArity, SymbolName: "FuncA2", File: "caller.go", Line: 4, Detail: "x"},
	}

	payload, err := Compile(store, impact.BlastResult{}, mismatches, nil, workspaceDefs)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if len(payload.Findings.FileGroups) != 3 {
		t.Fatalf("FileGroups len = %d, want 3", len(payload.Findings.FileGroups))
	}

	// Sorted by file path: a.go, m.go, z.go
	wantOrder := []string{"a.go", "m.go", "z.go"}
	for i, want := range wantOrder {
		if payload.Findings.FileGroups[i].File != want {
			t.Errorf("FileGroups[%d].File = %q, want %q", i, payload.Findings.FileGroups[i].File, want)
		}
	}

	// a.go contains FuncA2 and FuncB, sorted by SymbolName
	aGroup := payload.Findings.FileGroups[0]
	if len(aGroup.Findings) != 2 {
		t.Fatalf("a.go group len = %d, want 2", len(aGroup.Findings))
	}
	if aGroup.Findings[0].SymbolName != "FuncA2" {
		t.Errorf("a.go[0] = %q, want FuncA2", aGroup.Findings[0].SymbolName)
	}
	if aGroup.Findings[1].SymbolName != "FuncB" {
		t.Errorf("a.go[1] = %q, want FuncB", aGroup.Findings[1].SymbolName)
	}
	if aGroup.Severity != "WARNING" {
		t.Errorf("a.go severity = %q, want WARNING", aGroup.Severity)
	}

	// All groups should carry WARNING severity
	for _, g := range payload.Findings.FileGroups {
		if g.Severity != "WARNING" {
			t.Errorf("group %q severity = %q, want WARNING", g.File, g.Severity)
		}
	}
}

func TestCompileFileGroupsEmpty(t *testing.T) {
	store := testStore(t)
	payload, err := Compile(store, impact.BlastResult{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(payload.Findings.FileGroups) != 0 {
		t.Errorf("FileGroups len = %d, want 0", len(payload.Findings.FileGroups))
	}
}

// --- TestCompileAffectedCallers -----------------------------------------------

func TestCompileAffectedCallers(t *testing.T) {
	store := testStore(t)

	oldSyms := []symbol.Symbol{
		{Name: "Foo", Kind: symbol.KindFunction, File: "foo.go", Content: "func Foo() {}"},
	}
	if err := store.PutSymbols(oldSyms); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}

	workspaceDefs := map[string][]symbol.Symbol{
		"Foo": {{Name: "Foo", Kind: symbol.KindFunction, File: "foo.go", Content: "func Foo(x int) {}"}},
	}

	mismatches := []mismatch.Mismatch{
		{Type: mismatch.MismatchArity, SymbolName: "Foo", File: "caller.go", Line: 10, Detail: "arity"},
	}

	blast := impact.BlastResult{
		DirectlyAffected: []impact.AffectedSymbol{
			{Name: "Bar", File: "bar.go", Depth: 1, Via: "Foo", DependencyType: "direct_call"},
		},
		TransitivelyAffected: []impact.AffectedSymbol{
			// Baz's Via == "Bar" (the intermediate), NOT "Foo", so it must NOT
			// be attached to the Foo finding.
			{Name: "Baz", File: "baz.go", Depth: 2, Via: "Bar", DependencyType: "direct_call"},
			// Qux's Via == "Foo" even though it is transitive — it should be
			// attached because we match Via, not depth bucket.
			{Name: "Qux", File: "qux.go", Depth: 2, Via: "Foo", DependencyType: "direct_call"},
		},
	}

	payload, err := Compile(store, blast, mismatches, nil, workspaceDefs)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if len(payload.Findings.Findings) != 1 {
		t.Fatalf("findings len = %d, want 1", len(payload.Findings.Findings))
	}
	f := payload.Findings.Findings[0]
	if f.SymbolName != "Foo" {
		t.Fatalf("finding symbol = %q, want Foo", f.SymbolName)
	}

	// Two callers: Bar (direct, Via=Foo) and Qux (transitive, Via=Foo).
	// Baz (Via=Bar) must NOT be attached.
	if len(f.AffectedCallers) != 2 {
		t.Fatalf("AffectedCallers len = %d, want 2", len(f.AffectedCallers))
	}

	names := make([]string, 0, len(f.AffectedCallers))
	for _, c := range f.AffectedCallers {
		names = append(names, c.Name)
	}
	sort.Strings(names)
	if names[0] != "Bar" {
		t.Errorf("AffectedCallers[0] = %q, want Bar", names[0])
	}
	if names[1] != "Qux" {
		t.Errorf("AffectedCallers[1] = %q, want Qux", names[1])
	}

	// No caller named Baz should be attached.
	for _, c := range f.AffectedCallers {
		if c.Name == "Baz" {
			t.Errorf("Baz should NOT be attached to Foo finding (Via=Bar, not Foo)")
		}
	}
}

func TestCompileAffectedCallersNoMatch(t *testing.T) {
	store := testStore(t)
	oldSyms := []symbol.Symbol{
		{Name: "Foo", Kind: symbol.KindFunction, File: "foo.go", Content: "func Foo() {}"},
	}
	if err := store.PutSymbols(oldSyms); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}
	workspaceDefs := map[string][]symbol.Symbol{
		"Foo": {{Name: "Foo", Kind: symbol.KindFunction, File: "foo.go", Content: "func Foo(x int) {}"}},
	}
	mismatches := []mismatch.Mismatch{
		{Type: mismatch.MismatchArity, SymbolName: "Foo", File: "caller.go", Line: 10, Detail: "arity"},
	}
	blast := impact.BlastResult{
		DirectlyAffected: []impact.AffectedSymbol{
			{Name: "Other", File: "o.go", Depth: 1, Via: "DifferentSymbol", DependencyType: "direct_call"},
		},
	}

	payload, err := Compile(store, blast, mismatches, nil, workspaceDefs)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(payload.Findings.Findings) != 1 {
		t.Fatalf("findings len = %d, want 1", len(payload.Findings.Findings))
	}
	if len(payload.Findings.Findings[0].AffectedCallers) != 0 {
		t.Errorf("AffectedCallers len = %d, want 0 (no Via match)",
			len(payload.Findings.Findings[0].AffectedCallers))
	}
}

// --- Helpers test -------------------------------------------------------------

func TestDisplaySeverity(t *testing.T) {
	f := Finding{}
	if got := f.DisplaySeverity(); got != "WARNING" {
		t.Errorf("DisplaySeverity() = %q, want WARNING", got)
	}
}

func TestDisplayLinesChanged(t *testing.T) {
	f := Finding{}
	if got := f.DisplayLinesChanged(); got != "" {
		t.Errorf("DisplayLinesChanged() = %q, want empty (JS hook)", got)
	}
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
		// New visual scanability elements:
		"Executive Summary",
		"Total Findings",
		"Warnings",
		"Affected Files",
		"Dead Code",
		"WARNING",
		"Affected Callers",
		"diff-summary",
		"Show only changes",
		"summary-grid",
		"file-group",
		"Caller", // affected caller listed under OldFunc finding
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

	// 4. Edge case: empty report renders without panic and shows zero counts.
	emptyPayload, err := Compile(store, impact.BlastResult{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Compile empty: %v", err)
	}
	var bufEmpty bytes.Buffer
	if err := Render(&bufEmpty, emptyPayload); err != nil {
		t.Fatalf("Render empty: %v", err)
	}
	htmlEmpty := bufEmpty.String()
	for _, sub := range []string{"Executive Summary", "No signature mismatches detected."} {
		if !strings.Contains(htmlEmpty, sub) {
			t.Errorf("empty rendered HTML missing: %q", sub)
		}
	}
}
