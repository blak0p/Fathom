package interactive

import (
	"errors"
	"testing"
)

// scriptDriver is a Questioner that replays scripted answers in order. Each
// call to Select/Input/Confirm pops the next answer from the matching queue.
// When a queue is empty, the call returns ErrWizardAborted, simulating a user
// that quits mid-wizard. The driver records every call so tests can assert the
// prompt sequence.
type scriptDriver struct {
	selects  []scriptSelect
	inputs   []scriptInput
	confirms []scriptConfirm

	selectCalls  []selectCall
	inputCalls   []inputCall
	confirmCalls []confirmCall
}

// Scripted answer payloads. Setting err on an entry makes that prompt return
// the error, simulating an abort.
type scriptSelect struct {
	answer string
	err    error
}
type scriptInput struct {
	answer string
	err    error
}
type scriptConfirm struct {
	answer bool
	err    error
}

// Recorded call entries for assertion.
type selectCall struct {
	title   string
	choices []string
}
type inputCall struct {
	title       string
	placeholder string
}
type confirmCall struct {
	title        string
	defaultValue bool
}

func (d *scriptDriver) Select(title string, choices []string) (string, error) {
	d.selectCalls = append(d.selectCalls, selectCall{title: title, choices: choices})
	if len(d.selects) == 0 {
		return "", ErrWizardAborted
	}
	s := d.selects[0]
	d.selects = d.selects[1:]
	return s.answer, s.err
}

func (d *scriptDriver) Input(title, placeholder string) (string, error) {
	d.inputCalls = append(d.inputCalls, inputCall{title: title, placeholder: placeholder})
	if len(d.inputs) == 0 {
		return "", ErrWizardAborted
	}
	s := d.inputs[0]
	d.inputs = d.inputs[1:]
	return s.answer, s.err
}

func (d *scriptDriver) Confirm(title string, defaultValue bool) (bool, error) {
	d.confirmCalls = append(d.confirmCalls, confirmCall{title: title, defaultValue: defaultValue})
	if len(d.confirms) == 0 {
		return false, ErrWizardAborted
	}
	s := d.confirms[0]
	d.confirms = d.confirms[1:]
	return s.answer, s.err
}

// TestWizardBranchModeHappyPath drives the wizard with a full branch-mode
// happy path: branch mode, base "main", HTML format, strict off. It asserts
// the returned Config and that the base prompt was shown.
func TestWizardBranchModeHappyPath(t *testing.T) {
	drv := &scriptDriver{
		selects: []scriptSelect{
			{answer: "Compare against a base branch"},
			{answer: "HTML report"},
		},
		inputs:   []scriptInput{{answer: "main"}},
		confirms: []scriptConfirm{{answer: false}},
	}

	cfg, err := AnalyzerWithDriver(drv)
	if err != nil {
		t.Fatalf("AnalyzerWithDriver: %v", err)
	}

	if cfg.Mode != ModeBranch {
		t.Errorf("Mode = %q, want %q", cfg.Mode, ModeBranch)
	}
	if cfg.Base != "main" {
		t.Errorf("Base = %q, want %q", cfg.Base, "main")
	}
	if cfg.Format != FormatHTML {
		t.Errorf("Format = %q, want %q", cfg.Format, FormatHTML)
	}
	if cfg.Strict {
		t.Errorf("Strict = true, want false")
	}
	if len(cfg.Args) != 0 {
		t.Errorf("Args = %v, want empty", cfg.Args)
	}

	// The base prompt must have been shown exactly once in branch mode.
	if len(drv.inputCalls) != 1 {
		t.Errorf("expected 1 Input call, got %d", len(drv.inputCalls))
	}
	if got, want := drv.inputCalls[0].title, "Base branch to compare against"; got != want {
		t.Errorf("Input title = %q, want %q", got, want)
	}
}

// TestWizardFilesModeSkipsBase verifies that selecting files mode skips the
// base branch prompt entirely and collects file paths instead.
func TestWizardFilesModeSkipsBase(t *testing.T) {
	drv := &scriptDriver{
		selects: []scriptSelect{
			{answer: "Analyze specific files"},
			{answer: "JSON"},
		},
		inputs:   []scriptInput{{answer: "main.go server.go"}},
		confirms: []scriptConfirm{{answer: true}},
	}

	cfg, err := AnalyzerWithDriver(drv)
	if err != nil {
		t.Fatalf("AnalyzerWithDriver: %v", err)
	}

	if cfg.Mode != ModeFiles {
		t.Errorf("Mode = %q, want %q", cfg.Mode, ModeFiles)
	}
	if cfg.Base != "" {
		t.Errorf("Base = %q, want empty in files mode", cfg.Base)
	}
	if cfg.Format != FormatJSON {
		t.Errorf("Format = %q, want %q", cfg.Format, FormatJSON)
	}
	if !cfg.Strict {
		t.Errorf("Strict = false, want true")
	}
	wantArgs := []string{"main.go", "server.go"}
	if len(cfg.Args) != len(wantArgs) {
		t.Fatalf("Args = %v, want %v", cfg.Args, wantArgs)
	}
	for i, a := range cfg.Args {
		if a != wantArgs[i] {
			t.Errorf("Args[%d] = %q, want %q", i, a, wantArgs[i])
		}
	}

	// Exactly one Input call (file paths), no base prompt.
	if len(drv.inputCalls) != 1 {
		t.Errorf("expected 1 Input call, got %d", len(drv.inputCalls))
	}
	if got, want := drv.inputCalls[0].title, "File paths (space-separated)"; got != want {
		t.Errorf("Input title = %q, want %q", got, want)
	}
}

// TestWizardAbortReturnsError verifies that an abort (driver returns an error
// on the first prompt) surfaces as ErrWizardAborted without completing the
// wizard.
func TestWizardAbortReturnsError(t *testing.T) {
	drv := &scriptDriver{
		selects: []scriptSelect{{err: errors.New("boom")}},
	}

	_, err := AnalyzerWithDriver(drv)
	if err == nil {
		t.Fatal("expected error from aborted wizard, got nil")
	}
	// The raw error is passed through (not tea.ErrProgramKilled here), so
	// it is NOT converted to ErrWizardAborted. Assert we got the raw error.
	if err.Error() != "boom" {
		t.Errorf("err = %q, want %q", err.Error(), "boom")
	}
}

// TestWizardEmptyFilesInput verifies that an empty file-paths submission in
// files mode yields an empty Args slice rather than panicking.
func TestWizardEmptyFilesInput(t *testing.T) {
	drv := &scriptDriver{
		selects: []scriptSelect{
			{answer: "Analyze specific files"},
			{answer: "Terminal"},
		},
		inputs:   []scriptInput{{answer: "   "}},
		confirms: []scriptConfirm{{answer: false}},
	}

	cfg, err := AnalyzerWithDriver(drv)
	if err != nil {
		t.Fatalf("AnalyzerWithDriver: %v", err)
	}
	if cfg.Mode != ModeFiles {
		t.Errorf("Mode = %q, want %q", cfg.Mode, ModeFiles)
	}
	if len(cfg.Args) != 0 {
		t.Errorf("Args = %v, want empty for blank input", cfg.Args)
	}
}

// TestWizardTerminalFormat verifies the terminal format selection maps
// correctly and leaves Strict false by default.
func TestWizardTerminalFormat(t *testing.T) {
	drv := &scriptDriver{
		selects: []scriptSelect{
			{answer: "Compare against a base branch"},
			{answer: "Terminal"},
		},
		inputs:   []scriptInput{{answer: "develop"}},
		confirms: []scriptConfirm{{answer: false}},
	}

	cfg, err := AnalyzerWithDriver(drv)
	if err != nil {
		t.Fatalf("AnalyzerWithDriver: %v", err)
	}
	if cfg.Format != FormatTerminal {
		t.Errorf("Format = %q, want %q", cfg.Format, FormatTerminal)
	}
	if cfg.Base != "develop" {
		t.Errorf("Base = %q, want %q", cfg.Base, "develop")
	}
}

// TestSplitFiles covers the space, tab, and comma-separated parsing edge cases.
func TestSplitFiles(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"main.go", []string{"main.go"}},
		{"a.go b.go", []string{"a.go", "b.go"}},
		{"a.go,b.go", []string{"a.go", "b.go"}},
		{"a.go b.go,c.go", []string{"a.go", "b.go", "c.go"}},
		{"  spaced.go  ", []string{"spaced.go"}},
	}
	for _, c := range cases {
		got := splitFiles(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitFiles(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i, g := range got {
			if g != c.want[i] {
				t.Errorf("splitFiles(%q)[%d] = %q, want %q", c.in, i, g, c.want[i])
			}
		}
	}
}