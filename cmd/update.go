package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	updateOwner = "blak0p"
	updateRepo  = "Fathom"
)

// githubRelease matches the GitHub API response for a release.
type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// updateCmd implements "fathom update": self-update from GitHub releases.
var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update fathom to the latest release",
	Long: `fathom update checks GitHub releases for a newer version of fathom and
replaces the running binary in place with an atomic rename.

Use --force to re-download the latest version even when already up to date.`,
	Args: cobra.NoArgs,
	RunE: runUpdate,
}

var updateForce bool

func init() {
	updateCmd.Flags().BoolVar(&updateForce, "force", false, "Force update even when already on the latest version")
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(cmd *cobra.Command, args []string) error {
	fmt.Println("Checking for updates...")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	latest, err := fetchLatestRelease(ctx)
	if err != nil {
		return fmt.Errorf("update: failed to fetch latest release: %w", err)
	}

	currentVersion := strings.TrimPrefix(version, "v")
	latestVersion := strings.TrimPrefix(latest.TagName, "v")

	if latestVersion == currentVersion && !updateForce {
		fmt.Printf("Already up to date! (v%s)\n", currentVersion)
		return nil
	}

	if latestVersion != currentVersion {
		fmt.Printf("Update available: v%s → v%s\n", currentVersion, latestVersion)
	} else {
		fmt.Println("Force-updating to latest...")
	}

	dlCtx, dlCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer dlCancel()

	if err := downloadAndReplace(dlCtx, latest); err != nil {
		return fmt.Errorf("update: %w", err)
	}

	fmt.Printf("✓ Updated to v%s\n", latestVersion)
	return nil
}

func fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", updateOwner, updateRepo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "fathom")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api error: %s", resp.Status)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	return &release, nil
}

// downloadAndReplace downloads the matching asset for the current platform
// and atomically replaces the running binary.
func downloadAndReplace(ctx context.Context, release *githubRelease) error {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Build asset matchers: try goreleaser archive first, then raw binary.
	assetURL, isArchive, err := findAssetURL(release.Assets, goos, goarch)
	if err != nil {
		return fmt.Errorf("no compatible asset for %s/%s: %w", goos, goarch, err)
	}

	// Download to a temp file.
	suffix := ".tar.gz"
	if goos == "windows" {
		if isArchive {
			suffix = ".zip"
		} else {
			suffix = ".exe"
		}
	} else if !isArchive {
		suffix = ""
	}

	tmpFile, err := os.CreateTemp("", "fathom-update-*"+suffix)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if err := downloadFile(ctx, assetURL, tmpFile); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	// Extract the binary from the archive (or read raw).
	var binData []byte
	if isArchive {
		if _, err := tmpFile.Seek(0, 0); err != nil {
			return err
		}
		binData, err = extractBinary(tmpFile, goos)
		if err != nil {
			return fmt.Errorf("extract: %w", err)
		}
	} else {
		if _, err := tmpFile.Seek(0, 0); err != nil {
			return err
		}
		binData, err = io.ReadAll(tmpFile)
		if err != nil {
			return fmt.Errorf("read binary: %w", err)
		}
	}

	// Atomic binary replacement: write to a temp file in the same directory,
	// then rename over the current binary.
	currentPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	currentDir := filepath.Dir(currentPath)
	newPath := filepath.Join(currentDir, filepath.Base(currentPath)+".new")
	if err := os.WriteFile(newPath, binData, 0o755); err != nil {
		return fmt.Errorf("write new binary: %w", err)
	}
	defer os.Remove(newPath)

	if err := os.Rename(newPath, currentPath); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}

	return nil
}

// findAssetURL searches release assets for a matching platform binary.
// Returns the download URL and whether the asset is an archive.
func findAssetURL(assets []struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}, goos, goarch string) (string, bool, error) {
	// goreleaser archive pattern: fathom_<version>_<os>_<arch>.tar.gz or .zip
	archiveExt := ".tar.gz"
	if goos == "windows" {
		archiveExt = ".zip"
	}
	archiveRe := regexp.MustCompile(fmt.Sprintf(`fathom_\d+\.\d+\.\d+_%s_%s%s`, regexp.QuoteMeta(goos), regexp.QuoteMeta(goarch), regexp.QuoteMeta(archiveExt)))

	// raw binary pattern: fathom-<os>-<arch>[.exe]
	exeSuffix := ""
	if goos == "windows" {
		exeSuffix = `(\.exe)?`
	}
	rawRe := regexp.MustCompile(fmt.Sprintf(`^fathom-%s-%s%s$`, regexp.QuoteMeta(goos), regexp.QuoteMeta(goarch), exeSuffix))

	// Try archive first.
	for _, a := range assets {
		if archiveRe.MatchString(a.Name) {
			return a.BrowserDownloadURL, true, nil
		}
	}
	// Fallback to raw binary.
	for _, a := range assets {
		if rawRe.MatchString(a.Name) {
			return a.BrowserDownloadURL, false, nil
		}
	}

	return "", false, fmt.Errorf("no matching asset found")
}

func downloadFile(ctx context.Context, url string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "fathom")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	_, err = io.Copy(w, resp.Body)
	return err
}

// extractBinary extracts the fathom binary from a tar.gz (linux/macos) or
// zip (windows) archive.
func extractBinary(r io.Reader, goos string) ([]byte, error) {
	if goos == "windows" {
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("read zip: %w", err)
		}
		return extractBinaryFromZip(bytes.NewReader(data), int64(len(data)))
	}
	return extractBinaryFromTarGz(r)
}

func extractBinaryFromTarGz(r io.Reader) ([]byte, error) {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeReg && (header.Name == "fathom" || strings.HasSuffix(header.Name, "/fathom")) {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("binary not found in tar.gz archive")
}

func extractBinaryFromZip(r io.ReaderAt, size int64) ([]byte, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	for _, f := range zr.File {
		name := f.Name
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		if name == "fathom.exe" || name == "fathom" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open file in zip: %w", err)
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("binary not found in zip archive")
}