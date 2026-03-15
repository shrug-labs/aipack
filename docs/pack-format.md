# Pack Format Specification

Version: 0.1 (draft)

## Abstract

A **pack** is a portable, versioned bundle of AI agent configuration. It contains rules, skills, workflows, agent definitions, MCP server configs, and harness settings — authored once and rendered into any supported coding assistant's native format by a sync engine.

Packs solve three problems:

1. **Portability** — agent configuration is authored in a harness-independent format and rendered per-harness at sync time.
2. **Composition** — multiple packs from different sources (personal, team, organization) compose via profiles with explicit layering and conflict resolution.
3. **Distribution** — packs are git-native artifacts, installable from any git repository with no infrastructure beyond what teams already use.

## 1. Pack Structure

A pack is a directory with a manifest and content organized by vector:

```
my-pack/
├── pack.json              # manifest
├── rules/                 # behavioral constraints
│   ├── rule-one.md
│   └── rule-two.md
├── skills/                # on-demand knowledge
│   └── my-skill/
│       ├── SKILL.md       # entry point (required)
│       ├── reference.md   # supporting content (optional)
│       └── scripts/       # supporting code (optional)
│           └── helper.py
├── workflows/             # step-by-step processes
│   └── deploy-checklist.md
├── agents/                # scoped sub-personas
│   └── investigator.md
├── mcp/                   # MCP server configurations
│   └── my-server.json
├── configs/               # harness settings templates
│   └── claudecode/
│       └── settings.local.json
├── profiles/              # bundled profiles (optional, for team distribution)
│   └── team.yaml
└── registries/            # bundled registry fragments (optional)
    └── registry.yaml
```

### Naming conventions

- Pack names: lowercase alphanumeric with hyphens or underscores. No `..`, `/`, `\`, or null bytes.
- Content IDs: derived from filename without extension (e.g., `rules/anti-slop.md` → ID `anti-slop`).
- Skill IDs: derived from subdirectory name (e.g., `skills/deep-research/` → ID `deep-research`).

## 2. Manifest (`pack.json`)

The manifest declares pack metadata and, optionally, content inventory. Schema version 1.

A formal JSON Schema is available at [`pack.schema.json`](../schemas/pack.schema.json). Add `"$schema": "./schemas/pack.schema.json"` to pack.json for editor validation.

```json
{
  "schema_version": 1,
  "name": "my-pack",
  "version": "2026.03.12",
  "root": "."
}
```

### Required fields

| Field | Type | Description |
|-------|------|-------------|
| `schema_version` | integer | Currently `1`. Must be a positive integer. |
| `name` | string | Pack identifier (must match directory name) |
| `root` | string | Base path for content resolution (typically `"."`) |

### Optional fields

| Field | Type | Description |
|-------|------|-------------|
| `version` | string | Pack version (convention: `YYYY.MM.DD` or semver) |
| `rules` | string[] | Explicit rule IDs. If omitted, auto-discovered from `rules/*.md` |
| `agents` | string[] | Explicit agent IDs. If omitted, auto-discovered from `agents/*.md` |
| `workflows` | string[] | Explicit workflow IDs. If omitted, auto-discovered from `workflows/*.md` |
| `skills` | string[] | Explicit skill IDs. If omitted, auto-discovered from `skills/*/SKILL.md` |
| `prompts` | string[] | Local prompt library IDs. Not synced to harnesses — used for pack-internal prompt management only. |
| `mcp` | object | MCP server defaults (see [Section 6](#6-mcp-servers)) |
| `configs` | object | Harness settings and plugin inventory (see [Section 7](#7-configurations)) |
| `profiles` | string[] | Relative paths to bundled profile YAML files |
| `registries` | string[] | Relative paths to bundled registry YAML files |

### Content discovery

When a content vector field (`rules`, `agents`, `workflows`, `skills`) is **omitted or null**, the sync engine discovers content by scanning the corresponding directory:

| Vector | Discovery pattern |
|--------|------------------|
| Rules | `rules/*.md` |
| Agents | `agents/*.md` |
| Workflows | `workflows/*.md` |
| Skills | `skills/*/SKILL.md` (subdirectories containing a `SKILL.md` entry point) |

An **explicit empty array** (`"rules": []`) disables discovery for that vector — the pack declares it has no content of that type.

An **explicit non-empty array** (`"rules": ["rule-one", "rule-two"]`) acts as a filter — only listed IDs are included, even if the directory contains more files.

This convention-over-configuration approach means minimal packs need only a `pack.json` with name and schema version. The directory structure is the inventory.

## 3. Content Format

All authored content (rules, skills, workflows, agents) uses **Markdown with YAML frontmatter**:

```markdown
---
name: my-content-id
description: What this content does
---

Content body — this is what the AI agent reads.
```

The frontmatter block is delimited by `---` on its own line. Everything before the first delimiter is ignored. Everything after the closing delimiter is the body.

### Why markdown?

The content IS the prompt. AI agents read markdown natively — no transformation is needed between what the author writes and what the agent consumes. Frontmatter carries metadata for the sync engine; the body carries instructions for the agent.

## 4. Content Vectors

Each vector has distinct loading semantics and frontmatter requirements.

### 4.1 Rules

**Purpose:** Always-loaded behavioral constraints. Rules are injected into every conversation and shape how the agent behaves across all tasks.

**Loading:** Unconditional — every enabled rule is loaded into the agent's context at session start.

**File pattern:** `rules/<id>.md` (single file per rule)

**Size guidance:** Keep rules concise. They consume context in every conversation. Detailed procedures and reference material belong in skills or workflows.

**Frontmatter:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Rule identifier |
| `description` | string | Yes | When/why this rule applies |
| `paths` | string[] | No | File path globs that scope when the rule is active (harness-dependent) |
| `metadata` | object | No | Arbitrary key-value pairs (e.g., `owner`, `last_updated`) |

**Example:**

```markdown
---
name: verification-before-completion
description: Require fresh command output before claiming any task is complete
---

Before claiming a task is complete:
1. Identify the command that proves the claim
2. Execute it — fresh, not a previous run
3. Read the full output including exit code
4. Confirm output matches the claim
```

### 4.2 Skills

**Purpose:** On-demand domain knowledge and methodology. Skills are loaded when the agent needs them, not on every conversation.

**Loading:** On-demand — loaded when invoked by the user or agent, or when the harness determines relevance.

**File pattern:** `skills/<id>/SKILL.md` (directory per skill, with `SKILL.md` as the entry point)

Skills can contain supporting files — additional markdown, scripts, data files — in the same directory. The `SKILL.md` file is the entry point that the agent reads first; it can reference supporting files as needed.

**Frontmatter:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Skill identifier |
| `description` | string | Yes | What the skill teaches and when to use it |
| `metadata` | object | No | Arbitrary key-value pairs |

**Example:**

```
skills/
└── deep-research/
    ├── SKILL.md
    ├── search-strategies.md
    └── source-evaluation.md
```

```markdown
---
name: deep-research
description: Methodology for thorough multi-source research with source evaluation
---

## When to use

Invoke this skill when asked to research a topic that requires...
```

### 4.3 Workflows

**Purpose:** Repeatable multi-step processes. Workflows guide the agent through a specific procedure with defined steps.

**Loading:** On-demand — invoked explicitly (e.g., as a slash command or skill reference).

**File pattern:** `workflows/<id>.md` (single file per workflow)

**Frontmatter:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Workflow identifier |
| `description` | string | Yes | What process this workflow executes |
| `metadata` | object | No | Arbitrary key-value pairs |

**Example:**

```markdown
---
name: session-retro
description: End-of-session retrospective capturing pack usage, learnings, and memory updates
---

## Steps

1. Review pack content invocations during this session
2. Update usage tracking
3. Capture any new learnings for memory
4. Identify pack improvement candidates
```

### 4.4 Agents

**Purpose:** Scoped sub-personas with constrained tools and domain knowledge. Agents define a specialized role the AI can assume for specific tasks.

**Loading:** On-demand — spawned as subagents when the parent agent or user delegates a task.

**File pattern:** `agents/<id>.md` (single file per agent)

**Frontmatter:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Agent identifier |
| `description` | string | Yes | Agent's role and when to use it |
| `tools` | string[] | No | Tool allowlist (only these tools are available) |
| `disallowed_tools` | string[] | No | Tool blocklist (these tools are denied) |
| `skills` | string[] | No | Skills loaded into the agent's context |
| `mcp_servers` | string[] | No | MCP servers available to this agent |

The body of an agent file serves as the agent's system prompt — it defines the agent's persona, domain knowledge, and output expectations.

**Example:**

```markdown
---
name: confluence-navigator
description: Confluence domain specialist for navigating spaces, assessing page freshness, and mapping relationships
tools:
  - mcp__atlassian__confluence_search
  - mcp__atlassian__confluence_get_page
  - mcp__atlassian__confluence_get_page_children
  - Read
  - Grep
  - Glob
disallowed_tools:
  - Edit
  - Write
  - Bash
mcp_servers:
  - atlassian
---

You are a Confluence navigation specialist. Your job is to find, assess, and rank
Confluence pages using CQL queries...
```

## 5. Environment References and Parameters

Pack content uses placeholder syntax for values that vary by deployment — environment variables, user-specific paths, team URLs.

### 5.1 Parameter references

Syntax: `{params.KEY}`

Parameters are defined in profiles and expanded at sync time. Use parameters for values that differ between users or environments but are known at configuration time.

```json
{
  "command": ["{env:HOME}/.local/share/mcp-servers/my-server/run"],
  "env": {
    "API_URL": "{params.api_url}"
  }
}
```

Profile definition:

```yaml
params:
  api_url: "https://api.example.com"
```

### 5.2 Environment variable references

Syntax: `{env:VAR}`

Environment variable references are resolved at sync time: the placeholder is replaced with the literal value from the process environment. If the variable is not set, the MCP server is skipped entirely and a warning is emitted.

Pack authors write `{env:VAR}` once; the sync engine resolves it identically for all harnesses.

### 5.3 Expansion order

1. Parameter references (`{params.*}`) are expanded first, using values from the active profile.
2. Environment references (`{env:*}`) are then resolved to literal values from the process environment.

This means parameters can contain environment references: `{params.mcp_dir}` could expand to `{env:HOME}/.local/share/mcp-servers`, which then resolves to `/home/user/.local/share/mcp-servers` at sync time.

## 6. MCP Servers

The `mcp/` directory contains JSON files defining MCP server connections. Each file declares one server. The filename (minus `.json`) must match the `name` field (case-insensitive) — a mismatch causes the server to be silently invisible during sync.

A formal JSON Schema is available at [`mcp-server.schema.json`](../schemas/mcp-server.schema.json).

### Server definition

```json
{
  "name": "my-server",
  "transport": "stdio",
  "timeout": 120,
  "command": [
    "{env:HOME}/.local/share/mcp-servers/my-server/.venv/bin/my-server",
    "--url",
    "{params.server_url}"
  ],
  "env": {
    "API_TOKEN": "{env:MY_TOKEN}"
  },
  "available_tools": [
    "tool_one",
    "tool_two",
    "tool_three"
  ],
  "links": ["https://docs.example.com/my-server"],
  "notes": "Requires MY_TOKEN environment variable"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Server identifier (must be unique across all packs in a profile) |
| `transport` | string | Yes | `stdio`, `sse`, or `streamable-http` |
| `timeout` | integer | No | Connection timeout in seconds |
| `command` | string[] | Stdio only | Command and arguments to launch the server |
| `env` | object | No | Environment variables passed to the server process |
| `url` | string | SSE/HTTP only | Server endpoint URL |
| `headers` | object | No | HTTP headers (SSE/HTTP transports) |
| `available_tools` | string[] | No | Complete inventory of tools the server provides |
| `links` | string[] | No | Documentation URLs (metadata, not synced to harness) |
| `auth` | string | No | Authentication notes (metadata, not synced to harness) |
| `notes` | string | No | Human-readable notes (metadata, not synced to harness) |

### Tool allowlists

The `available_tools` field serves as a complete tool inventory. In the manifest, `mcp.servers.<name>.default_allowed_tools` defines which tools are enabled by default:

```json
{
  "mcp": {
    "servers": {
      "my-server": {
        "default_allowed_tools": ["tool_one", "tool_two"]
      }
    }
  }
}
```

Profiles can override allowed tools per server (see [Section 8.3](#83-mcp-server-overrides)).

## 7. Configurations

The `configs/` directory contains harness-specific settings templates and plugins, organized by harness name:

```
configs/
├── claudecode/
│   └── settings.local.json
├── codex/
│   └── config.toml
└── opencode/
    ├── opencode.json
    └── oh-my-opencode.json
```

The manifest declares which files are settings (merged with engine-managed keys) and which are plugins (pure copies):

```json
{
  "configs": {
    "harness_settings": {
      "claudecode": ["settings.local.json"],
      "opencode": ["opencode.json"]
    },
    "harness_plugins": {
      "opencode": ["oh-my-opencode.json"]
    }
  }
}
```

**Settings** are templates — the sync engine merges them with generated MCP configs, tool permissions, and content references. At most one pack per profile can provide settings for a given harness.

**Plugins** are pure copies — synced as-is regardless of `--skip-settings`.

## 8. Composition

Packs compose through **profiles** — YAML files that declare which packs to load, in what order, with what overrides.

### 8.1 Profile structure

```yaml
schema_version: 2

params:
  jira_url: "https://jira.example.com"
  confluence_url: "https://confluence.example.com"

packs:
  - name: team-ops
    enabled: true
    settings:
      enabled: true
    mcp:
      atlassian:
        enabled: true
        allowed_tools:
          - confluence_search
          - confluence_get_page
      dope:
        enabled: false

  - name: personal
    enabled: true
    rules:
      exclude: ["noisy-rule"]
    overrides:
      rules: ["anti-slop"]   # personal's anti-slop replaces team-ops's
```

### 8.2 Vector selectors

Each content vector supports `include` and `exclude` lists for fine-grained content selection:

| Configuration | Behavior |
|---------------|----------|
| Omitted (default) | All content from this vector is included |
| `include: [a, b]` | Only listed IDs are included |
| `exclude: [x, y]` | All content except listed IDs |
| `include: []` | No content from this vector |

Include and exclude support glob patterns (e.g., `ocm-*`).

### 8.3 MCP server overrides

Profiles can enable/disable servers and override tool permissions per server:

```yaml
mcp:
  my-server:
    enabled: true
    allowed_tools:       # overrides manifest default_allowed_tools
      - tool_one
    disabled_tools:      # explicitly blocked tools
      - dangerous_tool
```

### 8.4 Layering and precedence

- Packs are processed in profile order (first to last).
- If two packs declare the same content ID (e.g., both have `rules/anti-slop.md`), the sync engine raises a conflict unless the later pack explicitly declares it in `overrides`.
- Parameters are global to the profile — all packs share the same parameter namespace. (Legacy `globals` and `global` keys are accepted and merged into `params` for backward compatibility, but new profiles should use `params` exclusively.)
- At most one pack per profile can have `settings.enabled: true` for a given harness.

### 8.5 Override declarations

```yaml
packs:
  - name: base-pack
  - name: custom-pack
    overrides:
      rules: ["shared-rule"]      # custom-pack's version wins
      workflows: ["deploy"]       # custom-pack's version wins
```

Without the override declaration, duplicate IDs across packs are treated as errors.

## 9. Distribution

### 9.1 Git-native installation

Packs are installed from git repositories. Two fetch strategies are supported:

1. **Archive fetch** (preferred) — `git archive --remote` retrieves only declared content. Two phases: manifest first (to discover content), then declared files. Efficient for large repos where the pack is a subdirectory.
2. **Shallow clone** (fallback) — used when the remote doesn't support `git archive` (e.g., GitHub). Performs a depth-1 clone.

Both HTTPS and SSH URLs are supported. Packs can live in a subdirectory of a larger repository (common for team mono-repos).

### 9.2 Registry

A registry maps pack names to source repositories. Format:

```yaml
schema_version: 1
packs:
  essentials:
    repo: "https://github.com/shrug-labs/packs.git"
    path: "essentials"
    ref: "main"
    description: "Foundation pack for AI agent configuration"
    owner: "shrug-labs"
  team-ops:
    repo: "git@bitbucket.example.com:TEAM/tools.git"
    path: "ai-pack"
    ref: "main"
    description: "Team operational runbooks and MCP configs"
    owner: "ops-team"
    contact: "ops-team@example.com"
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `repo` | string | Yes | Git repository URL |
| `path` | string | No | Subdirectory within the repository |
| `ref` | string | No | Git ref (branch, tag, commit) |
| `description` | string | No | Human-readable description |
| `owner` | string | No | Pack maintainer or team |
| `contact` | string | No | Contact information |

Multiple registry sources can be configured. The merged view resolves pack names with local entries taking highest priority, followed by cached remote sources in configuration order.

### 9.3 Bundled profiles and registries

Packs can bundle profile and registry files for team distribution:

```json
{
  "profiles": ["profiles/team.yaml"],
  "registries": ["registries/team-registry.yaml"]
}
```

On install with `--seed`, bundled profiles are copied to the user's profile directory and bundled registries are merged into the user's registry. This enables single-command team onboarding:

```bash
aipack pack install --url https://github.com/org/tools.git --path team-pack --seed
aipack profile set team --install
aipack sync
```

### 9.4 Versioning

Pack versioning uses git refs. The `version` field in `pack.json` is informational. The authoritative version is the git ref (branch, tag, or commit) used at install time.

For archive-installed packs, the commit hash at install time is recorded, enabling update detection.

## 10. Harness Contract

The sync engine guarantees the following for all supported harnesses:

1. **Content fidelity** — the body of each content file (the text after frontmatter) is delivered to the agent without modification.
2. **Frontmatter transformation** — frontmatter is translated to harness-native format where required (e.g., agent `disallowed_tools` → Claude Code `disallowedTools`).
3. **MCP configuration** — server definitions are rendered into harness-native config files with environment references translated to the correct syntax.
4. **Conflict detection** — user modifications to managed files are detected via content digest and surfaced as diffs rather than silently overwritten.
5. **Determinism** — given identical inputs and profile, sync produces byte-identical outputs across runs.

Per-harness rendering details (file paths, config formats, merge behavior) are documented in the [aipack reference](./aipack.md#per-harness-behavior) and are implementation concerns of the sync engine, not part of the pack format specification.

### Supported harnesses

| Harness | Rules | Skills | Workflows | Agents | MCP | Settings |
|---------|-------|--------|-----------|--------|-----|----------|
| Claude Code | Individual files | Directories | Command files | Subagent files | JSON | JSON merge |
| OpenCode | Individual files | Directories | Command files | Individual files | JSON key | JSON merge |
| Codex | Flattened markdown | Directories | Promoted to skill dirs | Promoted to skill dirs | TOML tables | TOML merge |
| Cline | Individual files | Directories | Individual files | Promoted to skill dirs | Global JSON | N/A |

## Appendix A: Complete `pack.json` Example

```json
{
  "schema_version": 1,
  "name": "team-ops",
  "version": "2026.03.12",
  "root": ".",
  "mcp": {
    "servers": {
      "atlassian": {
        "default_allowed_tools": [
          "confluence_search",
          "confluence_get_page",
          "jira_search",
          "jira_get_issue"
        ]
      },
      "dope": {
        "default_allowed_tools": [
          "get_alarms",
          "search_logs",
          "get_metrics"
        ]
      }
    }
  },
  "configs": {
    "harness_settings": {
      "claudecode": ["settings.local.json"],
      "opencode": ["opencode.json"]
    },
    "harness_plugins": {
      "opencode": ["oh-my-opencode.json"]
    }
  },
  "profiles": [
    "profiles/default.yaml",
    "profiles/oncall.yaml"
  ],
  "registries": [
    "registries/team-registry.yaml"
  ]
}
```

## Appendix B: Minimal Pack

The smallest valid pack — auto-discovered content, no MCP, no configs:

```
minimal-pack/
├── pack.json
└── rules/
    └── be-concise.md
```

```json
{
  "schema_version": 1,
  "name": "minimal-pack",
  "root": "."
}
```

```markdown
---
name: be-concise
description: Keep responses short and direct
---

Lead with the answer. Skip preamble. If you can say it in one sentence, don't use three.
```

## Appendix C: JSON Schemas

Two JSON Schema (Draft-07) files provide machine-readable validation for pack artifacts:

### [`pack.schema.json`](../schemas/pack.schema.json) — Pack Manifest

Validates `pack.json` files:

- Required fields (`schema_version`, `name`, `root`)
- Pack and content ID naming patterns (`^[a-z0-9][a-z0-9_-]*$`)
- Content vector arrays with uniqueness constraints
- MCP server defaults and harness config structure
- Path traversal prevention in relative paths (`profiles`, `registries`)
- Strict mode — no unknown properties

### [`mcp-server.schema.json`](../schemas/mcp-server.schema.json) — MCP Server Definition

Validates `mcp/*.json` files:

- Required fields (`name`, `transport`)
- Transport-conditional requirements (`command` for stdio, `url` for sse/streamable-http)
- Server name pattern matching filename convention
- Runtime-only fields rejected (`allowed_tools`, `disabled_tools`, `source_pack` — these are set by profile resolution, not authored)
- Tool inventory uniqueness (`available_tools`)
- Strict mode — no unknown properties

### Editor integration

Add a `$schema` reference to enable editor autocomplete and validation:

```json
{
  "$schema": "../schemas/pack.schema.json",
  "schema_version": 1,
  "name": "my-pack",
  "root": "."
}
```
