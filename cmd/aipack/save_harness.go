package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

func resolveSaveHarnesses(harness string) ([]domain.Harness, error) {
	h := strings.ToLower(strings.TrimSpace(harness))
	if h == "all" {
		return domain.AllHarnesses(), nil
	}
	if h != "" {
		one, err := cmdutil.NormalizeHarness(h)
		if err != nil {
			return nil, err
		}
		return []domain.Harness{one}, nil
	}

	if d, err := config.DefaultConfigDir(os.Getenv("HOME")); err == nil {
		syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(d))
		if err != nil {
			return nil, err
		}
		var out []domain.Harness
		for _, h := range syncCfg.Defaults.Harnesses {
			if strings.TrimSpace(h) == "" {
				continue
			}
			one, err := cmdutil.NormalizeHarness(h)
			if err != nil {
				return nil, err
			}
			out = append(out, one)
		}
		if len(out) > 0 {
			return out, nil
		}
	}

	var envOut []domain.Harness
	for _, h := range cmdutil.ParseHarnessEnv(os.Getenv(cmdutil.DefaultHarnessEnv)) {
		one, err := cmdutil.NormalizeHarness(h)
		if err != nil {
			return nil, err
		}
		envOut = append(envOut, one)
	}
	if len(envOut) > 0 {
		return envOut, nil
	}

	return nil, fmt.Errorf("--harness is required (or configure defaults.harnesses in ~/.config/aipack/sync-config.yaml)")
}
