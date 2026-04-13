# Installing Packs

Agent knowledge is already written. Skills live in team repos, rules accumulate in dotfile directories, workflows exist as markdown that someone refined over months. Most of it was never structured as a pack — but the content is there, and the format is standard: markdown with YAML frontmatter.

aipack can install from these repositories directly. You point it at the directories that contain content, it extracts them into a standard pack layout, and discards the rest. The source repo doesn't need a `pack.json` or any restructuring — the consumer does the packaging.

## Installing a standard pack

A standard pack has a `pack.json` manifest and content in conventional directories. Three ways to install:

```bash
# From a git URL (HTTPS or SSH)
aipack pack install --url https://github.com/org/their-pack.git --add

# From a local path (symlinked by default, --copy for full copy)
aipack pack install ./path/to/pack --add

# By registry name (looked up in configured registries)
aipack pack install their-pack --add
```

`--add` adds the pack to the active profile. Without it, the pack is installed to disk but not active — use `aipack pack add <name>` to add it to a profile later.

After installing, `aipack sync` renders the pack content into your harness config.

If the active profile references packs you haven't installed yet, catch up in one command:

```bash
aipack pack install       # reconcile the active profile — installs anything missing
```

(Equivalent to `aipack pack install -m`, which stays as an explicit alias for scripts.)

For creating your own pack from scratch, see [Creating Packs](./creating-packs.md).

## Installing from any repository

Not every repository with useful content follows the pack directory structure. A skill collection might keep skills at `src/agent/skills/`. A dotfile repo might organize rules under `.claude/rules/`. Content type flags tell aipack where to find each type of content:

```bash
aipack pack install \
  --url https://github.com/org/their-repo.git \
  --skills src/skills --rules docs/rules \
  --name their-content -q --add
aipack sync
```

aipack clones the repo, copies the two mapped directories into a standard pack layout, generates a `pack.json`, and discards the clone. The installed pack contains only the extracted content:

```
~/.config/aipack/packs/their-content/
├── pack.json          # generated at install time
├── skills/
│   ├── deploy/SKILL.md
│   └── triage/SKILL.md
└── rules/
    ├── code-review.md
    └── testing.md
```

No `.git/`, no test directories, no CI files — just the content you pointed at.

The `-q` flag marks the pack as **quiet** in your profile: omitted content types include nothing instead of everything. Since you only mapped skills and rules, those are what you get. See [Profiles — Quiet packs](./profiles.md#quiet-packs) for the full semantics.

Content flags require `--url` (this is a remote extraction feature) and `--name` (there's no `pack.json` to derive a name from). Available flags: `--rules`, `--skills`, `--agents`, `--workflows`, `--prompts`. Each takes a directory path relative to the repository root.

## Selecting content from a quiet pack

A quiet pack with 50 skills doesn't dump all 50 into your harness. You choose what to activate in your profile:

```yaml
packs:
  - name: their-content
    quiet: true
    skills:
      include: [deploy, triage]
    rules:
      include: [code-review]
```

Only the three named items sync. Add or remove items from `include` and re-sync to adjust. Use `aipack search` to discover what's available in the pack.

For the full specification of selectors, quiet semantics, and role-based profiles, see [Profiles](./profiles.md).

## Registry entries for team distribution

When the whole team should consume the same external repo, encode the mapping in a registry entry instead of having everyone type the flags:

```yaml
schema_version: 1
packs:
  essentials:
    repo: "https://github.com/shrug-labs/packs.git"
    path: "essentials"
    description: "Foundation pack"

  team-ops:
    repo: "https://github.com/org/platform.git"
    quiet: true
    content_paths:
      skills: tools/agent/skills
      rules: tools/agent/rules
    description: "Operational skills from the platform monorepo"

  cline-setup:
    repo: "git@github.com:user/my-cline-setup.git"
    content_paths:
      rules: .clinerules
      skills: .cline/skills
    description: "Cline-native rules and skills"
```

Now anyone installs by name:

```bash
aipack pack install team-ops --add
```

The content flags, quiet hint, and description are baked into the registry entry. `pack update` re-clones and re-extracts, picking up upstream changes.

Registry entries also support `ref` (git branch/tag/commit) and `path` (subdirectory within the repo for standard packs that include a `pack.json`). For the full registry schema, see the [Pack Format Specification](./pack-format.md#92-registry).

## Common layouts

Repositories organized by harness convention work naturally with `content_paths`. Each example shows the source tree and its registry entry.

**Cline dotfile repo:**

```
my-cline-setup/
  .clinerules/
    code-style.md
    testing.md
  .cline/
    skills/
      deploy/SKILL.md
      review/SKILL.md
```

```yaml
my-cline-setup:
  repo: git@github.com:user/my-cline-setup.git
  content_paths:
    rules: .clinerules
    skills: .cline/skills
```

**Claude Code repo:**

```
my-claude-setup/
  .claude/
    rules/
      anti-slop.md
      commit-style.md
    skills/
      oncall/SKILL.md
```

```yaml
my-claude-setup:
  repo: git@github.com:user/my-claude-setup.git
  content_paths:
    rules: .claude/rules
    skills: .claude/skills
```

**Monorepo with agent config in a subdirectory:**

```
platform/
  tools/
    agent/
      skills/
        deploy/SKILL.md
        triage/SKILL.md
      rules/
        code-review.md
```

```yaml
platform-skills:
  repo: https://github.com/org/platform.git
  quiet: true
  content_paths:
    skills: tools/agent/skills
    rules: tools/agent/rules
```

In every case, the source repo stays untouched. aipack reads the content from wherever it lives and delivers it to whichever harness the consumer uses.

## Content path reference

Valid content type keys: `rules`, `agents`, `workflows`, `skills`, `prompts`. Values are directory paths relative to the repository root. When `--path` (subpath) is also set, paths are relative to the subpath root.

Files within mapped directories follow the same conventions as standard packs: `.md` files with YAML frontmatter for rules, agents, workflows, and prompts; `<name>/SKILL.md` subdirectories for skills.

Only declared types are discovered — omitted types have no content. This is independent of the `quiet` flag: `content_paths` controls what's *extracted*, while `quiet` controls what's *activated* in a profile.

Content path mappings, install metadata, and version pins are stored in `aipack.lock` (the lockfile) under the pack's entry:

```yaml
packs:
  team-ops:
    origin: https://github.com/org/platform.git
    method: clone
    installed_at: "2026-04-02T10:30:00Z"
    ref: v1.2.3                # v-prefixed semver or commit hash = pinned; omit to track default branch
    commit_hash: "abc123..."
    content_paths:
      skills: tools/agent/skills
      rules: tools/agent/rules
    resolved:                  # pack contents inventory — baseline for drift detection
      inventoried_at: "2026-04-02T10:30:00Z"
      rules: [anti-slop, show-your-work]
      skills:
        deploy-check:
          description: "Preflight checks before a deploy"
          assets: [check.sh, templates/report.md]
      mcp_servers:
        api:
          available_tools: [list, get, create]
          required_refs:
            - {kind: param, name: base_url}
            - {kind: env, name: API_TOKEN}
          default_allowed_tools: [list, get]
```

The lockfile is machine-managed — never hand-edit it. Earlier versions of aipack stored install metadata in `sync-config.yaml` under `installed_packs`; that section is migrated to the lockfile transparently the first time `aipack doctor` runs (or on the first lockfile read in any pack command). The `resolved` block records the pack's content state at the end of the last successful sync — rule/agent/workflow IDs, skill descriptions and bundled assets, and MCP server shapes — so the next sync can diff against it and surface drift. See [Sync and Save](./sync.md#content-drift-detection) for the drift detection flow.

## Versioning

Packs can be installed at a specific semver tag, a partial semver reference, or a commit hash. Without a version, `pack install` tracks the default branch's HEAD.

```bash
# Pin to a specific semver tag
aipack pack install my-pack@1.2.3
aipack pack install my-pack --version 1.2.3

# Partial reference: resolves to latest stable v1.x.x (or v1.2.x), pins to that exact tag
aipack pack install my-pack@v1
aipack pack install my-pack@v1.2

# Pin to a commit hash
aipack pack install my-pack@abc1234

# Track HEAD (default behavior — no version)
aipack pack install my-pack
```

Version sources are git tags filtered for valid semver. The `pack.json` `version` field is informational — git tags are authoritative. If they disagree at install time, aipack prints a warning to nudge pack authors but the install proceeds.

**URL shape matters when using `--version`.** Version flags require a clone-able git repo URL — either a plain repo URL like `https://github.com/org/repo` or an SSH URL like `git@host:path.git`. GitHub blob-style URLs (`https://github.com/org/repo/tree/main/subdir`) are not supported alongside `--version` or `@version`; use `--url <repo-url> --path <subdir> --version <X>` instead.

Partial references (`v1`, `v1.2`) query the remote tags, pick the highest matching stable tag, and pin to that exact version — they are a discovery shortcut, not a channel. The lockfile records the resolved tag (`1.5.0`), not the partial (`v1`), so `pack update` won't auto-bump when new v1.x.x tags are released; re-run `pack update my-pack --version v1` to move the pin. Prereleases are skipped during partial matching; install them with an exact tag if needed (`my-pack@v1.5.0-beta.1`).

List the available semver versions for a pack:

```bash
aipack pack versions my-pack
```

This works for installed packs (origin from the lockfile) and for packs in a registry but not yet installed.

### Parallel versions

aipack installs one version of a pack per machine. Running `pack install my-pack@1.0.0` followed by `pack install my-pack@2.0.0` upgrades in place rather than creating side-by-side installs — every profile that references `my-pack` sees whichever version was installed last. Profiles cannot declare per-profile version constraints; version pinning is a user-scoped safety feature, not a composition primitive.

If you need both versions available simultaneously — for example, to test an upgrade alongside your current install, to compare two releases side-by-side, or to keep a known-good fallback while experimenting — install the alternate under a different name:

```bash
aipack pack install my-pack@1.0.0 --name my-pack-v1
aipack pack install my-pack@2.0.0              # installs as my-pack
```

Each name is a separate pack from the lockfile's perspective; profiles reference them independently (`my-pack-v1` in one profile, `my-pack` in another). This is the supported escape hatch — reach for it only when you actually need both versions at once.

## Updating

`pack update` re-clones from origin and re-extracts content. The `commit_hash` recorded at install time enables change detection — if the remote HEAD hasn't changed, the update is skipped. When refreshing multiple packs, updates run in parallel (up to three concurrent clones), so total wall-clock time is bounded by the slowest pack rather than the sum. Clones for the same origin share a local bare-clone cache, so multiple packs installed from one monorepo only fetch from the remote once.

```bash
aipack pack update              # update all installed packs (default)
aipack pack update my-pack      # update one specific pack
```

**Pinned packs stay pinned.** A bare `pack update` on a versioned pack does not change the install. It checks the remote and reports the latest available version, but leaves the pin in place. Move the pin explicitly:

```bash
aipack pack update my-pack --version 1.3.0   # move the pin to a new tag
aipack pack update my-pack --version latest  # clear the pin, track HEAD again
```

For standard packs (no `content_paths`), the same re-clone-and-extract process applies. Installed packs always contain only content — no `.git/` directories, no non-pack files.

## What to read next

- [Getting Started](./getting-started.md) — install packs and sync for the first time
- [Creating Packs](./creating-packs.md) — author your own pack from scratch
- [Profiles](./profiles.md) — compose and filter installed packs, define parameters, scope to roles
- [Pack Format Specification](./pack-format.md) — content format, manifest schema, MCP server definitions
