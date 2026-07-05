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

func Render(w io.Writer, payload ReportPayload) error {
	t, err := template.New("report").Parse(rawTemplate)
	if err != nil {
		return err
	}
	return t.Execute(w, payload)
}
