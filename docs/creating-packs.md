# Creating Packs

Create a pack from scratch or from existing content, validate it, and share it. For installing existing packs, see [Getting Started](./getting-started.md). For installing from repositories that aren't structured as packs, see [Installing Packs](./installing-packs.md). For profiles and composition, see [Profiles](./profiles.md).

**Contents:**
[What's a pack?](#whats-a-pack) · [Create a pack](#create-a-pack) · [Write pack content](#write-pack-content) · [Validate](#validate-your-pack) · [Share your pack](#share-your-pack)

## What's a pack?

A pack is a directory with a manifest (`pack.json`) and markdown files organized by type. You write content once — aipack renders it into the native format for Claude Code, Codex, OpenCode, and Cline.

```
my-pack/
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
aipack pack create my-pack
```

This scaffolds the directory structure in the current directory, symlinks it into the packs directory, and generates a minimal `pack.json`:

```json
{
  "schema_version": 1,
  "name": "my-pack",
  "version": "0.1.0",
  "root": "."
}
```

Content vectors (rules, skills, etc.) are omitted from the manifest — aipack auto-discovers them by scanning the directories at sync time. Drop markdown files into the right directory and they're picked up automatically.

To create a pack that references existing content directories (useful when content already lives in your project):

```bash
aipack pack create my-pack --skills ./src/skills --rules ./docs/rules
```

This creates the pack scaffold with directory-level symlinks instead of empty directories, so edits to the source are immediately reflected in the pack. Available flags: `--rules`, `--skills`, `--agents`, `--workflows`, `--prompts`.

### From existing content

If you have a repo with rules, skills, or instructions for AI agents — pack content uses standard conventions (YAML frontmatter + markdown), so existing content from other frameworks typically works without modification.

1. Create the same minimal `pack.json` in the repo root (or a subdirectory).

2. Organize your existing markdown files into the conventional directories (`rules/`, `skills/`, `workflows/`, `agents/`). Auto-discovery handles the rest.

3. Add YAML frontmatter to each file. The `name` and `description` fields help with search indexing and harness rendering:

```markdown
---
name: code-review-standards
description: Code review conventions and quality gates
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
description: Enforce code review conventions on all changes
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
- **`{env:VAR}`** — resolved at sync time to the literal value from the process environment. If the variable is not set, the MCP server is skipped and a warning is emitted. Use for secrets and user-specific values that shouldn't be committed.

The manifest can declare default tool approvals, and profiles can override them per server. See the [Pack Format Specification](./pack-format.md#6-mcp-servers) for the full field reference and the [JSON Schema](../schemas/mcp-server.schema.json) for editor validation.

## Validate your pack

```bash
aipack pack validate /path/to/my-pack
```

Validate checks manifest structure, content inventory (declared files exist on disk, MCP server names match filenames), and content policy (frontmatter presence, no secrets, no hardcoded paths). It reports findings without modifying anything. JSON Schemas for `pack.json` and MCP server files are also available for [editor validation](./pack-format.md#appendix-c-json-schemas).

## Share your pack

Anyone consuming your pack needs aipack installed (see the [README](../README.md#install) for brew, script, and source options). On first use, `aipack init` bootstraps the config directory, default profile, and public registry. `pack install` also creates the config directory if it doesn't exist, so the onboarding flow below can skip the explicit init.

### The simplest path

Your pack lives in a git repo. Others install it directly:

```bash
# From a git URL
aipack pack install --url https://github.com/org/shared-repo.git --path my-pack --add

# From a local clone (symlinked by default, --copy for a full copy)
aipack pack install /path/to/local/clone/my-pack --add

# Then sync to their harness
aipack sync
```

### Scalable distribution with profiles and registries

For packs with dependencies or multiple profiles, bundle profiles and a registry with the pack. This turns setup into three commands.

**1. Add bundled profiles** — see [Profiles](./profiles.md#role-based-profiles) for role-based examples. At minimum, a default:

```yaml
# profiles/default.yaml
schema_version: 2
params:
  jira_url: "https://jira.example.com"
packs:
  - name: my-pack
```

**2. Add a bundled registry** — `registries/team-tools.yaml`:

```yaml
schema_version: 1
packs:
  my-pack:
    repo: "https://github.com/org/shared-repo.git"
    path: "my-pack"
    ref: "main"
    description: "Shared rules, skills, and workflows"
    owner: "my-team"
```

Profiles and registries are auto-discovered from their directories, just like rules and skills. No need to list them in `pack.json` unless you want to filter which ones are included.

**3. Install with bundled content** — three commands:

```bash
aipack pack install --url https://github.com/org/shared-repo.git \
  --path my-pack -w all
aipack profile set default --install
aipack sync
```

`-w all` accepts all bundled content (profiles, registries, extras). `--install` fetches any dependency packs. After this, the consumer's harness is fully configured.

### Releasing a new version

Pack versions are git tags. When you're ready to release, create a tag and push it — that's what consumers resolve against when they run `pack install my-pack@1.2.3`.

```bash
# 1. Bump the version field in pack.json
# 2. Commit the bump
git commit -am "release v1.2.3"

# 3. Tag the commit
git tag v1.2.3

# 4. Push the branch and the tag
git push origin main v1.2.3
```

Consumers can now install the new version with `aipack pack install my-pack@1.2.3`, `@v1.2`, or `@v1` (see [Installing Packs — Pinning to a ref](./installing-packs.md#pinning-to-a-ref) for the full matching rules).

The `version` field in `pack.json` is author metadata — it's displayed by `pack show` and `pack list`, but aipack's resolver reads git tags, not the manifest. **Skipping the tag does not make the version installable.** aipack prints a one-line warning at install time when the installed tag's manifest `version` differs from the tag name — that warning is the only automated feedback loop, so watch for it if you see authors bumping the field without tagging.

**Why git tags, not the manifest field:** tags are immutable, content-addressable, and distributed. They survive repo moves, mirrors, and forks without coordination. (Go modules use the same model.) Treating the manifest as the source of truth would introduce branch-conflict failure modes — what does "version 1.2.3" mean if it exists on two non-merged branches? — without buying anything the tag already provides.

### Releasing a namespaced version

Repositories that ship multiple packs in subdirectories need namespaced tags — a flat `v1.2.3` tag is ambiguous across sibling packs. The convention is `<pack-name>/vX.Y.Z` (Go-module style): the pack name goes in front, the semver goes after, separated by a single slash. Consumers pin against the namespaced form; aipack's resolver recognizes the shape automatically — no registry schema changes required.

```bash
# 1. Bump the version field in my-pack/pack.json
# 2. Commit the bump
git commit -am "my-pack: release v1.2.3"

# 3. Tag the commit with the namespaced form
git tag my-pack/v1.2.3

# 4. Push the branch and the tag
git push origin main my-pack/v1.2.3
```

Consumers install with `aipack pack install my-pack --ref my-pack/v1.2.3` (or with `@my-pack/v1.2.3` as the positional shorthand). Once installed, subsequent `pack update` and `pack versions` commands auto-derive the prefix from the lockfile — users can pass bare semver (`--ref 1.2.4`) and aipack resolves against `my-pack/v1.2.4` automatically.

The namespaced and flat forms are mutually exclusive per pack: a pack uses one convention or the other, not both. The choice depends on repo layout — single-pack repos keep flat tags (simpler, unchanged from above), multi-pack repos need namespacing (isolation, sibling independence). The two modes coexist across packs — one multi-pack repo can sit alongside many single-pack repos in the same registry.

## What to read next

- [Getting Started](./getting-started.md) — install packs and sync to your harness
- [Installing Packs](./installing-packs.md) — install from any git repository, including repos not structured as packs
- [Profiles](./profiles.md) — compose packs, filter content, expand parameters, scope to roles
- [Harness Reference](./harness-reference.md) — per-harness rendering behavior and write targets
- [Pack Format Specification](./pack-format.md) — content format, manifest schema, MCP server definitions
