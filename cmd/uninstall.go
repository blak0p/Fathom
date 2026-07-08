package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// uninstallCmd implements "fathom uninstall": removes the fathom binary and
// cleans up PATH entries it added.
var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the fathom binary and clean up PATH",
	Long: `fathom uninstall removes the fathom binary from disk and strips the PATH
entries it added to shell RC files (.bashrc, .zshrc, .profile, config.fish).

Use --force to skip the confirmation prompt.`,
	Args: cobra.NoArgs,
	RunE: runUninstall,
}

var uninstallForce bool

func init() {
	uninstallCmd.Flags().BoolVar(&uninstallForce, "force", false, "Skip confirmation prompt")
	rootCmd.AddCommand(uninstallCmd)
}

func runUninstall(cmd *cobra.Command, args []string) error {
	if !uninstallForce {
		fmt.Println("This will remove the fathom binary and clean up PATH entries.")
		fmt.Print("Proceed? [y/N] ")
		var resp string
		fmt.Scanln(&resp)
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(resp)), "y") {
			fmt.Println("Uninstall cancelled.")
			return nil
		}
	}

	fmt.Println("Uninstalling fathom...")

	// 1. Remove the binary.
	binPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ Could not locate binary: %v\n", err)
	} else {
		if err := os.Remove(binPath); err != nil {
			if os.IsNotExist(err) {
				fmt.Println("  ⚠ Binary not found")
			} else {
				return fmt.Errorf("remove binary: %w", err)
			}
		} else {
			fmt.Printf("  ✓ Removed: %s\n", binPath)
		}
	}

	// 2. Clean up PATH entries from shell RC files.
	binDir := ""
	if binPath != "" {
		binDir = filepath.Dir(binPath)
	}
	if binDir != "" {
		cleanPATH(binDir)
	}

	// 3. Remove .fathom/ indexes in the current directory (best-effort).
	if err := os.RemoveAll(".fathom"); err == nil {
		fmt.Println("  ✓ Removed local .fathom/ index")
	}

	fmt.Println("\n✓ fathom uninstalled!")
	return nil
}

// cleanPATH removes fathom-related PATH exports from common shell RC files.
func cleanPATH(binDir string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	rcFiles := []string{
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".config", "fish", "config.fish"),
	}

	for _, rc := range rcFiles {
		data, err := os.ReadFile(rc)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		var kept []string
		changed := false
		for _, line := range lines {
			// Skip lines that reference fathom and PATH together.
			if strings.Contains(line, "fathom") && strings.Contains(line, "PATH") {
				changed = true
				continue
			}
			kept = append(kept, line)
		}
		if changed {
			if err := os.WriteFile(rc, []byte(strings.Join(kept, "\n")), 0o644); err == nil {
				fmt.Printf("  ✓ Cleaned PATH entry in %s\n", rc)
			}
		}
	}
}