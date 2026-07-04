// Package cmd implements the Fathom command-line interface.
//
// Fathom is a repository impact analysis tool for Pull Requests. It builds a
// local symbol index from the working tree and uses it to answer "what does
// this PR actually touch?" questions for reviewers.
//
// The root command is exposed via Execute; subcommands (init, future analyze,
// etc.) register themselves in their own source files via init().
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// rootCmd is the Fathom CLI entry point. Subcommands attach themselves to it.
var rootCmd = &cobra.Command{
	Use:   "fathom",
	Short: "Fathom — repository impact analysis for Pull Requests",
	Long: `Fathom builds a local, tree-sitter-backed symbol index of a repository
and uses it to analyze the real impact of a Pull Request across the whole
codebase, not just the files in the diff.

Run "fathom init" inside a repository to create the .fathom/ index, then use
the analysis commands to ask what a given change actually touches.`,
	SilenceUsage: true,
}

// Execute runs the root command. It is the single entry point used by main.go.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// cobra already prints the error; exit non-zero without duplicating it.
		os.Exit(1)
	}
}