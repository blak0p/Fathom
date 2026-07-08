package parser

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	tspack "github.com/xberg-io/tree-sitter-language-pack/packages/go"
	"go.uber.org/zap"

	"github.com/blak0p/Fathom/internal/refs"
	"github.com/blak0p/Fathom/internal/symbol"
)

// Parser is the abstraction Fathom uses to turn files on disk into symbols.
// The interface is small on purpose: callers detect languages, download the
// parsers they need, and parse individual files. Concrete implementations
// live in this package so callers can substitute fakes in tests.
type Parser interface {
	// DownloadParsers fetches the named language parsers into cacheDir.
	// Languages already cached are skipped by the language pack.
	DownloadParsers(cacheDir string, languages []string) error

	// ParseFile reads path, detects its language, parses it, and returns
	// the extracted symbols with their File field set to path.
	ParseFile(path string) ([]symbol.Symbol, error)

	// DetectLanguage returns the language name for path and ok=true, or
	// ok=false when the path's extension is not recognized.
	DetectLanguage(path string) (string, bool)

	// DetectLanguagesFromExtensions maps a list of extensions (without
	// leading dot) to the unique set of language names they resolve to.
	// Unknown extensions are skipped.
	DetectLanguagesFromExtensions(exts []string) []string

	// ParseFileWithRefs parses path, detects its language, and returns both
	// symbols and references. The file is parsed once for symbols (via
	// tree-sitter language pack) and references are extracted via the refs
	// package's tags.scm engine.
	ParseFileWithRefs(path string) ([]symbol.Symbol, []refs.Reference, error)

	// ParseBytesWithRefs parses raw bytes representing the file content directly
	// in-memory, without requiring the file to exist on disk.
	ParseBytesWithRefs(path string, source []byte) ([]symbol.Symbol, []refs.Reference, error)
}

// treeSitterParser is the production Parser backed by the tree-sitter
// language pack. It holds no mutable state and is safe for concurrent use
// after DownloadParsers has run once.
type treeSitterParser struct{}

// New returns a Parser backed by the tree-sitter language pack.
func New() Parser { return &treeSitterParser{} }

// DownloadParsers configures the language pack cache directory and downloads
// the requested languages. A nil/empty languages slice is a no-op success so
// callers can pass through optional configuration without branching.
func (treeSitterParser) DownloadParsers(cacheDir string, languages []string) error {
	cfg := tspack.PackConfig{}
	if cacheDir != "" {
		cfg.CacheDir = &cacheDir
	}
	if err := tspack.Configure(cfg); err != nil {
		return fmt.Errorf("parser: configure language pack: %w", err)
	}
	if len(languages) == 0 {
		return nil
	}
	if _, err := tspack.Download(languages); err != nil {
		return fmt.Errorf("parser: download languages %v: %w", languages, err)
	}
	return nil
}

// ParseFile reads path, detects its language, parses it, and returns the
// extracted symbols. Files whose extension is unknown produce an error so
// callers can skip them explicitly rather than silently treating them as
// empty.
func (p treeSitterParser) ParseFile(path string) ([]symbol.Symbol, error) {
	lang, ok := p.DetectLanguage(path)
	if !ok {
		return nil, fmt.Errorf("parser: unsupported file extension: %s", filepath.Ext(path))
	}

	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("parser: read %s: %w", path, err)
	}

	symbols, err := ExtractSymbols(source, lang)
	if err != nil {
		return nil, fmt.Errorf("parser: extract %s: %w", path, err)
	}

	// Attach the originating file path to every symbol. ExtractSymbols
	// deliberately leaves File blank so it can be unit-tested without I/O.
	abs, _ := filepath.Abs(path)
	for i := range symbols {
		symbols[i].File = abs
	}
	return symbols, nil
}

// ParseFileWithRefs parses path, detects its language, and returns both
// symbols and references. Symbols are extracted via the language pack's
// Process() API (see ExtractSymbols); references are extracted via the refs
// package's tags.scm engine over a separate tree-sitter parse. Reference
// extraction failure is non-fatal: symbols are still returned with a warning
// log.
func (p treeSitterParser) ParseFileWithRefs(path string) ([]symbol.Symbol, []refs.Reference, error) {
	lang, ok := p.DetectLanguage(path)
	if !ok {
		return nil, nil, fmt.Errorf("parser: unsupported file extension: %s", filepath.Ext(path))
	}

	source, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("parser: read %s: %w", path, err)
	}

	// Symbols: language pack Process() (Structure + Symbols + Imports + Exports
	// merged + second parse for function params).
	symbols, err := ExtractSymbols(source, lang)
	if err != nil {
		return nil, nil, fmt.Errorf("parser: extract %s: %w", path, err)
	}

	// References: separate tree-sitter parse for the tags.scm query engine.
	var references []refs.Reference
	if refExtractor, hasRefs := refs.Get(lang); hasRefs {
		tsp, err := tspack.GetParser(lang)
		if err == nil {
			defer tsp.Free()
			if tree := tsp.ParseBytes(source); tree != nil {
				defer tree.Free()
				if root := tree.RootNode(); root != nil {
					references, err = refExtractor.ExtractReferences(root, source)
					if err != nil {
						zap.L().Warn("reference extraction failed; indexing symbols only",
							zap.String("path", path), zap.String("lang", lang), zap.Error(err))
					}
				}
			}
		} else {
			zap.L().Warn("reference parser unavailable; indexing symbols only",
				zap.String("path", path), zap.String("lang", lang), zap.Error(err))
		}
	}

	// Attach the originating file path.
	abs, _ := filepath.Abs(path)
	for i := range symbols {
		symbols[i].File = abs
	}
	for i := range references {
		references[i].SourceFile = abs
	}
	return symbols, references, nil
}

// ParseBytesWithRefs parses raw bytes representing the file content directly
// in-memory, without requiring the file to exist on disk.
func (p treeSitterParser) ParseBytesWithRefs(path string, source []byte) ([]symbol.Symbol, []refs.Reference, error) {
	lang, ok := p.DetectLanguage(path)
	if !ok {
		return nil, nil, fmt.Errorf("parser: unsupported file extension: %s", filepath.Ext(path))
	}

	// Symbols: language pack Process() (Structure + Symbols + Imports + Exports
	// merged + second parse for function params).
	symbols, err := ExtractSymbols(source, lang)
	if err != nil {
		return nil, nil, fmt.Errorf("parser: extract %s: %w", path, err)
	}

	// References: separate tree-sitter parse for the tags.scm query engine.
	var references []refs.Reference
	if refExtractor, hasRefs := refs.Get(lang); hasRefs {
		tsp, err := tspack.GetParser(lang)
		if err == nil {
			defer tsp.Free()
			if tree := tsp.ParseBytes(source); tree != nil {
				defer tree.Free()
				if root := tree.RootNode(); root != nil {
					references, err = refExtractor.ExtractReferences(root, source)
					if err != nil {
						zap.L().Warn("reference extraction failed; indexing symbols only",
							zap.String("path", path), zap.String("lang", lang), zap.Error(err))
					}
				}
			}
		} else {
			zap.L().Warn("reference parser unavailable; indexing symbols only",
				zap.String("path", path), zap.String("lang", lang), zap.Error(err))
		}
	}

	// Attach the originating file path.
	abs, _ := filepath.Abs(path)
	for i := range symbols {
		symbols[i].File = abs
	}
	for i := range references {
		references[i].SourceFile = abs
	}
	return symbols, references, nil
}

// DetectLanguage wraps the language pack's DetectLanguageFromPath. It returns
// the detected language name and true, or "" and false for unrecognized paths.
func (treeSitterParser) DetectLanguage(path string) (string, bool) {
	if name := tspack.DetectLanguageFromPath(path); name != nil {
		return *name, true
	}
	return "", false
}

// DetectLanguagesFromExtensions maps each extension (without leading dot) to
// its language name via DetectLanguageFromExtension and returns the unique,
// sorted set. Unknown extensions are skipped.
func (treeSitterParser) DetectLanguagesFromExtensions(exts []string) []string {
	seen := make(map[string]struct{})
	var langs []string
	for _, ext := range exts {
		if name := tspack.DetectLanguageFromExtension(ext); name != nil {
			if _, ok := seen[*name]; !ok {
				seen[*name] = struct{}{}
				langs = append(langs, *name)
			}
		}
	}
	sort.Strings(langs)
	return langs
}

// ErrNoParser is returned when a language is known but no parser could be
// loaded (e.g. the dynamic parser is not cached and download is disabled).
var ErrNoParser = errors.New("parser: language parser not available")
