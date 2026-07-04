package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Fathom/internal/db"
	"github.com/Fathom/internal/impact"
)

var jsonOutput bool

// analyzeCmd implements "fathom analyze": compute the blast radius of changes
// in the given files.
var analyzeCmd = &cobra.Command{
	Use:   "analyze <files...>",
	Short: "Analyze blast radius of changed files",
	Long: `fathom analyze computes the impact of changes in the given files.

It looks up which symbols are defined in each file, then calculates the
transitive closure of everything that references those symbols — directly
or indirectly. The output is a human-readable report (or JSON with --json).

Requires a .fathom/index.bolt built with Fathom v2+. Run 'fathom init' first.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runAnalyze,
}

func init() {
	analyzeCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output report as JSON")
	rootCmd.AddCommand(analyzeCmd)
}

func runAnalyze(cmd *cobra.Command, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("analyze: resolve working directory: %w", err)
	}

	indexPath := filepath.Join(wd, ".fathom", "index.bolt")
	store := db.New()
	if err := store.Open(indexPath); err != nil {
		return fmt.Errorf("analyze: open index: %w", err)
	}
	defer func() { _ = store.Close() }()

	// Schema guard: v1 databases cannot be analyzed.
	if err := store.CheckSchemaVersion(); err != nil {
		return err
	}

	// Resolve changed symbols from input files.
	var changedSymbols []string
	for _, file := range args {
		abs, _ := filepath.Abs(file)
		syms, err := store.ListSymbols(abs)
		if err != nil {
			return fmt.Errorf("analyze: list symbols for %s: %w", file, err)
		}
		if len(syms) == 0 {
			fmt.Fprintf(os.Stderr, "Warning: %s not found in index\n", file)
			continue
		}
		for _, s := range syms {
			changedSymbols = append(changedSymbols, s.Name)
		}
	}

	if len(changedSymbols) == 0 {
		return fmt.Errorf("analyze: no symbols found in any of the specified files")
	}

	// Calculate blast radius.
	engine := impact.New(store)
	result, err := engine.Calculate(changedSymbols)
	if err != nil {
		return fmt.Errorf("analyze: %w", err)
	}

	// Output.
	if jsonOutput {
		return outputJSON(result, changedSymbols)
	}
	return outputHuman(result, changedSymbols)
}

// outputJSON writes the blast result as JSON to stdout.
func outputJSON(result impact.BlastResult, changedSymbols []string) error {
	report := struct {
		ChangedSymbols  []string              `json:"changed_symbols"`
		AffectedSymbols []impact.AffectedSymbol `json:"affected_symbols"`
		AffectedFiles   []string              `json:"affected_files"`
	}{
		ChangedSymbols:  changedSymbols,
		AffectedSymbols: append(result.DirectlyAffected, result.TransitivelyAffected...),
		AffectedFiles:   result.AffectedFiles,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// outputHuman writes a human-readable blast radius report to stdout.
func outputHuman(result impact.BlastResult, changedSymbols []string) error {
	fmt.Printf("Changed symbols (%d):\n", len(changedSymbols))
	for _, s := range changedSymbols {
		fmt.Printf("  %s\n", s)
	}
	fmt.Println()

	if len(result.DirectlyAffected) > 0 {
		fmt.Printf("Directly affected (%d):\n", len(result.DirectlyAffected))
		for _, a := range result.DirectlyAffected {
			fmt.Printf("  %s (%s) — references %s\n", a.Name, a.File, a.Via)
		}
		fmt.Println()
	}

	if len(result.TransitivelyAffected) > 0 {
		fmt.Printf("Transitively affected (%d):\n", len(result.TransitivelyAffected))
		for _, a := range result.TransitivelyAffected {
			fmt.Printf("  %s (%s) — references %s (depth %d)\n", a.Name, a.File, a.Via, a.Depth)
		}
		fmt.Println()
	}

	if len(result.AffectedFiles) > 0 {
		fmt.Printf("Affected files (%d):\n", len(result.AffectedFiles))
		for _, f := range result.AffectedFiles {
			fmt.Printf("  %s\n", f)
		}
		fmt.Println()
	}

	return nil
}
