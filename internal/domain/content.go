package domain

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

// RuleFrontmatter is the harness-neutral rule frontmatter schema.
type RuleFrontmatter struct {
	Name        string         `yaml:"name,omitempty"`
	Description string         `yaml:"description,omitempty"`
	Paths       []string       `yaml:"paths,omitempty"`
	Metadata    map[string]any `yaml:"metadata,omitempty"`
}

// AgentFrontmatter is the harness-neutral agent frontmatter schema.
// The Harness field carries per-harness configuration overrides (e.g. model,
// reasoning_effort) keyed by harness ID. Harness adapters read their own key
// during native rendering and the field is stripped when agents are rendered
// as markdown for model consumption.
type AgentFrontmatter struct {
	Name            string                    `yaml:"name,omitempty"`
	Description     string                    `yaml:"description,omitempty"`
	Tools           []string                  `yaml:"tools,omitempty"`
	DisallowedTools []string                  `yaml:"disallowed_tools,omitempty"`
	Skills          []string                  `yaml:"skills,omitempty"`
	MCPServers      []string                  `yaml:"mcp_servers,omitempty"`
	Harness         map[string]map[string]any `yaml:"harness,omitempty"`
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

// HookEntryFile is the descriptor filename for a pack hook directory.
const HookEntryFile = "HOOK.yaml"

// Hook event names are AIPack's portable lifecycle vocabulary. Harness
// adapters map these to their native event names.
type HookEventName string

const (
	HookEventRunStart      HookEventName = "run.start"
	HookEventPromptSubmit  HookEventName = "prompt.submit"
	HookEventToolBefore    HookEventName = "tool.before"
	HookEventToolAfter     HookEventName = "tool.after"
	HookEventCompactBefore HookEventName = "compact.before"
)

func IsSupportedHookEvent(event HookEventName) bool {
	switch event {
	case HookEventRunStart, HookEventPromptSubmit, HookEventToolBefore, HookEventToolAfter, HookEventCompactBefore:
		return true
	default:
		return false
	}
}

// Hook is a parsed pack hook descriptor plus source location metadata.
type Hook struct {
	ID          string
	Name        string
	Description string
	Events      []HookEvent
	DirPath     string
	SourcePath  string
	SourcePack  string
}

// HookEvent describes one portable lifecycle event handled by a hook.
type HookEvent struct {
	On       HookEventName `yaml:"on"`
	Match    HookMatch     `yaml:"match,omitempty"`
	Handler  HookHandler   `yaml:"handler,omitempty"`
	Handlers []HookHandler `yaml:"handlers,omitempty"`
}

// EffectiveHandlers returns the handlers attached to the event. `handler` is
// the authoring shorthand for the common one-handler case.
func (e HookEvent) EffectiveHandlers() []HookHandler {
	if len(e.Handlers) > 0 {
		return e.Handlers
	}
	if e.Handler.Type != "" || e.Handler.Command != "" || e.Handler.CommandWindows != "" {
		return []HookHandler{e.Handler}
	}
	return nil
}

// HookMatch carries portable matcher fields. Harness adapters ignore fields
// that do not apply to the mapped native event.
type HookMatch struct {
	Tool   string `yaml:"tool,omitempty"`
	Source string `yaml:"source,omitempty"`
}

// NormalizeHookMatcher returns the matcher value harness-native renderers
// should emit. Empty and "*" both mean match everything in the portable
// contract, so native regex matchers can omit them.
func NormalizeHookMatcher(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "*" {
		return ""
	}
	return v
}

// HookHandlerType identifies the portable hook handler backend.
type HookHandlerType string

const HookHandlerTypeCommand HookHandlerType = "command"

// HookHandlerMode controls how a hook handler is executed.
type HookHandlerMode string

const (
	HookHandlerModeSync  HookHandlerMode = "sync"
	HookHandlerModeAsync HookHandlerMode = "async"
)

// HookHandler describes how AIPack should invoke a hook handler. Command is a
// shell command string; timeout accepts either seconds ("5") or Go duration
// syntax ("5s").
type HookHandler struct {
	Type           HookHandlerType `yaml:"type,omitempty"`
	Command        string          `yaml:"command,omitempty"`
	CommandWindows string          `yaml:"command_windows,omitempty"`
	Timeout        string          `yaml:"timeout,omitempty"`
	Mode           HookHandlerMode `yaml:"mode,omitempty"`
	StatusMessage  string          `yaml:"status_message,omitempty"`
}

// HookTimeoutSeconds parses a hook timeout string. Empty returns 0 so callers
// can omit native timeout fields and let harness defaults apply.
func HookTimeoutSeconds(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if n, err := strconv.Atoi(raw); err == nil {
		if n < 1 {
			return 0, fmt.Errorf("timeout must be at least 1 second")
		}
		return n, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("timeout must be seconds or duration syntax: %w", err)
	}
	if d < time.Second {
		return 0, fmt.Errorf("timeout must be at least 1 second")
	}
	return int(d.Round(time.Second) / time.Second), nil
}

// writableContent is satisfied by Rule, Workflow, and Agent for generic write generation.
type writableContent interface {
	writeName() string
	writeRaw() []byte
	writeSourcePack() string
	writeSourcePath() string
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

func (r Rule) writeName() string       { return r.Name }
func (r Rule) writeRaw() []byte        { return r.Raw }
func (r Rule) writeSourcePack() string { return r.SourcePack }
func (r Rule) writeSourcePath() string { return r.SourcePath }

// Agent is a parsed pack agent file.
type Agent struct {
	Name        string           // frontmatter Name if set, else filename
	Frontmatter AgentFrontmatter // parsed YAML frontmatter
	Body        []byte           // system prompt body (after frontmatter)
	Raw         []byte           // full original bytes
	SourcePath  string
	SourcePack  string
}

func (a Agent) writeName() string       { return a.Name }
func (a Agent) writeRaw() []byte        { return a.Raw }
func (a Agent) writeSourcePack() string { return a.SourcePack }
func (a Agent) writeSourcePath() string { return a.SourcePath }

// Workflow is a parsed pack workflow file.
type Workflow struct {
	Name        string              // derived from filename
	Frontmatter WorkflowFrontmatter // parsed YAML frontmatter (may be empty)
	Body        []byte              // markdown body
	Raw         []byte              // full original bytes
	SourcePath  string
	SourcePack  string
}

func (w Workflow) writeName() string       { return w.Name }
func (w Workflow) writeRaw() []byte        { return w.Raw }
func (w Workflow) writeSourcePack() string { return w.SourcePack }
func (w Workflow) writeSourcePath() string { return w.SourcePath }

// Skill is a parsed pack skill directory.
type Skill struct {
	Name           string           // directory name (= skill name)
	Frontmatter    SkillFrontmatter // parsed from SKILL.md
	Body           []byte           // markdown body (after frontmatter)
	DirPath        string           // absolute path to skill directory (for copy)
	SourcePack     string
	SourceBoundary string // repository or pack root allowed for source symlinks
	// Assets is the sorted list of files bundled in the skill directory
	// other than SKILL.md, as paths relative to DirPath. Populated at
	// parse time by walking DirPath; used by drift detection to surface
	// what a skill ships.
	Assets []string
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

// Plugin is a parsed plugin reference descriptor. The plugin bytes stay in
// the harness marketplace; aipack only carries the endorsement pointer.
type Plugin struct {
	Name        string // filename leaf sans .json
	Source      string `json:"source"`
	Marketplace string `json:"marketplace,omitempty"`
	SourcePath  string
	SourcePack  string
}

// MarketplaceName returns the harness marketplace name for this plugin. An
// empty marketplace uses the harness default; a source-prefixed marketplace
// derives its name from the source path leaf.
func (p Plugin) MarketplaceName(defaultMarketplace string) string {
	m := strings.TrimSpace(p.Marketplace)
	if m == "" {
		return defaultMarketplace
	}
	if strings.Contains(m, ":") {
		_, rest, _ := strings.Cut(m, ":")
		if leaf := path.Base(strings.TrimRight(rest, "/")); leaf != "." && leaf != "/" {
			return leaf
		}
	}
	return m
}

// Binding returns the harness plugin binding string: <plugin>@<marketplace>.
func (p Plugin) Binding(defaultMarketplace string) string {
	return p.Name + "@" + p.MarketplaceName(defaultMarketplace)
}

// HasSourceMarketplace reports whether Marketplace is a source identifier
// such as github:owner/repo rather than a bare marketplace name.
func (p Plugin) HasSourceMarketplace() bool {
	return strings.Contains(strings.TrimSpace(p.Marketplace), ":")
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
// AllowedTools/AlwaysAllowedTools/DisabledTools are populated from profile
// permissions (UNPREFIXED — each harness applies its own prefix format).
// AllowedTools = visible/callable (will prompt per call on harnesses that
// distinguish). AlwaysAllowedTools = visible AND auto-approved without
// prompting. Tools in AlwaysAllowedTools are implicitly callable even if
// absent from AllowedTools — the effective allow set is the union of both.
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
	AllowedTools       []string `json:"allowed_tools,omitempty"`
	AlwaysAllowedTools []string `json:"always_allowed_tools,omitempty"`
	DisabledTools      []string `json:"disabled_tools,omitempty"`
	SourcePack         string   `json:"source_pack,omitempty"`
	PackRoot           string   `json:"-"` // absolute path to source pack dir; resolves {pack:root}

	// RequiredRefs is the set of {params.*} and {env:*} references in the
	// raw (pre-expansion) Command/Env/URL/Headers strings. Both must be
	// satisfied at render time for the server to be emitted. Populated by
	// the resolver, read by drift detection.
	RequiredRefs []RequiredRef `json:"-"`

	// Doc-only metadata from inventory files. Not rendered to harness configs.
	// Links: URLs to source repos, setup docs, or reference pages for this server.
	// Auth: one-line summary of the authentication method.
	// Notes: anything specific to this server that doesn't fit elsewhere.
	Links []string `json:"links,omitempty"`
	Auth  string   `json:"auth,omitempty"`
	Notes string   `json:"notes,omitempty"`
}

// CapturedMCP is a per-server MCP record extracted from a harness config.
type CapturedMCP struct {
	Server             MCPServer
	HarnessPath        string
	AllowedTools       []string
	AlwaysAllowedTools []string
}

// IsStdio reports whether the server uses stdio transport (including empty, which defaults to stdio).
func (s MCPServer) IsStdio() bool {
	return s.Transport == "" || s.Transport == TransportStdio
}

// RefKind values for RequiredRef.Kind.
const (
	RefKindParam = "param"
	RefKindEnv   = "env"
)

// RequiredRef is a reference an MCP server makes to either a profile
// parameter ({params.X}) or an environment variable ({env:X}). Both must
// resolve at render time for the server to be emitted.
type RequiredRef struct {
	Kind string `yaml:"kind" json:"kind"` // RefKindParam or RefKindEnv
	Name string `yaml:"name" json:"name"`
}

// Display returns the canonical reference syntax a user sees in their
// MCP server JSON ({params.X} or {env:X}).
func (r RequiredRef) Display() string {
	switch r.Kind {
	case RefKindParam:
		return "{params." + r.Name + "}"
	case RefKindEnv:
		return "{env:" + r.Name + "}"
	}
	return r.Name
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
