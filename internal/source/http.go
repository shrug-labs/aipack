package source

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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

// httpPendingSymlink records a symlink found in an HTTP tarball that needs resolution.
type httpPendingSymlink struct {
	dest               string // absolute path where the resolved file goes
	targetFullStripped string // target path in stripped namespace (repo root)
}

// HTTPArchiveOptions controls generic HTTP archive downloads.
type HTTPArchiveOptions struct {
	ArchiveOpts
	NetrcPath string
	Client    *http.Client
	Validator ArchiveValidator
}

// ArchiveValidator is a provider-neutral HTTP representation validator.
// ETag takes precedence over Last-Modified when both are present.
type ArchiveValidator struct {
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
}

// ArchiveFetchResult describes the transport-level observation made while
// fetching an archive. Callers must still compare extracted content to decide
// whether the installed pack is semantically current.
type ArchiveFetchResult struct {
	NotModified      bool
	Validator        ArchiveValidator
	ResourceIdentity string
	ByteHash         string
}

// FetchHTTPTarball downloads a gzipped tarball from tarballURL and extracts its
// contents into destDir. The first path component of every tar entry is stripped
// (GitHub tarballs prefix entries with "{repo}-{ref}/"). If subPath is non-empty,
// only entries under that sub-directory (after prefix stripping) are extracted,
// and the subPath prefix itself is removed from the destination path.
//
// Symlinks with relative targets within the repository boundary are resolved to
// regular files. If a symlink's target is outside the subPath scope, a second
// pass over the tarball extracts the target file. Hard links and symlinks
// escaping the repo boundary are rejected.
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

	// Read the decompressed tar into memory so we can do a second pass if
	// symlinks reference files outside the subPath scope. The maxTotal limit
	// (default 50MB) bounds the memory usage.
	tarData, err := io.ReadAll(io.LimitReader(gz, maxTotal+1))
	if err != nil {
		return fmt.Errorf("reading decompressed tarball: %w", err)
	}
	if int64(len(tarData)) > maxTotal {
		return fmt.Errorf("decompressed tarball exceeds size limit (%d bytes)", maxTotal)
	}

	tr := tar.NewReader(bytes.NewReader(tarData))
	var totalSize int64
	var pendingSymlinks []httpPendingSymlink

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
			continue
		}

		// Symlink targets are repo-relative, not subPath-relative, so
		// preserve the pre-filter path for symlink validation.
		fullStripped := stripped

		// If subPath filtering is active, only keep entries under subPath/.
		if subPath != "" {
			rest, ok := strings.CutPrefix(stripped, subPath+"/")
			if !ok {
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
		if !util.IsWithinDir(dest, destDir) {
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

		case tar.TypeSymlink:
			// Validate using fullStripped because symlink targets are
			// repo-relative, not subPath-relative.
			targetFull, symErr := validateTarSymlink(fullStripped, hdr.Linkname)
			if symErr != nil {
				return symErr
			}
			pendingSymlinks = append(pendingSymlinks, httpPendingSymlink{
				dest:               dest,
				targetFullStripped: targetFull,
			})

		case tar.TypeLink:
			return fmt.Errorf("hard link not allowed in pack archive: %s", hdr.Name)

		default:
			continue
		}
	}

	if len(pendingSymlinks) == 0 {
		return nil
	}

	// Try to resolve symlinks from already-extracted files.
	var unresolved []httpPendingSymlink
	for _, ps := range pendingSymlinks {
		var targetOnDisk string
		if subPath != "" {
			if rest, ok := strings.CutPrefix(ps.targetFullStripped, subPath+"/"); ok {
				targetOnDisk = filepath.Join(destDir, filepath.Clean(rest))
			}
		} else {
			targetOnDisk = filepath.Join(destDir, filepath.Clean(ps.targetFullStripped))
		}

		if targetOnDisk != "" {
			data, readErr := os.ReadFile(targetOnDisk)
			if readErr == nil {
				if int64(len(data)) > maxFile {
					return fmt.Errorf("symlink target %s exceeds size limit (%d > %d bytes)", ps.targetFullStripped, len(data), maxFile)
				}
				totalSize += int64(len(data))
				if totalSize > maxTotal {
					return fmt.Errorf("total extraction size exceeds limit (%d > %d bytes)", totalSize, maxTotal)
				}
				if err := os.MkdirAll(filepath.Dir(ps.dest), 0o700); err != nil {
					return err
				}
				if err := util.WriteFileAtomicWithPerms(ps.dest, data, 0o700, 0o600); err != nil {
					return err
				}
				continue
			}
		}
		unresolved = append(unresolved, ps)
	}

	if len(unresolved) == 0 {
		return nil
	}

	// Second pass: re-read the tarball from memory to find unresolved targets.

	// Build lookup of needed target paths.
	neededTargets := make(map[string][]int) // targetFullStripped -> indices into unresolved
	for i, ps := range unresolved {
		neededTargets[ps.targetFullStripped] = append(neededTargets[ps.targetFullStripped], i)
	}

	tr2 := tar.NewReader(bytes.NewReader(tarData))
	resolved := make(map[string]bool)

	for {
		hdr, err := tr2.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar entry (second pass): %w", err)
		}

		stripped := stripFirstComponent(hdr.Name)
		if stripped == "" {
			continue
		}
		clean := filepath.Clean(stripped)

		indices, ok := neededTargets[clean]
		if !ok {
			continue
		}

		if hdr.Typeflag == tar.TypeSymlink {
			return fmt.Errorf("chained symlinks not allowed in pack archive: target %s is also a symlink", clean)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		data, err := io.ReadAll(io.LimitReader(tr2, maxFile+1))
		if err != nil {
			return fmt.Errorf("reading symlink target %s: %w", clean, err)
		}
		if int64(len(data)) > maxFile {
			return fmt.Errorf("symlink target %s exceeds size limit (%d > %d bytes)", clean, len(data), maxFile)
		}
		totalSize += int64(len(data))
		if totalSize > maxTotal {
			return fmt.Errorf("total extraction size exceeds limit (%d > %d bytes)", totalSize, maxTotal)
		}

		// Write content to all symlink destinations pointing to this target.
		for _, idx := range indices {
			ps := unresolved[idx]
			if err := os.MkdirAll(filepath.Dir(ps.dest), 0o700); err != nil {
				return err
			}
			if err := util.WriteFileAtomicWithPerms(ps.dest, data, 0o700, 0o600); err != nil {
				return err
			}
		}
		resolved[clean] = true
	}

	// Check for any still-unresolved targets.
	var missing []string
	for target := range neededTargets {
		if !resolved[target] {
			missing = append(missing, target)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("symlink targets not found in archive: %s", strings.Join(missing, ", "))
	}

	return nil
}

// FetchHTTPArchive downloads a zip, tar, tar.gz, or tgz archive from archiveURL
// and extracts it into destDir without stripping path components.
func FetchHTTPArchive(ctx context.Context, archiveURL, destDir string, opts HTTPArchiveOptions) error {
	_, err := FetchHTTPArchiveObserved(ctx, archiveURL, destDir, opts)
	return err
}

// FetchHTTPArchiveObserved is FetchHTTPArchive with conditional HTTP support
// and representation metadata. It treats validators only as transport hints;
// a 200 response is always extracted for the caller's semantic comparison.
func FetchHTTPArchiveObserved(ctx context.Context, archiveURL, destDir string, opts HTTPArchiveOptions) (ArchiveFetchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return ArchiveFetchResult{}, fmt.Errorf("creating request: %w", err)
	}
	if err := applyNetrcAuth(req, opts.NetrcPath); err != nil {
		return ArchiveFetchResult{}, err
	}
	if opts.Validator.ETag != "" {
		req.Header.Set("If-None-Match", opts.Validator.ETag)
	} else if opts.Validator.LastModified != "" {
		req.Header.Set("If-Modified-Since", opts.Validator.LastModified)
	}

	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return ArchiveFetchResult{}, fmt.Errorf("fetching archive: %w", err)
	}
	defer resp.Body.Close()

	result := ArchiveFetchResult{
		NotModified: resp.StatusCode == http.StatusNotModified,
		Validator: ArchiveValidator{
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
		},
	}
	if resp.Request != nil && resp.Request.URL != nil {
		result.ResourceIdentity = resp.Request.URL.String()
	}
	if result.NotModified {
		if result.Validator.ETag == "" {
			result.Validator.ETag = opts.Validator.ETag
		}
		if result.Validator.LastModified == "" {
			result.Validator.LastModified = opts.Validator.LastModified
		}
		return result, nil
	}
	if resp.StatusCode != http.StatusOK {
		return ArchiveFetchResult{}, fmt.Errorf("%w: HTTP %d", ErrHTTPTarballFailed, resp.StatusCode)
	}

	lower := strings.ToLower(req.URL.Path)
	if err := extractArchiveByPath(lower, resp.Body, destDir, opts.ArchiveOpts, archiveURL); err != nil {
		return ArchiveFetchResult{}, err
	}
	return result, nil
}

// FetchArchive extracts a local or HTTP zip, tar, tar.gz, or tgz archive into
// destDir without stripping path components.
func FetchArchive(ctx context.Context, archiveSource, destDir string, opts HTTPArchiveOptions) error {
	_, err := FetchArchiveObserved(ctx, archiveSource, destDir, opts)
	return err
}

// FetchArchiveObserved extracts an HTTP or local archive and returns
// provider-neutral observation metadata. Local archives use a byte hash;
// HTTP archives use standard validators when supplied by the server.
func FetchArchiveObserved(ctx context.Context, archiveSource, destDir string, opts HTTPArchiveOptions) (ArchiveFetchResult, error) {
	if IsHTTPURL(archiveSource) {
		return FetchHTTPArchiveObserved(ctx, archiveSource, destDir, opts)
	}
	if err := ctx.Err(); err != nil {
		return ArchiveFetchResult{}, err
	}
	f, err := os.Open(archiveSource)
	if err != nil {
		return ArchiveFetchResult{}, fmt.Errorf("opening archive: %w", err)
	}
	defer f.Close()
	hash := sha256.New()
	observed := io.TeeReader(f, hash)
	if err := extractArchiveByPath(strings.ToLower(archiveSource), observed, destDir, opts.ArchiveOpts, archiveSource); err != nil {
		return ArchiveFetchResult{}, err
	}
	// Some archive readers can stop after the logical end marker. Include any
	// trailing representation bytes so repacks are observed by the byte hash.
	if _, err := io.Copy(hash, f); err != nil {
		return ArchiveFetchResult{}, fmt.Errorf("hashing archive: %w", err)
	}
	return ArchiveFetchResult{ByteHash: hex.EncodeToString(hash.Sum(nil)), ResourceIdentity: archiveSource}, nil
}

// FetchLocalArchive extracts a local zip, tar, tar.gz, or tgz archive into
// destDir without stripping path components.
func FetchLocalArchive(archivePath, destDir string, opts ArchiveOpts) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("opening archive: %w", err)
	}
	defer f.Close()
	return extractArchiveByPath(strings.ToLower(archivePath), f, destDir, opts, archivePath)
}

func extractArchiveByPath(lower string, r io.Reader, destDir string, opts ArchiveOpts, label string) error {
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZipArchive(r, destDir, opts)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		gz, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("decompressing archive: %w", err)
		}
		defer gz.Close()
		return extractGenericTarArchive(gz, destDir, opts)
	case strings.HasSuffix(lower, ".tar"):
		return extractGenericTarArchive(r, destDir, opts)
	default:
		return fmt.Errorf("unsupported archive format for %s (expected .zip, .tar, .tar.gz, or .tgz)", label)
	}
}

func extractGenericTarArchive(r io.Reader, destDir string, opts ArchiveOpts) error {
	opts.EnforceTopLevelSymlinkBoundary = true
	result, err := ExtractArchive(r, destDir, opts)
	if err != nil {
		return err
	}
	if len(result.PendingSymlinks) > 0 {
		var missing []string
		for _, ps := range result.PendingSymlinks {
			missing = append(missing, ps.TargetTarPath)
		}
		slices.Sort(missing)
		return fmt.Errorf("symlink targets not found in archive: %s", strings.Join(missing, ", "))
	}
	return nil
}

func extractZipArchive(r io.Reader, destDir string, opts ArchiveOpts) error {
	maxFile := opts.MaxFileSize
	if maxFile <= 0 {
		maxFile = defaultMaxFileSize
	}
	maxTotal := opts.MaxTotalSize
	if maxTotal <= 0 {
		maxTotal = defaultMaxTotalSize
	}

	data, err := io.ReadAll(io.LimitReader(r, maxTotal+1))
	if err != nil {
		return fmt.Errorf("reading zip archive: %w", err)
	}
	if int64(len(data)) > maxTotal {
		return fmt.Errorf("zip archive exceeds size limit (%d bytes)", maxTotal)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("opening zip archive: %w", err)
	}

	var totalSize int64
	for _, file := range zr.File {
		clean, err := safeArchivePath(file.Name)
		if err != nil {
			return err
		}
		dest := filepath.Join(destDir, clean)
		if !util.IsWithinDir(dest, destDir) {
			return fmt.Errorf("zip entry escapes destination: %s", file.Name)
		}

		mode := file.FileInfo().Mode()
		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink not allowed in pack archive: %s", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o700); err != nil {
				return fmt.Errorf("creating directory %s: %w", clean, err)
			}
			continue
		}
		if !mode.IsRegular() {
			continue
		}
		if file.UncompressedSize64 > uint64(maxFile) {
			return fmt.Errorf("file %s exceeds size limit (%d > %d bytes)", clean, file.UncompressedSize64, maxFile)
		}
		rc, err := file.Open()
		if err != nil {
			return fmt.Errorf("opening zip entry %s: %w", clean, err)
		}
		content, readErr := io.ReadAll(io.LimitReader(rc, maxFile+1))
		closeErr := rc.Close()
		if readErr != nil {
			return fmt.Errorf("reading zip entry %s: %w", clean, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("closing zip entry %s: %w", clean, closeErr)
		}
		if int64(len(content)) > maxFile {
			return fmt.Errorf("file %s exceeds size limit (%d > %d bytes)", clean, len(content), maxFile)
		}
		totalSize += int64(len(content))
		if totalSize > maxTotal {
			return fmt.Errorf("total extraction size exceeds limit (%d > %d bytes)", totalSize, maxTotal)
		}
		if err := util.WriteFileAtomicWithPerms(dest, content, 0o700, 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", clean, err)
		}
	}
	return nil
}

func safeArchivePath(name string) (string, error) {
	clean := filepath.Clean(name)
	if clean == "." || strings.HasPrefix(clean, string(filepath.Separator)) ||
		clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) ||
		strings.Contains(clean, string(filepath.Separator)+".."+string(filepath.Separator)) ||
		strings.HasSuffix(clean, string(filepath.Separator)+"..") {
		return "", fmt.Errorf("path traversal in archive entry: %s", name)
	}
	return clean, nil
}

func applyNetrcAuth(req *http.Request, netrcPath string) error {
	if req.URL.User != nil {
		return nil
	}
	if netrcPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		netrcPath = filepath.Join(home, ".netrc")
	}
	entries, err := parseNetrc(netrcPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading netrc: %w", err)
	}
	host := req.URL.Host
	hostName := req.URL.Hostname()
	for _, entry := range entries {
		if entry.machine == host || entry.machine == hostName {
			if entry.login != "" || entry.password != "" {
				req.SetBasicAuth(entry.login, entry.password)
			}
			return nil
		}
	}
	return nil
}

type netrcEntry struct {
	machine  string
	login    string
	password string
}

func parseNetrc(path string) ([]netrcEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	tokens := strings.Fields(string(b))
	var entries []netrcEntry
	for i := 0; i < len(tokens); {
		if tokens[i] != "machine" {
			i++
			continue
		}
		if i+1 >= len(tokens) {
			break
		}
		entry := netrcEntry{machine: tokens[i+1]}
		i += 2
		for i < len(tokens) && tokens[i] != "machine" {
			key := tokens[i]
			if i+1 >= len(tokens) {
				break
			}
			value := tokens[i+1]
			switch key {
			case "login":
				entry.login = value
				i += 2
			case "password":
				entry.password = value
				i += 2
			default:
				i++
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
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
	_, after, ok := strings.Cut(name, "/")
	if !ok {
		return ""
	}
	// Trim trailing slash for directory entries that are just the prefix.
	if after == "" || after == "/" {
		return ""
	}
	return after
}
