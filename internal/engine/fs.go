package engine

import (
	"cmp"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/plugin"
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
	ReadPackage(path string) ([]domain.NativePluginFile, error)
	WritePackage(path string, files []domain.NativePluginFile) error
	RemovePackage(path string) error
}

type symlinkEvaluator interface {
	EvalSymlinks(path string) (string, error)
}

// OSFS delegates to the real filesystem with atomic writes.
type OSFS struct{}

func (OSFS) ReadFile(path string) ([]byte, error)         { return os.ReadFile(path) }
func (OSFS) Stat(path string) (os.FileInfo, error)        { return os.Stat(path) }
func (OSFS) Remove(path string) error                     { return os.Remove(path) }
func (OSFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (OSFS) ReadDir(path string) ([]os.DirEntry, error)   { return os.ReadDir(path) }
func (OSFS) WalkDir(root string, fn fs.WalkDirFunc) error { return filepath.WalkDir(root, fn) }
func (OSFS) EvalSymlinks(path string) (string, error)     { return filepath.EvalSymlinks(path) }
func (OSFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	return util.WriteFileAtomicWithPerms(path, data, 0o700, perm)
}

func (OSFS) ReadPackage(path string) ([]domain.NativePluginFile, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("payload root must be a directory, not a file or symlink: %s", path)
	}
	return plugin.ReadPayloadFiles(path)
}

func (OSFS) WritePackage(path string, files []domain.NativePluginFile) error {
	if err := plugin.ValidateFiles(files); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.IsDir() {
		return fmt.Errorf("payload root must be a directory: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(path), ".aipack-payload-*")
	if err != nil {
		return err
	}
	defer util.RemoveOwnedTree(staging)
	if err := plugin.WriteFiles(staging, files); err != nil {
		return err
	}
	if _, err := plugin.ReadPayloadFiles(staging); err != nil {
		return fmt.Errorf("validate staged payload: %w", err)
	}
	return util.ReplaceDirAtomic(path, staging)
}

func (OSFS) RemovePackage(path string) error {
	if info, err := os.Lstat(path); err == nil && !info.IsDir() {
		return fmt.Errorf("refusing to remove replaced payload root: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return util.RemoveOwnedTree(path)
}

// MemFS is an in-memory filesystem for testing.
type MemFS struct {
	mu    sync.RWMutex
	files map[string][]byte
	modes map[string]os.FileMode
	dirs  map[string]bool
}

func (m *MemFS) ReadPackage(path string) ([]domain.NativePluginFile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	path = filepath.Clean(path)
	if !m.dirs[path] {
		return nil, &os.PathError{Op: "read package", Path: path, Err: os.ErrNotExist}
	}
	files := []domain.NativePluginFile{}
	prefix := path + string(filepath.Separator)
	for key := range m.dirs {
		if rel, ok := strings.CutPrefix(key, prefix); ok {
			mode := m.modes[key]
			if mode == 0 {
				mode = 0o755
			}
			files = append(files, domain.NativePluginFile{Path: filepath.ToSlash(rel), Mode: os.ModeDir | mode.Perm()})
		}
	}
	for key, data := range m.files {
		if rel, ok := strings.CutPrefix(key, prefix); ok {
			file := domain.NativePluginFile{Path: filepath.ToSlash(rel), Mode: m.modes[key], Content: slices.Clone(data)}
			if file.Mode&os.ModeSymlink != 0 {
				file.Link, file.Content = string(data), nil
			}
			files = append(files, file)
		}
	}
	slices.SortFunc(files, func(a, b domain.NativePluginFile) int { return cmp.Compare(a.Path, b.Path) })
	return files, nil
}

func (m *MemFS) WritePackage(path string, files []domain.NativePluginFile) error {
	if err := plugin.ValidateFiles(files); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	path = filepath.Clean(path)
	if _, exists := m.files[path]; exists {
		return fmt.Errorf("payload root must be a directory: %s", path)
	}
	m.removePackage(path)
	for parent := path; parent != "."; parent = filepath.Dir(parent) {
		m.dirs[parent] = true
		if parent == filepath.Dir(parent) {
			break
		}
	}
	for _, file := range files {
		key := filepath.Join(path, filepath.FromSlash(file.Path))
		if file.Mode.IsDir() {
			m.dirs[key] = true
		} else {
			data := file.Content
			if file.Mode&os.ModeSymlink != 0 {
				data = []byte(file.Link)
			}
			m.files[key] = slices.Clone(data)
		}
		m.modes[key] = file.Mode
		for parent := filepath.Dir(key); parent != path; parent = filepath.Dir(parent) {
			m.dirs[parent] = true
		}
	}
	return nil
}

func (m *MemFS) RemovePackage(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	path = filepath.Clean(path)
	if _, exists := m.files[path]; exists {
		return fmt.Errorf("refusing to remove replaced payload root: %s", path)
	}
	m.removePackage(path)
	return nil
}

func (m *MemFS) removePackage(path string) {
	for key := range m.files {
		if key == path || strings.HasPrefix(key, path+string(filepath.Separator)) {
			delete(m.files, key)
			delete(m.modes, key)
		}
	}
	for key := range m.dirs {
		if key == path || strings.HasPrefix(key, path+string(filepath.Separator)) {
			delete(m.dirs, key)
			delete(m.modes, key)
		}
	}
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
	resolved, err := m.resolvePath(path)
	if err != nil {
		return nil, err
	}
	data, ok := m.files[resolved]
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
	var err error
	path, err = m.resolvePath(path)
	if err != nil {
		return nil, err
	}
	if _, ok := m.files[path]; ok {
		return memFileInfo{name: filepath.Base(path), size: int64(len(m.files[path])), mode: m.fileMode(path)}, nil
	}
	if m.dirExists(path) {
		return memFileInfo{name: filepath.Base(path), dir: true}, nil
	}
	return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
}

// resolvePath follows both file and directory links without holding another
// lock. Package reads inspect stored entries directly, like filesystem Lstat.
func (m *MemFS) resolvePath(path string) (string, error) {
	path = filepath.Clean(path)
	for range 40 {
		changed := false
		for candidate := path; candidate != "."; candidate = filepath.Dir(candidate) {
			if m.modes[candidate]&os.ModeSymlink != 0 {
				rest, err := filepath.Rel(candidate, path)
				if err != nil {
					return "", err
				}
				path = filepath.Clean(filepath.Join(filepath.Dir(candidate), string(m.files[candidate]), rest))
				changed = true
				break
			}
			if candidate == filepath.Dir(candidate) {
				break
			}
		}
		if !changed {
			return path, nil
		}
	}
	return "", fmt.Errorf("too many symlinks: %s", path)
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
		return mode
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
		return f.mode
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
	return e.mode.Type()
}
func (e memDirEntry) Info() (os.FileInfo, error) {
	return memFileInfo{name: e.name, size: e.size, dir: e.dir, mode: e.mode}, nil
}
