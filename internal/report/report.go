package report

import (
	"fmt"

	"github.com/Fathom/internal/db"
	"github.com/Fathom/internal/deadcode"
	"github.com/Fathom/internal/impact"
	"github.com/Fathom/internal/mismatch"
	"github.com/Fathom/internal/symbol"
)

type ReportPayload struct {
	Verdict     VerdictBlock     `json:"verdict"`
	Findings    FindingsBlock    `json:"findings"`
	BlastRadius BlastRadiusBlock `json:"blast_radius"`
	DeadCode    DeadCodeBlock    `json:"dead_code"`
}

type VerdictBlock struct {
	Verdict string `json:"verdict"` // "CLEAN" or "REVIEW"
	Summary string `json:"summary"`
}

type Finding struct {
	SymbolName        string              `json:"symbol_name"`
	File              string              `json:"file"`
	ChangeDescription string              `json:"change_description"`
	OldContent        string              `json:"old_content"`
	NewContent        string              `json:"new_content"`
	Mismatches        []mismatch.Mismatch `json:"mismatches"`
}

type FindingsBlock struct {
	Findings []Finding `json:"findings"`
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
	summary := "No signature mismatches or affected callers detected."
	if len(mismatches) > 0 || len(blast.DirectlyAffected) > 0 {
		verdict = "REVIEW"
		summary = fmt.Sprintf("Review required: %d signature mismatch(es) and %d directly affected symbol(s) detected.", len(mismatches), len(blast.DirectlyAffected))
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

		findings = append(findings, Finding{
			SymbolName:        name,
			File:              file,
			ChangeDescription: desc,
			OldContent:        oldContent,
			NewContent:        newContent,
			Mismatches:        ms,
		})
	}

	return ReportPayload{
		Verdict: VerdictBlock{
			Verdict: verdict,
			Summary: summary,
		},
		Findings: FindingsBlock{
			Findings: findings,
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
