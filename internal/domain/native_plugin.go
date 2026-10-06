package domain

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

var nativeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func ValidNativeName(name string) bool { return nativeName.MatchString(name) }

func ValidNativeRootName(name string) bool {
	return name == "" || (filepath.IsLocal(name) && name != "." && !strings.ContainsAny(name, "/\\\x00"))
}

func NativeMarketplaceDir(configDir string, harness Harness, marketplace, rootName string) string {
	root := filepath.Join(configDir, "rendered-plugins", string(harness), marketplace)
	if rootName != "" && rootName != marketplace {
		root = filepath.Join(root, rootName)
	}
	return root
}

// PluginSource retains catalog identity independently of package acquisition.
// Entry and MarketplaceMetadata preserve native policy, auth, and attribution.
type PluginSource struct {
	NPM                 *NPMSource    `json:"npm,omitempty" yaml:"npm,omitempty"`
	Format              string        `json:"format,omitempty" yaml:"format,omitempty"`
	Name                string        `json:"name" yaml:"name"`
	Marketplace         string        `json:"marketplace" yaml:"marketplace"`
	MarketplaceURL      string        `json:"marketplace_url,omitempty" yaml:"marketplace_url,omitempty"`
	MarketplacePath     string        `json:"marketplace_path,omitempty" yaml:"marketplace_path,omitempty"`
	MarketplaceRef      string        `json:"marketplace_ref,omitempty" yaml:"marketplace_ref,omitempty"`
	Entry               NativeJSONMap `json:"entry,omitempty" yaml:"entry,omitempty"`
	MarketplaceMetadata NativeJSONMap `json:"marketplace_metadata,omitempty" yaml:"marketplace_metadata,omitempty"`
	CatalogCommandOrder []string      `json:"catalog_command_order,omitempty" yaml:"catalog_command_order,omitempty"`
	// SourceRevision is acquired locally, never accepted from a registry or lockfile.
	SourceRevision string `json:"-" yaml:"-"`
}

type NPMSource struct {
	Package  string `json:"package" yaml:"package"`
	Version  string `json:"version,omitempty" yaml:"version,omitempty"`
	Registry string `json:"registry,omitempty" yaml:"registry,omitempty"`
}

// NativePlugin describes a complete imported package. Paths are relative to
// upstream/ and retain the author's layout; selectors are pack-relative IDs.
// Native executable definitions never pass through the portable hook parser.
// An empty Manifest means catalog-backed loading. Empty component source paths
// identify inline declarations retained in MarketplaceEntry, not payload files.
type NativePlugin struct {
	Format              string                               `json:"format"`
	Harness             Harness                              `json:"harness"`
	Name                string                               `json:"name"`
	Marketplace         string                               `json:"marketplace"`
	Manifest            string                               `json:"manifest"`
	ConverterVersion    int                                  `json:"converter_version"`
	CopiedSource        bool                                 `json:"copied_source,omitempty"`
	CacheVersion        string                               `json:"cache_version,omitempty"`
	RootDirectoryName   string                               `json:"root_directory_name,omitempty"`
	Components          map[PackCategory]map[string][]string `json:"components"`
	HookEvents          map[string]string                    `json:"hook_events,omitempty"`
	SettingsFiles       []string                             `json:"settings_files,omitempty"`
	MarketplaceEntry    map[string]any                       `json:"marketplace_entry,omitempty"`
	MarketplaceMetadata map[string]any                       `json:"marketplace_metadata,omitempty"`
	CatalogCommandOrder []string                             `json:"catalog_command_order,omitempty"`
}

func (p NativePlugin) Binding() string { return p.Name + "@" + p.Marketplace }

// NativePluginSelection carries resolved profile choices without changing
// the stored upstream inventory or inferring content dependencies.
type NativePluginSelection struct {
	Package         NativePlugin
	Root            string
	SourcePack      string
	Selected        map[PackCategory][]string
	MCPPolicy       map[string]NativeMCPPolicy
	SettingsEnabled bool
}

type NativeMCPPolicy struct {
	AllowedTools       []string
	AlwaysAllowedTools []string
	DisabledTools      []string
	StartupTimeout     string
}

// NativePluginFile is one entry of a complete rendered native package.
type NativePluginFile struct {
	Path    string      `json:"path"`
	Content []byte      `json:"content,omitempty"`
	Mode    fs.FileMode `json:"mode"`
	Link    string      `json:"link,omitempty"`
}

// NativePluginAction proposes delivery through the host's supported installer.
// Planning reads the payload but never installs, executes, or trusts it.
type NativePluginAction struct {
	Package              NativePlugin
	Selection            *NativePluginSelection `json:"-" yaml:"-"`
	SharedRoot           bool
	Namespace            string
	Files                []NativePluginFile
	MarketplaceDir       string
	ConfigHome           string
	SettingsPath         string
	SourcePack           string
	Scope                Scope
	MCPPolicy            map[string]NativeMCPPolicy
	MCPPermissionServers []string
}
