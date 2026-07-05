// Package impact computes the transitive blast radius of a set of changed
// symbols: given symbols that have been modified, it finds every symbol that
// references them (directly or transitively) and the files containing those
// symbols.
//
// The engine uses BFS with a visited set for cycle detection. It is backed by
// a Fathom Store that provides GetReferences(symbolName) for the reference
// graph.
package impact

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Fathom/internal/db"
)

// AffectedSymbol represents a symbol impacted by a change.
type AffectedSymbol struct {
	// Name of the affected symbol.
	Name string `json:"name"`
	// File path where the affected symbol is defined.
	File string `json:"file"`
	// Depth of impact: 1 = direct caller/referencer, 2+ = transitive.
	Depth int `json:"depth"`
	// Via is the symbol whose reference led to this one (empty for depth 0).
	Via string `json:"via"`
	// DependencyType classifies the type of connection: "direct_call", "interface_call", or "struct_embedding".
	DependencyType string `json:"dependency_type"`
}

// BlastResult is the output of a blast radius calculation.
type BlastResult struct {
	// DirectlyAffected lists symbols that directly reference a changed symbol.
	DirectlyAffected []AffectedSymbol `json:"directly_affected"`
	// TransitivelyAffected lists symbols that reference directly-affected
	// symbols (depth 2+).
	TransitivelyAffected []AffectedSymbol `json:"transitively_affected"`
	// AffectedFiles is the deduplicated, sorted set of files containing
	// affected symbols.
	AffectedFiles []string `json:"affected_files"`
}

// Engine computes blast radius from a reference graph stored in a Fathom DB.
type Engine struct {
	store db.Store
}

// New creates an Engine backed by the given store.
func New(store db.Store) *Engine {
	return &Engine{store: store}
}

// Calculate computes the transitive closure of everything that references the
// given changed symbols. It uses BFS with a visited set for cycle detection.
//
// An empty changedSymbols slice returns an empty BlastResult with no error.
func (e *Engine) Calculate(changedSymbols []string) (BlastResult, error) {
	if len(changedSymbols) == 0 {
		return BlastResult{}, nil
	}

	// Pre-load all interface method names to classify interface_call.
	interfaceMethods := make(map[string]bool)
	allSymbols, err := e.store.ListSymbols("")
	if err == nil {
		for _, sym := range allSymbols {
			if sym.Kind == "interface" {
				words := strings.FieldsFunc(sym.Content, func(r rune) bool {
					return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_')
				})
				for _, w := range words {
					if w != "" {
						interfaceMethods[w] = true
					}
				}
			}
		}
	}

	// Cache containing/caller symbol kinds to classify struct_embedding.
	callerKinds := make(map[string]string)
	getCallerKind := func(file, name string) string {
		if name == "" {
			return ""
		}
		key := file + "#" + name
		if k, ok := callerKinds[key]; ok {
			return k
		}
		sym, err := e.store.GetSymbol(file, name)
		if err != nil {
			callerKinds[key] = ""
			return ""
		}
		k := string(sym.Kind)
		callerKinds[key] = k
		return k
	}

	// visited tracks every symbol we have already enqueued or processed.
	visited := make(map[string]bool, len(changedSymbols))
	for _, s := range changedSymbols {
		visited[s] = true
	}

	// BFS queue: each entry is (symbolName, depth, via).
	type queueEntry struct {
		name  string
		depth int
		via   string
	}
	queue := make([]queueEntry, 0, len(changedSymbols))
	for _, s := range changedSymbols {
		queue = append(queue, queueEntry{name: s, depth: 0, via: ""})
	}

	var direct []AffectedSymbol
	var transitive []AffectedSymbol
	fileSet := make(map[string]bool)

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		refs, err := e.store.GetReferences(current.name)
		if err != nil {
			return BlastResult{}, fmt.Errorf("impact: get references for %q: %w", current.name, err)
		}

		for _, ref := range refs {
			caller := ref.ContainingSymbol
			if caller == "" {
				// Top-level reference (e.g., script scope). Use the source file
				// as the identifier so the file appears in the affected set.
				caller = ref.SourceFile
			}
			if visited[caller] {
				continue // cycle detected or already visited
			}
			visited[caller] = true

			// Resolve dependency type.
			depType := "direct_call"
			if ref.Kind == "call" {
				if interfaceMethods[current.name] {
					depType = "interface_call"
				} else {
					depType = "direct_call"
				}
			} else {
				ck := getCallerKind(ref.SourceFile, ref.ContainingSymbol)
				if ck == "type" || ck == "class" || ref.Kind == "type_use" {
					depType = "struct_embedding"
				}
			}

			affected := AffectedSymbol{
				Name:           caller,
				File:           ref.SourceFile,
				Depth:          current.depth + 1,
				Via:            current.name,
				DependencyType: depType,
			}

			if current.depth == 0 {
				direct = append(direct, affected)
			} else {
				transitive = append(transitive, affected)
			}

			fileSet[ref.SourceFile] = true

			// Enqueue for further BFS.
			queue = append(queue, queueEntry{
				name:  caller,
				depth: current.depth + 1,
				via:   current.name,
			})
		}
	}

	// Build sorted file list.
	files := make([]string, 0, len(fileSet))
	for f := range fileSet {
		files = append(files, f)
	}
	sort.Strings(files)

	return BlastResult{
		DirectlyAffected:     direct,
		TransitivelyAffected: transitive,
		AffectedFiles:        files,
	}, nil
}
