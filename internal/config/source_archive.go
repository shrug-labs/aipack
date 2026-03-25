package config

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/util"
)

// ArchiveOpts controls safety limits for tar extraction.
type ArchiveOpts struct {
	MaxFileSize  int64 // per-file limit in bytes; 0 = default (1MB)
	MaxTotalSize int64 // total extraction limit in bytes; 0 = default (50MB)
}

const (
	defaultMaxFileSize  = 1 << 20  // 1MB
	defaultMaxTotalSize = 50 << 20 // 50MB
)

// ExtractSingleFileFromTar extracts a single file from tar data by path.
// Rejects symlinks and hard links, and limits the file size to 1MB.
func ExtractSingleFileFromTar(tarData []byte, targetPath string) ([]byte, error) {
	tr := tar.NewReader(bytes.NewReader(tarData))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}
		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
			return nil, fmt.Errorf("unexpected link in archive: %s", hdr.Name)
		}
		clean := filepath.Clean(hdr.Name)
		if clean == filepath.Clean(targetPath) && hdr.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(io.LimitReader(tr, defaultMaxFileSize+1))
			if err != nil {
				return nil, fmt.Errorf("reading %s from tar: %w", targetPath, err)
			}
			if int64(len(data)) > defaultMaxFileSize {
				return nil, fmt.Errorf("file %s exceeds size limit (%d bytes)", targetPath, len(data))
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("file %s not found in archive", targetPath)
}

// PendingSymlink records a symlink tar entry that needs post-extraction resolution.
type PendingSymlink struct {
	Dest          string // absolute path on disk where the resolved file should be written
	TargetTarPath string // relative path within the tar namespace to the target file
}

// ExtractArchiveResult holds the outcome of archive extraction.
type ExtractArchiveResult struct {
	// PendingSymlinks contains symlinks whose targets were not found in the
	// extracted archive. Empty when all symlinks resolved successfully.
	// Callers can fetch the missing targets and call ResolvePendingSymlinks.
	PendingSymlinks []PendingSymlink
}

// validateTarSymlink validates a symlink entry in a tar archive. Returns the
// resolved target path within the tar namespace. Rejects absolute targets,
// targets escaping the archive root, and targets traversing .git directories.
func validateTarSymlink(entryName, linkname string) (string, error) {
	// filepath.IsAbs on Windows requires a drive letter, but tar archives
	// use Unix-style paths. Check for both OS-native absolute paths and
	// Unix-rooted paths (leading /) to reject absolute symlink targets.
	if filepath.IsAbs(linkname) || strings.HasPrefix(linkname, "/") {
		return "", fmt.Errorf("absolute symlink target not allowed in pack archive: %s -> %s", entryName, linkname)
	}
	entryDir := filepath.Dir(entryName)
	resolved := filepath.Clean(filepath.Join(entryDir, linkname))
	if resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("symlink escapes archive boundary in pack archive: %s -> %s (resolves to %s)", entryName, linkname, resolved)
	}
	for _, comp := range strings.Split(filepath.ToSlash(resolved), "/") {
		if comp == ".git" {
			return "", fmt.Errorf("symlink target traverses .git directory in pack archive: %s -> %s", entryName, linkname)
		}
	}
	return resolved, nil
}

// ResolvePendingSymlinks resolves symlinks by reading their targets from
// destDir and writing regular files at the symlink destinations. Returns
// any symlinks whose targets were not found (still unresolved). Returns an
// error if a target is itself a pending symlink (chained symlinks).
func ResolvePendingSymlinks(destDir string, pending []PendingSymlink, opts ArchiveOpts, currentTotal int64) (unresolved []PendingSymlink, newTotal int64, err error) {
	maxFile := opts.MaxFileSize
	if maxFile <= 0 {
		maxFile = defaultMaxFileSize
	}
	maxTotal := opts.MaxTotalSize
	if maxTotal <= 0 {
		maxTotal = defaultMaxTotalSize
	}

	// Detect chained symlinks: if any symlink's target path matches
	// another symlink entry's own tar-namespace path (derived from Dest).
	symlinkEntryPaths := make(map[string]bool, len(pending))
	for _, ps := range pending {
		if rel, relErr := filepath.Rel(destDir, ps.Dest); relErr == nil {
			symlinkEntryPaths[rel] = true
		}
	}
	for _, ps := range pending {
		if symlinkEntryPaths[ps.TargetTarPath] {
			return nil, currentTotal, fmt.Errorf("chained symlinks not allowed in pack archive: target %s is also a symlink", ps.TargetTarPath)
		}
	}

	newTotal = currentTotal
	for _, ps := range pending {
		targetDest := filepath.Join(destDir, ps.TargetTarPath)
		data, readErr := os.ReadFile(targetDest)
		if readErr != nil {
			unresolved = append(unresolved, ps)
			continue
		}
		if int64(len(data)) > maxFile {
			return nil, newTotal, fmt.Errorf("symlink target %s exceeds size limit (%d > %d bytes)", ps.TargetTarPath, len(data), maxFile)
		}
		newTotal += int64(len(data))
		if newTotal > maxTotal {
			return nil, newTotal, fmt.Errorf("total extraction size exceeds limit (%d > %d bytes)", newTotal, maxTotal)
		}
		if err := os.MkdirAll(filepath.Dir(ps.Dest), 0o700); err != nil {
			return nil, newTotal, fmt.Errorf("creating parent dir for resolved symlink: %w", err)
		}
		if err := util.WriteFileAtomicWithPerms(ps.Dest, data, 0o700, 0o600); err != nil {
			return nil, newTotal, fmt.Errorf("writing resolved symlink %s: %w", ps.Dest, err)
		}
	}
	return unresolved, newTotal, nil
}

// ExtractArchive reads a tar stream and extracts files into destDir with
// safety validation. Symlinks with relative targets within the archive
// boundary are resolved to regular files. Hard links, path traversal, and
// absolute symlink targets are rejected. Size limits are enforced.
func ExtractArchive(r io.Reader, destDir string, opts ArchiveOpts) (ExtractArchiveResult, error) {
	maxFile := opts.MaxFileSize
	if maxFile <= 0 {
		maxFile = defaultMaxFileSize
	}
	maxTotal := opts.MaxTotalSize
	if maxTotal <= 0 {
		maxTotal = defaultMaxTotalSize
	}

	tr := tar.NewReader(r)
	var totalSize int64
	var pendingSymlinks []PendingSymlink

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ExtractArchiveResult{}, fmt.Errorf("reading tar entry: %w", err)
		}

		// Fast-reject obvious traversal before filepath.Join resolves it.
		clean := filepath.Clean(hdr.Name)
		if strings.Contains(clean, "..") {
			return ExtractArchiveResult{}, fmt.Errorf("path traversal in tar entry: %s", hdr.Name)
		}

		dest := filepath.Join(destDir, clean)
		if !util.IsWithinDir(dest, destDir) {
			return ExtractArchiveResult{}, fmt.Errorf("tar entry escapes destination: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o700); err != nil {
				return ExtractArchiveResult{}, fmt.Errorf("creating directory %s: %w", clean, err)
			}

		case tar.TypeReg:
			data, err := io.ReadAll(io.LimitReader(tr, maxFile+1))
			if err != nil {
				return ExtractArchiveResult{}, fmt.Errorf("reading %s: %w", clean, err)
			}
			if int64(len(data)) > maxFile {
				return ExtractArchiveResult{}, fmt.Errorf("file %s exceeds size limit (%d > %d bytes)", clean, int64(len(data)), maxFile)
			}
			totalSize += int64(len(data))
			if totalSize > maxTotal {
				return ExtractArchiveResult{}, fmt.Errorf("total extraction size exceeds limit (%d > %d bytes)", totalSize, maxTotal)
			}
			if err := util.WriteFileAtomicWithPerms(dest, data, 0o700, 0o600); err != nil {
				return ExtractArchiveResult{}, fmt.Errorf("writing %s: %w", clean, err)
			}

		case tar.TypeSymlink:
			targetTarPath, err := validateTarSymlink(clean, hdr.Linkname)
			if err != nil {
				return ExtractArchiveResult{}, err
			}
			pendingSymlinks = append(pendingSymlinks, PendingSymlink{
				Dest:          dest,
				TargetTarPath: targetTarPath,
			})

		case tar.TypeLink:
			return ExtractArchiveResult{}, fmt.Errorf("hard link not allowed in pack archive: %s", hdr.Name)

		default:
			continue
		}
	}

	// Resolve symlinks from extracted content.
	if len(pendingSymlinks) > 0 {
		unresolved, _, err := ResolvePendingSymlinks(destDir, pendingSymlinks, opts, totalSize)
		if err != nil {
			return ExtractArchiveResult{}, err
		}
		return ExtractArchiveResult{PendingSymlinks: unresolved}, nil
	}

	return ExtractArchiveResult{}, nil
}
