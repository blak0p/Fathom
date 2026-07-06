// Package interactive implements the Bubbletea TUI wizard for `fathom analyze`.
//
// The package exposes a Questioner interface so the analyze wizard can be
// driven either by the real Bubbletea driver (interactive.Analyzer) or by a
// scripted test driver (AnalyzerWithDriver) without a real terminal.
package interactive

import (
	"io"
	"os"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

// Questioner abstracts a prompt so tests can script answers without a real TTY.
// The three methods cover the prompt primitives the analyze wizard needs: a
// single-choice select, a free-form text input, and a yes/no confirm.
type Questioner interface {
	// Select shows title and lets the user pick one of choices. It returns
	// the selected string, or an error if the user aborts.
	Select(title string, choices []string) (string, error)
	// Input shows a free-form text prompt with the given placeholder. It
	// returns the entered string (which may be empty), or an error on abort.
	Input(title, placeholder string) (string, error)
	// Confirm shows a yes/no prompt with a default value. It returns the
	// user's choice, or an error on abort.
	Confirm(title string, defaultValue bool) (bool, error)
}

// realDriver implements Questioner using Bubbletea + Bubbles components. Each
// prompt spins up its own short-lived tea.Program so the wizard stays a plain
// sequential function rather than one large state machine.
type realDriver struct {
	input  io.Reader
	output io.Writer
}

// NewDriver returns a Questioner backed by real Bubbletea programs that read
// from stdin and write to stdout. Tests that want to feed scripted keystrokes
// can construct a driver with custom io.Reader/io.Writer via newDriver.
func NewDriver() Questioner { return newDriver(os.Stdin, os.Stdout) }

func newDriver(in io.Reader, out io.Writer) Questioner {
	return realDriver{input: in, output: out}
}

// Select runs a single-choice list prompt and returns the picked item. The
// user aborts with Ctrl+C / Esc, which is surfaced as tea.ErrProgramKilled.
// Navigation: j/k or ↑/↓ to move, enter to select.
func (d realDriver) Select(title string, choices []string) (string, error) {
	items := make([]list.Item, 0, len(choices))
	for _, c := range choices {
		items = append(items, choiceItem{title: c})
	}

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false

	// Narrow the list height to the choices so the viewport stays tidy.
	m := list.New(items, delegate, 50, len(items)+4)
	m.Title = title
	m.SetFilteringEnabled(false)
	m.SetShowStatusBar(false)
	m.SetShowHelp(false)
	m.SetShowPagination(false)

	p := tea.NewProgram(selectModel{list: m},
		tea.WithInput(d.input),
		tea.WithOutput(d.output),
		tea.WithAltScreen(),
	)
	out, err := p.Run()
	if err != nil {
		return "", err
	}
	res, ok := out.(selectModel)
	if !ok || res.aborted {
		return "", tea.ErrProgramKilled
	}
	if res.selected == "" {
		return "", tea.ErrProgramKilled
	}
	return res.selected, nil
}

// Input runs a free-form text prompt and returns what the user typed. An empty
// submission is a valid (non-abort) return value.
func (d realDriver) Input(title, placeholder string) (string, error) {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.Focus()

	m := inputModel{title: title, input: ti}
	p := tea.NewProgram(m,
		tea.WithInput(d.input),
		tea.WithOutput(d.output),
		tea.WithAltScreen(),
	)
	out, err := p.Run()
	if err != nil {
		return "", err
	}
	res, ok := out.(inputModel)
	if !ok || res.aborted {
		return "", tea.ErrProgramKilled
	}
	return res.input.Value(), nil
}

// Confirm runs a yes/no prompt. The default value is highlighted and used when
// the user presses Enter without typing y/n.
func (d realDriver) Confirm(title string, defaultValue bool) (bool, error) {
	m := confirmModel{title: title, defaultYes: defaultValue, value: defaultValue}
	p := tea.NewProgram(m,
		tea.WithInput(d.input),
		tea.WithOutput(d.output),
		tea.WithAltScreen(),
	)
	out, err := p.Run()
	if err != nil {
		return false, err
	}
	res, ok := out.(confirmModel)
	if !ok || res.aborted {
		return false, tea.ErrProgramKilled
	}
	return res.value, nil
}

// choiceItem is a list.Item that wraps a single option string.
type choiceItem struct{ title string }

func (i choiceItem) Title() string       { return i.title }
func (i choiceItem) Description() string { return "" }
func (i choiceItem) FilterValue() string { return i.title }

// selectModel wraps bubbles/list for a single-choice prompt.
type selectModel struct {
	list     list.Model
	selected string
	aborted  bool
}

func (m selectModel) Init() tea.Cmd { return nil }

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.aborted = true
			return m, tea.Quit
		case "enter":
			it, ok := m.list.SelectedItem().(choiceItem)
			if ok {
				m.selected = it.title
			}
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m selectModel) View() string { return m.list.View() }

// inputModel wraps bubbles/textinput for a free-form prompt.
type inputModel struct {
	title   string
	input   textinput.Model
	aborted bool
}

func (m inputModel) Init() tea.Cmd { return nil }

func (m inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.aborted = true
			return m, tea.Quit
		case "enter":
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m inputModel) View() string {
	return lipgloss.NewStyle().Margin(1, 2).Render(
		m.title + "\n\n" + m.input.View() + "\n\n" +
			"(enter to submit, esc to abort)",
	)
}

// confirmModel is a minimal yes/no prompt rendered with lipgloss.
type confirmModel struct {
	title     string
	defaultYes bool
	value      bool
	aborted    bool
}

func (m confirmModel) Init() tea.Cmd { return nil }

func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "ctrl+c", "esc":
		m.aborted = true
		return m, tea.Quit
	case "y", "Y":
		m.value = true
		return m, tea.Quit
	case "n", "N":
		m.value = false
		return m, tea.Quit
	case "enter":
		m.value = m.defaultYes
		return m, tea.Quit
	}
	return m, nil
}

func (m confirmModel) View() string {
	yes, no := "Yes", "No"
	if m.defaultYes {
		yes = "[Yes]"
	} else {
		no = "[No]"
	}
	return lipgloss.NewStyle().Margin(1, 2).Render(
		m.title + "\n\n  " + yes + "   " + no + "\n\n" +
			"(y/n, enter accepts default, esc to abort)",
	)
}

// IsTTY reports whether stdout is a terminal. It wraps go-isatty so the
// caller doesn't need to depend on the package directly. Used as a safety net
// before launching the wizard so a piped stdout never blocks on input.
func IsTTY() bool { return isatty.IsTerminal(os.Stdout.Fd()) }