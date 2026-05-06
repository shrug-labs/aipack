package engine

import (
	"fmt"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// Resolve produces a fully-typed domain.Profile from a ProfileConfig.
// It resolves pack sources, parses all content into typed structs,
// builds MCP servers with params expanded, and loads harness settings.
//
// This is the single entry point for profile resolution. It absorbs:
//   - config.ResolveProfile (validation, pack resolution, selector application)
//   - resolvePackContent (rule/agent/workflow/skill parsing)
//   - buildMCPServers (MCP resolution + param expansion)
//   - loadHarnessSettings (settings + drop-in config loading)
func (e *Engine) Resolve(
	profileCfg config.ProfileConfig,
	profilePath string,
	configDir string,
	collisionStrategy config.CollisionStrategy,
	prevInventories map[string]domain.PackInventory,
) (domain.Profile, []domain.Warning, error) {
	// Step 1: Validate profile, resolve packs, apply selectors, check overrides.
	resolved, err := config.ResolveProfile(profileCfg, profilePath, configDir, collisionStrategy, prevInventories)
	if err != nil {
		return domain.Profile{}, nil, err
	}
	resolvedPacks, settingsPacks := resolved.Packs, resolved.SettingsPacks

	env, err := config.LoadDotEnv(config.DotEnvPath(configDir))
	if err != nil {
		return domain.Profile{}, nil, err
	}

	var warnings []domain.Warning
	warnings = append(warnings, resolved.CollisionWarnings...)
	warnings = append(warnings, brokenRefWarnings(resolved.BrokenRefs)...)

	// Step 2: Parse content per pack into typed domain structs.
	packs, contentWarnings, err := e.resolvePackContent(resolvedPacks)
	if err != nil {
		return domain.Profile{}, warnings, err
	}
	warnings = append(warnings, contentWarnings...)

	// Step 3: Build MCP servers (load inventory, expand params, apply permissions).
	mcpServers, mcpWarnings, err := e.resolveMCPServers(resolvedPacks, profileCfg.Params, env)
	if err != nil {
		return domain.Profile{}, warnings, err
	}
	warnings = append(warnings, mcpWarnings...)

	// Step 4: Load harness settings for all harnesses.
	allH := domain.AllHarnesses()
	settings, settingsWarnings, err := e.loadHarnessSettingsWithEnv(resolvedPacks, settingsPacks, allH, profileCfg.Params, env)
	if err != nil {
		return domain.Profile{}, warnings, err
	}
	warnings = append(warnings, settingsWarnings...)

	p := domain.NewProfile()
	p.Params = profileCfg.Params
	p.Packs = packs
	p.MCPServers = mcpServers
	p.BaseSettings = settings
	p.SettingsPacks = settingsPacks
	p.BrokenRefs = resolved.BrokenRefs
	return p, warnings, nil
}

// brokenRefWarnings converts structured BrokenRef entries into user-facing
// Warning entries so they flow through the standard warnings printer.
// The structured BrokenRefs are also retained on domain.Profile so drift
// detection can surface them in its "affects your profile" section
// without re-parsing messages.
func brokenRefWarnings(refs []domain.BrokenRef) []domain.Warning {
	if len(refs) == 0 {
		return nil
	}
	out := make([]domain.Warning, 0, len(refs))
	for _, br := range refs {
		var location string
		switch {
		case br.Direction == "overrides":
			location = string(br.Category) + ".overrides"
		case br.Category == domain.CategoryMCP:
			location = "mcp." + br.ID
		case br.Direction != "":
			location = string(br.Category) + "." + br.Direction
		default:
			location = string(br.Category)
		}
		out = append(out, domain.Warning{
			Field: "broken-ref",
			Message: fmt.Sprintf(
				"pack %q %s references %q — present in previous lockfile inventory but removed from current pack contents",
				br.PackName, location, br.ID,
			),
		})
	}
	return out
}

// resolvePackContent parses all content from resolved packs into typed Pack structs.
func (e *Engine) resolvePackContent(resolvedPacks []config.ResolvedPack) ([]domain.Pack, []domain.Warning, error) {
	var packs []domain.Pack
	var warnings []domain.Warning

	for _, rp := range resolvedPacks {
		rules, w, err := e.parseRules(rp)
		if err != nil {
			return nil, warnings, err
		}
		warnings = append(warnings, w...)

		agents, w, err := e.parseAgents(rp)
		if err != nil {
			return nil, warnings, err
		}
		warnings = append(warnings, w...)

		workflows, w, err := e.parseWorkflows(rp)
		if err != nil {
			return nil, warnings, err
		}
		warnings = append(warnings, w...)

		skills, w, err := e.parseSkills(rp)
		if err != nil {
			return nil, warnings, err
		}
		warnings = append(warnings, w...)

		plugins, w, err := e.parsePlugins(rp)
		if err != nil {
			return nil, warnings, err
		}
		warnings = append(warnings, w...)

		packs = append(packs, domain.Pack{
			Name:       rp.Name,
			Version:    rp.Manifest.Version,
			Root:       rp.Root,
			Rules:      rules,
			Agents:     agents,
			Workflows:  workflows,
			Skills:     skills,
			Plugins:    plugins,
			Registries: rp.Manifest.Registries,
		})
	}

	return packs, warnings, nil
}

// resolveMCPServers loads MCP inventory from packs and builds typed servers.
func (e *Engine) resolveMCPServers(packs []config.ResolvedPack, params map[string]string, env map[string]string) ([]domain.MCPServer, []domain.Warning, error) {
	inv, err := e.LoadMCPInventoryForPacks(packs)
	if err != nil {
		return nil, nil, err
	}
	servers, warnings := buildMCPServers(params, env, packs, inv)
	return servers, warnings, nil
}
