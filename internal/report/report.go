package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Fathom/internal/db"
	"github.com/Fathom/internal/deadcode"
	"github.com/Fathom/internal/impact"
	"github.com/Fathom/internal/mismatch"
	"github.com/Fathom/internal/symbol"
)

type ReportPayload struct {
	Summary         SummaryBlock         `json:"summary"`
	Verdict         VerdictBlock         `json:"verdict"`
	Findings        FindingsBlock        `json:"findings"`
	BlastRadius     BlastRadiusBlock     `json:"blast_radius"`
	DeadCode        DeadCodeBlock        `json:"dead_code"`
	ReviewAssistant ReviewAssistantBlock `json:"review_assistant"`
}

// SummaryBlock carries the executive-summary counts pre-computed by Compile().
// The template iterates these fields directly — no counting logic in the view.
type SummaryBlock struct {
	TotalFindings     int `json:"total_findings"`
	WarningCount      int `json:"warning_count"`
	AffectedFiles     int `json:"affected_files"`
	DeadCodeCount     int `json:"dead_code_count"`
	BreakingCount     int `json:"breaking_count"`
	OverrideCount     int `json:"override_count"`
	InternalCount     int `json:"internal_count"`
	DirectCallers     int `json:"direct_callers"`
	TransitiveCallers int `json:"transitive_callers"`
}

type VerdictBlock struct {
	Verdict string `json:"verdict"` // "CLEAN" or "REVIEW"
	Summary string `json:"summary"`
}

type Finding struct {
	SymbolName        string                  `json:"symbol_name"`
	File              string                  `json:"file"`
	ChangeDescription string                  `json:"change_description"`
	OldContent        string                  `json:"old_content"`
	NewContent        string                  `json:"new_content"`
	Mismatches        []mismatch.Mismatch     `json:"mismatches"`
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
	Findings   []Finding   `json:"findings"`    // raw slice retained for backward compatibility
	FileGroups []FileGroup `json:"file_groups"` // pre-grouped, sorted by file path
}

type BlastRadiusBlock struct {
	DirectlyAffected     []impact.AffectedSymbol `json:"directly_affected"`
	TransitivelyAffected []impact.AffectedSymbol `json:"transitively_affected"`
	AffectedFiles        []string                `json:"affected_files"`
}

type DeadCodeBlock struct {
	DeadSymbols []deadcode.DeadSymbol `json:"dead_symbols"`
}

// ImpactRow is one entry of the reviewer assistant's prioritized impact table.
// It condenses a single finding into the columns a reviewer needs to triage
// the change quickly: which symbol, where it lives, how the change is
// classified, and how wide the blast radius is.
type ImpactRow struct {
	SymbolName         string `json:"symbol_name"`
	File               string `json:"file"`
	ChangeType         string `json:"change_type"` // "breaking" | "override" | "internal"
	CallerCount        int    `json:"caller_count"`
	AffectedFilesCount int    `json:"affected_files_count"`
}

// ReviewerQuestion is a prompt the assistant surfaces to the reviewer for a
// given finding. Category drives the badge color in the rendered report.
type ReviewerQuestion struct {
	Text     string `json:"text"`
	Category string `json:"category"` // "signature" | "override" | "internal" | "spread"
}

// RecommendedAction is a concrete next-step the reviewer should perform.
// Category drives the badge color and groups actions of the same kind.
type RecommendedAction struct {
	Text     string `json:"text"`
	Category string `json:"category"` // "review" | "deadcode" | "override"
}

// ReviewAssistantBlock is the fifth report block: a reviewer-facing summary
// computed by reusing the already-built findings + dead slices, so no extra
// database work is required. The counts feed both the impact table sort and
// the expanded SummaryBlock.
type ReviewAssistantBlock struct {
	ImpactTable   []ImpactRow         `json:"impact_table"`
	Questions     []ReviewerQuestion  `json:"questions"`
	Actions       []RecommendedAction `json:"actions"`
	BreakingCount int                 `json:"breaking_count"`
	OverrideCount int                 `json:"override_count"`
	InternalCount int                 `json:"internal_count"`
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
	// The reviewer assistant is built from the already-cross-referenced
	// findings slice + dead symbols, so it adds no extra DB work; its
	// Breaking/Override/Internal counts feed the expanded summary directly.
	reviewAssistant := buildReviewAssistantBlock(findings, dead)

	summary := SummaryBlock{
		TotalFindings:     len(findings),
		WarningCount:      len(findings),
		AffectedFiles:     len(blast.AffectedFiles),
		DeadCodeCount:     len(dead),
		BreakingCount:     reviewAssistant.BreakingCount,
		OverrideCount:     reviewAssistant.OverrideCount,
		InternalCount:     reviewAssistant.InternalCount,
		DirectCallers:     len(blast.DirectlyAffected),
		TransitiveCallers: len(blast.TransitivelyAffected),
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
		ReviewAssistant: reviewAssistant,
	}, nil
}

// classifyChangeType maps a finding to one of three reviewer-facing change
// types. Override takes precedence because an override mismatch is the most
// specific signal (a parent contract diverged); a finding with no callers in
// the blast radius is "internal" (the change is contained); everything else
// with callers is "breaking".
func classifyChangeType(f Finding) string {
	hasOverride := false
	for _, m := range f.Mismatches {
		if m.Type == mismatch.MismatchOverride {
			hasOverride = true
			break
		}
	}
	if hasOverride {
		return "override"
	}
	if len(f.AffectedCallers) == 0 {
		return "internal"
	}
	return "breaking"
}

// dedupCallerFiles returns the set of distinct file paths among a finding's
// affected callers, preserving first-seen order. The count drives the
// "AffectedFilesCount" column in the impact table and the "spread" question
// threshold.
func dedupCallerFiles(callers []impact.AffectedSymbol) []string {
	seen := map[string]bool{}
	var files []string
	for _, c := range callers {
		if !seen[c.File] {
			seen[c.File] = true
			files = append(files, c.File)
		}
	}
	return files
}

// changeTypeRank assigns a sort rank to each change type so that within equal
// CallerCount the more severe change sorts first (breaking > override >
// internal). Lower rank == higher priority.
func changeTypeRank(t string) int {
	switch t {
	case "breaking":
		return 0
	case "override":
		return 1
	case "internal":
		return 2
	}
	return 3
}

// firstN returns the first n elements of s, or s itself if it is shorter than
// n. Used to cap the file list in the "review calls" action at 5 entries so
// the action stays readable on wide-blast changes.
func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// buildReviewAssistantBlock turns the already-built findings slice and dead
// symbols into the reviewer-facing assistant block: a sorted impact table,
// threshold-gated reviewer questions, and concrete recommended actions. It
// also returns the Breaking/Override/Internal counts so the expanded
// SummaryBlock can reuse them without a second pass.
func buildReviewAssistantBlock(findings []Finding, dead []deadcode.DeadSymbol) ReviewAssistantBlock {
	var rows []ImpactRow
	var questions []ReviewerQuestion
	var actions []RecommendedAction
	var breaking, override, internal int

	for _, f := range findings {
		changeType := classifyChangeType(f)
		files := dedupCallerFiles(f.AffectedCallers)
		row := ImpactRow{
			SymbolName:         f.SymbolName,
			File:               f.File,
			ChangeType:         changeType,
			CallerCount:        len(f.AffectedCallers),
			AffectedFilesCount: len(files),
		}
		rows = append(rows, row)

		switch changeType {
		case "breaking":
			breaking++
		case "override":
			override++
		case "internal":
			internal++
		}

		// Questions — each rule gates on its own threshold so noise stays low.
		if row.CallerCount >= 2 && changeType == "breaking" {
			questions = append(questions, ReviewerQuestion{
				Text:     fmt.Sprintf("`%s` has %d callers and changed its signature — did you verify all callers?", f.SymbolName, row.CallerCount),
				Category: "signature",
			})
		}
		if changeType == "override" {
			questions = append(questions, ReviewerQuestion{
				Text:     fmt.Sprintf("`%s` is an override — does the parent contract still match?", f.SymbolName),
				Category: "override",
			})
		}
		if row.CallerCount == 0 {
			questions = append(questions, ReviewerQuestion{
				Text:     fmt.Sprintf("`%s` has no callers in the blast radius — is it dead code or was the index stale?", f.SymbolName),
				Category: "internal",
			})
		}
		if row.AffectedFilesCount >= 3 {
			questions = append(questions, ReviewerQuestion{
				Text:     fmt.Sprintf("`%s` affects %d files — consider splitting this change", f.SymbolName, row.AffectedFilesCount),
				Category: "spread",
			})
		}

		// Actions — review-calls lists the (capped) caller files; override
		// gets a dedicated contract-check action pointing at the finding file.
		if row.CallerCount >= 1 {
			actions = append(actions, RecommendedAction{
				Text:     fmt.Sprintf("Review calls to `%s` in %d files: %s", f.SymbolName, len(files), strings.Join(firstN(files, 5), ", ")),
				Category: "review",
			})
		}
		if changeType == "override" {
			actions = append(actions, RecommendedAction{
				Text:     fmt.Sprintf("Check override contract for `%s` in %s", f.SymbolName, f.File),
				Category: "override",
			})
		}
	}

	// One verify action per dead symbol — each echoes its own confidence so
	// the reviewer can prioritize High-confidence removals first.
	for _, d := range dead {
		actions = append(actions, RecommendedAction{
			Text:     fmt.Sprintf("Verify `%s` is no longer used (dead code, %s)", d.Symbol.Name, d.Confidence),
			Category: "deadcode",
		})
	}

	// Sort the impact table by CallerCount desc, then by change-type severity
	// (breaking > override > internal) so the highest-impact rows surface first.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CallerCount != rows[j].CallerCount {
			return rows[i].CallerCount > rows[j].CallerCount
		}
		return changeTypeRank(rows[i].ChangeType) < changeTypeRank(rows[j].ChangeType)
	})

	return ReviewAssistantBlock{
		ImpactTable:   rows,
		Questions:     questions,
		Actions:       actions,
		BreakingCount: breaking,
		OverrideCount: override,
		InternalCount: internal,
	}
}
