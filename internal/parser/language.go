// Package parser wraps the tree-sitter language pack and exposes a small,
// testable surface for turning source files into Fathom symbols.
//
// The package is organized around three concerns:
//   - language.go: language detection helpers and the extension ↔ language
//     reference table.
//   - extractor.go: the tree-sitter CST walker that maps grammar node kinds
//     to Fathom symbol kinds.
//   - parser.go: the Parser interface and its tree-sitter implementation that
//     ties detection, parsing, and extraction together.
package parser

import (
	"path/filepath"
	"sort"
	"strings"

	tspack "github.com/xberg-io/tree-sitter-language-pack/packages/go"
)

// extToLang maps common file extensions (without the leading dot) to the
// language name recognized by the tree-sitter language pack. It is a
// reference table for callers that want a static lookup; runtime detection
// delegates to the language pack so aliases and new entries are picked up
// without a code change here.
var extToLang = map[string]string{
	// Go
	"go": "go",
	// JavaScript / TypeScript
	"js":  "javascript",
	"jsx": "javascript",
	"mjs": "javascript",
	"cjs": "javascript",
	"ts":  "typescript",
	"tsx": "tsx",
	"mts": "typescript",
	"cts": "typescript",
	// Python
	"py":  "python",
	"pyw": "python",
	"pyi": "python",
	// Rust
	"rs": "rust",
	// Java
	"java": "java",
	// C / C++
	"c":   "c",
	"h":   "c",
	"cpp": "cpp",
	"cc":  "cpp",
	"cxx": "cpp",
	"hpp": "cpp",
	"hxx": "cpp",
	// Ruby
	"rb": "ruby",
	// PHP
	"php": "php",
}

// CommonLanguageGroups names bundles of languages that are typically fetched
// together. They mirror the group names exposed by the language pack manifest
// and are convenient arguments for DownloadParsers.
const (
	GroupWeb       = "web"
	GroupSystems   = "systems"
	GroupScripting = "scripting"
)

// CommonLanguages lists the languages Fathom explicitly supports in its
// extractor. The set is intentionally small and curated — adding a language
// here implies a kind map entry in extractor.go.
var CommonLanguages = []string{
	"go", "javascript", "typescript", "tsx", "python", "rust",
	"java", "c", "cpp", "ruby", "php",
}

// SupportedExtensions returns the file extensions (without leading dot) that
// the language pack can detect. The list is derived from the pack's
// AvailableLanguages combined with the local extension table so callers get a
// stable, deduplicated, sorted view.
//
// The language pack's AvailableLanguages can return an empty slice when the
// dynamic manifest is unavailable (e.g. offline runs); in that case the local
// reference table is used as the fallback so the function is always useful.
func SupportedExtensions() []string {
	seen := make(map[string]struct{})
	var exts []string

	// Always seed from the local reference table so common languages are
	// represented even when the dynamic manifest is unavailable.
	for ext := range extToLang {
		if _, ok := seen[ext]; !ok {
			seen[ext] = struct{}{}
			exts = append(exts, ext)
		}
	}

	// Best-effort enrichment from the language pack: for each available
	// language, add the known extensions from the reference table. Unknown
	// languages are skipped — we only advertise extensions we can actually
	// map. This keeps the output deterministic across runs.
	for _, lang := range tspack.AvailableLanguages() {
		for ext, l := range extToLang {
			if l == lang {
				if _, ok := seen[ext]; !ok {
					seen[ext] = struct{}{}
					exts = append(exts, ext)
				}
			}
		}
	}

	sort.Strings(exts)
	return exts
}

// extensionOf returns the lowercase file extension (without dot) for path,
// or "" if there is none. It is a small helper kept here so language detection
// logic has a single definition of "extension".
func extensionOf(path string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(ext, "."))
}
