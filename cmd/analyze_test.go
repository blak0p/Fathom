package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestAnalyzeValidation(t *testing.T) {
	// Reset global flags to prevent test pollution
	baseBranch = ""
	jsonOutput = false
	t.Cleanup(func() {
		baseBranch = ""
		jsonOutput = false
	})

	cmd := &cobra.Command{}
	err := runAnalyze(cmd, []string{})
	if err == nil {
		t.Fatal("expected error with zero args and no --base, got nil")
	}
	if !strings.Contains(err.Error(), "either specify files to analyze or a --base branch") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestAnalyzeGitValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping git-backed integration test in -short mode")
	}

	baseBranch = "main"
	jsonOutput = false
	t.Cleanup(func() {
		baseBranch = ""
		jsonOutput = false
	})

	dir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	// No git repo initialized
	cmd := &cobra.Command{}
	err = runAnalyze(cmd, []string{})
	if err == nil {
		t.Fatal("expected error for missing git repository, got nil")
	}
	if !strings.Contains(err.Error(), "git repository not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestAnalyzeNonexistentBranch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping git-backed integration test in -short mode")
	}

	baseBranch = "nonexistent"
	jsonOutput = false
	t.Cleanup(func() {
		baseBranch = ""
		jsonOutput = false
	})

	dir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	// Initialize git repo but with no branch named nonexistent
	gitInit(t, dir)

	cmd := &cobra.Command{}
	err = runAnalyze(cmd, []string{})
	if err == nil {
		t.Fatal("expected error for nonexistent base branch, got nil")
	}
	if !strings.Contains(err.Error(), "base branch nonexistent not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestAnalyzeBaseSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping git-backed integration test in -short mode")
	}

	baseBranch = "main"
	jsonOutput = false
	t.Cleanup(func() {
		baseBranch = ""
		jsonOutput = false
	})

	dir := t.TempDir()

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
	writeFixture(t, dir, "main.go", contentA)

	// Initialize git and commit main.go
	gitInit(t, dir)

	// Run fathom init to build index database for the first time
	if err := runInit(dir); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	// Change directory to the temp repo
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	// Modify main.go (this is the workspace changes)
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
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(contentB), 0644); err != nil {
		t.Fatalf("write modified main.go: %v", err)
	}

	cmd := &cobra.Command{}
	err = runAnalyze(cmd, []string{})
	if err != nil {
		t.Fatalf("runAnalyze failed: %v", err)
	}
}
