package util

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

func WriteFileAtomic(path string, content []byte) error {
	return WriteFileAtomicWithPerms(path, content, 0o755, 0o644)
}

func WriteFileAtomicWithPerms(path string, content []byte, dirPerm os.FileMode, filePerm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, ".aipack-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(content)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, filePerm); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// PathExists reports whether path exists on disk (file or directory, follows symlinks).
func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ExistsFile reports whether path exists and is a regular file.
func ExistsFile(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !st.IsDir()
}

// IgnoredName reports whether a file or directory name should be excluded
// from sync operations. These are runtime artifacts that appear in synced
// directories but must not affect digest computation or be copied.
func IgnoredName(name string) bool {
	switch name {
	case "__pycache__", ".DS_Store", ".venv", ".git", "node_modules":
		return true
	}
	return false
}

// CopyDir recursively copies src to dst, using atomic writes with
// 0o700 directory and 0o600 file permissions. Skips ignored names.
func CopyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if IgnoredName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// WalkDir does not follow symlinks, so Type includes ModeSymlink.
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink not allowed in pack content: %s", path)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return WriteFileAtomicWithPerms(target, data, 0o700, 0o600)
	})
}

// IsWithinDir reports whether path is inside or equal to dir.
// Both paths are cleaned before comparison.
func IsWithinDir(path, dir string) bool {
	cleanPath := filepath.Clean(path)
	cleanDir := filepath.Clean(dir)
	if cleanPath == cleanDir {
		return true
	}
	return strings.HasPrefix(cleanPath+string(filepath.Separator), cleanDir+string(filepath.Separator))
}

// ValidateSymlinkTarget checks that a resolved symlink target is safe for
// inclusion in pack content. The target must be within boundary, must not
// traverse a .git directory, and must be a regular file (not a directory).
func ValidateSymlinkTarget(resolvedTarget, boundary string) error {
	// Resolve the boundary itself so that platform-level symlinks
	// (e.g. macOS /var -> /private/var) don't cause false rejections.
	if resolved, err := filepath.EvalSymlinks(boundary); err == nil {
		boundary = resolved
	}
	if !IsWithinDir(resolvedTarget, boundary) {
		return fmt.Errorf("symlink target escapes boundary: %s is not within %s", resolvedTarget, boundary)
	}
	rel, err := filepath.Rel(boundary, resolvedTarget)
	if err != nil {
		return fmt.Errorf("computing relative path: %w", err)
	}
	if slices.Contains(strings.Split(rel, string(filepath.Separator)), ".git") {
		return fmt.Errorf("symlink target traverses .git directory: %s", resolvedTarget)
	}
	info, err := os.Lstat(resolvedTarget)
	if err != nil {
		return fmt.Errorf("symlink target not accessible: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("symlink to directory not allowed: %s", resolvedTarget)
	}
	return nil
}

// FindRepoRoot walks up from dir looking for a .git directory and returns
// the repository root. Returns empty string if no .git directory is found.
func FindRepoRoot(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// CopyDirResolvingSymlinks recursively copies src to dst, resolving file
// symlinks whose targets are within boundary. If boundary is empty, symlinks
// are rejected (same behavior as CopyDir). Directory symlinks are always
// rejected.
func CopyDirResolvingSymlinks(src, dst, boundary string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if IgnoredName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		// WalkDir does not follow symlinks, so Type includes ModeSymlink.
		if d.Type()&os.ModeSymlink != 0 {
			if boundary == "" {
				return fmt.Errorf("symlink in pack content cannot be resolved (no git repository found above pack directory): %s", path)
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("resolving symlink %s: %w", path, err)
			}
			if err := ValidateSymlinkTarget(resolved, boundary); err != nil {
				return fmt.Errorf("symlink not allowed in pack content: %s: %w", path, err)
			}
			data, err := os.ReadFile(resolved)
			if err != nil {
				return fmt.Errorf("reading symlink target %s: %w", resolved, err)
			}
			return WriteFileAtomicWithPerms(target, data, 0o700, 0o600)
		}

		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return WriteFileAtomicWithPerms(target, data, 0o700, 0o600)
	})
}

// ResolveSymlinksInDir walks dir and replaces file symlinks with the content
// of their resolved targets. Only symlinks resolving within dir are allowed.
// Skips .git directories and ignored names.
func ResolveSymlinksInDir(dir string) error {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("resolving directory path: %w", err)
	}
	dir = resolved
	// Collect symlinks first to avoid modifying the tree during walk.
	var symlinks []string
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Name() == ".git" && d.IsDir() {
			return filepath.SkipDir
		}
		if IgnoredName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			symlinks = append(symlinks, path)
		}
		return nil
	})
	if err != nil {
		return err
	}

	for _, path := range symlinks {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolving symlink %s: %w", path, err)
		}
		if err := ValidateSymlinkTarget(resolved, dir); err != nil {
			return fmt.Errorf("symlink not allowed in pack content: %s: %w", path, err)
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return fmt.Errorf("reading symlink target %s: %w", resolved, err)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing symlink %s: %w", path, err)
		}
		if err := WriteFileAtomicWithPerms(path, data, 0o700, 0o600); err != nil {
			return fmt.Errorf("writing resolved symlink %s: %w", path, err)
		}
	}
	return nil
}

// ListSubDirs returns sorted absolute paths of subdirectories in dir.
func ListSubDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	return out
}
