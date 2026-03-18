package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ghDownloadBase is the base URL for GitHub release asset downloads.
// Var so tests can override it.
var ghDownloadBase = "https://github.com/shrug-labs/aipack/releases/download"

// executablePath returns the path of the currently running binary.
// Var so tests can override it.
var executablePath = os.Executable

// downloadTimeout caps the total time for downloading a release binary.
const downloadTimeout = 60 * time.Second

// Update downloads the latest release and replaces the current binary.
// Progress is written to w. Returns nil on success or if already up to date.
func Update(ctx context.Context, currentVersion string, w io.Writer) error {
	if currentVersion == "dev" || currentVersion == "" {
		return fmt.Errorf("cannot update a dev build; install a release version first")
	}

	latest, _, err := fetchLatest(ctx)
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}

	current := strings.TrimPrefix(currentVersion, "v")
	r := &Result{Latest: latest, Current: current}
	if !r.Newer() {
		fmt.Fprintf(w, "aipack %s is already the latest version.\n", current)
		return nil
	}

	asset := AssetName()
	tag := "v" + latest

	fmt.Fprintf(w, "Updating aipack %s → %s\n", current, latest)

	execPath, err := executablePath()
	if err != nil {
		return fmt.Errorf("finding current executable: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}

	// Create temp file in same directory to guarantee same-filesystem rename.
	dir := filepath.Dir(realPath)
	tmp, err := os.CreateTemp(dir, ".aipack-update-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	dlCtx, dlCancel := context.WithTimeout(ctx, downloadTimeout)
	defer dlCancel()

	// Download the binary, computing SHA256 as we stream.
	binaryURL := fmt.Sprintf("%s/%s/%s", ghDownloadBase, tag, asset)
	fmt.Fprintf(w, "Downloading %s...\n", binaryURL)

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, binaryURL, nil)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("downloading binary: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("downloading binary: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		tmp.Close()
		return fmt.Errorf("no release binary found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		tmp.Close()
		return fmt.Errorf("downloading binary: HTTP %d", resp.StatusCode)
	}

	hasher := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, hasher), resp.Body)
	resp.Body.Close()
	tmp.Close()
	if err != nil {
		return fmt.Errorf("downloading binary: %w", err)
	}
	gotHash := hex.EncodeToString(hasher.Sum(nil))

	// Verify checksum against published SHA256SUMS.
	sumsURL := fmt.Sprintf("%s/%s/SHA256SUMS", ghDownloadBase, tag)
	wantHash, err := fetchChecksum(dlCtx, sumsURL, asset)
	if err != nil {
		return fmt.Errorf("verifying checksum: %w", err)
	}
	if gotHash != wantHash {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset, gotHash, wantHash)
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}

	// Atomic replace. On Unix, the running process keeps its fd to the old inode.
	if err := os.Rename(tmpPath, realPath); err != nil {
		return fmt.Errorf("replacing %s: %w", realPath, err)
	}

	// Ad-hoc code sign on macOS to suppress Gatekeeper warnings.
	if runtime.GOOS == "darwin" {
		if cs, lookErr := exec.LookPath("codesign"); lookErr == nil {
			_ = exec.CommandContext(ctx, cs, "-s", "-", "-f", realPath).Run()
		}
	}

	fmt.Fprintf(w, "Updated to aipack %s\n", latest)
	return nil
}

// AssetName returns the expected release asset name for the current platform.
func AssetName() string {
	return fmt.Sprintf("aipack-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// fetchChecksum downloads a SHA256SUMS file and extracts the hash for asset.
func fetchChecksum(ctx context.Context, url, asset string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SHA256SUMS: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == asset {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("no checksum found for %s", asset)
}
