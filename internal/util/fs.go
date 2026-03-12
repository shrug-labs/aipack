package util

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func WriteFileAtomic(path string, content []byte) error {
	return WriteFileAtomicWithPerms(path, content, 0o755, 0o644)
}

func WriteFileAtomicWithPerms(path string, content []byte, dirPerm os.FileMode, filePerm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, filePerm)
	if err != nil {
		return err
	}
	_, werr := f.Write(content)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
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
	case "__pycache__", ".DS_Store":
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
