package deadcode

import (
	"path/filepath"
	"strings"

	"github.com/Fathom/internal/db"
	"github.com/Fathom/internal/symbol"
)

type Confidence string

const (
	ConfidenceHigh   Confidence = "High"
	ConfidenceMedium Confidence = "Medium"
	ConfidenceLow    Confidence = "Low"
)

type DeadSymbol struct {
	Symbol     symbol.Symbol `json:"symbol"`
	Confidence Confidence    `json:"confidence"`
	Reason     string        `json:"reason"`
}

type Scanner interface {
	Scan(changedSymbols []symbol.Symbol) ([]DeadSymbol, error)
}

type scanner struct {
	store db.Store
}

func New(store db.Store) Scanner {
	return &scanner{store: store}
}

func (s *scanner) Scan(changedSymbols []symbol.Symbol) ([]DeadSymbol, error) {
	var dead []DeadSymbol
	for _, sym := range changedSymbols {
		// Ignore imports/exports/etc if they are not real code declarations we want to audit for dead code.
		if sym.Kind == "import" || sym.Kind == "export" {
			continue
		}

		refs, err := s.store.GetReferences(sym.Name)
		if err != nil {
			return nil, err
		}

		hasRefs := false
		for _, ref := range refs {
			// Exclude self-references / recursion.
			if ref.ContainingSymbol == sym.Name && ref.SourceFile == sym.File {
				continue
			}
			hasRefs = true
			break
		}

		if !hasRefs {
			isPub := s.resolveVisibility(sym)
			var conf Confidence
			var reason string
			if isPub {
				conf = ConfidenceMedium
				reason = "Public symbol with no references found in the workspace"
			} else {
				conf = ConfidenceHigh
				reason = "Private symbol with no references found in the workspace"
			}
			dead = append(dead, DeadSymbol{
				Symbol:     sym,
				Confidence: conf,
				Reason:     reason,
			})
		}
	}
	return dead, nil
}

func (s *scanner) resolveVisibility(sym symbol.Symbol) bool {
	ext := strings.ToLower(filepath.Ext(sym.File))
	switch ext {
	case ".go":
		// Go: Check if first rune of symbol name is uppercase.
		if len(sym.Name) > 0 {
			firstRune := rune(sym.Name[0])
			if firstRune >= 'A' && firstRune <= 'Z' {
				return true
			}
		}
		return false

	case ".js", ".ts", ".jsx", ".tsx":
		// JS/TS: Check if name is default or if we find export.
		if sym.Name == "default" || strings.Contains(sym.Content, "export") {
			return true
		}
		symbols, err := s.store.ListSymbols(sym.File)
		if err == nil {
			for _, s := range symbols {
				if s.Name == sym.Name && s.Kind == "export" {
					return true
				}
			}
		}
		return false

	case ".py":
		// Python: Check if name starts with `_`.
		if strings.HasPrefix(sym.Name, "_") {
			return false
		}
		return true

	case ".rs":
		// Rust: Check for `pub ` prefix in content.
		content := strings.TrimSpace(sym.Content)
		if strings.HasPrefix(content, "pub") || strings.Contains(content, "pub ") {
			return true
		}
		return false

	case ".java":
		// Java: Check for `public ` modifier in content.
		if strings.Contains(sym.Content, "public ") {
			return true
		}
		return false

	case ".c", ".cpp", ".h", ".hpp":
		// C/C++: Check if content does not start with `static `.
		content := strings.TrimSpace(sym.Content)
		if strings.HasPrefix(content, "static ") || strings.HasPrefix(content, "static\t") {
			return false
		}
		return true

	default:
		// PHP/Ruby/others: Public by default unless marked private.
		if strings.Contains(sym.Content, "private") {
			return false
		}
		return true
	}
}
