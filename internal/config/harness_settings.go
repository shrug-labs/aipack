package config

import (
	"errors"
	"fmt"
	"strings"
)

// HarnessSettingsPackForHarness finds the resolved pack that provides base
// settings. settingsPack is the single pack name selected during profile
// resolution; harness is used only to filter packs that actually declare
// settings/plugins files for that harness.
func HarnessSettingsPackForHarness(packs []ResolvedPack, settingsPack string, harness string) (ResolvedPack, error) {
	h := strings.ToLower(strings.TrimSpace(harness))
	if h == "" {
		return ResolvedPack{}, errors.New("empty harness")
	}
	if len(packs) == 0 {
		return ResolvedPack{}, errors.New("no packs resolved")
	}
	if settingsPack == "" {
		return ResolvedPack{}, fmt.Errorf("no settings pack configured for profile")
	}

	for _, p := range packs {
		if p.Name != settingsPack {
			continue
		}
		// Verify this pack actually has settings or plugin files for the harness.
		if len(p.Manifest.Configs.HarnessSettings[h]) == 0 && len(p.Manifest.Configs.HarnessPlugins[h]) == 0 {
			return ResolvedPack{}, fmt.Errorf("settings pack %q has no settings or plugins for harness %q", settingsPack, h)
		}
		return p, nil
	}
	return ResolvedPack{}, fmt.Errorf("settings pack %q not found in resolved packs", settingsPack)
}
