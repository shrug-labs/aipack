package config

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/util"
)

// ErrHTTPTarballFailed indicates the HTTP tarball download returned a non-200 status.
var ErrHTTPTarballFailed = errors.New("HTTP tarball download failed")

// GitHubTarballURL builds the tarball download URL for a GitHub repository.
// If ref is empty it defaults to "HEAD", which GitHub resolves to the default branch.
func GitHubTarballURL(repoURL, ref string) (string, error) {
	u, err := url.Parse(repoURL)
	if err != nil {
		return "", fmt.Errorf("parsing repo URL: %w", err)
	}
	if strings.ToLower(u.Hostname()) != "github.com" {
		return "", fmt.Errorf("not a GitHub URL: %s", repoURL)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("cannot extract owner/repo from %s", repoURL)
	}
	owner := parts[0]
	repo := strings.TrimSuffix(parts[1], ".git")
	if ref == "" {
		ref = "HEAD"
	}
	return fmt.Sprintf("https://github.com/%s/%s/archive/%s.tar.gz", owner, repo, ref), nil
}

// FetchHTTPTarball downloads a gzipped tarball from tarballURL and extracts its
// contents into destDir. The first path component of every tar entry is stripped
// (GitHub tarballs prefix entries with "{repo}-{ref}/"). If subPath is non-empty,
// only entries under that sub-directory (after prefix stripping) are extracted,
// and the subPath prefix itself is removed from the destination path.
func FetchHTTPTarball(ctx context.Context, tarballURL, destDir, subPath string, opts ArchiveOpts) error {
	maxFile := opts.MaxFileSize
	if maxFile <= 0 {
		maxFile = defaultMaxFileSize
	}
	maxTotal := opts.MaxTotalSize
	if maxTotal <= 0 {
		maxTotal = defaultMaxTotalSize
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tarballURL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching tarball: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: HTTP %d", ErrHTTPTarballFailed, resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("decompressing tarball: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var totalSize int64

	// Normalise subPath for prefix matching.
	subPath = strings.Trim(subPath, "/")

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar entry: %w", err)
		}

		// Strip the first path component (the GitHub prefix directory).
		stripped := stripFirstComponent(hdr.Name)
		if stripped == "" {
			continue // prefix directory entry itself, or root-level entry with no slash
		}

		// If subPath filtering is active, only keep entries under subPath/.
		if subPath != "" {
			rest, ok := strings.CutPrefix(stripped, subPath+"/")
			if !ok {
				// Also match the subPath directory entry itself.
				if strings.TrimSuffix(stripped, "/") == subPath && hdr.Typeflag == tar.TypeDir {
					continue
				}
				continue
			}
			stripped = rest
			if stripped == "" {
				continue
			}
		}

		// Safety: reject traversal.
		clean := filepath.Clean(stripped)
		if strings.Contains(clean, "..") {
			return fmt.Errorf("path traversal in tar entry: %s", hdr.Name)
		}
		dest := filepath.Join(destDir, clean)
		if !isWithinDir(dest, destDir) {
			return fmt.Errorf("tar entry escapes destination: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o700); err != nil {
				return fmt.Errorf("creating directory %s: %w", clean, err)
			}

		case tar.TypeReg:
			data, err := io.ReadAll(io.LimitReader(tr, maxFile+1))
			if err != nil {
				return fmt.Errorf("reading %s: %w", clean, err)
			}
			if int64(len(data)) > maxFile {
				return fmt.Errorf("file %s exceeds size limit (%d > %d bytes)", clean, int64(len(data)), maxFile)
			}
			totalSize += int64(len(data))
			if totalSize > maxTotal {
				return fmt.Errorf("total extraction size exceeds limit (%d > %d bytes)", totalSize, maxTotal)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
				return fmt.Errorf("creating parent dir for %s: %w", clean, err)
			}
			if err := util.WriteFileAtomicWithPerms(dest, data, 0o700, 0o600); err != nil {
				return fmt.Errorf("writing %s: %w", clean, err)
			}

		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("symlink or hard link not allowed in pack archive: %s", hdr.Name)

		default:
			continue
		}
	}
	return nil
}

// FetchFileHTTPTarball downloads a gzipped tarball and returns the content of a
// single file identified by filePath (after stripping the first path component).
func FetchFileHTTPTarball(ctx context.Context, tarballURL, filePath string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tarballURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching tarball: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", ErrHTTPTarballFailed, resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decompressing tarball: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	target := filepath.Clean(filePath)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar entry: %w", err)
		}

		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
			return nil, fmt.Errorf("unexpected link in archive: %s", hdr.Name)
		}

		stripped := stripFirstComponent(hdr.Name)
		if stripped == "" {
			continue
		}

		if filepath.Clean(stripped) == target && hdr.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(io.LimitReader(tr, defaultMaxFileSize+1))
			if err != nil {
				return nil, fmt.Errorf("reading %s from tar: %w", filePath, err)
			}
			if int64(len(data)) > defaultMaxFileSize {
				return nil, fmt.Errorf("file %s exceeds size limit (%d bytes)", filePath, len(data))
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("file %s not found in tarball", filePath)
}

// stripFirstComponent removes the first path component from a tar entry name.
// For example "repo-main/dir/file.txt" becomes "dir/file.txt".
// Returns "" if there is no second component (e.g. "repo-main/" or "repo-main").
func stripFirstComponent(name string) string {
	idx := strings.IndexByte(name, '/')
	if idx < 0 {
		return ""
	}
	rest := name[idx+1:]
	// Trim trailing slash for directory entries that are just the prefix.
	if rest == "" || rest == "/" {
		return ""
	}
	return rest
}
