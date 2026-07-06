package report

import (
	_ "embed"
	"html/template"
	"io"
	"sort"
	"strings"
)

//go:embed report.html
var rawTemplate string

// FileTreeNode is a display-only structure built from FileGroups so the
// template can render a nested <details> directory tree without reconstructing
// the tree client-side. Nodes are either directories (Name = dir segment,
// Children non-empty) or file leaves (Name = file name, Severity set).
type FileTreeNode struct {
	Name     string
	FullPath string            // original full path, only set on file leaves
	Severity string            // only set on file leaves
	Children []FileTreeNode    // only set on directories
	Files    []FileTreeNode    // file leaves, sorted by Name
	IsDir    bool
}

// buildFileTree turns a flat slice of FileGroup (each carrying a full file
// path) into a nested directory tree. Directories come first, then files,
// both sorted alphabetically. The root node is a synthetic "" directory that
// the template iterates directly.
//
// Severity is propagated up: a directory's children inherit the most severe
// leaf badge. For now the only severity is WARNING, so propagation is trivial;
// the structure is ready for phase-2 severities (BREAKING, etc.) without a
// rewrite.
func buildFileTree(groups []FileGroup) FileTreeNode {
	root := FileTreeNode{IsDir: true, Name: ""}
	for _, g := range groups {
		segments := splitPath(g.File)
		insertLeaf(&root, segments, g.File, g.Severity)
	}
	sortTree(&root)
	return root
}

// insertLeaf walks the tree along segments, creating directories as needed,
// then appends a file leaf at the final depth. child points into node.Children's
// backing array; the append on line 70 re-slices in place so the pointer stays
// valid across the append.
func insertLeaf(node *FileTreeNode, segments []string, fullPath, severity string) {
	if len(segments) == 1 {
		// File leaf. skip empty-string filenames from splitPath("") edge case.
		if segments[0] == "" {
			return
		}
		node.Files = append(node.Files, FileTreeNode{
			Name:     segments[0],
			FullPath: fullPath,
			Severity: severity,
			IsDir:    false,
		})
		return
	}
	head := segments[0]
	rest := segments[1:]
	// Find or create the child directory.
	var child *FileTreeNode
	for i := range node.Children {
		if node.Children[i].Name == head {
			child = &node.Children[i]
			break
		}
	}
	if child == nil {
		node.Children = append(node.Children, FileTreeNode{Name: head, IsDir: true})
		child = &node.Children[len(node.Children)-1]
	}
	insertLeaf(child, rest, fullPath, severity)
}

func sortTree(node *FileTreeNode) {
	sort.Slice(node.Children, func(i, j int) bool {
		return node.Children[i].Name < node.Children[j].Name
	})
	sort.Slice(node.Files, func(i, j int) bool {
		return node.Files[i].Name < node.Files[j].Name
	})
	for i := range node.Children {
		sortTree(&node.Children[i])
	}
}

// splitPath breaks a file path into its components, splitting on both Unix '/'
// and Windows '\' separators. The template file tree uses this to build nested
// <details> directories without reconstructing the tree client-side.
//
// Empty segments (e.g. leading slash on absolute paths) are preserved as-is
// so the rendered tree mirrors the stored path. An empty path returns a
// single-element slice [""] to keep template `range` safe.
func splitPath(path string) []string {
	if path == "" {
		return []string{""}
	}
	// Normalize backslashes to forward slashes so the tree is stable across
	// platforms (Fathom is multi-language and stores paths from git which is
	// forward-slash based). Splitting keeps empty segments: a leading "/"
	// yields ["", "path"] so the root is visible.
	normalized := strings.ReplaceAll(path, "\\", "/")
	return strings.Split(normalized, "/")
}

func (f Finding) DisplayOldContent() string {
	if f.OldContent == "" {
		return "[ Code Not Available ]"
	}
	return f.OldContent
}

func (f Finding) DisplayNewContent() string {
	if f.NewContent == "" {
		if f.OldContent != "" {
			return "[ Symbol Deleted ]"
		}
		return "[ Code Not Available ]"
	}
	return f.NewContent
}

// DisplaySeverity returns the badge label for a finding. Fathom is
// multi-language via Tree-sitter, so every mismatch type (arity, type,
// override) maps to the single WARNING severity for now.
func (f Finding) DisplaySeverity() string {
	return "WARNING"
}

// DisplayLinesChanged is a template hook that intentionally returns an empty
// string. The actual "X lines changed" count is computed client-side from the
// LCS-rendered diff grid (where added/removed rows are known), so the Go side
// stays free of diff-line accounting. The template renders the empty value; JS
// replaces it with the real count.
func (f Finding) DisplayLinesChanged() string {
	return ""
}

func Render(w io.Writer, payload ReportPayload) error {
	t, err := template.New("report").Funcs(template.FuncMap{
		"splitPath":    splitPath,
		"buildFileTree": buildFileTree,
	}).Parse(rawTemplate)
	if err != nil {
		return err
	}
	return t.Execute(w, payload)
}
