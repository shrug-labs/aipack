package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/util"
)

// InitRequest holds the inputs for an init operation.
type InitRequest struct {
	ConfigDir string
	Force     bool
}

// RunInit initializes the user's config directory with a sync-config.yaml and
// an empty default profile.
func RunInit(req InitRequest, stdout io.Writer) error {
	if strings.TrimSpace(req.ConfigDir) == "" {
		return fmt.Errorf("config dir is required")
	}

	if st, err := os.Stat(req.ConfigDir); err == nil {
		if !st.IsDir() {
			return fmt.Errorf("config dir is not a directory: %s", req.ConfigDir)
		}
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(req.ConfigDir, 0o700); err != nil {
			return err
		}
	} else {
		return err
	}

	syncConfigPath := config.SyncConfigPath(req.ConfigDir)
	if err := writeInitFile(syncConfigPath, config.InitSyncConfigBytes, req.Force, stdout); err != nil {
		return err
	}

	destProfilePath := filepath.Join(req.ConfigDir, "profiles", "default.yaml")
	if err := writeInitFile(destProfilePath, config.InitProfileBytes, req.Force, stdout); err != nil {
		return err
	}

	registryPath := filepath.Join(req.ConfigDir, "registry.yaml")
	if err := writeInitFile(registryPath, config.InitRegistryBytes, req.Force, stdout); err != nil {
		return err
	}

	return nil
}

func writeInitFile(path string, content []byte, force bool, stdout io.Writer) error {
	st, err := os.Stat(path)
	if err == nil {
		if !st.Mode().IsRegular() {
			return fmt.Errorf("path exists and is not a file: %s", path)
		}
		if !force {
			fmt.Fprintf(stdout, "Skip: %s exists (pass --force to overwrite)\n", path)
			return nil
		}
		fmt.Fprintf(stdout, "Overwriting: %s\n", path)
		return util.WriteFileAtomicWithPerms(path, content, 0o700, 0o600)
	}
	if !os.IsNotExist(err) {
		return err
	}

	fmt.Fprintf(stdout, "Creating: %s\n", path)
	return util.WriteFileAtomicWithPerms(path, content, 0o700, 0o600)
}
