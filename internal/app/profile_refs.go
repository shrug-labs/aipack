package app

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/util"
)

type ProfileRefsRequest struct {
	ConfigDir    string
	ProfileName  string
	ProfilePath  string
	Collision    config.CollisionStrategy
	Namespaced   bool
	ProcessEnvFn func(string) (string, bool)
}

type ProfileRefsResult struct {
	Profile     string       `json:"profile"`
	ProfilePath string       `json:"profile_path"`
	Refs        []ProfileRef `json:"refs"`
}

type ProfileRef struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Display    string `json:"display"`
	Status     string `json:"status"`
	HasDefault bool   `json:"has_default,omitempty"`
	Source     string `json:"source,omitempty"`
	Pack       string `json:"pack,omitempty"`
	Target     string `json:"target,omitempty"`
	Location   string `json:"location,omitempty"`
}

type ProfileParamRequest struct {
	ConfigDir   string
	ProfileName string
	Key         string
	Value       string
	Stdout      io.Writer
}

// ProfileRefs reports profile parameter and environment references used by
// enabled MCP servers and contributing harness settings.
func ProfileRefs(req ProfileRefsRequest) (ProfileRefsResult, error) {
	if req.ConfigDir == "" {
		return ProfileRefsResult{}, fmt.Errorf("config dir is required")
	}
	profileName := strings.TrimSpace(req.ProfileName)
	if sc, err := config.LoadSyncConfig(config.SyncConfigPath(req.ConfigDir)); err == nil {
		if profileName == "" {
			profileName = strings.TrimSpace(sc.Defaults.Profile)
		}
		if req.Collision == "" {
			req.Collision = sc.Defaults.CollisionStrategy
		}
		req.Namespaced = req.Namespaced || sc.Defaults.Namespaced
	}
	if profileName == "" {
		profileName = "default"
	}
	profilePath, err := config.ResolveProfilePath(req.ProfilePath, req.ConfigDir, profileName, config.HomeDir())
	if err != nil {
		return ProfileRefsResult{}, err
	}
	profileCfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return ProfileRefsResult{}, err
	}
	resolved, err := config.ResolveProfileWithOptions(profileCfg, profilePath, req.ConfigDir, config.ResolveOptions{
		CollisionStrategy: req.Collision,
		Namespaced:        req.Namespaced,
	})
	if err != nil {
		return ProfileRefsResult{}, err
	}
	dotenv, err := config.LoadDotEnv(config.DotEnvPath(req.ConfigDir))
	if err != nil {
		return ProfileRefsResult{}, err
	}
	lookup := req.ProcessEnvFn
	if lookup == nil {
		lookup = os.LookupEnv
	}

	var refs []ProfileRef
	addRef := func(ref ProfileRef) {
		ref.Status = profileRefStatus(ref, profileCfg.Params, dotenv, lookup)
		refs = append(refs, ref)
	}
	scanString := func(s string, pack, target, location string) error {
		if err := util.WalkParamRefs(s, func(pr util.ParamRef) error {
			name, _, hasDefault := strings.Cut(pr.Name, ":-")
			if name == "" {
				return nil
			}
			addRef(ProfileRef{
				Kind:       domain.RefKindParam,
				Name:       name,
				Display:    pr.Prefix + pr.Name + "}",
				HasDefault: hasDefault,
				Pack:       pack,
				Target:     target,
				Location:   location,
			})
			return nil
		}); err != nil {
			return fmt.Errorf("scanning %s: %w", location, err)
		}
		if err := util.WalkEnvRefs(s, func(er util.EnvRef) error {
			if er.Name == "" {
				return nil
			}
			display := "{env:" + er.Name + "}"
			if er.HasDefault {
				display = "{env:" + er.Name + ":-" + er.Default + "}"
			}
			addRef(ProfileRef{
				Kind:       domain.RefKindEnv,
				Name:       er.Name,
				Display:    display,
				HasDefault: er.HasDefault,
				Pack:       pack,
				Target:     target,
				Location:   location,
			})
			return nil
		}); err != nil {
			return fmt.Errorf("scanning %s: %w", location, err)
		}
		return nil
	}

	eng := engine.New(nil, nil)
	inv, err := eng.LoadMCPInventoryForPacks(resolved.Packs)
	if err != nil {
		return ProfileRefsResult{}, err
	}
	// Pre-build server → pack provider map. Without this, profileMCPProvider
	// walks all packs per server, which was O(N_servers × N_packs).
	serverProvider := buildMCPProviderMap(resolved.Packs)
	serverNames := slices.Sorted(maps.Keys(inv))
	for _, server := range serverNames {
		entry := inv[server]
		pack := serverProvider[server]
		for i, part := range entry.Command {
			if err := scanString(part, pack, server, fmt.Sprintf("mcp.%s.command[%d]", server, i)); err != nil {
				return ProfileRefsResult{}, err
			}
		}
		for key, val := range entry.Env {
			if err := scanString(val, pack, server, fmt.Sprintf("mcp.%s.env.%s", server, key)); err != nil {
				return ProfileRefsResult{}, err
			}
		}
		if entry.URL != "" {
			if err := scanString(entry.URL, pack, server, fmt.Sprintf("mcp.%s.url", server)); err != nil {
				return ProfileRefsResult{}, err
			}
		}
		for key, val := range entry.Headers {
			if err := scanString(val, pack, server, fmt.Sprintf("mcp.%s.headers.%s", server, key)); err != nil {
				return ProfileRefsResult{}, err
			}
		}
	}

	settingsPack := map[string]struct{}{}
	for _, name := range resolved.SettingsPacks {
		settingsPack[name] = struct{}{}
	}
	for _, pack := range resolved.Packs {
		if _, ok := settingsPack[pack.Name]; ok {
			for harness, files := range pack.Manifest.Configs.HarnessSettings {
				for _, f := range files {
					path := filepath.Join(pack.Root, "configs", harness, f)
					b, err := os.ReadFile(path)
					if err != nil {
						continue
					}
					if err := scanString(string(b), pack.Name, harness, "configs."+harness+"."+f); err != nil {
						return ProfileRefsResult{}, err
					}
				}
			}
		}
		for _, id := range pack.Hooks {
			path := filepath.Join(pack.Root, filepath.FromSlash(pack.Manifest.RelPath(domain.CategoryHooks, id)))
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if err := scanString(string(b), pack.Name, id, "hooks."+id); err != nil {
				return ProfileRefsResult{}, err
			}
		}
	}

	slices.SortFunc(refs, func(a, b ProfileRef) int {
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		if a.Name != b.Name {
			return strings.Compare(a.Name, b.Name)
		}
		if a.Location != b.Location {
			return strings.Compare(a.Location, b.Location)
		}
		return strings.Compare(a.Target, b.Target)
	})
	return ProfileRefsResult{Profile: profileName, ProfilePath: profilePath, Refs: refs}, nil
}

func ProfileSetParam(req ProfileParamRequest) error {
	return mutateProfileParam(req, func(cfg *config.ProfileConfig, key string) {
		if cfg.Params == nil {
			cfg.Params = map[string]string{}
		}
		cfg.Params[key] = req.Value
	})
}

func ProfileUnsetParam(req ProfileParamRequest) error {
	return mutateProfileParam(req, func(cfg *config.ProfileConfig, key string) {
		delete(cfg.Params, key)
		if len(cfg.Params) == 0 {
			cfg.Params = nil
		}
	})
}

func mutateProfileParam(req ProfileParamRequest, fn func(*config.ProfileConfig, string)) error {
	if req.ConfigDir == "" {
		return fmt.Errorf("config dir is required")
	}
	name, err := config.NormalizeProfileName(req.ProfileName)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(req.Key)
	if !validProfileParamKey(key) {
		return fmt.Errorf("invalid param key %q", req.Key)
	}
	cfg, _, _, err := loadProfileParamsConfig(ProfileParamsRequest{ConfigDir: req.ConfigDir, ProfileName: name})
	if err != nil {
		return err
	}
	fn(&cfg, key)
	if req.Stdout != nil {
		warnBundledProfileEdit(req.ConfigDir, name, req.Stdout)
	}
	return ProfileSave(ProfileSaveRequest{ConfigDir: req.ConfigDir, Name: name, Config: cfg})
}

func validProfileParamKey(key string) bool {
	return key != "" && !strings.ContainsAny(key, "{} \t\r\n")
}

func profileRefStatus(ref ProfileRef, params, dotenv map[string]string, lookup func(string) (string, bool)) string {
	switch ref.Kind {
	case domain.RefKindParam:
		if _, ok := params[ref.Name]; ok {
			return "set"
		}
		if ref.HasDefault {
			return "defaulted"
		}
		return "missing"
	case domain.RefKindEnv:
		if _, ok := dotenv[ref.Name]; ok {
			return "dotenv"
		}
		if _, ok := lookup(ref.Name); ok {
			return "env"
		}
		if ref.HasDefault {
			return "defaulted"
		}
		return "missing"
	default:
		return "unknown"
	}
}

// buildMCPProviderMap returns server name → declaring-pack name. Walks packs
// in install order so the last writer wins, matching the previous semantics
// of profileMCPProvider's reverse scan.
func buildMCPProviderMap(packs []config.ResolvedPack) map[string]string {
	out := make(map[string]string)
	for _, p := range packs {
		for server := range p.MCP {
			out[server] = p.Name
		}
	}
	return out
}
