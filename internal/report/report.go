package report

import (
	"fmt"
	"sort"

	"github.com/Fathom/internal/db"
	"github.com/Fathom/internal/deadcode"
	"github.com/Fathom/internal/impact"
	"github.com/Fathom/internal/mismatch"
	"github.com/Fathom/internal/symbol"
)

type ReportPayload struct {
	Summary     SummaryBlock      `json:"summary"`
	Verdict     VerdictBlock      `json:"verdict"`
	Findings    FindingsBlock     `json:"findings"`
	BlastRadius BlastRadiusBlock  `json:"blast_radius"`
	DeadCode    DeadCodeBlock     `json:"dead_code"`
}

// SummaryBlock carries the executive-summary counts pre-computed by Compile().
// The template iterates these fields directly — no counting logic in the view.
type SummaryBlock struct {
	TotalFindings int `json:"total_findings"`
	WarningCount  int `json:"warning_count"`
	AffectedFiles int `json:"affected_files"`
	DeadCodeCount int `json:"dead_code_count"`
}

type VerdictBlock struct {
	Verdict string `json:"verdict"` // "CLEAN" or "REVIEW"
	Summary string `json:"summary"`
}

type Finding struct {
	SymbolName        string                `json:"symbol_name"`
	File              string                `json:"file"`
	ChangeDescription string                `json:"change_description"`
	OldContent        string                `json:"old_content"`
	NewContent        string                `json:"new_content"`
	Mismatches        []mismatch.Mismatch   `json:"mismatches"`
	AffectedCallers   []impact.AffectedSymbol `json:"affected_callers,omitempty"` // cross-referenced from blast radius where Via == SymbolName
}

// FileGroup clusters findings by file path. Compile() builds a []FileGroup
// sorted by File so the template can iterate groups without any grouping logic.
type FileGroup struct {
	File     string    `json:"file"`
	Findings []Finding `json:"findings"`
	Severity string    `json:"severity"` // "WARNING" when len(Findings) > 0, empty otherwise
}

type FindingsBlock struct {
	Findings   []Finding   `json:"findings"`     // raw slice retained for backward compatibility
	FileGroups []FileGroup `json:"file_groups"`  // pre-grouped, sorted by file path
}

type BlastRadiusBlock struct {
	DirectlyAffected     []impact.AffectedSymbol `json:"directly_affected"`
	TransitivelyAffected []impact.AffectedSymbol `json:"transitively_affected"`
	AffectedFiles        []string                `json:"affected_files"`
}

type DeadCodeBlock struct {
	DeadSymbols []deadcode.DeadSymbol `json:"dead_symbols"`
}

func Compile(
	store db.Store,
	blast impact.BlastResult,
	mismatches []mismatch.Mismatch,
	dead []deadcode.DeadSymbol,
	workspaceDefs map[string][]symbol.Symbol,
) (ReportPayload, error) {
	// 1. Compile Verdict
	verdict := "CLEAN"
	verdictSummary := "No signature mismatches or affected callers detected."
	if len(mismatches) > 0 || len(blast.DirectlyAffected) > 0 {
		verdict = "REVIEW"
		verdictSummary = fmt.Sprintf("Review required: %d signature mismatch(es) and %d directly affected symbol(s) detected.", len(mismatches), len(blast.DirectlyAffected))
	}

	// 2. Compile Findings
	mismatchesBySymbol := make(map[string][]mismatch.Mismatch)
	for _, m := range mismatches {
		mismatchesBySymbol[m.SymbolName] = append(mismatchesBySymbol[m.SymbolName], m)
	}

	// Load all database symbols for looking up old content
	dbSymbols, err := store.ListSymbols("")
	if err != nil {
		dbSymbols = nil
	}
	dbSymbolsByName := make(map[string][]symbol.Symbol)
	for _, s := range dbSymbols {
		dbSymbolsByName[s.Name] = append(dbSymbolsByName[s.Name], s)
	}

	var findings []Finding
	for name, ms := range mismatchesBySymbol {
		var symNew symbol.Symbol
		foundNew := false
		if workspaceDefs != nil {
			if wsList, ok := workspaceDefs[name]; ok && len(wsList) > 0 {
				symNew = wsList[0]
				foundNew = true
			}
		}

		var symOld symbol.Symbol
		foundOld := false
		if oldList, ok := dbSymbolsByName[name]; ok && len(oldList) > 0 {
			symOld = oldList[0]
			foundOld = true
		}

		if workspaceDefs == nil && foundOld {
			symNew = symOld
			foundNew = true
		}

		file := ""
		if foundNew {
			file = symNew.File
		} else if foundOld {
			file = symOld.File
		}

		oldContent := ""
		if foundOld {
			oldContent = symOld.Content
		}

		newContent := ""
		if foundNew {
			newContent = symNew.Content
		}

		hasOverride := false
		for _, m := range ms {
			if m.Type == mismatch.MismatchOverride {
				hasOverride = true
				break
			}
		}
		desc := ""
		if hasOverride {
			desc = "Override signature mismatch: parent method signature differs"
		} else {
			desc = fmt.Sprintf("Signature modified; %d call site mismatch(es) detected", len(ms))
		}

		// Cross-reference affected callers from the blast radius: any symbol
		// whose Via == SymbolName is a caller of this changed symbol. Both
		// DirectlyAffected and TransitivelyAffected are scanned so the finding
		// surfaces every entry whose Via points back to it, regardless of
		// depth bucket. The blast engine sets Via to the symbol that led to
		// the affected entry, so a transitive caller's Via points at an
		// intermediate (not at Foo), keeping the cross-reference precise.
		var callers []impact.AffectedSymbol
		for _, a := range blast.DirectlyAffected {
			if a.Via == name {
				callers = append(callers, a)
			}
		}
		for _, a := range blast.TransitivelyAffected {
			if a.Via == name {
				callers = append(callers, a)
			}
		}

		findings = append(findings, Finding{
			SymbolName:        name,
			File:              file,
			ChangeDescription: desc,
			OldContent:        oldContent,
			NewContent:        newContent,
			Mismatches:        ms,
			AffectedCallers:   callers,
		})
	}

	// Group findings by file, sorted by file path. Files without findings do
	// not appear. Findings within a group preserve the iteration order of
	// mismatchesBySymbol — to keep that order stable across runs we sort each
	// group's findings by SymbolName.
	fileGroupsMap := make(map[string][]Finding)
	for _, f := range findings {
		key := f.File
		fileGroupsMap[key] = append(fileGroupsMap[key], f)
	}
	fileGroups := make([]FileGroup, 0, len(fileGroupsMap))
	for file, groupFindings := range fileGroupsMap {
		sort.Slice(groupFindings, func(i, j int) bool {
			return groupFindings[i].SymbolName < groupFindings[j].SymbolName
		})
		severity := ""
		if len(groupFindings) > 0 {
			severity = "WARNING"
		}
		fileGroups = append(fileGroups, FileGroup{
			File:     file,
			Findings: groupFindings,
			Severity: severity,
		})
	}
	sort.Slice(fileGroups, func(i, j int) bool {
		return fileGroups[i].File < fileGroups[j].File
	})

	// Executive summary counts. Derived from the existing slices — no separate
	// counting passes. WarningCount == TotalFindings because every mismatch
	// type maps to WARNING severity for now (multi-language via Tree-sitter).
	summary := SummaryBlock{
		TotalFindings: len(findings),
		WarningCount:  len(findings),
		AffectedFiles: len(blast.AffectedFiles),
		DeadCodeCount: len(dead),
	}

	return ReportPayload{
		Summary: summary,
		Verdict: VerdictBlock{
			Verdict: verdict,
			Summary: verdictSummary,
		},
		Findings: FindingsBlock{
			Findings:   findings,
			FileGroups: fileGroups,
		},
		BlastRadius: BlastRadiusBlock{
			DirectlyAffected:     blast.DirectlyAffected,
			TransitivelyAffected: blast.TransitivelyAffected,
			AffectedFiles:        blast.AffectedFiles,
		},
		DeadCode: DeadCodeBlock{
			DeadSymbols: dead,
		},
	}, nil
}
