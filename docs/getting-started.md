# Getting Started: Authoring and Sharing Packs

This guide walks through turning your team's shared agent configuration into a pack and distributing it. By the end, your team installs your pack in three commands and gets rules, skills, and workflows synced to whatever coding assistant they use.

**Contents:**
[What's a pack?](#whats-a-pack) · [Create a pack](#create-a-pack) · [Write pack content](#write-pack-content) · [Validate](#validate-your-pack) · [Share with your team](#share-with-your-team) · [Profiles](#profiles) · [Multi-harness support](#multi-harness-support)

## What's a pack?

A pack is a directory with a manifest (`pack.json`) and markdown files organized by type. You write content once — aipack renders it into the native format for Claude Code, Codex, OpenCode, and Cline.

```
my-team-pack/
├── pack.json          # manifest (name + version)
├── rules/             # always-on behavioral constraints
├── skills/            # on-demand knowledge (subdirectories)
├── workflows/         # step-by-step procedures
├── agents/            # tool-using sub-personas
├── mcp/               # MCP server definitions
└── configs/           # harness settings templates (advanced)
```

If you already have markdown files with instructions for AI agents — in a shared repo, an `agents.md`, a set of review guidelines — you have most of a pack already.

## Create a pack

### Starting fresh

```bash
aipack pack create ./my-team-pack
```

This scaffolds the directory structure and a minimal `pack.json`:

```json
{
  "schema_version": 1,
  "name": "my-team-pack",
  "version": "0.1.0",
  "root": "."
}
```

Content vectors (rules, skills, etc.) are omitted from the manifest — aipack auto-discovers them by scanning the directories at sync time. Drop markdown files into the right directory and they're picked up automatically.

### From existing content

If you have a repo with shared rules or instructions:

1. Create the same minimal `pack.json` in the repo root (or a subdirectory).

2. Organize your existing markdown files into the conventional directories (`rules/`, `skills/`, `workflows/`, `agents/`). Auto-discovery handles the rest.

3. Add YAML frontmatter to each file. The `name` and `description` fields help with search indexing and harness rendering:

```markdown
---
name: code-review-standards
description: Team code review conventions and quality gates
---

Your existing content here...
```

That's it. No need to enumerate files in the manifest — auto-discovery finds everything in the right directories. If you need to include only specific files, list their IDs explicitly in `pack.json` — the list acts as a filter. See the [Pack Format Specification](./pack-format.md#2-manifest-packjson) for details.

## Write pack content

All content is markdown with YAML frontmatter. The frontmatter carries metadata for the sync engine; the body is what the agent reads. Full field reference for each vector is in the [Pack Format Specification](./pack-format.md#4-content-vectors).

### Rules

Always-on constraints, loaded into every conversation. Keep them concise — they cost context in every session.

```markdown
---
name: code-review-standards
description: Enforce team code review conventions on all changes
---

Before approving any PR:
1. All public methods have tests
2. No TODO comments without a linked ticket
3. Error messages include enough context to debug without the source
4. No secrets, credentials, or internal URLs in committed code
```

File: `rules/code-review-standards.md`

### Skills

On-demand knowledge, loaded when relevant. Each skill is a subdirectory with a `SKILL.md` entry point and optional supporting files.

```
skills/api-patterns/
├── SKILL.md           # entry point
├── error-handling.md  # supporting reference
└── pagination.md      # supporting reference
```

```markdown
---
name: api-patterns
description: Use when implementing or reviewing API endpoints
---

Invoke this skill when working on API endpoints in this codebase...
```

File: `skills/api-patterns/SKILL.md`

### Workflows

Repeatable multi-step procedures, invoked explicitly.

```markdown
---
name: pr-review
description: Systematic PR review with security, testing, and style checks
---

1. Read the PR description and linked tickets
2. Review each changed file for correctness
3. Check test coverage for new/changed behavior
4. Flag security concerns (injection, auth, secrets)
5. Summarize findings with severity ratings
```

File: `workflows/pr-review.md`

### Agents

Scoped sub-personas with constrained tools and domain knowledge.

```markdown
---
name: security-reviewer
description: Focused security review of code changes
tools:
  - Read
  - Grep
  - Glob
disallowed_tools:
  - Edit
  - Write
  - Bash
---

You are a security reviewer. Analyze code changes for injection
vulnerabilities, auth gaps, secrets in source, and insecure defaults.
Report findings with severity, location, and remediation.
```

File: `agents/security-reviewer.md`

### MCP servers

JSON definitions in `mcp/`, one file per server. The filename (minus `.json`) must match the `name` field.

```json
{
  "name": "jira",
  "transport": "stdio",
  "command": ["{env:HOME}/.local/bin/jira-mcp-server"],
  "env": {
    "JIRA_URL": "{params.jira_url}",
    "JIRA_TOKEN": "{env:JIRA_API_TOKEN}"
  },
  "available_tools": ["jira_search", "jira_get_issue", "jira_add_comment"]
}
```

File: `mcp/jira.json`

Two kinds of placeholders keep server definitions portable:

- **`{params.KEY}`** — expanded from the active profile at sync time. Use for values that differ between teams or environments (URLs, project names).
- **`{env:VAR}`** — translated to each harness's native variable syntax. Use for secrets and user-specific values that shouldn't be committed.

The manifest can declare default tool approvals, and profiles can override them per server. See the [Pack Format Specification](./pack-format.md#6-mcp-servers) for the full field reference and the [JSON Schema](../schemas/mcp-server.schema.json) for editor validation.

## Validate your pack

```bash
aipack validate /path/to/my-team-pack
```

Validate checks manifest structure, content inventory (declared files exist on disk, MCP server names match filenames), and content policy (frontmatter presence, no secrets, no hardcoded paths). It reports findings without modifying anything. JSON Schemas for `pack.json` and MCP server files are also available for [editor validation](./pack-format.md#appendix-c-json-schemas).

## Share with your team

Team members need aipack installed (see the [README](../README.md#install) for brew, script, and source options). On first use, `aipack init` bootstraps the config directory, default profile, and public registry. `pack install` also creates the config directory if it doesn't exist, so teams using the onboarding flow below can skip the explicit init.

### The simplest path

Your pack lives in a git repo. Team members install it directly:

```bash
# From a git URL
aipack pack install --url https://github.com/org/shared-repo.git --path my-team-pack

# From a local clone (symlinked by default, --copy for a full copy)
aipack pack install /path/to/local/clone/my-team-pack

# Then sync to their harness
aipack sync
```

### Scalable distribution with profiles and registries

For teams with multiple packs or complex configurations, bundle profiles and a registry with your pack. This turns onboarding into three commands.

**1. Add bundled profiles** — see [Profiles](#profiles) below for role-based examples. At minimum, a default:

```yaml
# profiles/default.yaml
schema_version: 2
params:
  jira_url: "https://jira.example.com"
packs:
  - name: my-team-pack
```

**2. Add a bundled registry** — `registries/registry.yaml`:

```yaml
schema_version: 1
packs:
  my-team-pack:
    repo: "https://github.com/org/shared-repo.git"
    path: "my-team-pack"
    ref: "main"
    description: "Team shared rules, skills, and workflows"
    owner: "platform-team"
```

**3. Declare them in pack.json**:

```json
{
  "schema_version": 1,
  "name": "my-team-pack",
  "version": "0.1.0",
  "root": ".",
  "profiles": [
    "profiles/default.yaml",
    "profiles/oncall.yaml",
    "profiles/new-engineer.yaml"
  ],
  "registries": ["registries/registry.yaml"]
}
```

**4. Team onboarding** — three commands:

```bash
aipack pack install --url https://github.com/org/shared-repo.git \
  --path my-team-pack --seed
aipack profile set default --install
aipack sync
```

`--seed` applies the bundled profiles and registry. `--install` fetches any dependency packs. After this, the team member's harness is fully configured.

## Profiles

Profiles are YAML files (schema version 2) that control which packs to load, what parameters to expand, and what content each role needs. They live in `~/.config/aipack/profiles/` once installed.

### Parameters

Parameters make pack content portable. Define them in the profile; they expand into MCP server definitions, harness settings, and any pack content using `{params.KEY}` placeholders. Two teams using the same pack with different Jira instances just need different parameter values.

### Role-based profiles

One pack can serve an entire team. Different profiles scope content and MCP servers to what each role actually needs. Here's the pattern:

**`profiles/default.yaml`** — baseline, full access:

```yaml
schema_version: 2
params:
  jira_url: "https://jira.example.com"
  confluence_url: "https://confluence.example.com"
packs:
  - name: my-team-pack
    settings:
      enabled: true
    mcp:
      jira:        { enabled: true }
      confluence:  { enabled: true }
      build-system: { enabled: true }
```

The oncall and new-engineer profiles start from the same structure but scope differently:

| Profile | Workflows | Skills | MCP |
|---------|-----------|--------|-----|
| **default** | all | all | jira, confluence, build-system |
| **oncall** | all except `onboard` | all | + monitoring |
| **new-engineer** | only `onboard` | only `codebase-overview`, `dev-setup` | jira, confluence only |

In practice, each profile is a full YAML file. The oncall profile adds a `monitoring` MCP server and excludes onboarding workflows. The new-engineer profile uses `include` lists to narrow skills and workflows to what's relevant for ramping up, and disables build-system and monitoring MCP servers.

Team members activate the profile that matches their role:

```bash
aipack profile set oncall --install
aipack sync
```

Bundle these profiles with the pack (list them in `pack.json`'s `profiles` field) so they're installed automatically with `--seed`.

### Layering multiple packs

Profiles can compose packs from different sources — an org-wide base, a team pack, and personal preferences:

```yaml
schema_version: 2
params:
  jira_url: "https://jira.example.com"
packs:
  - name: org-base          # shared across all teams
  - name: my-team-pack      # team-specific rules and skills
  - name: personal           # individual preferences
    overrides:
      rules: ["anti-slop"]  # personal version replaces org/team version
```

Packs are processed in order. Duplicate content IDs require explicit `overrides` or the sync engine raises a conflict. See the [Pack Format Specification](./pack-format.md#84-layering-and-precedence) for the full precedence rules.

### Content selection

Each content vector supports `include` and `exclude` for fine-grained control, with glob pattern support:

```yaml
packs:
  - name: org-base
    rules:
      exclude: ["noisy-rule"]
    skills:
      include: ["api-*"]
```

## Multi-harness support

aipack calls each coding assistant a "harness." The same pack works across all four supported harnesses — the content is identical, only the rendering differs:

```bash
aipack sync --harness claudecode    # Claude Code
aipack sync --harness codex         # Codex
aipack sync --harness opencode      # OpenCode
aipack sync --harness cline         # Cline
```

By default, `aipack sync` targets whichever harnesses are configured in `~/.config/aipack/sync-config.yaml`.

## What to read next

- [Pack Format Specification](./pack-format.md) — full format reference including content vectors, MCP servers, harness settings, environment references, and JSON Schemas
- [aipack Reference](./aipack.md) — complete CLI reference, per-harness behavior, sync contract, and save modes
