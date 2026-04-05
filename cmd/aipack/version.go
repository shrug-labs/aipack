package main

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/update"
	"github.com/shrug-labs/aipack/internal/util"
)

// Set via -ldflags "-X main.version=<version> -X main.commit=<sha>".
var (
	version       = "dev"
	commit        = "unknown"
	readBuildInfo = debug.ReadBuildInfo
)

type VersionCmd struct{}

func (c *VersionCmd) Run(ctx context.Context, g *Globals) error {
	resolvedVersion, resolvedCommit := resolveVersionInfo(version, commit)
	updateCh := update.CheckAsync(ctx, resolvedVersion, config.HomeDir())

	fmt.Fprintf(g.Stdout, "aipack %s (%s)\n", resolvedVersion, resolvedCommit)

	select {
	case res := <-updateCh:
		if notice := res.Notice(); notice != "" {
			fmt.Fprint(g.Stderr, notice)
		}
	case <-time.After(2 * time.Second):
	}
	return nil
}

func resolveVersionInfo(currentVersion, currentCommit string) (string, string) {
	resolvedVersion := currentVersion
	resolvedCommit := currentCommit

	info, ok := readBuildInfo()
	if !ok || info == nil {
		return resolvedVersion, resolvedCommit
	}

	if resolvedVersion == "dev" {
		if v := normalizeBuildVersion(info.Main.Version); v != "" {
			resolvedVersion = v
		}
	}
	if resolvedCommit == "unknown" {
		if c := buildSetting(info, "vcs.revision"); c != "" {
			resolvedCommit = util.ShortHash(c)
		}
	}
	if buildSetting(info, "vcs.modified") == "true" && !strings.HasSuffix(resolvedCommit, "-dirty") {
		resolvedCommit += "-dirty"
	}

	return resolvedVersion, resolvedCommit
}

func normalizeBuildVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "(devel)" {
		return ""
	}
	return strings.TrimPrefix(v, "v")
}

func buildSetting(info *debug.BuildInfo, key string) string {
	for _, setting := range info.Settings {
		if setting.Key == key {
			return strings.TrimSpace(setting.Value)
		}
	}
	return ""
}
