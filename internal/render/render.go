package render

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
	"github.com/shrug-labs/aipack/internal/util"
)

// Run renders pack outputs to a fresh system temp directory and returns the
// output directory path.
func Run(profile domain.Profile, harnesses []harness.Harness) (string, error) {
	outDir, err := os.MkdirTemp("", "aipack-render-")
	if err != nil {
		return "", err
	}
	if err := RunToDir(profile, outDir, harnesses); err != nil {
		_ = os.RemoveAll(outDir)
		return "", err
	}
	return outDir, nil
}

// RunToDir renders all pack content into outDir using the v2 harness Render methods.
// Each harness produces a Fragment of writes; all writes are applied atomically.
func RunToDir(profile domain.Profile, outDir string, harnesses []harness.Harness) error {
	outDirAbs, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}
	if err := validateOutDir(profile, outDirAbs); err != nil {
		return err
	}

	parent := filepath.Dir(outDirAbs)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}

	// Render to a temp sibling and swap into place for atomicity.
	tmpDir := filepath.Join(parent, fmt.Sprintf(".%s.tmp-%d-%08x", filepath.Base(outDirAbs), os.Getpid(), rand.Uint32()))
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	ctx := harness.RenderContext{
		OutDir:  tmpDir,
		Profile: profile,
	}

	for _, h := range harnesses {
		frag, err := h.Render(ctx)
		if err != nil {
			return err
		}
		for _, w := range frag.Writes {
			if err := util.WriteFileAtomic(w.Dst, w.Content); err != nil {
				return err
			}
		}
	}

	// Swap into place (atomic within filesystem).
	backupDir := ""
	if st, err := os.Lstat(outDirAbs); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to replace out-dir symlink: %s", outDirAbs)
		}
		if !st.IsDir() {
			return fmt.Errorf("out-dir exists and is not a directory: %s", outDirAbs)
		}
		backupDir = filepath.Join(parent, fmt.Sprintf(".%s.bak-%s-%08x", filepath.Base(outDirAbs), time.Now().UTC().Format("20060102T150405Z"), rand.Uint32()))
		if err := os.Rename(outDirAbs, backupDir); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpDir, outDirAbs); err != nil {
		if backupDir != "" {
			_ = os.Rename(backupDir, outDirAbs)
		}
		return err
	}
	if backupDir != "" {
		if err := os.RemoveAll(backupDir); err != nil {
			return err
		}
	}
	return nil
}

func validateOutDir(profile domain.Profile, outDirAbs string) error {
	// Refuse to render into any pack source directory.
	for _, pack := range profile.Packs {
		packRoot := filepath.Clean(pack.Root)
		if filepath.Clean(outDirAbs) == packRoot || domain.IsUnder(outDirAbs, packRoot) {
			return fmt.Errorf("refusing to render into pack content dir: out-dir=%s", outDirAbs)
		}
	}

	if st, err := os.Lstat(outDirAbs); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to render into out-dir symlink: %s", outDirAbs)
		}
		if !st.IsDir() {
			return fmt.Errorf("out-dir exists and is not a directory: %s", outDirAbs)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}
