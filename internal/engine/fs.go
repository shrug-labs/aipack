package engine

import (
	"cmp"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/shrug-labs/aipack/internal/util"
)

// FS abstracts filesystem operations so the engine can be tested without
// touching the real filesystem. Callers must create directories explicitly
// with MkdirAll before calling WriteFile — WriteFile does not create parents.
type FS interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	Stat(path string) (os.FileInfo, error)
	Remove(path string) error
	MkdirAll(path string, perm os.FileMode) error
	ReadDir(path string) ([]os.DirEntry, error)
	WalkDir(root string, fn fs.WalkDirFunc) error
}

// OSFS delegates to the real filesystem with atomic writes.
type OSFS struct{}

func (OSFS) ReadFile(path string) ([]byte, error)         { return os.ReadFile(path) }
func (OSFS) Stat(path string) (os.FileInfo, error)        { return os.Stat(path) }
func (OSFS) Remove(path string) error                     { return os.Remove(path) }
func (OSFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (OSFS) ReadDir(path string) ([]os.DirEntry, error)   { return os.ReadDir(path) }
func (OSFS) WalkDir(root string, fn fs.WalkDirFunc) error { return filepath.WalkDir(root, fn) }
func (OSFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	return util.WriteFileAtomicWithPerms(path, data, 0o700, perm)
}

// MemFS is an in-memory filesystem for testing.
type MemFS struct {
	mu    sync.RWMutex
	files map[string][]byte
	modes map[string]os.FileMode
	dirs  map[string]bool
}

func NewMemFS() *MemFS {
	return &MemFS{
		files: map[string][]byte{},
		modes: map[string]os.FileMode{},
		dirs:  map[string]bool{"/": true},
	}
}

func (m *MemFS) ReadFile(path string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.files[filepath.Clean(path)]
	if !ok {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	return slices.Clone(data), nil
}

func (m *MemFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	if !m.dirExists(dir) {
		return &os.PathError{Op: "write", Path: path, Err: os.ErrNotExist}
	}
	if perm == 0 {
		perm = defaultWriteMode
	}
	m.files[path] = slices.Clone(data)
	m.modes[path] = perm.Perm()
	return nil
}

func (m *MemFS) Stat(path string) (os.FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	path = filepath.Clean(path)
	if _, ok := m.files[path]; ok {
		return memFileInfo{name: filepath.Base(path), size: int64(len(m.files[path])), mode: m.fileMode(path)}, nil
	}
	if m.dirExists(path) {
		return memFileInfo{name: filepath.Base(path), dir: true}, nil
	}
	return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
}

func (m *MemFS) Remove(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	path = filepath.Clean(path)
	if _, ok := m.files[path]; ok {
		delete(m.files, path)
		delete(m.modes, path)
		return nil
	}
	if m.dirs[path] {
		// Only remove empty directories.
		for k := range m.files {
			if strings.HasPrefix(k, path+string(filepath.Separator)) {
				return &os.PathError{Op: "remove", Path: path, Err: os.ErrExist}
			}
		}
		delete(m.dirs, path)
		return nil
	}
	return &os.PathError{Op: "remove", Path: path, Err: os.ErrNotExist}
}

func (m *MemFS) MkdirAll(path string, _ os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	path = filepath.Clean(path)
	for path != "." && path != string(filepath.Separator) {
		m.dirs[path] = true
		path = filepath.Dir(path)
	}
	return nil
}

func (m *MemFS) ReadDir(path string) ([]os.DirEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	path = filepath.Clean(path)
	if !m.dirExists(path) {
		return nil, &os.PathError{Op: "readdir", Path: path, Err: os.ErrNotExist}
	}
	seen := map[string]bool{}
	var entries []os.DirEntry
	prefix := path + string(filepath.Separator)
	for k := range m.files {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		name := strings.SplitN(rest, string(filepath.Separator), 2)[0]
		if seen[name] {
			continue
		}
		seen[name] = true
		if strings.Contains(rest, string(filepath.Separator)) {
			entries = append(entries, memDirEntry{name: name, dir: true})
		} else {
			entries = append(entries, memDirEntry{name: name, size: int64(len(m.files[k])), mode: m.fileMode(k)})
		}
	}
	// Also add explicitly created dirs that are direct children.
	for d := range m.dirs {
		if filepath.Dir(d) == path && d != path {
			name := filepath.Base(d)
			if !seen[name] {
				seen[name] = true
				entries = append(entries, memDirEntry{name: name, dir: true})
			}
		}
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return cmp.Compare(a.Name(), b.Name()) })
	return entries, nil
}

func (m *MemFS) WalkDir(root string, fn fs.WalkDirFunc) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	root = filepath.Clean(root)

	if !m.dirExists(root) {
		if _, ok := m.files[root]; ok {
			return fn(root, memDirEntry{name: filepath.Base(root), mode: m.fileMode(root)}, nil)
		}
		return &os.PathError{Op: "walk", Path: root, Err: os.ErrNotExist}
	}

	// Walk root itself.
	if err := fn(root, memDirEntry{name: filepath.Base(root), dir: true}, nil); err != nil {
		if err == filepath.SkipDir {
			return nil
		}
		return err
	}

	prefix := root + string(filepath.Separator)
	var paths []string
	// Collect all file paths, intermediate dir paths, and explicit empty dirs.
	dirsSeen := map[string]bool{root: true}
	for k := range m.files {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		paths = append(paths, k)
		d := filepath.Dir(k)
		for d != root && !dirsSeen[d] {
			dirsSeen[d] = true
			paths = append(paths, d)
			d = filepath.Dir(d)
		}
	}
	for d := range m.dirs {
		if strings.HasPrefix(d, prefix) && !dirsSeen[d] {
			dirsSeen[d] = true
			paths = append(paths, d)
		}
	}
	slices.Sort(paths)

	skipPrefixes := map[string]bool{}
	for _, p := range paths {
		// Check if under a skipped directory.
		skip := false
		for sp := range skipPrefixes {
			if strings.HasPrefix(p, sp+string(filepath.Separator)) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		isDir := dirsSeen[p] && m.files[p] == nil
		if _, isFile := m.files[p]; isFile {
			isDir = false
		}
		entry := memDirEntry{name: filepath.Base(p), dir: isDir}
		if !isDir {
			entry.size = int64(len(m.files[p]))
			entry.mode = m.fileMode(p)
		}
		if err := fn(p, entry, nil); err != nil {
			if err == filepath.SkipDir {
				if isDir {
					skipPrefixes[p] = true
				}
				continue
			}
			return err
		}
	}
	return nil
}

func (m *MemFS) fileMode(path string) os.FileMode {
	if mode := m.modes[filepath.Clean(path)]; mode != 0 {
		return mode.Perm()
	}
	return defaultWriteMode
}

func (m *MemFS) dirExists(path string) bool {
	if m.dirs[path] {
		return true
	}
	prefix := path + string(filepath.Separator)
	for k := range m.files {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// memFileInfo implements os.FileInfo for MemFS.
type memFileInfo struct {
	name string
	size int64
	dir  bool
	mode os.FileMode
}

func (f memFileInfo) Name() string { return f.name }
func (f memFileInfo) Size() int64  { return f.size }
func (f memFileInfo) Mode() os.FileMode {
	if f.dir {
		return os.ModeDir | 0o755
	}
	if f.mode != 0 {
		return f.mode.Perm()
	}
	return defaultWriteMode
}
func (f memFileInfo) ModTime() time.Time { return time.Time{} }
func (f memFileInfo) IsDir() bool        { return f.dir }
func (f memFileInfo) Sys() any           { return nil }

// memDirEntry implements os.DirEntry for MemFS.
type memDirEntry struct {
	name string
	dir  bool
	size int64
	mode os.FileMode
}

func (e memDirEntry) Name() string { return e.name }
func (e memDirEntry) IsDir() bool  { return e.dir }
func (e memDirEntry) Type() os.FileMode {
	if e.dir {
		return os.ModeDir
	}
	return 0
}
func (e memDirEntry) Info() (os.FileInfo, error) {
	return memFileInfo{name: e.name, size: e.size, dir: e.dir, mode: e.mode}, nil
}
