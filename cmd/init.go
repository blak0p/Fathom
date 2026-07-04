package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/Fathom/internal/db"
	"github.com/Fathom/internal/parser"
)

// maxFileBytes caps the size of a single source file Fathom will parse. Files
// larger than this are skipped during the index walk to keep init bounded on
// huge repositories (vendored dumps, generated bundles, etc.).
const maxFileBytes = 10 * 1024 * 1024 // 10 MB

// initCmd implements "fathom init": build the .fathom/ index for the current
// repository. See runInit for the full flow.
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Build the .fathom/ symbol index for this repository",
	Long: `fathom init walks the working tree, detects the languages present from
file extensions, downloads the tree-sitter parsers it needs into
.fathom/parsers, parses every supported source file, and stores the extracted
symbols in .fathom/index.bolt along with repo metadata.

Re-running init is safe: it rewrites the index from scratch and does not
duplicate symbols.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("init: resolve working directory: %w", err)
		}
		return runInit(wd)
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}

// runInit performs the full init flow against workdir. It is split out of the
// cobra RunE so tests can drive it against a temp directory without changing
// the process working directory.
//
// Flow:
//  1. Ensure .fathom/ exists.
//  2. Walk the tree once to collect unique file extensions.
//  3. Map extensions → languages via the parser.
//  4. Download the tree-sitter parsers for those languages into .fathom/parsers.
//  5. Open the bbolt store at .fathom/index.bolt.
//  6. Best-effort: read the current commit hash from git.
//  7. Walk the tree again, parsing and indexing each supported file atomically.
//  8. Persist metadata (schema version, commit, timestamp, indexed file list).
//  9. Print a one-line summary.
func runInit(workdir string) error {
	start := time.Now()
	logger := zap.L()

	fathomDir := filepath.Join(workdir, ".fathom")
	if err := os.MkdirAll(fathomDir, 0o755); err != nil {
		return fmt.Errorf("init: create %s: %w", fathomDir, err)
	}
	logger.Info("fathom directory ready", zap.String("path", fathomDir))

	// --- Pass 1: collect extensions ---------------------------------------
	exts, err := collectExtensions(workdir)
	if err != nil {
		return fmt.Errorf("init: walk for extensions: %w", err)
	}
	logger.Info("collected extensions", zap.Strings("exts", exts))

	// --- Detect languages --------------------------------------------------
	p := parser.New()
	languages := p.DetectLanguagesFromExtensions(exts)
	logger.Info("detected languages", zap.Strings("languages", languages))

	// --- Download parsers --------------------------------------------------
	parserCache := filepath.Join(fathomDir, "parsers")
	if err := p.DownloadParsers(parserCache, languages); err != nil {
		return fmt.Errorf("init: download parsers: %w", err)
	}
	logger.Info("parsers ready", zap.String("cache", parserCache),
		zap.Int("count", len(languages)))

	// --- Open store --------------------------------------------------------
	store := db.New()
	indexPath := filepath.Join(fathomDir, "index.bolt")
	if err := store.Open(indexPath); err != nil {
		return fmt.Errorf("init: open store: %w", err)
	}
	defer func() { _ = store.Close() }()

	// --- Commit hash (best effort) ----------------------------------------
	commitHash, gitErr := readCommitHash(workdir)
	if gitErr != nil {
		logger.Warn("could not read git commit hash; indexing without one",
			zap.Error(gitErr))
	}

	// --- Pass 2: parse + index ---------------------------------------------
	indexedFiles, symbolCount, refCount, err := indexTree(workdir, p, store)
	if err != nil {
		return err
	}
	logger.Info("index pass complete",
		zap.Int("files", len(indexedFiles)), zap.Int("symbols", symbolCount), zap.Int("references", refCount))

	// --- Metadata ----------------------------------------------------------
	if err := writeMetadata(store, commitHash, indexedFiles); err != nil {
		return fmt.Errorf("init: write metadata: %w", err)
	}

	// --- Summary -----------------------------------------------------------
	duration := time.Since(start)
	fmt.Printf("Detected %d languages. Downloaded %d parsers. Indexed %d files, %d symbols, %d references in %s\n",
		len(languages), len(languages), len(indexedFiles), symbolCount, refCount, duration.Round(time.Millisecond))
	return nil
}

// collectExtensions walks workdir once and returns the sorted, deduplicated
// set of file extensions (lowercase, no leading dot) found under it. .fathom/
// and .git/ are pruned, symlinks are skipped, and files larger than
// maxFileBytes are ignored.
func collectExtensions(workdir string) ([]string, error) {
	seen := make(map[string]struct{})
	err := filepath.WalkDir(workdir, func(path string, d fs.DirEntry, err error) error {
		return walkSkip(path, d, err, func() {
			ext := strings.TrimPrefix(filepath.Ext(path), ".")
			if ext == "" {
				return
			}
			ext = strings.ToLower(ext)
			seen[ext] = struct{}{}
		})
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for ext := range seen {
		out = append(out, ext)
	}
	sort.Strings(out)
	return out, nil
}

// indexTree walks workdir a second time, parses every supported file, and
// writes its symbols and references to store atomically. Parse or store
// failures for one file are logged and skipped so a single bad file cannot
// abort the whole index. Returns the list of indexed file paths, the total
// symbol count, and the total reference count.
func indexTree(workdir string, p parser.Parser, store db.Store) ([]string, int, int, error) {
	var indexedFiles []string
	var symbolCount int
	var refCount int
	logger := zap.L()

	err := filepath.WalkDir(workdir, func(path string, d fs.DirEntry, err error) error {
		return walkSkip(path, d, err, func() {
			// Only attempt files whose language we can detect; ParseFileWithRefs
			// would otherwise return an "unsupported extension" error for
			// every non-source file, drowning the logs.
			if _, ok := p.DetectLanguage(path); !ok {
				return
			}
			symbols, refs, perr := p.ParseFileWithRefs(path)
			if perr != nil {
				logger.Warn("parse failed; skipping file",
					zap.String("path", path), zap.Error(perr))
				return
			}
			if len(symbols) > 0 {
				if serr := store.PutSymbols(symbols); serr != nil {
					logger.Warn("store write symbols failed; skipping file",
						zap.String("path", path), zap.Error(serr))
					return
				}
			}
			if len(refs) > 0 {
				if rerr := store.PutReferences(path, refs); rerr != nil {
					logger.Warn("store write references failed; skipping file",
						zap.String("path", path), zap.Error(rerr))
					return
				}
			}
			indexedFiles = append(indexedFiles, path)
			symbolCount += len(symbols)
			refCount += len(refs)
		})
	})
	if err != nil {
		return nil, 0, 0, fmt.Errorf("init: index walk: %w", err)
	}
	return indexedFiles, symbolCount, refCount, nil
}

// walkSkip is the shared WalkDir callback for both passes. It centralizes the
// skip rules (prune .fathom/.git, skip symlinks, skip oversized files, swallow
// per-entry read errors) and calls emit for every regular source file that
// survives the filters. emit must not return an error.
func walkSkip(path string, d fs.DirEntry, err error, emit func()) error {
	if err != nil {
		// A readable failure on one entry should not abort the whole walk.
		zap.L().Warn("walk entry error; skipping", zap.String("path", path), zap.Error(err))
		return nil
	}
	if d == nil {
		return nil
	}

	if d.IsDir() {
		base := filepath.Base(path)
		if base == ".fathom" || base == ".git" {
			return filepath.SkipDir
		}
		return nil
	}

	// Skip symlinks so we never follow links out of the repo or loop.
	if d.Type()&os.ModeSymlink != 0 {
		return nil
	}

	info, err := d.Info()
	if err != nil {
		zap.L().Warn("stat failed; skipping", zap.String("path", path), zap.Error(err))
		return nil
	}
	if info.Size() > maxFileBytes {
		return nil
	}

	emit()
	return nil
}

// readCommitHash returns the HEAD commit hash of the git repo at workdir, or
// an error if workdir is not inside a git repo / git is unavailable. The call
// is bounded by a 5s timeout so a hung git never blocks init.
func readCommitHash(workdir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", workdir, "rev-parse", "HEAD")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// writeMetadata persists the schema version, current commit hash, indexing
// timestamp, and the JSON-encoded list of indexed file paths. Each value is
// written in its own PutMeta transaction; they are independent metadata items
// rather than a single coherent record, so partial writes still leave useful
// breadcrumbs.
func writeMetadata(store db.Store, commitHash string, indexedFiles []string) error {
	filesJSON, err := json.Marshal(indexedFiles)
	if err != nil {
		return fmt.Errorf("marshal indexed files: %w", err)
	}

	meta := []struct{ key, value string }{
		{"schema_version", db.CurrentSchemaVersion},
		{"commit_hash", commitHash},
		{"indexed_at", time.Now().UTC().Format(time.RFC3339)},
		{"indexed_files", string(filesJSON)},
	}
	for _, m := range meta {
		if err := store.PutMeta(m.key, m.value); err != nil {
			return fmt.Errorf("write meta %q: %w", m.key, err)
		}
	}
	return nil
}