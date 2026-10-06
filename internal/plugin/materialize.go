package plugin

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// File is a package-relative payload entry. Preserve executable permissions,
// empty directories and bounded relative symlinks, including bundled dependencies.
type File = domain.NativePluginFile

func ReadFiles(root string) ([]File, error) {
	return readFiles(root, true)
}

// ReadPayloadFiles includes runtime additions such as Git metadata when
// checking ownership of an already delivered tree.
func ReadPayloadFiles(root string) ([]File, error) {
	return readFiles(root, false)
}

func readFiles(root string, excludeGit bool) ([]File, error) {
	var files []File
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Git metadata is acquisition state, never part of a native package.
		if excludeGit && d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		file := File{Path: filepath.ToSlash(rel), Mode: info.Mode()}
		switch {
		case info.IsDir():
		case info.Mode()&fs.ModeSymlink != 0:
			if _, err := safePath(root, rel); err != nil {
				return err
			}
			file.Link, err = os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(file.Link) {
				return fmt.Errorf("native payload symlink %q must be relative", rel)
			}
		case info.Mode().IsRegular():
			file.Content, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported native payload file type: %s", rel)
		}
		files = append(files, file)
		return nil
	})
	return files, err
}

// WriteFiles writes a fresh package tree. Callers stage this before replacing
// an installed generation. It executes no plugin code.
func WriteFiles(root string, files []File) error {
	if err := ValidateFiles(files); err != nil {
		return err
	}
	var directories []File
	for _, file := range files {
		if !filepath.IsLocal(file.Path) {
			return fmt.Errorf("payload path %q escapes package", file.Path)
		}
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		switch {
		case file.Mode.IsDir():
			// Finish directory permissions after children are written, so
			// read-only source directories can still be staged completely.
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
			directories = append(directories, file)
		case file.Mode&fs.ModeSymlink != 0:
			if filepath.IsAbs(file.Link) || !filepath.IsLocal(filepath.Join(filepath.Dir(file.Path), file.Link)) {
				return fmt.Errorf("payload symlink %q escapes package", file.Path)
			}
			if err := os.Symlink(file.Link, path); err != nil {
				return err
			}
		case file.Mode.IsRegular():
			if err := os.WriteFile(path, file.Content, 0o600); err != nil {
				return err
			}
			if err := os.Chmod(path, file.Mode); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported payload file type: %s", file.Path)
		}
	}
	for i := len(directories) - 1; i >= 0; i-- {
		file := directories[i]
		if err := os.Chmod(filepath.Join(root, filepath.FromSlash(file.Path)), file.Mode); err != nil {
			return err
		}
	}
	return nil
}

// ValidateFiles rejects ambiguous layouts before a fresh tree is written.
// Package entries cannot write children through another entry's symlink.
func ValidateFiles(files []File) error {
	entries := map[string]File{}
	for _, file := range files {
		path := filepath.FromSlash(file.Path)
		if !filepath.IsLocal(path) || path == "." || filepath.Clean(path) != path {
			return fmt.Errorf("payload path %q escapes package or is not canonical", file.Path)
		}
		if _, exists := entries[path]; exists {
			return fmt.Errorf("duplicate payload path %q", file.Path)
		}
		switch {
		case file.Mode.IsDir(), file.Mode.IsRegular():
			if file.Link != "" {
				return fmt.Errorf("non-symlink payload %q has a link target", file.Path)
			}
		case file.Mode&fs.ModeSymlink != 0:
			if file.Link == "" || filepath.IsAbs(file.Link) || !filepath.IsLocal(filepath.Join(filepath.Dir(path), file.Link)) {
				return fmt.Errorf("payload symlink %q escapes package", file.Path)
			}
		default:
			return fmt.Errorf("unsupported payload file type: %s", file.Path)
		}
		entries[path] = file
	}
	for path := range entries {
		for parent := filepath.Dir(path); parent != "."; parent = filepath.Dir(parent) {
			if entry, exists := entries[parent]; exists && !entry.Mode.IsDir() {
				return fmt.Errorf("payload path %q traverses non-directory %q", path, parent)
			}
		}
		if strings.ContainsRune(path, 0) {
			return fmt.Errorf("invalid payload path %q", path)
		}
	}
	return nil
}

// MaterializeCodex preserves the entire package under upstream/ and emits
// only AIPack metadata at its root. The native manifests are unchanged.
func MaterializeCodex(src, dst, name, marketplace string) (config.PackManifest, error) {
	manifest, err := ReadCodex(src, marketplace)
	if err != nil {
		return config.PackManifest{}, err
	}
	return materialize(src, dst, name, manifest)
}

func MaterializeClaude(src, dst, name string, spec domain.PluginSource) (config.PackManifest, error) {
	manifest, err := ReadClaude(src, spec)
	if err != nil {
		return config.PackManifest{}, err
	}
	return materialize(src, dst, name, manifest)
}

func materialize(src, dst, name string, manifest config.PackManifest) (config.PackManifest, error) {
	if name != "" {
		manifest.Name = name
	}
	files, err := ReadFiles(src)
	if err != nil {
		return config.PackManifest{}, err
	}
	if err := WriteFiles(filepath.Join(dst, "upstream"), files); err != nil {
		return config.PackManifest{}, err
	}
	if err := config.SavePackManifest(filepath.Join(dst, "pack.json"), manifest); err != nil {
		return config.PackManifest{}, err
	}
	return manifest, nil
}

func selected(selection domain.NativePluginSelection, cat domain.PackCategory, id string) bool {
	return slices.Contains(selection.Selected[cat], id)
}
