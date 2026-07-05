package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/Fathom/internal/db"
	"github.com/Fathom/internal/deadcode"
	"github.com/Fathom/internal/diff"
	"github.com/Fathom/internal/git"
	"github.com/Fathom/internal/impact"
	"github.com/Fathom/internal/mismatch"
	"github.com/Fathom/internal/parser"
	"github.com/Fathom/internal/report"
	"github.com/Fathom/internal/symbol"
)

var (
	jsonOutput     bool
	baseBranch     string
	failOnMismatch bool
	htmlPath       string
)

// analyzeCmd implements "fathom analyze": compute the blast radius of changes
// in the given files.
var analyzeCmd = &cobra.Command{
	Use:   "analyze [files...]",
	Short: "Analyze blast radius of changed files",
	Long: `fathom analyze computes the impact of changes in the given files or against a base branch.

It looks up which symbols are defined in each file, then calculates the
transitive closure of everything that references those symbols — directly
or indirectly. It also runs signature mismatch detection: call sites whose
argument count or literal types no longer match the changed declaration, and
overriding methods whose signature diverges from the parent. The output is a
human-readable report (or JSON with --json).

By default mismatches are printed as advisory warnings (exit code 0). Pass
--fail-on-mismatch to exit with code 1 when any signature mismatch is found.

Requires a .fathom/index.bolt built with Fathom v3+. Run 'fathom init' first.`,
	Args: cobra.ArbitraryArgs,
	RunE: runAnalyze,
}

func init() {
	analyzeCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output report as JSON")
	analyzeCmd.Flags().StringVar(&baseBranch, "base", "", "Base branch to compare against")
	analyzeCmd.Flags().BoolVar(&failOnMismatch, "fail-on-mismatch", false, "Exit with code 1 when signature mismatches are detected")
	analyzeCmd.Flags().StringVar(&htmlPath, "html", "", "Output report as HTML to the specified file")
	rootCmd.AddCommand(analyzeCmd)
}

func runAnalyze(cmd *cobra.Command, args []string) error {
	if len(args) == 0 && baseBranch == "" {
		return fmt.Errorf("either specify files to analyze or a --base branch")
	}

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("analyze: resolve working directory: %w", err)
	}

	var repo *git.Repository
	if len(args) == 0 {
		repo = git.NewRepository(wd)
		if err := repo.Validate(); err != nil {
			return err
		}
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

	var changedSymbols []string

	// workspaceDefs holds definitions from the working tree for changed
	// symbols. It is non-nil only in --base mode and is passed to the
	// mismatch engine so it compares the NEW (workspace) definitions
	// against the STORED (base branch) references, detecting mismatches
	// introduced by the current changes.
	var workspaceDefs map[string][]symbol.Symbol

	if len(args) > 0 {
		// Resolve changed symbols from input files.
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
	} else {
		// Auto-magic differential analysis

		if _, err := repo.ResolveCommit(baseBranch); err != nil {
			return err
		}

		C, err := repo.MergeBase(baseBranch)
		if err != nil {
			return err
		}

		p := parser.New()
		if err := syncIndex(store, repo, p, C); err != nil {
			return err
		}

		diffs, err := repo.Diff(C)
		if err != nil {
			return err
		}

		nameSet := make(map[string]bool)
		for _, diffItem := range diffs {
			symNames, err := diff.AlignSymbols(diffItem, p, repo, C)
			if err != nil {
				return err
			}
			for _, name := range symNames {
				if !nameSet[name] {
					nameSet[name] = true
					changedSymbols = append(changedSymbols, name)
				}
			}
		}

		// Build workspace definitions for mismatch detection against
		// base-branch references. This enables detecting new mismatches
		// introduced by the workspace changes rather than only pre-existing ones.
		workspaceDefs = make(map[string][]symbol.Symbol)
		for _, diffItem := range diffs {
			if diffItem.Status == git.StatusDeleted {
				continue
			}
			syms, err := p.ParseFile(diffItem.Path)
			if err != nil {
				if strings.Contains(err.Error(), "unsupported") {
					continue
				}
				return fmt.Errorf("analyze: parse workspace file for mismatch: %w", err)
			}
			for _, sym := range syms {
				if sym.Kind == symbol.KindFunction && nameSet[sym.Name] {
					workspaceDefs[sym.Name] = append(workspaceDefs[sym.Name], sym)
				}
			}
		}

		if len(changedSymbols) == 0 {
			payload, _ := report.Compile(store, impact.BlastResult{}, nil, nil, nil)
			if htmlPath != "" {
				f, err := os.Create(htmlPath)
				if err != nil {
					return fmt.Errorf("analyze: create HTML report: %w", err)
				}
				defer f.Close()
				if err := report.Render(f, payload); err != nil {
					return fmt.Errorf("analyze: render HTML report: %w", err)
				}
			}
			if jsonOutput {
				return outputJSON(payload, impact.BlastResult{}, nil, nil, nil, failOnMismatch)
			}
			fmt.Println("No changed symbols found.")
			return nil
		}
	}

	// Calculate blast radius.
	engine := impact.New(store)
	result, err := engine.Calculate(changedSymbols)
	if err != nil {
		return fmt.Errorf("analyze: %w", err)
	}

	// Run signature mismatch detection over the changed symbols. The
	// mismatch engine is a parallel pass to the blast radius: it compares
	// call sites and overrides against the definitions. When workspaceDefs
	// is set (--base mode), the engine uses the NEW (workspace) definitions
	// against the STORED (base branch) references, detecting mismatches
	// introduced by the current changes.
	mmEngine := mismatch.New(store)
	if workspaceDefs != nil {
		mmEngine.SetWorkspaceDefs(workspaceDefs)
	}
	mismatches, err := mmEngine.Detect(changedSymbols)
	if err != nil {
		return fmt.Errorf("analyze: mismatch detection: %w", err)
	}

	// Run deadcode analysis.
	var changedSymObjects []symbol.Symbol
	if workspaceDefs != nil {
		for _, syms := range workspaceDefs {
			changedSymObjects = append(changedSymObjects, syms...)
		}
	} else {
		dbSyms, err := store.ListSymbols("")
		if err == nil {
			changedNamesSet := make(map[string]bool)
			for _, n := range changedSymbols {
				changedNamesSet[n] = true
			}
			for _, s := range dbSyms {
				if changedNamesSet[s.Name] {
					changedSymObjects = append(changedSymObjects, s)
				}
			}
		}
	}

	deadScanner := deadcode.New(store)
	deadSymbols, err := deadScanner.Scan(changedSymObjects)
	if err != nil {
		return fmt.Errorf("analyze: deadcode scan: %w", err)
	}

	// Compile Report Payload.
	payload, err := report.Compile(store, result, mismatches, deadSymbols, workspaceDefs)
	if err != nil {
		return fmt.Errorf("analyze: compile report: %w", err)
	}

	// Render HTML report if requested.
	if htmlPath != "" {
		f, err := os.Create(htmlPath)
		if err != nil {
			return fmt.Errorf("analyze: create HTML report: %w", err)
		}
		defer f.Close()
		if err := report.Render(f, payload); err != nil {
			return fmt.Errorf("analyze: render HTML report: %w", err)
		}
	}

	// Output.
	if jsonOutput {
		return outputJSON(payload, result, changedSymbols, mismatches, deadSymbols, failOnMismatch)
	}
	if err := outputHuman(result, changedSymbols); err != nil {
		return err
	}
	if len(mismatches) > 0 {
		fmt.Print(mismatch.FormatHuman(mismatches))
		if failOnMismatch {
			// Signal non-zero exit by returning an error; cobra translates
			// any non-nil error into exit code 1.
			return fmt.Errorf("analyze: %d signature mismatch(es) detected", len(mismatches))
		}
	}
	return nil
}

func syncIndex(store db.Store, repo *git.Repository, p parser.Parser, targetSHA string) error {
	indexedSHA, err := store.GetMeta("commit_hash")
	if err != nil {
		// If not found, we can't do incremental sync. Let's warn and skip.
		zap.L().Warn("could not retrieve indexed commit hash; skipping incremental sync", zap.Error(err))
		return nil
	}

	if indexedSHA == targetSHA {
		return nil
	}

	zap.L().Info("syncing index", zap.String("from", indexedSHA), zap.String("to", targetSHA))

	// Get repo root
	root, err := repo.Root()
	if err != nil {
		return err
	}

	// Run git diff --name-status <indexedSHA> <targetSHA>
	diffOutput, err := repo.Run("diff", "--name-status", indexedSHA, targetSHA)
	if err != nil {
		// If database is read-only (CI/write-lock), fail-safe by logging a warning and continuing with the stale index.
		zap.L().Warn("failed to run git diff for index sync; continuing with stale index", zap.Error(err))
		return nil
	}

	lines := strings.Split(diffOutput, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		status := parts[0]
		relPath := parts[1]
		absPath := filepath.Join(root, relPath)

		// Delete symbols and references for this file
		if err := store.DeleteSymbolsForFile(absPath); err != nil {
			zap.L().Warn("failed to delete stale symbols/references during sync; continuing", zap.String("path", absPath), zap.Error(err))
			continue
		}

		// If status is Modified or Added, parse the new version at targetSHA
		if status == "M" || status == "A" {
			// Check if file is supported
			if _, ok := p.DetectLanguage(absPath); !ok {
				continue
			}

			// Read file content at targetSHA
			content, err := repo.Show(targetSHA, relPath)
			if err != nil {
				zap.L().Warn("failed to retrieve file content from git during sync; skipping file", zap.String("path", relPath), zap.Error(err))
				continue
			}

			syms, refs, err := p.ParseBytesWithRefs(absPath, content)
			if err != nil {
				zap.L().Warn("failed to parse file content during sync; skipping file", zap.String("path", relPath), zap.Error(err))
				continue
			}

			if len(syms) > 0 {
				if err := store.PutSymbols(syms); err != nil {
					zap.L().Warn("failed to store symbols during sync; continuing", zap.String("path", absPath), zap.Error(err))
				}
			}
			if len(refs) > 0 {
				if err := store.PutReferences(absPath, refs); err != nil {
					zap.L().Warn("failed to store references during sync; continuing", zap.String("path", absPath), zap.Error(err))
				}
			}
		}
	}

	// Update commit_hash to targetSHA
	if err := store.PutMeta("commit_hash", targetSHA); err != nil {
		zap.L().Warn("failed to update indexed commit hash; continuing with stale commit hash metadata", zap.Error(err))
	}

	return nil
}

// outputJSON writes the blast result and any detected mismatches as JSON to
// stdout. When failOnMismatch is set and mismatches exist, it returns an
// error so cobra exits with a non-zero code; the JSON report is still emitted
// first so callers (and CI) see the full picture before the failure.
func outputJSON(payload report.ReportPayload, result impact.BlastResult, changedSymbols []string, mismatches []mismatch.Mismatch, deadSymbols []deadcode.DeadSymbol, failOnMismatch bool) error {
	report := struct {
		Verdict         report.VerdictBlock     `json:"verdict"`
		Findings        []report.Finding        `json:"findings"`
		ChangedSymbols  []string                `json:"changed_symbols"`
		AffectedSymbols []impact.AffectedSymbol `json:"affected_symbols"`
		AffectedFiles   []string                `json:"affected_files"`
		Mismatches      []mismatch.Mismatch     `json:"mismatches"`
		DeadCode        []deadcode.DeadSymbol   `json:"dead_code"`
	}{
		Verdict:         payload.Verdict,
		Findings:        payload.Findings.Findings,
		ChangedSymbols:  changedSymbols,
		AffectedSymbols: append(result.DirectlyAffected, result.TransitivelyAffected...),
		AffectedFiles:   result.AffectedFiles,
		Mismatches:      mismatches,
		DeadCode:        deadSymbols,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return err
	}
	if failOnMismatch && len(mismatches) > 0 {
		return fmt.Errorf("analyze: %d signature mismatch(es) detected", len(mismatches))
	}
	return nil
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
