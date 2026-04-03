package engine

import (
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
) (domain.Profile, []domain.Warning, error) {
	// Step 1: Validate profile, resolve packs, apply selectors, check overrides.
	resolvedPacks, settingsPacks, err := config.ResolveProfile(profileCfg, profilePath, configDir)
	if err != nil {
		return domain.Profile{}, nil, err
	}

	var warnings []domain.Warning

	// Step 2: Parse content per pack into typed domain structs.
	packs, contentWarnings, err := e.resolvePackContent(resolvedPacks)
	if err != nil {
		return domain.Profile{}, warnings, err
	}
	warnings = append(warnings, contentWarnings...)

	// Step 3: Build MCP servers (load inventory, expand params, apply permissions).
	mcpServers, mcpWarnings, err := e.resolveMCPServers(resolvedPacks, profileCfg.Params)
	if err != nil {
		return domain.Profile{}, warnings, err
	}
	warnings = append(warnings, mcpWarnings...)

	// Step 4: Load harness settings for all harnesses.
	allH := domain.AllHarnesses()
	settings, settingsWarnings, err := e.loadHarnessSettings(resolvedPacks, settingsPacks, allH)
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
	return p, warnings, nil
}

// resolvePackContent parses all content from resolved packs into typed Pack structs.
func (e *Engine) resolvePackContent(resolvedPacks []config.ResolvedPack) ([]domain.Pack, []domain.Warning, error) {
	var packs []domain.Pack
	var warnings []domain.Warning

	for _, rp := range resolvedPacks {
		rules, w, err := e.parseRules(rp.Root, rp.Rules, rp.Name)
		if err != nil {
			return nil, warnings, err
		}
		warnings = append(warnings, w...)

		agents, w, err := e.parseAgents(rp.Root, rp.Agents, rp.Name)
		if err != nil {
			return nil, warnings, err
		}
		warnings = append(warnings, w...)

		workflows, w, err := e.parseWorkflows(rp.Root, rp.Workflows, rp.Name)
		if err != nil {
			return nil, warnings, err
		}
		warnings = append(warnings, w...)

		skills, w, err := e.parseSkills(rp.Root, rp.Skills, rp.Name)
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
			Registries: rp.Manifest.Registries,
		})
	}

	return packs, warnings, nil
}

// resolveMCPServers loads MCP inventory from packs and builds typed servers.
func (e *Engine) resolveMCPServers(packs []config.ResolvedPack, params map[string]string) ([]domain.MCPServer, []domain.Warning, error) {
	inv, err := e.LoadMCPInventoryForPacks(packs)
	if err != nil {
		return nil, nil, err
	}
	servers, warnings := buildMCPServers(params, packs, inv)
	return servers, warnings, nil
}
