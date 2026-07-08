package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// reportCmd implements "fathom report": a convenience alias that runs
// "fathom analyze --html" with a default output path when none is provided.
//
// It shares the same flags and logic as analyze, but defaults htmlPath to a
// temp file and opens the browser automatically — so a reviewer can run
// `fathom report` without remembering the --html flag.
var reportCmd = &cobra.Command{
	Use:   "report [files...]",
	Short: "Generate an HTML impact report (alias for analyze --html)",
	Long: `fathom report generates an HTML impact report for the given files or base
branch. It is a convenience alias for "fathom analyze --html <path>".

When no output path is provided via --output, the report is written to a
temporary file and opened in the default browser.

Requires a .fathom/index.bolt built with Fathom v3+. Run 'fathom init' first.`,
	Args: cobra.ArbitraryArgs,
	RunE: runReport,
}

var reportOutput string

func init() {
	reportCmd.Flags().StringVar(&reportOutput, "output", "", "Output HTML file path (default: temp file + open browser)")
	reportCmd.Flags().StringVar(&baseBranch, "base", "", "Base branch to compare against")
	reportCmd.Flags().BoolVar(&failOnMismatch, "fail-on-mismatch", false, "Exit with code 1 when signature mismatches are detected")
	rootCmd.AddCommand(reportCmd)
}

func runReport(cmd *cobra.Command, args []string) error {
	// Delegate to the analyze command by setting htmlPath and invoking its
	// RunE. This keeps a single source of truth for the analysis logic.
	if reportOutput != "" {
		htmlPath = reportOutput
	} else {
		htmlPath = filepath.Join(os.TempDir(), "fathom-report.html")
	}

	if err := runAnalyze(analyzeCmd, args); err != nil {
		return fmt.Errorf("report: %w", err)
	}
	return nil
}