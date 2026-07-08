package report

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/blak0p/Fathom/internal/db"
	"github.com/blak0p/Fathom/internal/deadcode"
	"github.com/blak0p/Fathom/internal/impact"
	"github.com/blak0p/Fathom/internal/mismatch"
	"github.com/blak0p/Fathom/internal/symbol"
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

func TestSplitPath(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{""}},
		{"a.go", []string{"a.go"}},
		{"cmd/foo/main.go", []string{"cmd", "foo", "main.go"}},
		{"/abs/path/file.go", []string{"", "abs", "path", "file.go"}},
		{"a/b/c/d.go", []string{"a", "b", "c", "d.go"}},
		// Windows backslash normalized to forward slash.
		{"cmd\\foo\\main.go", []string{"cmd", "foo", "main.go"}},
		// Trailing slash keeps the empty last segment — harmless for the tree.
		{"dir/", []string{"dir", ""}},
	}
	for _, c := range cases {
		got := splitPath(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitPath(%q) = %v (len %d), want %v (len %d)", c.in, got, len(got), c.want, len(c.want))
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitPath(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestBuildFileTree(t *testing.T) {
	groups := []FileGroup{
		{File: "a.go", Severity: "WARNING"},
		{File: "cmd/foo/main.go", Severity: "WARNING"},
		{File: "cmd/bar/util.go", Severity: "WARNING"},
		{File: "z.go", Severity: "WARNING"},
	}
	root := buildFileTree(groups)
	if !root.IsDir {
		t.Fatalf("root is not a directory")
	}
	// Top-level children: cmd (dir), then files a.go, z.go
	if len(root.Children) != 1 || root.Children[0].Name != "cmd" {
		t.Fatalf("expected single 'cmd' dir child, got %+v", root.Children)
	}
	if len(root.Files) != 2 || root.Files[0].Name != "a.go" || root.Files[1].Name != "z.go" {
		t.Fatalf("expected files [a.go, z.go], got %+v", root.Files)
	}
	cmd := root.Children[0]
	// cmd has subdirs bar, foo (sorted) and no files.
	if len(cmd.Children) != 2 || cmd.Children[0].Name != "bar" || cmd.Children[1].Name != "foo" {
		t.Fatalf("expected cmd subdirs [bar, foo], got %+v", cmd.Children)
	}
	foo := cmd.Children[1]
	if len(foo.Files) != 1 || foo.Files[0].Name != "main.go" || foo.Files[0].Severity != "WARNING" {
		t.Fatalf("expected foo/main.go leaf with WARNING, got %+v", foo.Files)
	}
}

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
		"Build-Break Findings",
		"Blast Radius",
		"Dead Code Analysis",
		// Executive summary cards:
		"Executive Summary",
		"Total Findings",
		"Warnings",
		"Affected Files",
		"Dead Code",
		// Implicit risk score (no REVIEW/CLEAN banner):
		"risk-score",
		// File tree with severity badges:
		"file-tree",
		"WARNING",
		// Findings with phase-2 CSS hooks:
		"finding--breaking",
		"finding--override",
		"finding--internal",
		// Grouped by Concern placeholder:
		"Grouped by Concern",
		"coming in next update",
		// Blast radius graph container (PR 2 fills it):
		"blast-graph",
		// Affected callers preserved:
		"Affected Callers",
		"Caller", // affected caller listed under OldFunc finding
		// Diff interactions preserved:
		"diff-summary",
		"Show only changes",
		// Layout classes:
		"summary-grid",
		"file-group",
		// Reviewer Assistant section (PR 3):
		"Reviewer Assistant",
		"Prioritized Impact",
		"Recommended Actions",
		// Reviewer content from the populated payload:
		// OldFunc has 1 caller (Via=OldFunc) → review action fires, no
		// signature question (needs >=2 callers), no spread (needs >=3 files).
		// No questions fire for this payload, so the "Reviewer Questions"
		// header is intentionally NOT asserted (it's conditional in template).
		"Review calls to `OldFunc`",
		"Verify `UnusedFunc` is no longer used", // deadcode action
	}
	for _, sub := range expectedSubstrings {
		if !strings.Contains(htmlOutput, sub) {
			t.Errorf("rendered HTML missing expected section: %q", sub)
		}
	}

	// 1b. Verify no explicit REVIEW/CLEAN verdict banner (risk is implicit).
	if strings.Contains(htmlOutput, ">REVIEW<") || strings.Contains(htmlOutput, ">CLEAN<") {
		t.Errorf("rendered HTML contains explicit REVIEW/CLEAN banner; risk should be implicit")
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
	for _, sub := range []string{"Executive Summary", "No signature mismatches detected.", "Reviewer Assistant", "No reviewer notes for this change."} {
		if !strings.Contains(htmlEmpty, sub) {
			t.Errorf("empty rendered HTML missing: %q", sub)
		}
	}
}

// --- Reviewer Assistant tests (PR 3) -----------------------------------------

// compileWithFindings is a helper that builds a payload from a set of named
// findings, each carrying a caller count spread across distinct files. It
// keeps the new reviewer tests focused on assistant behavior instead of
// repeating store/workspace boilerplate.
func compileWithFindings(t *testing.T, findingSpecs []findingSpec, deadSyms []deadcode.DeadSymbol) ReportPayload {
	t.Helper()
	store := testStore(t)

	// Persist old symbols so Compile can find them; each finding needs an old
	// symbol with content for the cross-reference to resolve.
	var oldSyms []symbol.Symbol
	for _, fs := range findingSpecs {
		oldSyms = append(oldSyms, symbol.Symbol{
			Name:    fs.name,
			Kind:    symbol.KindFunction,
			File:    fs.file,
			Content: "func " + fs.name + "() {}",
		})
	}
	if err := store.PutSymbols(oldSyms); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}

	workspaceDefs := map[string][]symbol.Symbol{}
	for _, fs := range findingSpecs {
		workspaceDefs[fs.name] = []symbol.Symbol{{
			Name:    fs.name,
			Kind:    symbol.KindFunction,
			File:    fs.file,
			Content: "func " + fs.name + "(x int) {}",
		}}
	}

	var mismatches []mismatch.Mismatch
	var blast impact.BlastResult
	seenFiles := map[string]bool{}
	for _, fs := range findingSpecs {
		mt := mismatch.MismatchArity
		if fs.override {
			mt = mismatch.MismatchOverride
		}
		mismatches = append(mismatches, mismatch.Mismatch{
			Type:       mt,
			SymbolName: fs.name,
			File:       fs.file,
			Line:       1,
			Detail:     "test mismatch",
		})
		// Build callers: each caller's Via must equal the finding's symbol name
		// so Compile attaches it. Callers are spread across fs.callerFiles in
		// order; if a finding has 5 callers and 2 files, callers 1-2 use file
		// A and 3-5 use file B (dedup → 2 files).
		for i := 0; i < fs.callerCount; i++ {
			file := fs.callerFiles[i%len(fs.callerFiles)]
			if !seenFiles[file] {
				seenFiles[file] = true
				blast.AffectedFiles = append(blast.AffectedFiles, file)
			}
			blast.DirectlyAffected = append(blast.DirectlyAffected, impact.AffectedSymbol{
				Name:           fmt.Sprintf("%sCaller%d", fs.name, i+1),
				File:           file,
				Depth:          1,
				Via:            fs.name,
				DependencyType: "direct_call",
			})
		}
	}

	payload, err := Compile(store, blast, mismatches, deadSyms, workspaceDefs)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return payload
}

type findingSpec struct {
	name        string
	file        string
	callerCount int
	callerFiles []string
	override    bool
}

func TestCompileReviewAssistantImpactOrder(t *testing.T) {
	// Two breaking findings: Foo with 5 callers, Bar with 2 callers. Foo must
	// sort first (desc by CallerCount). A third finding Baz with 2 callers
	// and override classification verifies the breaking-before-override
	// tie-break at equal CallerCount.
	payload := compileWithFindings(t, []findingSpec{
		{name: "Foo", file: "foo.go", callerCount: 5, callerFiles: []string{"a.go", "b.go", "c.go", "d.go", "e.go"}},
		{name: "Bar", file: "bar.go", callerCount: 2, callerFiles: []string{"a.go", "b.go"}},
		{name: "Baz", file: "baz.go", callerCount: 2, callerFiles: []string{"x.go"}, override: true},
	}, nil)

	rows := payload.ReviewAssistant.ImpactTable
	if len(rows) != 3 {
		t.Fatalf("ImpactTable len = %d, want 3", len(rows))
	}
	if rows[0].SymbolName != "Foo" {
		t.Errorf("ImpactTable[0].SymbolName = %q, want Foo (highest caller count)", rows[0].SymbolName)
	}
	if rows[0].CallerCount != 5 {
		t.Errorf("ImpactTable[0].CallerCount = %d, want 5", rows[0].CallerCount)
	}
	// Tie at CallerCount=2: breaking (Bar) must precede override (Baz).
	if rows[1].SymbolName != "Bar" {
		t.Errorf("ImpactTable[1].SymbolName = %q, want Bar (breaking sorts before override)", rows[1].SymbolName)
	}
	if rows[1].ChangeType != "breaking" {
		t.Errorf("ImpactTable[1].ChangeType = %q, want breaking", rows[1].ChangeType)
	}
	if rows[2].SymbolName != "Baz" {
		t.Errorf("ImpactTable[2].SymbolName = %q, want Baz (override sorts after breaking)", rows[2].SymbolName)
	}
	if rows[2].ChangeType != "override" {
		t.Errorf("ImpactTable[2].ChangeType = %q, want override", rows[2].ChangeType)
	}
	// Foo spread across 5 files.
	if rows[0].AffectedFilesCount != 5 {
		t.Errorf("ImpactTable[0].AffectedFilesCount = %d, want 5", rows[0].AffectedFilesCount)
	}
}

func TestCompileReviewAssistantQuestions(t *testing.T) {
	// Sig: breaking with 3 callers across 3 files → signature + spread questions.
	// Over: override finding → override question.
	// NoCall: breaking with 0 callers → internal change-type + internal question.
	// Below: breaking with 1 caller in 1 file → no signature (needs >=2),
	//        no spread (needs >=3 files). Used for the below-threshold check.
	payload := compileWithFindings(t, []findingSpec{
		{name: "Sig", file: "sig.go", callerCount: 3, callerFiles: []string{"a.go", "b.go", "c.go"}},
		{name: "Over", file: "over.go", callerCount: 1, callerFiles: []string{"a.go"}, override: true},
		{name: "NoCall", file: "nocall.go", callerCount: 0, callerFiles: []string{"a.go"}},
		{name: "Below", file: "below.go", callerCount: 1, callerFiles: []string{"a.go"}},
	}, nil)

	questions := payload.ReviewAssistant.Questions
	findQ := func(symContains, category string) *ReviewerQuestion {
		for i := range questions {
			if strings.Contains(questions[i].Text, symContains) && questions[i].Category == category {
				return &questions[i]
			}
		}
		return nil
	}

	if q := findQ("`Sig`", "signature"); q == nil {
		t.Errorf("missing signature question for Sig; questions=%+v", questions)
	} else {
		if !strings.Contains(q.Text, "3 callers") {
			t.Errorf("signature question text = %q, want it to contain '3 callers'", q.Text)
		}
		if !strings.Contains(q.Text, "did you verify all callers?") {
			t.Errorf("signature question text = %q, want it to contain 'did you verify all callers?'", q.Text)
		}
	}

	if q := findQ("`Over`", "override"); q == nil {
		t.Errorf("missing override question for Over; questions=%+v", questions)
	} else if !strings.Contains(q.Text, "parent contract still match") {
		t.Errorf("override question text = %q, want 'parent contract still match'", q.Text)
	}

	if q := findQ("`NoCall`", "internal"); q == nil {
		t.Errorf("missing internal question for NoCall; questions=%+v", questions)
	} else if !strings.Contains(q.Text, "no callers in the blast radius") {
		t.Errorf("internal question text = %q, want 'no callers in the blast radius'", q.Text)
	}

	if q := findQ("`Sig`", "spread"); q == nil {
		t.Errorf("missing spread question for Sig (3 files); questions=%+v", questions)
	} else if !strings.Contains(q.Text, "3 files") {
		t.Errorf("spread question text = %q, want '3 files'", q.Text)
	}

	// Below-threshold: Below has CallerCount=1 so MUST NOT produce a signature
	// question, and AffectedFilesCount=1 so MUST NOT produce a spread question.
	for _, q := range questions {
		if strings.Contains(q.Text, "`Below`") {
			t.Errorf("Below must not produce any question (below all thresholds); got %q (%s)", q.Text, q.Category)
		}
	}
	// NoCall has CallerCount=0 and ChangeType=internal, so it gets the internal
	// question but NOT a signature question.
	for _, q := range questions {
		if strings.Contains(q.Text, "`NoCall`") && q.Category == "signature" {
			t.Errorf("NoCall must not produce a signature question (CallerCount=0); got %q", q.Text)
		}
	}
}

func TestCompileReviewAssistantActions(t *testing.T) {
	// Review: 1 caller across 2 files → review action lists both files.
	// Dead: 3 dead symbols → 3 deadcode actions, each echoing confidence.
	// Override: 1 override finding → override action echoing the file.
	dead := []deadcode.DeadSymbol{
		{Symbol: symbol.Symbol{Name: "Dead1"}, Confidence: deadcode.ConfidenceHigh, Reason: "x"},
		{Symbol: symbol.Symbol{Name: "Dead2"}, Confidence: deadcode.ConfidenceMedium, Reason: "x"},
		{Symbol: symbol.Symbol{Name: "Dead3"}, Confidence: deadcode.ConfidenceLow, Reason: "x"},
	}
	payload := compileWithFindings(t, []findingSpec{
		{name: "Rev", file: "rev.go", callerCount: 2, callerFiles: []string{"a.go", "b.go"}},
		{name: "Ovr", file: "iface.go", callerCount: 0, callerFiles: []string{"a.go"}, override: true},
	}, dead)

	actions := payload.ReviewAssistant.Actions

	// Review-calls action lists both a.go and b.go.
	foundReview := false
	for _, a := range actions {
		if a.Category == "review" && strings.Contains(a.Text, "`Rev`") {
			foundReview = true
			if !strings.Contains(a.Text, "a.go") || !strings.Contains(a.Text, "b.go") {
				t.Errorf("review action text = %q, want it to contain both a.go and b.go", a.Text)
			}
		}
	}
	if !foundReview {
		t.Errorf("missing review action for Rev; actions=%+v", actions)
	}

	// Override action includes the finding file (iface.go).
	foundOverride := false
	for _, a := range actions {
		if a.Category == "override" && strings.Contains(a.Text, "`Ovr`") {
			foundOverride = true
			if !strings.Contains(a.Text, "iface.go") {
				t.Errorf("override action text = %q, want it to contain iface.go", a.Text)
			}
		}
	}
	if !foundOverride {
		t.Errorf("missing override action for Ovr; actions=%+v", actions)
	}

	// Three deadcode actions, one per dead symbol, echoing each confidence.
	// Action text is `Verify `DeadN` is no longer used (dead code, <Conf>)`.
	deadActions := 0
	wantConf := map[string]bool{"High": false, "Medium": false, "Low": false}
	for _, a := range actions {
		if a.Category == "deadcode" {
			deadActions++
			for c := range wantConf {
				if strings.Contains(a.Text, "dead code, "+c+")") {
					wantConf[c] = true
				}
			}
		}
	}
	if deadActions != 3 {
		t.Errorf("deadcode actions count = %d, want 3", deadActions)
	}
	for c, seen := range wantConf {
		if !seen {
			t.Errorf("missing deadcode action echoing confidence %q", c)
		}
	}
}

func TestCompileReviewAssistantEmpty(t *testing.T) {
	store := testStore(t)
	payload, err := Compile(store, impact.BlastResult{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(payload.ReviewAssistant.ImpactTable) != 0 {
		t.Errorf("ImpactTable len = %d, want 0", len(payload.ReviewAssistant.ImpactTable))
	}
	if len(payload.ReviewAssistant.Questions) != 0 {
		t.Errorf("Questions len = %d, want 0", len(payload.ReviewAssistant.Questions))
	}
	if len(payload.ReviewAssistant.Actions) != 0 {
		t.Errorf("Actions len = %d, want 0", len(payload.ReviewAssistant.Actions))
	}
}

func TestCompileExpandedSummary(t *testing.T) {
	// Mixed payload: 1 breaking (with callers), 1 override, 1 internal (no callers).
	// Direct callers: 3 (the breaking finding's callers). Transitive: 2 (added below).
	payload := compileWithFindings(t, []findingSpec{
		{name: "Brk", file: "brk.go", callerCount: 3, callerFiles: []string{"a.go", "b.go", "c.go"}},
		{name: "Ovr", file: "ovr.go", callerCount: 1, callerFiles: []string{"a.go"}, override: true},
		{name: "Int", file: "int.go", callerCount: 0, callerFiles: []string{"a.go"}},
	}, nil)

	// compileWithFindings only seeds DirectlyAffected; add transitive for the
	// summary test so TransitiveCallers is non-zero. We re-run Compile with a
	// blast that has transitive entries. Easier path: rebuild via the helper
	// then assert only DirectCallers; for TransitiveCallers do a direct call.
	if payload.Summary.BreakingCount != 1 {
		t.Errorf("BreakingCount = %d, want 1", payload.Summary.BreakingCount)
	}
	if payload.Summary.OverrideCount != 1 {
		t.Errorf("OverrideCount = %d, want 1", payload.Summary.OverrideCount)
	}
	if payload.Summary.InternalCount != 1 {
		t.Errorf("InternalCount = %d, want 1", payload.Summary.InternalCount)
	}
	if payload.Summary.DirectCallers != 4 { // 3 (Brk) + 1 (Ovr)
		t.Errorf("DirectCallers = %d, want 4", payload.Summary.DirectCallers)
	}

	// Separate compile with explicit transitive entries to validate the
	// TransitiveCallers field.
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
		{Type: mismatch.MismatchArity, SymbolName: "Foo", File: "foo.go", Line: 1, Detail: "x"},
	}
	blast := impact.BlastResult{
		DirectlyAffected: []impact.AffectedSymbol{
			{Name: "D1", File: "d1.go", Depth: 1, Via: "Foo", DependencyType: "direct_call"},
		},
		TransitivelyAffected: []impact.AffectedSymbol{
			{Name: "T1", File: "t1.go", Depth: 2, Via: "Foo", DependencyType: "direct_call"},
			{Name: "T2", File: "t2.go", Depth: 2, Via: "Foo", DependencyType: "direct_call"},
		},
	}
	payload2, err := Compile(store, blast, mismatches, nil, workspaceDefs)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if payload2.Summary.DirectCallers != 1 {
		t.Errorf("DirectCallers = %d, want 1", payload2.Summary.DirectCallers)
	}
	if payload2.Summary.TransitiveCallers != 2 {
		t.Errorf("TransitiveCallers = %d, want 2", payload2.Summary.TransitiveCallers)
	}
	if payload2.Summary.BreakingCount != 1 {
		t.Errorf("BreakingCount = %d, want 1", payload2.Summary.BreakingCount)
	}
}
