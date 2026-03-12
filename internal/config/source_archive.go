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

// isWithinDir reports whether path is inside or equal to dir.
func isWithinDir(path, dir string) bool {
	cleanPath := filepath.Clean(path)
	cleanDir := filepath.Clean(dir)
	if cleanPath == cleanDir {
		return true
	}
	return strings.HasPrefix(cleanPath+string(filepath.Separator), cleanDir+string(filepath.Separator))
}

// ExtractArchive reads a tar stream and extracts files into destDir with
// safety validation. Rejects symlinks, hard links, path traversal, and
// enforces size limits. Only regular files and directories are extracted.
func ExtractArchive(r io.Reader, destDir string, opts ArchiveOpts) error {
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

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar entry: %w", err)
		}

		// Fast-reject obvious traversal before filepath.Join resolves it.
		clean := filepath.Clean(hdr.Name)
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
			if err := util.WriteFileAtomicWithPerms(dest, data, 0o700, 0o600); err != nil {
				return fmt.Errorf("writing %s: %w", clean, err)
			}

		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("symlink or hard link not allowed in pack archive: %s", hdr.Name)

		default:
			// Skip other types (block devices, char devices, etc.)
			continue
		}
	}
	return nil
}
