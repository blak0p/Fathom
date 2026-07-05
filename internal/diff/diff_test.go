package diff

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fathom/internal/git"
	"github.com/Fathom/internal/parser"
	"github.com/Fathom/internal/symbol"
)

func TestIntersects(t *testing.T) {
	sym := symbol.Symbol{
		Name:    "foo",
		Line:    10,
		Content: "func foo() {\n\t// line 11\n\t// line 12\n}",
	} // spans lines 10 to 13

	tests := []struct {
		r    git.LineRange
		want bool
	}{
		{git.LineRange{Start: 9, End: 9}, false},
		{git.LineRange{Start: 9, End: 10}, true},
		{git.LineRange{Start: 10, End: 10}, true},
		{git.LineRange{Start: 11, End: 12}, true},
		{git.LineRange{Start: 13, End: 14}, true},
		{git.LineRange{Start: 14, End: 15}, false},
	}

	for _, tt := range tests {
		got := Intersects(sym, tt.r)
		if got != tt.want {
			t.Errorf("Intersects(%+v, %+v) = %v, want %v", sym, tt.r, got, tt.want)
		}
	}
}

func TestAlignSymbolsGo(t *testing.T) {
	// Create a temp Git repo
	dir := t.TempDir()

	runCmd := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v, output: %s", args, err, string(out))
		}
		return strings.TrimSpace(string(out))
	}

	runCmd("init")
	runCmd("config", "user.name", "Test User")
	runCmd("config", "user.email", "test@example.com")
	// Set default branch name to main
	runCmd("symbolic-ref", "HEAD", "refs/heads/main")

	// Write initial file
	contentA := `package main

import "fmt"

func Unmodified() {
	fmt.Println("unmodified")
}

func Modified() {
	fmt.Println("modified original")
}
`
	filePath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(filePath, []byte(contentA), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	runCmd("add", "main.go")
	runCmd("commit", "-m", "Initial commit")

	// Resolve the base commit
	commitC := runCmd("rev-parse", "HEAD")

	// Modify the file
	contentB := `package main

import "fmt"

func Unmodified() {
	fmt.Println("unmodified")
}

func Modified() {
	fmt.Println("modified new content")
}

func Added() {
	fmt.Println("added func")
}
`
	if err := os.WriteFile(filePath, []byte(contentB), 0644); err != nil {
		t.Fatalf("write modified file: %v", err)
	}

	repo := git.NewRepository(dir)
	p := parser.New()

	// Get file diffs
	diffs, err := repo.Diff(commitC)
	if err != nil {
		t.Fatalf("repo.Diff: %v", err)
	}

	if len(diffs) != 1 {
		t.Fatalf("expected 1 file diff, got %d", len(diffs))
	}

	modifiedSymbols, err := AlignSymbols(diffs[0], p, repo, commitC)
	if err != nil {
		t.Fatalf("AlignSymbols: %v", err)
	}

	expected := map[string]bool{
		"Modified": true,
		"Added":    true,
	}

	got := make(map[string]bool)
	for _, name := range modifiedSymbols {
		got[name] = true
	}

	if len(got) != len(expected) {
		t.Errorf("expected modified symbols %v, got %v", expected, got)
	}
	for name := range expected {
		if !got[name] {
			t.Errorf("expected symbol %q to be marked modified", name)
		}
	}
	if got["Unmodified"] {
		t.Errorf("Unmodified symbol was incorrectly marked modified")
	}
}
