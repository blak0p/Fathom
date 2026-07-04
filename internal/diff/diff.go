package diff

import (
	"path/filepath"
	"strings"

	"github.com/Fathom/internal/git"
	"github.com/Fathom/internal/parser"
	"github.com/Fathom/internal/symbol"
)

// Intersects checks if a symbol's line span intersects with a LineRange.
func Intersects(sym symbol.Symbol, r git.LineRange) bool {
	symStart := sym.Line
	symEnd := sym.Line + strings.Count(sym.Content, "\n")

	maxStart := symStart
	if r.Start > maxStart {
		maxStart = r.Start
	}

	minEnd := symEnd
	if r.End < minEnd {
		minEnd = r.End
	}

	return maxStart <= minEnd
}

// AlignSymbols resolves the set of modified symbols from a FileDiff.
func AlignSymbols(
	fileDiff git.FileDiff,
	p parser.Parser,
	repo *git.Repository,
	commitC string,
) ([]string, error) {
	root, err := repo.Root()
	if err != nil {
		return nil, err
	}

	relPath, err := filepath.Rel(root, fileDiff.Path)
	if err != nil {
		return nil, err
	}

	var modifiedNames []string
	nameSet := make(map[string]bool)

	addName := func(name string) {
		if name != "" && !nameSet[name] {
			nameSet[name] = true
			modifiedNames = append(modifiedNames, name)
		}
	}

	switch fileDiff.Status {
	case git.StatusAdded:
		symbols, err := p.ParseFile(fileDiff.Path)
		if err != nil {
			if strings.Contains(err.Error(), "unsupported file extension") {
				return nil, nil
			}
			return nil, err
		}
		for _, sym := range symbols {
			addName(sym.Name)
		}

	case git.StatusDeleted:
		baseBytes, err := repo.Show(commitC, relPath)
		if err != nil {
			// If git show fails (e.g. file was not in C, or binary), just skip/ignore
			return nil, nil
		}
		symbols, _, err := p.ParseBytesWithRefs(relPath, baseBytes)
		if err != nil {
			if strings.Contains(err.Error(), "unsupported file extension") {
				return nil, nil
			}
			return nil, err
		}
		for _, sym := range symbols {
			addName(sym.Name)
		}

	case git.StatusModified:
		// 1. Process old symbols in pre-image (C)
		baseBytes, err := repo.Show(commitC, relPath)
		if err == nil {
			oldSymbols, _, err := p.ParseBytesWithRefs(relPath, baseBytes)
			if err == nil {
				for _, sym := range oldSymbols {
					for _, r := range fileDiff.OldRanges {
						if Intersects(sym, r) {
							addName(sym.Name)
							break
						}
					}
				}
			} else if !strings.Contains(err.Error(), "unsupported file extension") {
				return nil, err
			}
		}

		// 2. Process new symbols in post-image (working tree)
		newSymbols, err := p.ParseFile(fileDiff.Path)
		if err != nil {
			if strings.Contains(err.Error(), "unsupported file extension") {
				return nil, nil
			}
			return nil, err
		}
		for _, sym := range newSymbols {
			for _, r := range fileDiff.NewRanges {
				if Intersects(sym, r) {
					addName(sym.Name)
					break
				}
			}
		}
	}

	return modifiedNames, nil
}
