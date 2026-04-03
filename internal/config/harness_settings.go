package config

import "strings"

// HarnessSettingsPacksForHarness returns all resolved packs that contribute
// settings or plugin files for the given harness, in profile order.
// settingsPacks is the ordered list of contributing pack names from ResolveProfile.
func HarnessSettingsPacksForHarness(packs []ResolvedPack, settingsPacks []string, harness string) []ResolvedPack {
	h := strings.ToLower(strings.TrimSpace(harness))
	if h == "" || len(packs) == 0 || len(settingsPacks) == 0 {
		return nil
	}

	packByName := map[string]ResolvedPack{}
	for _, p := range packs {
		packByName[p.Name] = p
	}

	var result []ResolvedPack
	for _, name := range settingsPacks {
		p, ok := packByName[name]
		if !ok {
			continue
		}
		if len(p.Manifest.Configs.HarnessSettings[h]) == 0 && len(p.Manifest.Configs.HarnessPlugins[h]) == 0 {
			continue
		}
		result = append(result, p)
	}
	return result
}
