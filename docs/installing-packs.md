# Installing Packs

Agent knowledge is already written. Skills live in team repos, rules accumulate in dotfile directories, workflows exist as markdown that someone refined over months. Most of it was never structured as a pack — but the content is there, and the format is standard: markdown with YAML frontmatter.

aipack can install from these repositories directly. You point it at the directories that contain content, it extracts them into a standard pack layout, and discards the rest. The source repo doesn't need a `pack.json` or any restructuring — the consumer does the packaging.

## Installing a standard pack

A standard pack has a `pack.json` manifest and content in conventional directories. Three ways to install:

```bash
# From a git URL (HTTPS or SSH)
aipack pack install --url https://github.com/org/their-pack.git

# From a local path (symlinked by default, --copy for full copy)
aipack pack install ./path/to/pack

# By registry name (looked up in configured registries)
aipack pack install their-pack
```

After installing, `aipack sync` renders the pack content into your harness config.

If the active profile references packs you haven't installed yet, catch up in one command:

```bash
aipack pack install -m    # install all missing packs from the active profile
```

For creating your own pack from scratch, see [Creating Packs](./creating-packs.md).

## Installing from any repository

Not every repository with useful content follows the pack directory structure. A skill collection might keep skills at `src/agent/skills/`. A dotfile repo might organize rules under `.claude/rules/`. Content type flags tell aipack where to find each type of content:

```bash
aipack pack install \
  --url https://github.com/org/their-repo.git \
  --skills src/skills --rules docs/rules \
  --name their-content -q
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

The `-q` flag registers the pack as **quiet** in your profile: omitted content types include nothing instead of everything. Since you only mapped skills and rules, those are what you get. See [Profiles — Quiet packs](./profiles.md#quiet-packs) for the full semantics.

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
aipack pack install team-ops
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

Content path mappings are stored in `sync-config.yaml` under the pack's `installed_packs` entry:

```yaml
installed_packs:
  team-ops:
    origin: https://github.com/org/platform.git
    method: clone
    installed_at: "2026-04-02T10:30:00Z"
    commit_hash: "abc123..."
    content_paths:
      skills: tools/agent/skills
      rules: tools/agent/rules
```

## Updating

`pack update` for clone-method packs re-clones from origin and re-extracts content using the same content path mappings from the original install. The `commit_hash` recorded at install time enables change detection — if the remote HEAD hasn't changed, the update is skipped.

```bash
aipack pack update team-ops     # update one pack
aipack pack update --all        # update all remote packs
```

For standard packs (no `content_paths`), the same re-clone-and-extract process applies. Installed packs always contain only content — no `.git/` directories, no non-pack files.

## What to read next

- [Getting Started](./getting-started.md) — install packs and sync for the first time
- [Creating Packs](./creating-packs.md) — author your own pack from scratch
- [Profiles](./profiles.md) — compose and filter installed packs, define parameters, scope to roles
- [Pack Format Specification](./pack-format.md) — content format, manifest schema, MCP server definitions
