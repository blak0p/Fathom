package report

import (
	_ "embed"
	"html/template"
	"io"
)

//go:embed report.html
var rawTemplate string

func (f Finding) DisplayOldContent() string {
	if f.OldContent == "" {
		return "[ Code Not Available ]"
	}
	return f.OldContent
}

func (f Finding) DisplayNewContent() string {
	if f.NewContent == "" {
		if f.OldContent != "" {
			return "[ Symbol Deleted ]"
		}
		return "[ Code Not Available ]"
	}
	return f.NewContent
}

// DisplaySeverity returns the badge label for a finding. Fathom is
// multi-language via Tree-sitter, so every mismatch type (arity, type,
// override) maps to the single WARNING severity for now.
func (f Finding) DisplaySeverity() string {
	return "WARNING"
}

// DisplayLinesChanged is a template hook that intentionally returns an empty
// string. The actual "X lines changed" count is computed client-side from the
// LCS-rendered diff grid (where added/removed rows are known), so the Go side
// stays free of diff-line accounting. The template renders the empty value; JS
// replaces it with the real count.
func (f Finding) DisplayLinesChanged() string {
	return ""
}

func Render(w io.Writer, payload ReportPayload) error {
	t, err := template.New("report").Parse(rawTemplate)
	if err != nil {
		return err
	}
	return t.Execute(w, payload)
}
