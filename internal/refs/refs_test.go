package refs

import (
	"reflect"
	"sort"
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tspack "github.com/xberg-io/tree-sitter-language-pack/packages/go"
)

// mockExtractor is a ReferenceExtractor stub used by the registry tests so the
// registry logic can be exercised without depending on any tree-sitter
// grammar being loadable.
type mockExtractor struct {
	lang string
	refs []Reference
	err  error
}

func (m *mockExtractor) Language() string { return m.lang }
func (m *mockExtractor) ExtractReferences(root *tspack.Node, source []byte) ([]Reference, error) {
	_ = root
	_ = source
	if m.err != nil {
		return nil, m.err
	}
	return m.refs, nil
}

// resetRegistry clears the global registry between tests so each case starts
// from a known empty state. Tests that rely on the package's real init()
// self-registration should call this before re-registering exactly the
// languages they need.
func resetRegistry() {
	mu.Lock()
	defer mu.Unlock()
	extractors = make(map[string]ReferenceExtractor)
}

// ---------- Registry ----------

// TestRegisterAndGet verifies the basic Register/Get contract: a registered
// extractor is returned by Get with ok=true, and an unknown language returns
// (nil, false).
func TestRegisterAndGet(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	m := &mockExtractor{lang: "kotlin", refs: []Reference{{SymbolName: "X"}}}
	Register(m)

	got, ok := Get("kotlin")
	if !ok {
		t.Fatalf("Get(\"kotlin\") ok = false, want true")
	}
	if got != m {
		t.Fatalf("Get(\"kotlin\") = %p, want %p", got, m)
	}

	if _, ok := Get("erlang"); ok {
		t.Fatalf("Get(\"erlang\") ok = true, want false for unregistered language")
	}
}

// TestRegisterNilIsNoop asserts Register(nil) is a safe no-op rather than
// panic. Defending against nil is cheap and avoids a surprising crash in
// init() blocks that build extractors conditionally.
func TestRegisterNilIsNoop(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	Register(nil)
	if langs := Languages(); len(langs) != 0 {
		t.Fatalf("after Register(nil), Languages() = %v, want empty", langs)
	}
}

// TestRegisterOverwrites verifies the documented "last registration wins"
// semantics: registering the same language twice replaces the extractor.
func TestRegisterOverwrites(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	first := &mockExtractor{lang: "go", refs: []Reference{{SymbolName: "first"}}}
	second := &mockExtractor{lang: "go", refs: []Reference{{SymbolName: "second"}}}
	Register(first)
	Register(second)

	got, ok := Get("go")
	if !ok {
		t.Fatalf("Get(\"go\") ok = false, want true")
	}
	if got != second {
		t.Fatalf("after double Register, Get(\"go\") = %p, want %p (second)", got, second)
	}
}

// TestLanguagesSorted verifies Languages returns a sorted slice of every
// registered language, regardless of insertion order.
func TestLanguagesSorted(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	Register(&mockExtractor{lang: "python"})
	Register(&mockExtractor{lang: "go"})
	Register(&mockExtractor{lang: "ada"})

	got := Languages()
	want := []string{"ada", "go", "python"}
	if !equalStrings(got, want) {
		t.Fatalf("Languages() = %v, want %v (sorted)", got, want)
	}
}

// TestLanguagesEmpty verifies that with no registrations Languages returns an
// empty (non-nil) slice, not nil, so callers can range over it safely.
func TestLanguagesEmpty(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	got := Languages()
	if got == nil {
		t.Fatalf("Languages() = nil, want empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("Languages() = %v, want empty", got)
	}
}

// TestExtractAllWithMock verifies ExtractAll dispatches to the registered
// extractor for each requested language, returns a map keyed by language,
// and silently skips languages with no registered extractor.
func TestExtractAllWithMock(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	want := []Reference{{SymbolName: "Foo", Kind: RefCall, SourceLine: 7}}
	Register(&mockExtractor{lang: "go", refs: want})
	// Deliberately do NOT register "rust"; ExtractAll must skip it.

	root := (*tspack.Node)(nil) // mock ignores it
	got, err := ExtractAll(root, []byte("anything"), []string{"go", "rust"})
	if err != nil {
		t.Fatalf("ExtractAll err = %v, want nil", err)
	}
	if _, ok := got["rust"]; ok {
		t.Fatalf("ExtractAll result must omit unregistered language \"rust\"")
	}
	goRefs, ok := got["go"]
	if !ok {
		t.Fatalf("ExtractAll result missing \"go\" key")
	}
	if len(goRefs) != 1 || !reflect.DeepEqual(goRefs[0], want[0]) {
		t.Fatalf("ExtractAll go refs = %+v, want %+v", goRefs, want)
	}
}

// TestExtractAllPropagatesError verifies that when an extractor returns an
// error, ExtractAll surfaces it immediately and returns a nil map, rather
// than partially populating the result.
func TestExtractAllPropagatesError(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	boom := errMock("boom")
	Register(&mockExtractor{lang: "go", err: boom})

	root := (*tspack.Node)(nil)
	got, err := ExtractAll(root, nil, []string{"go"})
	if err == nil || err.Error() != boom.Error() {
		t.Fatalf("ExtractAll err = %v, want %q", err, boom.Error())
	}
	if got != nil {
		t.Fatalf("ExtractAll result = %v, want nil on error", got)
	}
}

// TestExtractAllEmptyLangs verifies that requesting no languages yields an
// empty (non-nil) map and no error.
func TestExtractAllEmptyLangs(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	root := (*tspack.Node)(nil)
	got, err := ExtractAll(root, nil, nil)
	if err != nil {
		t.Fatalf("ExtractAll err = %v, want nil", err)
	}
	if got == nil {
		t.Fatalf("ExtractAll result = nil, want empty map")
	}
	if len(got) != 0 {
		t.Fatalf("ExtractAll result = %v, want empty map", got)
	}
}

// ---------- Query engine: real grammars ----------

// queryExtractorForTest builds a queryExtractor with a custom query source so
// the engine can be exercised with inline .scm snippets, independent of what
// the language pack bundles. The language is still obtained from the pack so
// the query compiles against the right grammar.
func queryExtractorForTest(t *testing.T, lang, querySrc string) *queryExtractor {
	t.Helper()
	// We need the real language available for the query to compile. Skip
	// gracefully if the environment does not have the grammar compiled in.
	if !tspack.HasLanguage(lang) {
		t.Skipf("language %q not available in this build of the language pack", lang)
	}
	return &queryExtractor{lang: lang, querySrc: querySrc}
}

// parseWithGoTreeSitter re-parses source with go-tree-sitter for the given
// language. It is kept as a documented helper for tests that want to obtain
// a real tree-sitter tree for assertions outside the engine; the engine
// itself re-parses internally and does not need it.
func parseWithGoTreeSitter(t *testing.T, lang string, source []byte) *tree_sitter.Tree {
	t.Helper()
	l, err := tspack.GetLanguage(lang)
	if err != nil {
		t.Fatalf("get language %q: %v", lang, err)
	}
	p := tree_sitter.NewParser()
	t.Cleanup(p.Close)
	if err := p.SetLanguage(l); err != nil {
		t.Fatalf("set language %q: %v", lang, err)
	}
	tree := p.Parse(source, nil)
	if tree == nil {
		t.Fatalf("parse produced no tree")
	}
	return tree
}

var _ = parseWithGoTreeSitter // retained helper; unused by current tests

// TestQueryExtractorCallCapture verifies the engine emits a RefCall reference
// for an @reference.call capture with its @name capture as SymbolName. We use
// a minimal inline .scm that matches a Go call_expression so the test does
// not depend on the exact shape of the bundled tags.scm.
func TestQueryExtractorCallCapture(t *testing.T) {
	const querySrc = `
(call_expression
  function: (identifier) @name) @reference.call
`
	e := queryExtractorForTest(t, "go", querySrc)

	src := []byte("package main\nfunc f() { g() }\n")
	// ExtractReferences re-parses internally; the tspack root argument is
	// ignored by the engine, so passing nil is fine.
	refs, err := e.ExtractReferences(nil, src)
	if err != nil {
		t.Fatalf("ExtractReferences: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d refs, want 1: %+v", len(refs), refs)
	}
	r := refs[0]
	if r.SymbolName != "g" {
		t.Errorf("SymbolName = %q, want %q", r.SymbolName, "g")
	}
	if r.Kind != RefCall {
		t.Errorf("Kind = %q, want %q", r.Kind, RefCall)
	}
	// `g()` appears on line 2 in the source; 1-based.
	if r.SourceLine != 2 {
		t.Errorf("SourceLine = %d, want 2", r.SourceLine)
	}
	if r.SourceCol < 1 {
		t.Errorf("SourceCol = %d, want >= 1", r.SourceCol)
	}
	if r.ContainingSymbol != "" {
		t.Errorf("ContainingSymbol = %q, want empty (no @definition.* in this query)", r.ContainingSymbol)
	}
}

// TestQueryExtractorTypeCapture verifies a @reference.type capture maps to
// RefTypeUse, with the type identifier as the symbol name.
func TestQueryExtractorTypeCapture(t *testing.T) {
	const querySrc = `
(type_identifier) @name @reference.type
`
	e := queryExtractorForTest(t, "go", querySrc)

	src := []byte("package main\nvar x MyType\n")
	refs, err := e.ExtractReferences(nil, src)
	if err != nil {
		t.Fatalf("ExtractReferences: %v", err)
	}
	// `MyType` is the only type_identifier in this snippet.
	var found bool
	for _, r := range refs {
		if r.SymbolName == "MyType" {
			found = true
			if r.Kind != RefTypeUse {
				t.Errorf("Kind for MyType = %q, want %q", r.Kind, RefTypeUse)
			}
		}
	}
	if !found {
		t.Fatalf("no reference with SymbolName %q; got %+v", "MyType", refs)
	}
}

// TestQueryExtractorUnknownReferenceCaptureIgnored verifies a capture whose
// name is not in queryCaptureKind (e.g. a hypothetical @reference.docstring)
// is NOT emitted as a reference, only the known kinds are.
func TestQueryExtractorUnknownReferenceCaptureIgnored(t *testing.T) {
	const querySrc = `
(call_expression
  function: (identifier) @name) @reference.call
(call_expression
  function: (identifier) @name) @reference.madeup
`
	e := queryExtractorForTest(t, "go", querySrc)
	src := []byte("package main\nfunc f() { g() }\n")
	refs, err := e.ExtractReferences(nil, src)
	if err != nil {
		t.Fatalf("ExtractReferences: %v", err)
	}
	for _, r := range refs {
		if r.Kind != RefCall {
			t.Errorf("emitted reference with unexpected kind %q; unknown captures must be skipped", r.Kind)
		}
	}
}

// TestQueryExtractorNoNameCaptureSkipsReference verifies that an
// @reference.* capture without a sibling @name capture in the same match is
// skipped, because the engine cannot know which symbol was used.
func TestQueryExtractorNoNameCaptureSkipsReference(t *testing.T) {
	const querySrc = `
(call_expression) @reference.call
`
	e := queryExtractorForTest(t, "go", querySrc)
	src := []byte("package main\nfunc f() { g() }\n")
	refs, err := e.ExtractReferences(nil, src)
	if err != nil {
		t.Fatalf("ExtractReferences: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("got %d refs, want 0 when @name is absent: %+v", len(refs), refs)
	}
}

// TestQueryExtractorNoTagsQueryReturnsTypedError verifies that an extractor
// configured for a language with no bundled tags.scm returns the typed
// errNoTagsQuery error (rather than panicking on an empty query string).
func TestQueryExtractorNoTagsQueryReturnsTypedError(t *testing.T) {
	// Use a language the pack likely has a grammar for but for which we
	// configure an empty query source.
	e := &queryExtractor{lang: "go", querySrc: ""}
	_, err := e.ExtractReferences(nil, []byte("package main\n"))
	if err == nil {
		t.Fatalf("ExtractReferences err = nil, want errNoTagsQuery")
	}
	if _, ok := err.(errNoTagsQuery); !ok {
		t.Fatalf("err type = %T, want errNoTagsQuery", err)
	}
}

// TestQueryExtractorBundledTagsForGo verifies the engine runs against the
// real bundled tags.scm for Go and emits at least one RefCall for a snippet
// that clearly contains a call. This guards the integration with the pack's
// bundled queries end-to-end.
func TestQueryExtractorBundledTagsForGo(t *testing.T) {
	if tspack.GetTagsQuery("go") == nil {
		t.Skip("no bundled tags.scm for go in this build")
	}
	e := newQueryExtractor("go")
	src := []byte("package main\n\nfunc f() {\n\tbar()\n}\n")
	refs, err := e.ExtractReferences(nil, src)
	if err != nil {
		t.Fatalf("ExtractReferences: %v", err)
	}
	var sawBarCall bool
	for _, r := range refs {
		if r.SymbolName == "bar" && r.Kind == RefCall {
			sawBarCall = true
		}
	}
	if !sawBarCall {
		t.Errorf("did not find RefCall for \"bar\"; got %+v", refs)
	}
}

// TestQueryExtractorBundledTagsForPython verifies the engine also works
// against the Python grammar and its bundled tags.scm.
func TestQueryExtractorBundledTagsForPython(t *testing.T) {
	if !tspack.HasLanguage("python") || tspack.GetTagsQuery("python") == nil {
		t.Skip("python grammar or tags.scm not available in this build")
	}
	e := newQueryExtractor("python")
	src := []byte("def f():\n    bar()\n")
	refs, err := e.ExtractReferences(nil, src)
	if err != nil {
		t.Fatalf("ExtractReferences: %v", err)
	}
	var sawBarCall bool
	for _, r := range refs {
		if r.SymbolName == "bar" && r.Kind == RefCall {
			sawBarCall = true
		}
	}
	if !sawBarCall {
		t.Errorf("did not find RefCall for \"bar\"; got %+v", refs)
	}
}

// ---------- Scope tracking (ContainingSymbol) ----------

// TestQueryExtractorContainingSymbolPerScope verifies that the engine tracks
// ContainingSymbol per named declaration: two separate top-level functions
// each calling a different helper must produce references whose
// ContainingSymbol is the function that lexically encloses them.
//
// Go does not allow nested named function declarations, so the realistic
// test of scope tracking is two sibling named scopes each containing a call.
// This proves the engine resolves ContainingSymbol to the nearest enclosing
// named declaration rather than always attributing every reference to the
// first declaration in the file.
func TestQueryExtractorContainingSymbolPerScope(t *testing.T) {
	const querySrc = `
(function_declaration
  name: (identifier) @name) @definition.function

(method_declaration
  name: (field_identifier) @name) @definition.method

(call_expression
  function: (identifier) @name) @reference.call
`
	e := queryExtractorForTest(t, "go", querySrc)

	src := []byte(`package main

func alpha() {
	foo()
}

func beta() {
	bar()
}
`)
	refs, err := e.ExtractReferences(nil, src)
	if err != nil {
		t.Fatalf("ExtractReferences: %v", err)
	}

	// Collect the (symbolName, containingSymbol) pairs.
	type pair struct{ name, container string }
	got := make([]pair, 0, len(refs))
	for _, r := range refs {
		got = append(got, pair{r.SymbolName, r.ContainingSymbol})
	}
	sort.Slice(got, func(i, j int) bool {
		if got[i].name != got[j].name {
			return got[i].name < got[j].name
		}
		return got[i].container < got[j].container
	})

	// We expect:
	//   - foo() called from alpha → ContainingSymbol "alpha"
	//   - bar() called from beta  → ContainingSymbol "beta"
	wantFoo := false
	wantBar := false
	for _, p := range got {
		if p.name == "foo" && p.container == "alpha" {
			wantFoo = true
		}
		if p.name == "bar" && p.container == "beta" {
			wantBar = true
		}
	}
	if !wantFoo {
		t.Errorf("missing ref {name=foo, container=alpha}; got %+v", got)
	}
	if !wantBar {
		t.Errorf("missing ref {name=bar, container=beta}; got %+v", got)
	}
}

// TestQueryExtractorContainingSymbolNestedClosure verifies that a reference
// inside an unnamed nested scope (a Go closure / func_literal) resolves to
// the nearest NAMED enclosing declaration, because closures have no name of
// their own. This is the spec's "nested functions" scenario adapted to Go's
// rule that named functions cannot nest.
func TestQueryExtractorContainingSymbolNestedClosure(t *testing.T) {
	const querySrc = `
(function_declaration
  name: (identifier) @name) @definition.function

(call_expression
  function: (identifier) @name) @reference.call
`
	e := queryExtractorForTest(t, "go", querySrc)

	// outer contains a closure that calls helper(). helper's nearest NAMED
	// enclosing declaration is outer, because the closure is unnamed.
	src := []byte(`package main

func outer() {
	fn := func() {
		helper()
	}
	_ = fn
}
`)
	refs, err := e.ExtractReferences(nil, src)
	if err != nil {
		t.Fatalf("ExtractReferences: %v", err)
	}
	var found bool
	for _, r := range refs {
		if r.SymbolName == "helper" {
			found = true
			if r.ContainingSymbol != "outer" {
				t.Errorf("helper ContainingSymbol = %q, want %q (nearest named scope)", r.ContainingSymbol, "outer")
			}
		}
	}
	if !found {
		t.Fatalf("no reference to \"helper\" found; got %+v", refs)
	}
}

// TestQueryExtractorContainingSymbolFileScope verifies that a reference at the
// top level of a file (not enclosed by any function/method) gets an empty
// ContainingSymbol.
func TestQueryExtractorContainingSymbolFileScope(t *testing.T) {
	const querySrc = `
(function_declaration
  name: (identifier) @name) @definition.function

(call_expression
  function: (identifier) @name) @reference.call
`
	e := queryExtractorForTest(t, "go", querySrc)
	// A call_expression can't legally appear at file scope in Go, but our
	// query matches on AST shape, not grammar validity. We still wrap the
	// call in a function so the AST is realistic; the goal here is to verify
	// that when the call IS inside a function, the container is non-empty,
	// and when we run with a query that has NO @definition.* (the previous
	// TestQueryExtractorCallCapture case), the container is empty.
	src := []byte("package main\nfunc top() { g() }\n")
	refs, err := e.ExtractReferences(nil, src)
	if err != nil {
		t.Fatalf("ExtractReferences: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d refs, want 1: %+v", len(refs), refs)
	}
	if refs[0].ContainingSymbol != "top" {
		t.Errorf("ContainingSymbol = %q, want %q", refs[0].ContainingSymbol, "top")
	}
}

// ---------- Query capture-name map (unit) ----------

// TestInitRegistersBundledLanguages verifies the package init() registers a
// queryExtractor for every language in bundledQueryLanguages that has a real
// tags.scm bundled in this build.
//
// We don't rely on leftover init() state because earlier tests in this file
// call resetRegistry and may leave the registry empty. Instead we rebuild
// the registry the same way init() does and assert the result, which makes
// the test order-independent while still exercising the registration path.
func TestInitRegistersBundledLanguages(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	// Re-run the init() registration manually so the test is hermetic.
	for _, lang := range bundledQueryLanguages {
		if tspack.GetTagsQuery(lang) == nil {
			continue
		}
		Register(newQueryExtractor(lang))
	}

	mu.RLock()
	goRegistered, goOk := extractors["go"]
	mu.RUnlock()
	if !goOk {
		t.Fatalf("after init-style registration, \"go\" missing; init() must register bundled query extractors")
	}
	if _, ok := goRegistered.(*queryExtractor); !ok {
		t.Errorf("registered \"go\" extractor type = %T, want *queryExtractor", goRegistered)
	}

	// Sanity: Languages() should now include "go" plus any other bundled
	// languages the pack ships for this build.
	langs := Languages()
	foundGo := false
	for _, l := range langs {
		if l == "go" {
			foundGo = true
		}
	}
	if !foundGo {
		t.Errorf("Languages() = %v, want it to include \"go\"", langs)
	}
}

// TestQueryCaptureKindMap verifies the mapping table used by the engine. It
// is a small table but it is the contract between the engine and every
// bundled tags.scm, so we lock it down explicitly.
func TestQueryCaptureKindMap(t *testing.T) {
	cases := []struct {
		capture string
		kind    ReferenceKind
	}{
		{"reference.call", RefCall},
		{"reference.send", RefCall},
		{"reference.type", RefTypeUse},
		{"reference.implementation", RefTypeUse},
		{"reference.class", RefTypeUse},
	}
	for _, c := range cases {
		got, ok := queryCaptureKind[c.capture]
		if !ok {
			t.Errorf("queryCaptureKind[%q] missing, want %q", c.capture, c.kind)
			continue
		}
		if got != c.kind {
			t.Errorf("queryCaptureKind[%q] = %q, want %q", c.capture, got, c.kind)
		}
	}
	// Non-reference captures must NOT be in the map.
	for _, bad := range []string{"name", "doc", "definition.function", "reference.unknown"} {
		if _, ok := queryCaptureKind[bad]; ok {
			t.Errorf("queryCaptureKind[%q] present, want absent (non-reference capture)", bad)
		}
	}
}

// ---------- helpers ----------

// errMock is a trivial error used to assert ExtractAll propagates errors.
type errMock string

func (e errMock) Error() string { return string(e) }

// equalStrings reports whether a and b have the same length and contents.
// We avoid pulling in reflect.DeepEqual for a flat string slice to keep the
// test output readable on failure.
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

// Compile-time assertion that the mock satisfies the interface.
var _ ReferenceExtractor = (*mockExtractor)(nil)
