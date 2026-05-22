# Pack Format Specification

Version: 0.1 (draft)

## Abstract

A **pack** is a portable, versioned bundle of AI agent configuration — rules, skills, workflows, agent definitions, MCP server configs, portable hook descriptors, and harness settings — authored once and rendered into any supported coding assistant's native format at sync time. Packs are harness-independent (write once, render per-harness), composable (personal, team, and org packs layer via profiles with explicit conflict resolution), and git-native (installable from any repository with no infrastructure beyond what teams already use).

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
├── plugins/               # Harness plugin references
│   └── linear.json
├── mcp/                   # MCP server configurations
│   └── my-server.json
├── hooks/                 # portable hook descriptors
│   └── datetime-injector/
│       └── HOOK.yaml      # hook entry point (required)
├── configs/               # harness settings and drop-ins
│   ├── claudecode/
│   │   └── settings.local.json
│   └── codex/
│       └── config.toml
├── profiles/              # bundled profiles (curated slices of skills, tools, and knowledge)
│   └── dev.yaml
├── registries/            # bundled registry fragments (optional)
│   └── team-tools.yaml
└── scripts/               # extras — bundled assets referenced via {pack:root}
    └── run-server.sh
```

### Naming conventions

- Pack names: lowercase alphanumeric with hyphens or underscores. No `..`, `/`, `\`, or null bytes.
- Content IDs: derived from filename without extension (e.g., `rules/anti-slop.md` → ID `anti-slop`).
- Skill IDs: derived from subdirectory name (e.g., `skills/deep-research/` → ID `deep-research`).

## 2. Manifest (`pack.json`)

The manifest declares pack metadata and, optionally, content inventory. Schema version 2 is the current format.

A formal JSON Schema is available at [`pack.schema.json`](../schemas/pack.schema.json). Use `"$schema": "https://raw.githubusercontent.com/shrug-labs/aipack/main/schemas/pack.schema.json"` for editor validation, or a relative path if you vendor the schema with your pack.

```json
{
  "schema_version": 2,
  "name": "my-pack",
  "version": "2026.03.12",
  "root": "."
}
```

### Required fields

| Field | Type | Description |
|-------|------|-------------|
| `schema_version` | integer | `2` for current packs (flat `mcp` array). `1` is the pre-v0.23 shape with nested `mcp: { servers: { ... } }` and is still accepted. The shape of `mcp` is strictly tied to the version — a mismatch is rejected at parse time. |
| `name` | string | Pack identifier (must match directory name). `__aipack__` is reserved for rendered content identity and is not allowed in pack names. |
| `root` | string | Base path for content resolution (typically `"."`) |

### Optional fields

| Field | Type | Description |
|-------|------|-------------|
| `version` | string | Pack version (convention: `YYYY.MM.DD` or semver) |
| `description` | string | Human-readable pack summary |
| `rules` | string[] | Explicit rule IDs. Auto-discovered from `rules/**/*.md`. Slashed ids allowed (`team-a/style`); literal `__` reserved. |
| `agents` | string[] | Explicit agent IDs (flat — no slashes or `__aipack__`). Auto-discovered from `agents/**/*.md`; the file basename is the id, the subdirectory is authoring organization. |
| `workflows` | string[] | Explicit workflow IDs (flat — no slashes or `__aipack__`). Auto-discovered from `workflows/**/*.md`; the file basename is the id. |
| `skills` | string[] | Explicit skill IDs (flat — no slashes or `__aipack__`). Auto-discovered from `skills/**/SKILL.md`; the immediate parent directory's name is the id. |
| `hooks` | string[] | Explicit hook IDs (flat — no slashes or `__aipack__`). Auto-discovered from `hooks/**/HOOK.yaml`; the immediate parent directory's name is the id. See [Section 9](#9-hooks). |
| `prompts` | string[] | Local prompt library IDs. Not synced to harnesses — used for pack-internal prompt management only. Auto-discovered from `prompts/**/*.md`. |
| `plugins` | string[] | Harness plugin reference IDs (flat — no slashes). Auto-discovered from `plugins/**/*.json`; the file basename is the id. See [Section 7](#7-plugin-references). |
| `mcp` | string[] | Explicit MCP server IDs. Auto-discovered from `mcp/**/*.json`. See [Section 6](#6-mcp-servers). |
| `configs` | object | Harness settings and drop-in plugin inventory (see [Section 8](#8-configurations)) |
| `profiles` | string[] | Profile IDs. Auto-discovered from `profiles/**/*.yaml` |
| `registries` | string[] | Registry IDs. Auto-discovered from `registries/**/*.yaml` |
| `extras` | string[] | Relative paths to bundled assets (scripts, data files, helper source) preserved through install. Referenced via `{pack:root}` in MCP configs. Max 50 entries. Must not collide with standard content directories. |

### Content discovery

When a content vector field is **empty** — omitted, null, or an empty array — the sync engine discovers content by scanning the corresponding directory recursively. An **explicit non-empty array** acts as a filter — only listed IDs are included, even if the directory contains more files.

Minimal packs need only a `pack.json` with name and schema version — the directory structure is the inventory.

**Subdirectories are allowed everywhere.** For rules they're part of the id (`rules/team-a/style.md` → `team-a/style`); the harness filename encodes `/` as `__` (`team-a__style.md`). For agents, workflows, skills, hooks, and plugins the subdirectory is authoring organization only — the id is always the file basename (or entry directory name). Two same-leaf entries within one pack collide; rename one. Cross-pack same-leaf goes through the configured collision behavior, or through namespaced rendered IDs (`<id>__aipack__<pack>`) when `defaults.namespaced: true`.

The literal `__aipack__` sentinel is reserved for rendered identity. Existing packs that used `__aipack__` in a pack name, agent ID, workflow ID, skill ID, or hook ID must rename those entries before validation or sync. Rule IDs use `/` for authored nesting; their harness filenames may contain generated `__` escapes, but authored rule IDs cannot contain literal `__`.

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

Pack content is the prompt — agents read markdown natively, so no transformation is needed between what the author writes and what the agent consumes. Frontmatter carries metadata for the sync engine; the body is what the agent reads.

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
| `harness` | map[string]map[string]any | No | Per-harness configuration overrides (see below) |

The body of an agent file serves as the agent's system prompt — it defines the agent's persona, domain knowledge, and output expectations.

**Harness-specific overrides:** The `harness` field carries configuration that only applies to a specific harness. Each key is a harness ID (`codex`, `claude`, `cline`, `opencode`) and the value is a map of harness-native settings. Harness adapters read their own key during rendering and ignore the rest. When agents are rendered as markdown (for harnesses that promote agents to skill directories), the `harness` block is automatically stripped from the output.

**Example:**

```markdown
---
name: explorer
description: Reads codebase and context deeply before proposing changes
tools:
  - Read
  - Grep
  - Glob
mcp_servers:
  - issue-tracker
harness:
  codex:
    model_reasoning_effort: xhigh
---

You are the Explorer role.

Mission:
- Read the codebase heavily and build strong context before proposing changes.
- Map entry points, core modules, ownership boundaries, and data/control flow.
...
```

For Codex, `harness.codex` values are rendered as top-level keys in the native agent TOML. Common Codex agent fields include `model`, `model_reasoning_effort`, `service_tier`, and `sandbox_mode`. Unknown keys pass through without requiring aipack code changes.

## 5. Environment References and Parameters

Pack content uses placeholder syntax for values that vary by deployment — environment variables, user-specific paths, team URLs.

### 5.1 Parameter references

Syntax: `{params.KEY}` or `{params.KEY:-default}`

Parameters are defined in profiles and expanded at sync time. Use parameters for values that differ between users or environments but are known at configuration time. A `:-default` suffix supplies a literal fallback when the parameter is absent; bare `{params.KEY}` remains strict. Empty defaults are allowed (`{params.optional:-}`), but nested placeholders inside defaults are rejected.

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

Syntax: `{env:VAR}` or `{env:VAR:-default}`

Environment variable references are resolved at sync time: the placeholder is replaced with the literal value from the active config directory's `.env` file first, then from the process environment. `{env:VAR:-default}` uses `default` only when `VAR` is unset; a set-but-empty value remains empty. In MCP server definitions, an unset variable without a default skips that server and emits a warning. In harness settings templates, unresolved references fail sync so aipack does not write broken config.

Pack authors write `{env:VAR}` once; the sync engine resolves it identically for all harnesses.

```json
{
  "env": {
    "API_BASE_URL": "{env:API_BASE_URL:-https://api.example.com}"
  }
}
```

### 5.3 Pack root references

Syntax: `{pack:root}`

Resolves to the absolute path of the installed pack directory. Use this in MCP server `command` arrays, MCP `env` values, and harness settings templates to reference bundled extras — scripts, binaries, data files — that ship with the pack.

```json
{
  "command": ["{pack:root}/scripts/run-server.sh", "--port", "8080"],
  "env": {
    "DATA_DIR": "{pack:root}/data"
  }
}
```

Pack root references are resolved at sync time after the pack is installed, so the path is always the concrete location on the user's machine.

### 5.4 Expansion order

1. Pack root references (`{pack:root}`) are expanded first, resolving to the installed pack's absolute path.
2. Parameter references (`{params.*}`) are expanded next, using values from the active profile.
3. Environment references (`{env:*}`) are then resolved to literal values from config-dir `.env`, falling back to the process environment and then any inline default.

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
| `available_tools` | string[] or null | No | Complete inventory of tools the server provides. Populate or refresh with `aipack mcp inspect-tools <server> --save`; null means unknown/unprobed. |
| `links` | string[] | No | Documentation URLs (metadata, not synced to harness) |
| `auth` | string | No | Authentication notes (metadata, not synced to harness) |
| `notes` | string | No | Human-readable notes (metadata, not synced to harness) |

### Tool allowlists

The pack manifest's `mcp` field is a flat list of server IDs. The pack declares *what* servers exist; *which tools are granted* is a profile decision. If omitted, the field is auto-discovered from `mcp/*.json`.

```json
{
  "mcp": ["my-server", "another-server"]
}
```

Tool permissions are configured in profiles, not the manifest. See [Profiles — MCP server overrides](./profiles.md#mcp-server-overrides).

A silent profile (no `allowed_tools`, `always_allowed_tools`, or `disabled_tools` entries for a server) maps to "no allow list emitted" at the harness — the harness's native default (ask per call) applies. Packs that want to ship opinionated defaults do so through a bundled profile (`profiles/default.yaml`), not the manifest.

## 7. Plugin References

The `plugins/` directory contains JSON descriptors for harness plugins. A pack carries a reference to a marketplace plugin; it does not vendor plugin code.

```json
{
  "source": "github:linear/linear-codex-plugin"
}
```

The plugin id comes from the descriptor filename: `plugins/linear.json` declares plugin id `linear`. Descriptor fields are intentionally small:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source` | string | Yes | Human-readable plugin source reference. |
| `marketplace` | string | No | Omitted means the harness default marketplace. Bare names are used as-is. Source-prefixed values such as `github:obra/superpowers-marketplace` derive the marketplace name from the path leaf. |

Sync is additive-only in v1. aipack adds or updates plugin enablement in harness config, but removing a descriptor from a pack or profile does not disable or uninstall a plugin from the harness.

Codex writes declarative plugin stanzas to `config.toml`:

```toml
[plugins."linear@openai-curated"]
enabled = true
```

Claude Code writes `enabledPlugins` in `.claude/settings.json`. Source-prefixed marketplaces also add a registration to `~/.claude/plugins/known_marketplaces.json`.

```json
{
  "enabledPlugins": {
    "linear@claude-plugins-official": true
  }
}
```

Codex source-prefixed marketplaces must already be known to the local Codex installation. aipack does not run marketplace install commands.

## 8. Configurations

The `configs/` directory contains harness-specific settings templates and drop-in plugin config files, organized by harness name:

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

The manifest declares which files are settings (merged with engine-managed keys) and drop-in harness plugins (pure copies):

```json
{
  "configs": {
    "harness_settings": {
      "claudecode": ["settings.local.json"],
      "opencode": ["opencode.json"],
      "codex": ["config.toml"]
    },
    "harness_plugins": {
      "opencode": ["oh-my-opencode.json"]
    }
  }
}
```

**Settings** are base templates containing non-managed user preferences (theme, editor config, non-MCP permissions). String values in settings templates may use `{env:*}`, `{params.*}`, and `{pack:root}` references; aipack expands them before merging. The sync engine then merges settings with computed managed keys (MCP configs, tool permissions, content paths). Multiple packs can contribute settings for the same harness — they are deep-merged in profile order, with the first pack winning at leaf value conflicts. Both the expansion and the merge parse and re-marshal the file, so comments and source key ordering are not preserved in the rendered output.

**Drop-in harness plugins** are pure copies — synced as-is regardless of `--skip-settings`. Template references are not expanded in these files; if you need `{env:*}` or `{pack:root}` substitution, declare the file under `harness_settings` instead. Same-name plugin files from different packs produce an error. This is separate from first-class `plugins/<id>.json` marketplace references.

## 9. Hooks

The `hooks/` directory contains portable AIPack hook descriptors. Each hook is a directory with a required `HOOK.yaml` entry point. Handler scripts and supporting files live beside the descriptor and can be referenced with `{hook:root}`:

```
hooks/
└── datetime-injector/
    └── HOOK.yaml
```

The top-level `hooks` manifest field declares hook IDs:

```json
{
  "hooks": ["datetime-injector"]
}
```

When the field is omitted or empty, aipack auto-discovers `hooks/**/HOOK.yaml`. As with skills, the hook ID is the leaf directory name; subdirectories are authoring organization only.

`HOOK.yaml` describes portable lifecycle events, not Codex/Claude/Cline native JSON:

```yaml
name: datetime-injector
description: Print the current local time before each prompt is submitted.
events:
  - on: prompt.submit
    handler:
      type: command
      command: "printf 'Current local time: '; date '+%Y-%m-%d %H:%M:%S %Z'"
      timeout: 2s
```

Handler commands can be inline shell snippets like the example above. Put scripts
or other assets beside `HOOK.yaml` only when the hook needs more than a simple
command string.

For example, a hook can carry a small static asset and reference it through
`{hook:root}`:

```
hooks/
└── project-context/
    ├── HOOK.yaml
    └── context.md
```

```yaml
name: project-context
description: Print a short project note when a new run starts.
events:
  - on: run.start
    match:
      source: startup
    handler:
      type: command
      command: "cat {hook:root}/context.md"
      timeout: 2s
```

Supported portable events are `run.start`, `prompt.submit`, `tool.before`, and `tool.after`. Codex currently renders those events. Other harness-specific hook events are not portable pack events unless they are documented here.

String values in command handlers may use `{hook:root}`, `{pack:root}`, `{params.*}`, and `{env:*}` references. aipack expands them at sync time and renders the resulting command into the native harness hook configuration.

For Codex targets, sync renders `.codex/hooks.json` or `~/.codex/hooks.json`, maps portable events onto Codex native hook names, and writes matching `hooks.state.*.trusted_hash` entries into `config.toml` so rendered command hooks work without a manual trust step. Codex still provides its native hook stdin payload to commands; simple hooks can ignore stdin. `--skip-settings` skips base settings files but still writes the managed hook trust state needed for rendered hooks.

## 10. Composition

Packs compose through **profiles** — YAML files that declare which packs to load, how to filter their content, and what parameters to expand. For the full specification including profile structure, vector selectors, layering, overrides, quiet packs, and MCP server configuration, see [Profiles](./profiles.md).

## 11. Distribution

### 11.1 Installation sources

Git-backed packs are installed with shallow clone and then extracted into a clean content-only pack directory. Both HTTPS and SSH URLs are supported. Packs can live in a subdirectory of a larger repository (common for team mono-repos).

Archive-backed packs use static zip, tar, tar.gz, or tgz URLs. Archive installs record `method: archive`, can use `path` to select a subdirectory inside the archive, and are full-replaced on update because they do not carry git refs or commit hashes.

### 11.2 Registry

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
  example-pack:
    repo: "git@bitbucket.example.com:TEAM/tools.git"
    path: "ai-pack"
    ref: "main"
    description: "Team operational runbooks and MCP configs"
    owner: "ops-team"
    contact: "ops-team@example.com"
  team-archive:
    method: archive
    url: "https://downloads.example.com/aipack/team-archive.zip"
    description: "Static archive distribution"
collections:
  team-dev:
    description: "Team developer starter set"
    packs:
      - essentials
      - name: example-pack
        ref: "example-pack/v1.2.3"
        with: [profiles, registries]
      - name: team-archive
        with: all
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `method` | string | No | `clone` (default) or `archive` |
| `repo` | string | Yes for clone | Git repository URL |
| `url` | string | Yes for archive | Zip, tar, tar.gz, or tgz archive URL |
| `path` | string | No | Subdirectory within the repository or archive |
| `ref` | string | No | Git ref (branch, tag, commit); invalid for archive entries |
| `description` | string | No | Human-readable description |
| `owner` | string | No | Pack maintainer or team |
| `contact` | string | No | Contact information |
| `quiet` | bool | No | Hint: install with `quiet: true` in the profile entry (omitted selectors include nothing) |
| `content_paths` | map | No | Content type directory mappings for non-standard repo layouts |

Collections are install recipes for multiple registry packs. A collection entry has a human-readable `description` and a required ordered `packs` list. Each item can be a bare pack name or an object with:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Registry pack name to install |
| `ref` | string | No | Git ref override for this collection install; invalid for archive packs |
| `with` | string or string[] | No | Bundled content to accept for this pack: `profiles`, `registries`, `extras`, or `all` |

Multiple registry sources can be configured. The merged cached view resolves pack and collection name conflicts in source order, first source wins. Commands with `--registry <path>` use that one registry file instead of the merged cache.

### 11.3 Bundled profiles and registries

Packs can bundle profiles and registries for distribution. Drop YAML files into the standard directories and they're auto-discovered like any other content vector:

```
my-pack/
├── profiles/
│   ├── dev.yaml
│   └── full-stack.yaml
└── registries/
    └── team-tools.yaml
```

On install with `-w all`, bundled profiles are copied to the user's profile directory and bundled registry entries are merged into the user's embedded registry cache (`~/.config/aipack/registries/_embedded.yaml`) using first-seen-wins semantics — existing entries with the same pack name are not overwritten. This enables single-command team onboarding:

```bash
aipack pack install --url https://github.com/org/tools.git --path team-pack -w all
aipack profile set team --install
aipack sync
```

### 11.4 Content path remapping

Repositories that aren't structured as standard packs can be consumed via `content_paths` — directory mappings declared in registry entries or as CLI flags at install time. For the full specification and worked examples, see [Installing Packs](./installing-packs.md#installing-from-any-repository).

### 11.5 Quiet packs

Pack entries in a profile can be marked `quiet: true`, which changes the default from "include all content" to "include nothing." Content activates only via explicit `include` lists. See [Profiles — Quiet packs](./profiles.md#quiet-packs) for the full specification.

### 11.6 Pinning and git refs

Pack versioning uses git refs. Pack authors release new versions by tagging commits as `v1.2.3` (or `1.2.3` — the v-prefix is optional), or as `<pack-name>/v1.2.3` in multi-pack monorepos. aipack classifies any ref spec passed at install time by shape — exact semver, partial semver, namespaced semver, commit hash, `latest` sentinel, or literal ref (branch, non-semver tag) — and dispatches the appropriate resolution path. `--ref` is the primary flag; `--version` is a Kong alias kept for historical scripts.

The `version` field in `pack.json` is author metadata for display in `pack show` and `pack list`. **Git tags are the authoritative source** — aipack's resolver reads tags, not the manifest field. When a pack is installed with `--ref 1.2.3` and the tag exists, the install proceeds; if the resulting `pack.json` `version` field disagrees with the tag, aipack prints a non-blocking warning to nudge the author. The rationale for git-tags-as-truth (immutable, distributed, branch-conflict-free, matches Go modules) is documented in [Creating Packs — Releasing a new version](./creating-packs.md#releasing-a-new-version).

**Partial semver references.** `pack install` and `pack update` accept partial version specifiers — `v1` matches the highest stable `v1.x.x` tag, `v1.2` matches the highest `v1.2.x`. Partial resolution queries the remote tags, picks the highest matching stable tag (prereleases skipped), and pins the lockfile to that exact version. Partial references are a discovery shortcut, not a channel: the lockfile records the resolved tag, and users move the pin by re-running with the partial specifier. Prereleases (`v1.2.0-beta.1`) only match exact specifiers.

**Namespaced tags.** Multi-pack monorepos use `<pack-name>/vX.Y.Z` tags (Go-module style) to version sibling packs independently. When the lockfile records a namespaced ref, subsequent `pack update` and `pack versions` commands auto-derive the prefix — users can pass bare semver on update (`--ref 0.3.1`) and aipack resolves against `<pack-name>/v0.3.1` automatically.

**One version per machine.** aipack installs exactly one version of a pack at a time. Every profile that references `essentials` sees the same installed version — profiles cannot declare per-profile version constraints. Users who need parallel versions install the alternate under a different name via `--name`; see [Installing Packs — Parallel versions](./installing-packs.md#parallel-versions). This is an intentional design choice: packs are content delivered to harnesses, not libraries consumed transitively, so there is no dependency graph to resolve and version pinning functions as a user safety feature (closer to `brew pin` than to an npm lockfile range).

aipack records two pieces of ref metadata per installed pack in the lockfile, and derives pin state from them on read:

- **Git ref** (`ref`) — the raw remote tag spelling used at clone time, preserved exactly. Flat semver (`v1.2.3`), namespaced semver (`my-pack/v1.2.3`), or a commit hash marks the pack as **pinned**; a branch name or empty ref tracks upstream. `pack install --ref 1.2.3` and `pack install --version 1.2.3` produce the same on-disk state because `--version` is a Kong alias for `--ref`.
- **Commit hash** (`commit_hash`) — the resolved SHA at install/update time. Enables fast-path update detection via `git ls-remote` — `pack update` can skip the clone entirely when the remote hash hasn't changed.

Pin state is not stored as a separate field; it is computed from `ref`'s shape. `pack show --json` and `pack list --json` expose a derived `pin` field (omitted for unpinned installs) so downstream tooling can distinguish pinned from tracking installs without reproducing the classification logic. The same label surfaces in `pack list` text output and the TUI details panel as `v1.2.3 (pinned)` or `@abc1234 (pinned)`; namespaced pins strip the prefix for display (`v0.3.0 (pinned)` for `my-pack/v0.3.0`).

Pinned packs stay at their resolved commit on `pack update` until the user explicitly moves the pin. See [Installing Packs — Pinning to a ref](./installing-packs.md#pinning-to-a-ref).

## 10. Harness Contract

The sync engine guarantees the following for all supported harnesses:

1. **Content fidelity** — the body of each content file (the text after frontmatter) is delivered to the agent without modification.
2. **Frontmatter transformation** — frontmatter is translated to harness-native format where required (e.g., agent `disallowed_tools` → Claude Code `disallowedTools`).
3. **MCP configuration** — server definitions are rendered into harness-native config files with environment references translated to the correct syntax.
4. **Conflict detection** — user modifications to managed files are detected via content digest and surfaced as diffs rather than silently overwritten.
5. **Determinism** — given identical inputs and profile, sync produces byte-identical outputs across runs.

Per-harness rendering details (file paths, config formats, merge behavior) are documented in the [Harness Reference](./harness-reference.md). Four harnesses are supported: Claude Code, OpenCode, Codex, and Cline.

## Appendix A: Complete `pack.json` Example

```json
{
  "schema_version": 2,
  "name": "example-pack",
  "version": "2026.03.12",
  "root": ".",
  "mcp": ["issue-tracker", "deploy-tool"],
  "hooks": ["datetime-injector"],
  "configs": {
    "harness_settings": {
      "claudecode": ["settings.local.json"],
      "opencode": ["opencode.json"]
    },
    "harness_plugins": {
      "opencode": ["oh-my-opencode.json"]
    }
  },
  "extras": [
    "scripts/run-server.sh",
    "data"
  ],
  "profiles": ["dev", "full-stack"],
  "registries": ["team-tools"]
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
  "schema_version": 2,
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
- Current `schema_version: 2` MCP server lists and legacy `schema_version: 1` MCP objects
- Harness settings and drop-in plugin config inventory
- Current `extras` plus the legacy v1 `include` list used by older packs
- Strict mode — no unknown properties

### [`mcp-server.schema.json`](../schemas/mcp-server.schema.json) — MCP Server Definition

Validates `mcp/*.json` files:

- Required fields (`name`, `transport`)
- Transport-conditional requirements (`command` for stdio, `url` for sse/streamable-http)
- Server name pattern matching filename convention
- Runtime-only fields rejected (`allowed_tools`, `always_allowed_tools`, `disabled_tools`, `source_pack` — these are set by profile resolution, not authored)
- Tool inventory uniqueness (`available_tools`)
- Strict mode — no unknown properties

### Editor integration

Add a `$schema` reference to enable editor autocomplete and validation:

```json
{
  "$schema": "https://raw.githubusercontent.com/shrug-labs/aipack/main/schemas/pack.schema.json",
  "schema_version": 2,
  "name": "my-pack",
  "root": "."
}
```
