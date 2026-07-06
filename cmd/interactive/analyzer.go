package interactive

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Analysis mode constants. Branch mode compares the workspace against a base
// branch; files mode analyzes a specific set of file paths.
const (
	ModeBranch = "branch"
	ModeFiles  = "files"
)

// Output format constants for the wizard's format step.
const (
	FormatTerminal = "terminal"
	FormatJSON     = "json"
	FormatHTML     = "html"
)

// Step identifiers in the wizard sequence. The order is:
//
//	mode → base (branch only) or file paths (files) → format → strict → done
type step int

const (
	stepMode step = iota
	stepBase
	stepFiles
	stepFormat
	stepStrict
	stepDone
)

// Config is what the wizard hands back to runAnalyze. The caller applies the
// fields to the existing package-level flag variables and reuses the same
// analysis pipeline as the flag-based flow.
type Config struct {
	Mode   string   // "branch" | "files"
	Base   string   // set only in branch mode
	Format string   // "terminal" | "json" | "html"
	Strict bool
	Args   []string // file paths in files mode
}

// ErrWizardAborted is returned by Analyzer/AnalyzerWithDriver when the user
// quits the wizard (Ctrl+C / Esc) before completing all prompts. No analysis
// is performed when this is returned.
var ErrWizardAborted = errors.New("analyze wizard aborted by user")

// model is the wizard state machine. It is intentionally a plain struct, not a
// tea.Model: the wizard is driven sequentially through the Questioner driver,
// so each prompt is its own short-lived program. model exists to keep the step
// transitions and Config assembly in one place.
type model struct {
	driver Questioner
	config Config
}

// newModel builds a wizard model bound to the given driver.
func newModel(driver Questioner) model {
	return model{driver: driver, config: Config{Mode: ModeBranch}}
}

// run advances the wizard through the step sequence and returns the assembled
// Config. On any abort (driver returns tea.ErrProgramKilled) it converts the
// error to ErrWizardAborted so callers can distinguish a user cancel from a
// real failure.
func (m model) run() (Config, error) {
	mode, err := m.driver.Select(
		"What do you want to analyze?",
		[]string{"Compare against a base branch", "Analyze specific files"},
	)
	if err != nil {
		return Config{}, toAbort(err)
	}

	// The first option is the branch mode, the second is files mode. Keep
	// the label-to-constant mapping in one place so the prompts can stay
	// human-readable.
	switch mode {
	case "Compare against a base branch":
		m.config.Mode = ModeBranch
	case "Analyze specific files":
		m.config.Mode = ModeFiles
	default:
		return Config{}, fmt.Errorf("analyze wizard: unexpected mode %q", mode)
	}

	switch m.config.Mode {
	case ModeBranch:
		base, err := m.driver.Input("Base branch to compare against", "main")
		if err != nil {
			return Config{}, toAbort(err)
		}
		m.config.Base = strings.TrimSpace(base)
	case ModeFiles:
		raw, err := m.driver.Input("File paths (space-separated)", "main.go")
		if err != nil {
			return Config{}, toAbort(err)
		}
		m.config.Args = splitFiles(raw)
	}

	format, err := m.driver.Select(
		"Output format",
		[]string{"HTML report", "Terminal", "JSON"},
	)
	if err != nil {
		return Config{}, toAbort(err)
	}
	switch format {
	case "HTML report":
		m.config.Format = FormatHTML
	case "Terminal":
		m.config.Format = FormatTerminal
	case "JSON":
		m.config.Format = FormatJSON
	default:
		return Config{}, fmt.Errorf("analyze wizard: unexpected format %q", format)
	}

	strict, err := m.driver.Confirm(
		"Fail on signature mismatch? (exit non-zero when mismatches are found)",
		false,
	)
	if err != nil {
		return Config{}, toAbort(err)
	}
	m.config.Strict = strict

	return m.config, nil
}

// splitFiles turns the space- or comma-separated file list from the Input
// prompt into a clean slice of paths. Empty entries are dropped.
func splitFiles(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ' ' || r == '\t' || r == ','
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// toAbort normalizes a driver error: a tea.ErrProgramKilled (Ctrl+C / Esc) is
// mapped to ErrWizardAborted so callers can treat a user cancel distinctly
// from an unexpected failure. Any other error is passed through unchanged.
func toAbort(err error) error {
	if errors.Is(err, tea.ErrProgramKilled) {
		return ErrWizardAborted
	}
	return err
}

// Analyzer launches the interactive analyze wizard using the real Bubbletea
// driver and returns the collected Config. The caller (runAnalyze) is
// responsible for applying the Config to the package-level flag variables and
// running the analysis pipeline. Returns ErrWizardAborted when the user quits.
func Analyzer() (Config, error) {
	return AnalyzerWithDriver(NewDriver())
}

// AnalyzerWithDriver runs the wizard against an injected Questioner. It is the
// test seam: tests pass a scripted driver to exercise the step sequence
// without a real terminal. Production code calls Analyzer instead.
func AnalyzerWithDriver(driver Questioner) (Config, error) {
	return newModel(driver).run()
}