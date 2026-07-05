package git

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type LineRange struct {
	Start int // 1-based, inclusive
	End   int // 1-based, inclusive
}

type DeltaStatus string

const (
	StatusAdded    DeltaStatus = "A"
	StatusModified DeltaStatus = "M"
	StatusDeleted  DeltaStatus = "D"
)

type FileDiff struct {
	Path      string      // Absolute path to the file
	Status    DeltaStatus
	OldRanges []LineRange // Line ranges changed in base version (pre-image)
	NewRanges []LineRange // Line ranges changed in current version (post-image)
}

type Repository struct {
	WorkDir string
}

// NewRepository returns a new Repository. If workDir is empty, it uses the current directory.
func NewRepository(workDir string) *Repository {
	return &Repository{WorkDir: workDir}
}

// Run runs a git command with a timeout context.
func (r *Repository) Run(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.WorkDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w (output: %q)", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// Validate verifies that the directory is inside a Git work tree.
func (r *Repository) Validate() error {
	out, err := r.Run("rev-parse", "--is-inside-work-tree")
	if err != nil || out != "true" {
		return fmt.Errorf("git repository not found")
	}
	return nil
}

// Root returns the absolute path of the repository root.
func (r *Repository) Root() (string, error) {
	return r.Run("rev-parse", "--show-toplevel")
}

// ResolveCommit resolves a branch or ref to a 40-character SHA.
func (r *Repository) ResolveCommit(ref string) (string, error) {
	sha, err := r.Run("rev-parse", ref)
	if err != nil {
		return "", fmt.Errorf("base branch %s not found", ref)
	}
	return sha, nil
}

// MergeBase resolves the common ancestor commit between base and HEAD.
func (r *Repository) MergeBase(base string) (string, error) {
	return r.Run("merge-base", base, "HEAD")
}

// Show returns the contents of a file at a specific commit.
func (r *Repository) Show(commit, relPath string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use standard command to stream bytes (avoiding CombinedOutput, which might mix stdout/stderr
	// or return string, and we need raw bytes).
	cmd := exec.CommandContext(ctx, "git", "show", fmt.Sprintf("%s:%s", commit, relPath))
	cmd.Dir = r.WorkDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git show %s:%s failed: %w", commit, relPath, err)
	}
	return out, nil
}

// Diff executes git diff -U0 against base and returns the parsed FileDiffs with absolute paths.
func (r *Repository) Diff(base string) ([]FileDiff, error) {
	root, err := r.Root()
	if err != nil {
		return nil, err
	}

	diffOutput, err := r.Run("diff", "--no-ext-diff", "-U0", base)
	if err != nil {
		return nil, err
	}

	fileDiffs, err := ParseDiff(diffOutput)
	if err != nil {
		return nil, err
	}

	// Translate paths to absolute paths
	for i := range fileDiffs {
		fileDiffs[i].Path = filepath.Join(root, fileDiffs[i].Path)
	}

	return fileDiffs, nil
}

// ParseDiff parses the output of git diff -U0.
func ParseDiff(diffOutput string) ([]FileDiff, error) {
	var fileDiffs []FileDiff
	var currentDiff *FileDiff

	lines := strings.Split(diffOutput, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "diff --git ") {
			if currentDiff != nil {
				fileDiffs = append(fileDiffs, *currentDiff)
			}
			currentDiff = &FileDiff{
				Status: StatusModified, // default
			}
			// Parse standard b/ path as fallback/default
			rest := line[11:]
			aIdx := strings.Index(rest, " a/")
			bIdx := strings.Index(rest, " b/")
			if aIdx >= 0 && bIdx > aIdx {
				currentDiff.Path = rest[aIdx+3 : bIdx]
			} else {
				parts := strings.Split(rest, " ")
				if len(parts) >= 2 {
					p := parts[len(parts)-1]
					if strings.HasPrefix(p, "b/") {
						currentDiff.Path = p[2:]
					} else {
						currentDiff.Path = p
					}
				}
			}
			currentDiff.Path = strings.Trim(currentDiff.Path, "\"")
			continue
		}

		if currentDiff == nil {
			continue
		}

		if strings.HasPrefix(line, "new file mode ") {
			currentDiff.Status = StatusAdded
		} else if strings.HasPrefix(line, "deleted file mode ") {
			currentDiff.Status = StatusDeleted
		} else if strings.HasPrefix(line, "--- /dev/null") {
			currentDiff.Status = StatusAdded
		} else if strings.HasPrefix(line, "+++ /dev/null") {
			currentDiff.Status = StatusDeleted
		} else if strings.HasPrefix(line, "--- a/") {
			p := strings.Trim(line[6:], "\"")
			if currentDiff.Path == "" {
				currentDiff.Path = p
			}
		} else if strings.HasPrefix(line, "+++ b/") {
			p := strings.Trim(line[6:], "\"")
			currentDiff.Path = p
		} else if strings.HasPrefix(line, "@@ ") {
			oldStart, oldLen, newStart, newLen, err := parseChunkHeader(line)
			if err != nil {
				return nil, err
			}
			if oldLen > 0 {
				currentDiff.OldRanges = append(currentDiff.OldRanges, LineRange{
					Start: oldStart,
					End:   oldStart + oldLen - 1,
				})
			}
			if newLen > 0 {
				currentDiff.NewRanges = append(currentDiff.NewRanges, LineRange{
					Start: newStart,
					End:   newStart + newLen - 1,
				})
			}
		}
	}

	if currentDiff != nil {
		fileDiffs = append(fileDiffs, *currentDiff)
	}

	return fileDiffs, nil
}

// parseChunkHeader parses a unified diff chunk header like @@ -10,3 +12,4 @@
func parseChunkHeader(header string) (oldStart, oldLen, newStart, newLen int, err error) {
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(header, "@@ ") {
		return 0, 0, 0, 0, fmt.Errorf("invalid chunk header: %q", header)
	}
	idxEnd := strings.Index(header, " @@")
	if idxEnd < 0 {
		idxEnd = len(header)
	}
	partsStr := header[3:idxEnd] // e.g. "-10,3 +12,4" or "-10 +12"
	parts := strings.Split(partsStr, " ")
	if len(parts) != 2 {
		return 0, 0, 0, 0, fmt.Errorf("invalid chunk header: %q", header)
	}
	oldPart := parts[0]
	newPart := parts[1]

	if !strings.HasPrefix(oldPart, "-") || !strings.HasPrefix(newPart, "+") {
		return 0, 0, 0, 0, fmt.Errorf("invalid chunk header: %q", header)
	}

	parsePart := func(p string) (start, length int, err error) {
		p = p[1:] // strip - or +
		if idx := strings.IndexByte(p, ','); idx >= 0 {
			start, err = strconv.Atoi(p[:idx])
			if err != nil {
				return 0, 0, err
			}
			length, err = strconv.Atoi(p[idx+1:])
			if err != nil {
				return 0, 0, err
			}
		} else {
			start, err = strconv.Atoi(p)
			if err != nil {
				return 0, 0, err
			}
			length = 1
		}
		return start, length, nil
	}

	oldStart, oldLen, err = parsePart(oldPart)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	newStart, newLen, err = parsePart(newPart)
	if err != nil {
		return 0, 0, 0, 0, err
	}

	return oldStart, oldLen, newStart, newLen, nil
}
