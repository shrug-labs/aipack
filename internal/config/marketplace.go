package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	sourcepkg "github.com/shrug-labs/aipack/internal/source"
	"github.com/shrug-labs/aipack/internal/util"
)

func ValidateMarketplaceFormat(format string) error {
	switch format {
	case "", "claude", "codex-legacy", "agent-plugins":
		return nil
	default:
		return fmt.Errorf("unsupported marketplace format %q (expected claude, codex-legacy, or agent-plugins)", format)
	}
}

// ParseMarketplace normalizes a native catalog without fetching or executing
// packages. Unsupported sources stay discoverable with their exact limitation.
func ParseMarketplace(data []byte, source RegistrySourceEntry) (Registry, error) {
	if err := ValidateMarketplaceFormat(source.Format); err != nil {
		return Registry{}, err
	}
	var catalog struct {
		Name    string            `json:"name"`
		Plugins []json.RawMessage `json:"plugins"`
	}
	if err := util.UnmarshalJSON(data, &catalog); err != nil {
		return Registry{}, fmt.Errorf("parsing marketplace: %w", err)
	}
	if !domain.ValidNativeName(catalog.Name) || catalog.Plugins == nil {
		return Registry{}, fmt.Errorf("marketplace requires a valid name and plugins array")
	}
	var metadata map[string]any
	if err := util.UnmarshalJSON(data, &metadata); err != nil {
		return Registry{}, err
	}
	delete(metadata, "plugins")
	reg := Registry{SchemaVersion: RegistrySchemaVersion, Packs: map[string]RegistryEntry{}}
	for _, body := range catalog.Plugins {
		var raw map[string]any
		if err := util.UnmarshalJSON(body, &raw); err != nil {
			return Registry{}, err
		}
		name, _ := raw["name"].(string)
		if !domain.ValidNativeName(name) {
			return Registry{}, fmt.Errorf("invalid marketplace plugin name %q", name)
		}
		if _, exists := reg.Packs[name]; exists {
			return Registry{}, fmt.Errorf("duplicate marketplace plugin %q", name)
		}
		entry := RegistryEntry{Repo: source.URL, Ref: source.Ref, Plugin: &domain.PluginSource{Name: name, Marketplace: catalog.Name, MarketplaceURL: source.URL, MarketplacePath: source.Path, MarketplaceRef: source.Ref, Entry: maps.Clone(raw), MarketplaceMetadata: maps.Clone(metadata)}}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			return Registry{}, err
		}
		if _, ok := raw["commands"].(map[string]any); ok {
			order, err := util.JSONPropertyNames(fields["commands"])
			if err != nil {
				return Registry{}, err
			}
			entry.Plugin.CatalogCommandOrder = order
		}
		catalogPath := filepath.ToSlash(source.Path)
		if catalogPath == "" && sourcepkg.IsHTTPURL(source.URL) {
			if parsed, err := url.Parse(source.URL); err == nil {
				catalogPath = strings.TrimPrefix(parsed.Path, "/")
			}
		}
		entry.Plugin.Format = source.Format
		if source.Format == "" && (catalogPath == ".claude-plugin/marketplace.json" || strings.HasSuffix(catalogPath, "/.claude-plugin/marketplace.json")) {
			entry.Plugin.Format = "claude"
		}
		entry.Description, _ = raw["description"].(string)
		if author, ok := raw["author"].(map[string]any); ok {
			entry.Owner, _ = author["name"].(string)
			entry.Contact, _ = author["email"].(string)
		}
		if path, ok := raw["source"].(string); ok {
			entry.Path = path
		} else if obj, ok := raw["source"].(map[string]any); ok {
			kind, _ := obj["source"].(string)
			entry.Path, _ = obj["path"].(string)
			switch kind {
			case "local":
			case "npm":
				entry.Path = ""
				npm := &domain.NPMSource{}
				for _, field := range []struct {
					key    string
					target *string
				}{{"package", &npm.Package}, {"version", &npm.Version}, {"registry", &npm.Registry}} {
					value, present := obj[field.key]
					if !present || value == nil && field.key != "package" {
						continue
					}
					text, ok := value.(string)
					if ok && text == "" && field.key == "version" && entry.Plugin.Format == "claude" {
						continue
					}
					if !ok || strings.TrimSpace(text) == "" {
						entry.Unsupported = fmt.Sprintf("npm %s must be a nonempty string", field.key)
						break
					}
					*field.target = strings.TrimSpace(text)
				}
				if entry.Unsupported != "" {
					break
				}
				var npmErr error
				if entry.Plugin.Format == "claude" {
					*npm, npmErr = sourcepkg.NormalizeClaudeNPMSource(*npm)
				} else {
					npmErr = sourcepkg.ValidateNPMSource(*npm)
				}
				if npmErr != nil {
					entry.Unsupported = npmErr.Error()
					break
				}
				entry.Method, entry.Repo, entry.Ref, entry.Path, entry.Plugin.NPM = MethodNPM, "npm:"+npm.Package, npm.Version, "", npm
			case "github", "url", "git-subdir":
				if kind == "url" && entry.Plugin.Format == "claude" {
					entry.Path = ""
				}
				if kind == "github" {
					entry.Path = ""
					if entry.Plugin.Format != "claude" {
						entry.Unsupported = "github source objects require a Claude marketplace; use a Git URL source for Codex"
						break
					}
					repo, _ := obj["repo"].(string)
					var err error
					entry.Repo, err = claudeGitHubRepo(repo)
					if err != nil {
						entry.Unsupported = err.Error()
						break
					}
				} else {
					var ok bool
					entry.Repo, ok = obj["url"].(string)
					if !ok || entry.Repo == "" {
						entry.Unsupported = "git source url must be a nonempty string"
						break
					}
					if entry.Plugin.Format != "claude" {
						var err error
						entry.Repo, err = codexGitRepo(entry.Repo, source)
						if err != nil {
							entry.Unsupported = err.Error()
							break
						}
					} else if kind == "git-subdir" && !sourcepkg.LooksLikeURL(entry.Repo) {
						var err error
						entry.Repo, err = claudeGitHubRepo(entry.Repo)
						if err != nil {
							entry.Unsupported = err.Error()
							break
						}
					}
				}
				for _, field := range []string{"ref", "sha"} {
					if value, present := obj[field]; present && (value != nil || entry.Plugin.Format == "claude") {
						text, ok := value.(string)
						if !ok {
							entry.Unsupported = fmt.Sprintf("git source %s must be a string", field)
							break
						}
						if entry.Plugin.Format == "claude" && field == "sha" && (len(text) != 40 || strings.Trim(text, "0123456789abcdef") != "") {
							entry.Unsupported = "Claude git source sha must be a full 40-character lowercase commit SHA"
							break
						}
					}
				}
				if entry.Unsupported != "" {
					break
				}
				entry.Ref, _ = obj["ref"].(string)
				sha, _ := obj["sha"].(string)
				if entry.Plugin.Format != "claude" {
					entry.Ref, sha = strings.TrimSpace(entry.Ref), strings.TrimSpace(sha)
					if sha != "" && (len(sha) != 40 || strings.Trim(sha, "0123456789abcdefABCDEF") != "") {
						entry.Unsupported = "Codex git source sha must match the full checked-out commit SHA"
						break
					}
					if value, present := obj["path"]; kind == "git-subdir" || present && value != nil {
						path, ok := value.(string)
						entry.Path = strings.TrimPrefix(strings.TrimSpace(path), "./")
						if !ok || entry.Path == "" || entry.Path == "." || !filepath.IsLocal(entry.Path) || strings.Contains(entry.Path, "\\") || strings.Contains("/"+entry.Path+"/", "/../") {
							entry.Unsupported = "git source path must be a nonempty subdirectory within the repository"
							break
						}
					}
				}
				if sha != "" {
					entry.Ref = sha
				}
				if entry.Repo == "" {
					return Registry{}, fmt.Errorf("plugin %q: source URL is required", name)
				}
			default:
				entry.Unsupported = fmt.Sprintf("marketplace source %q is not supported", kind)
			}
		} else {
			entry.Unsupported = "marketplace source must be a relative path or a source object"
		}
		if entry.Unsupported == "" && entry.Method != MethodNPM && entry.Repo == source.URL {
			if st, err := os.Stat(source.URL); err == nil && st.IsDir() {
				entry.Method = MethodCopy
			} else if !RegistrySourceUsesGit(source) {
				entry.Unsupported = "relative plugin sources require a Git repository or local marketplace root; this catalog URL provides neither"
			}
		}
		if entry.Unsupported == "" && entry.Path != "" && (!filepath.IsLocal(entry.Path) || strings.Contains(entry.Path, "\\")) {
			return Registry{}, fmt.Errorf("plugin %q: source path escapes repository", name)
		}
		if path, ok := raw["source"].(string); ok && !strings.HasPrefix(path, "./") {
			return Registry{}, fmt.Errorf("plugin %q: native local source must start with ./", name)
		}
		if entry.Unsupported == "" && (entry.Plugin.Format == "codex-legacy" || entry.Plugin.Format == "agent-plugins") {
			if err := ValidateCodexMarketplacePolicy(raw); err != nil {
				entry.Unsupported = err.Error()
			}
		}
		reg.Packs[name] = entry
	}
	return reg, nil
}

// ValidateCodexMarketplacePolicy follows Codex 0.159.2's marketplace admission
// rules. It preserves declarations; execution and authentication remain native.
func ValidateCodexMarketplacePolicy(entry map[string]any) error {
	value, present := entry["policy"]
	if !present {
		return nil
	}
	policy, ok := value.(map[string]any)
	if !ok {
		// Serde's policy struct accepts a complete field sequence too.
		fields, sequence := value.([]any)
		if !sequence || len(fields) != 3 {
			return fmt.Errorf("codex marketplace policy must be an object or three-field sequence")
		}
		policy = map[string]any{"installation": fields[0], "authentication": fields[1], "products": fields[2]}
	}
	for _, field := range []struct {
		name   string
		values []string
	}{
		{"installation", []string{"AVAILABLE", "INSTALLED_BY_DEFAULT", "NOT_AVAILABLE"}},
		{"authentication", []string{"ON_INSTALL", "ON_USE"}},
	} {
		if value, present := policy[field.name]; present {
			text, ok := value.(string)
			if !ok || !slices.Contains(field.values, text) {
				return fmt.Errorf("codex marketplace policy %s must be one of %s", field.name, strings.Join(field.values, ", "))
			}
		}
	}
	if policy["installation"] == "NOT_AVAILABLE" {
		return fmt.Errorf("codex marketplace policy makes this plugin unavailable")
	}
	if value := policy["products"]; value != nil {
		products, ok := value.([]any)
		if !ok {
			return fmt.Errorf("codex marketplace policy products must be an array")
		}
		allowed := false
		for _, product := range products {
			switch product {
			case "codex", "CODEX":
				allowed = true
			case "chatgpt", "CHATGPT", "atlas", "ATLAS":
			default:
				return fmt.Errorf("codex marketplace policy contains an invalid product")
			}
		}
		if !allowed {
			return fmt.Errorf("codex marketplace policy does not allow this plugin in Codex")
		}
	}
	return nil
}

func codexGitRepo(repo string, marketplace RegistrySourceEntry) (string, error) {
	repo = strings.TrimSpace(repo)
	switch {
	case strings.HasPrefix(repo, "http://"), strings.HasPrefix(repo, "https://"):
		if strings.HasPrefix(repo, "https://github.com/") && !strings.HasSuffix(repo, ".git") {
			repo += ".git"
		}
		return repo, nil
	case strings.HasPrefix(repo, "./"), strings.HasPrefix(repo, "../"), strings.HasPrefix(repo, ".\\"), strings.HasPrefix(repo, "..\\"):
		parts := strings.FieldsFunc(repo, func(ch rune) bool { return ch == '/' || ch == '\\' })
		if slices.Contains(parts, "..") {
			return "", fmt.Errorf("relative git source URL must stay within the marketplace root")
		}
		if RegistrySourceUsesGit(marketplace) {
			return "./" + filepath.ToSlash(filepath.Join(parts...)), nil
		}
		if st, err := os.Stat(marketplace.URL); sourcepkg.LooksLikeURL(marketplace.URL) || err != nil || !st.IsDir() {
			return "", fmt.Errorf("relative git source URLs require a local or Git-acquired marketplace root")
		}
		abs, err := filepath.Abs(filepath.Join(marketplace.URL, filepath.Join(parts...)))
		if err != nil {
			return "", err
		}
		return sourcepkg.FileURL(abs), nil
	case filepath.IsAbs(repo):
		return sourcepkg.FileURL(repo), nil
	case strings.HasPrefix(repo, "file://"), strings.HasPrefix(repo, "ssh://"), strings.HasPrefix(repo, "git@") && strings.Contains(repo, ":"):
		return repo, nil
	default:
		return claudeGitHubRepo(strings.TrimSuffix(repo, ".git"))
	}
}

func claudeGitHubRepo(repo string) (string, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("github repo must use owner/repo shorthand")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid github repository %q", repo)
		}
		for _, ch := range part {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || strings.ContainsRune("-_.", ch)) {
				return "", fmt.Errorf("invalid github repository %q", repo)
			}
		}
	}
	// Claude appends .git even when the declared repository name ends in .git.
	return "https://github.com/" + repo + ".git", nil
}

// Native local manifests live beneath the marketplace root. Ordinary registry
// files can use the same loader without changing their existing coordinates.
func LocalRegistrySource(path string) (RegistrySourceEntry, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return RegistrySourceEntry{}, err
	}
	root := filepath.Dir(abs)
	for _, rel := range []string{".agents/plugins/marketplace.json", ".agents/plugins/api_marketplace.json", ".claude-plugin/marketplace.json", ".cursor-plugin/marketplace.json"} {
		if strings.HasSuffix(filepath.ToSlash(abs), "/"+rel) {
			root = filepath.FromSlash(strings.TrimSuffix(filepath.ToSlash(abs), "/"+rel))
			break
		}
	}
	rel, err := filepath.Rel(root, abs)
	return RegistrySourceEntry{URL: root, Path: filepath.ToSlash(rel)}, err
}

// ParseRegistrySource keeps ordinary registries on their existing schema while
// accepting native catalogs at the same configured source boundary.
func ParseRegistrySource(data []byte, source RegistrySourceEntry) (Registry, error) {
	if err := ValidateMarketplaceFormat(source.Format); err != nil {
		return Registry{}, err
	}
	var header map[string]json.RawMessage
	if json.Unmarshal(data, &header) == nil && header["plugins"] != nil && header["schema_version"] == nil {
		return ParseMarketplace(data, source)
	}
	if header["name"] != nil && header["schema_version"] != nil && header["packs"] == nil {
		manifest, err := ParsePackManifest(data)
		if err != nil {
			return Registry{}, err
		}
		if source.Format != "" {
			return Registry{}, fmt.Errorf("plugin format requires a native marketplace catalog")
		}
		path := filepath.ToSlash(filepath.Dir(source.Path))
		if path == "." {
			path = ""
		}
		method := ""
		if st, err := os.Stat(source.URL); err == nil && st.IsDir() {
			method = MethodCopy
		}
		return Registry{SchemaVersion: RegistrySchemaVersion, Packs: map[string]RegistryEntry{
			manifest.Name: {Repo: source.URL, Ref: source.Ref, Path: path, Method: method},
		}}, nil
	}
	if source.Format != "" {
		return Registry{}, fmt.Errorf("plugin format requires a native marketplace catalog")
	}
	return ParseRegistry(data)
}

// DiscoverRegistrySource combines conventional repository entry points in
// priority order. Explicit registry entries win over root packs and plugins.
func DiscoverRegistrySource(source RegistrySourceEntry, readFile func(string) ([]byte, error)) (Registry, error) {
	merged := Registry{SchemaVersion: RegistrySchemaVersion, Packs: map[string]RegistryEntry{}, Collections: map[string]RegistryCollection{}}
	found := false
	for _, path := range []string{DefaultRegistryPath, "pack.json", ".agents/plugins/marketplace.json", ".agents/plugins/api_marketplace.json", ".claude-plugin/marketplace.json"} {
		if source.Format != "" && (path == DefaultRegistryPath || path == "pack.json") {
			continue
		}
		if source.Format == "claude" && strings.HasPrefix(path, ".agents/") || source.Format != "" && source.Format != "claude" && strings.HasPrefix(path, ".claude-plugin/") {
			continue
		}
		data, err := readFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Registry{}, fmt.Errorf("reading %s: %w", path, err)
		}
		coordinates := source
		coordinates.Path = path
		reg, err := ParseRegistrySource(data, coordinates)
		if err != nil {
			return Registry{}, fmt.Errorf("parsing %s: %w", path, err)
		}
		found = true
		for name, entry := range reg.Packs {
			if _, exists := merged.Packs[name]; !exists {
				merged.Packs[name] = entry
			}
		}
		for name, collection := range reg.Collections {
			if _, exists := merged.Collections[name]; !exists {
				merged.Collections[name] = collection
			}
		}
	}
	if !found {
		return Registry{}, fmt.Errorf("repository has no registry.yaml, pack.json or supported marketplace catalog")
	}
	return merged, nil
}

func ReadRepositoryFile(root, path string) ([]byte, error) {
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, path))
	if err != nil {
		return nil, err
	}
	boundary, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if !util.IsWithinDir(resolved, boundary) {
		return nil, fmt.Errorf("source file escapes repository: %s", path)
	}
	return os.ReadFile(resolved)
}
