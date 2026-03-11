package domain

// RuleFrontmatter is the harness-neutral rule frontmatter schema.
type RuleFrontmatter struct {
	Name        string         `yaml:"name,omitempty"`
	Description string         `yaml:"description,omitempty"`
	Paths       []string       `yaml:"paths,omitempty"`
	Metadata    map[string]any `yaml:"metadata,omitempty"`
}

// AgentFrontmatter is the harness-neutral agent frontmatter schema.
type AgentFrontmatter struct {
	Name            string   `yaml:"name,omitempty"`
	Description     string   `yaml:"description,omitempty"`
	Tools           []string `yaml:"tools,omitempty"`
	DisallowedTools []string `yaml:"disallowed_tools,omitempty"`
	Skills          []string `yaml:"skills,omitempty"`
	MCPServers      []string `yaml:"mcp_servers,omitempty"`
}

// WorkflowFrontmatter is the harness-neutral workflow frontmatter schema.
type WorkflowFrontmatter struct {
	Name        string         `yaml:"name,omitempty"`
	Title       string         `yaml:"title,omitempty"` // deprecated: use Name
	Description string         `yaml:"description,omitempty"`
	Metadata    map[string]any `yaml:"metadata,omitempty"`
}

// DisplayName returns Name if set, falling back to Title for backwards compat.
func (w WorkflowFrontmatter) DisplayName() string {
	if w.Name != "" {
		return w.Name
	}
	return w.Title
}

// SkillFrontmatter is the parsed SKILL.md frontmatter.
type SkillFrontmatter struct {
	Name        string         `yaml:"name,omitempty"`
	Description string         `yaml:"description,omitempty"`
	Metadata    map[string]any `yaml:"metadata,omitempty"`
}

// Rule is a parsed pack rule file.
type Rule struct {
	Name        string          // filename sans .md
	Frontmatter RuleFrontmatter // parsed YAML frontmatter
	Body        []byte          // markdown body (after frontmatter)
	Raw         []byte          // full original bytes (frontmatter + body)
	SourcePath  string          // absolute path to source file
	SourcePack  string          // pack name this came from
}

// Agent is a parsed pack agent file.
type Agent struct {
	Name        string           // frontmatter Name if set, else filename
	Frontmatter AgentFrontmatter // parsed YAML frontmatter
	Body        []byte           // system prompt body (after frontmatter)
	Raw         []byte           // full original bytes
	SourcePath  string
	SourcePack  string
}

// Workflow is a parsed pack workflow file.
type Workflow struct {
	Name        string              // derived from filename
	Frontmatter WorkflowFrontmatter // parsed YAML frontmatter (may be empty)
	Body        []byte              // markdown body
	Raw         []byte              // full original bytes
	SourcePath  string
	SourcePack  string
}

// Skill is a parsed pack skill directory.
type Skill struct {
	Name        string           // directory name (= skill name)
	Frontmatter SkillFrontmatter // parsed from SKILL.md
	Body        []byte           // markdown body (after frontmatter)
	DirPath     string           // absolute path to skill directory (for copy)
	SourcePack  string
}

// PromptFrontmatter is the parsed prompt frontmatter schema.
type PromptFrontmatter struct {
	Description string   `yaml:"description,omitempty"`
	Category    string   `yaml:"category,omitempty"`
	Models      []string `yaml:"models,omitempty"`
}

// Prompt is a parsed pack prompt file (local library, not synced to harnesses).
type Prompt struct {
	Name        string            // filename sans .md
	Frontmatter PromptFrontmatter // parsed YAML frontmatter
	Body        []byte            // prompt content (after frontmatter)
	Raw         []byte            // full original bytes
	SourcePath  string            // absolute path to source file
	SourcePack  string            // pack name this came from
}

// MCP transport type constants.
const (
	TransportStdio          = "stdio"
	TransportSSE            = "sse"
	TransportStreamableHTTP = "streamable-http"
)

// MCPServer is the single MCP server type used throughout the codebase.
// At load time, Command/Env/URL/Headers may contain {params.*} and {env:VAR} refs.
// After resolution, params are expanded; env refs stay as-is for harness transform.
// AllowedTools/DisabledTools are populated from profile permissions (UNPREFIXED —
// each harness applies its own prefix format).
type MCPServer struct {
	Name           string            `json:"name"`
	Transport      string            `json:"transport"`
	Timeout        int               `json:"timeout"`
	Command        []string          `json:"command,omitempty"` // stdio only
	Env            map[string]string `json:"env,omitempty"`     // stdio only
	URL            string            `json:"url,omitempty"`     // sse / streamable-http
	Headers        map[string]string `json:"headers,omitempty"` // sse / streamable-http
	AvailableTools []string          `json:"available_tools"`

	// Profile-level fields — omitted from pack inventory JSON.
	AllowedTools  []string `json:"allowed_tools,omitempty"`
	DisabledTools []string `json:"disabled_tools,omitempty"`
	SourcePack    string   `json:"source_pack,omitempty"`

	// Doc-only metadata from inventory files.
	Links []string `json:"links,omitempty"`
	Auth  string   `json:"auth,omitempty"`
	Notes string   `json:"notes,omitempty"`
}

// IsStdio reports whether the server uses stdio transport (including empty, which defaults to stdio).
func (s MCPServer) IsStdio() bool {
	return s.Transport == "" || s.Transport == TransportStdio
}

// Warning is a non-fatal validation issue found during content parsing.
type Warning struct {
	Path    string // source file path
	Field   string // frontmatter field name (empty for structural issues)
	Message string
}

// String formats the warning as a human-readable line (without "warning:" prefix).
func (w Warning) String() string {
	switch {
	case w.Path != "" && w.Field != "":
		return w.Path + ": [" + w.Field + "] " + w.Message
	case w.Path != "":
		return w.Path + ": " + w.Message
	default:
		return w.Message
	}
}
