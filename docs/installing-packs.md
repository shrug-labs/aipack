# Installing Packs

Agent knowledge is already written. Skills live in team repos, rules accumulate in dotfile directories, workflows exist as markdown that someone refined over months. Most of it was never structured as a pack — but the content is there, and the format is standard: markdown with YAML frontmatter.

aipack can install from these repositories directly. You point it at the directories that contain content, it extracts them into a standard pack layout, and discards the rest. The source repo doesn't need a `pack.json` or any restructuring — the consumer does the packaging.

## Fast path

For the common path, inspect, install into the active profile, check setup, then sync:

```bash
aipack pack inspect <source>
aipack pack install <source> --add
aipack setup
aipack sync
```

`pack inspect` is the trust step. `pack install --add` installs the source and adds it to the active profile. `setup` reports missing `{params.*}` and `{env:*}` values before sync.

## Installing a standard pack

A standard pack has a `pack.json` manifest and content in conventional directories. Four ways to install:

```bash
# From a git URL (HTTPS or SSH)
aipack pack install --url https://github.com/org/their-pack.git --add

# From a local path (symlinked by default, --copy for full copy)
aipack pack install ./path/to/pack --add

# From a static archive URL or file
aipack pack install https://downloads.example.com/team-pack.zip --add
aipack pack install ./team-pack.zip --add

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

## Inspecting before install

Use `pack inspect` when you want to see what a pack contains before it touches installed state. It works with the same source shapes as install — local paths, registry names, git URLs, subpaths, archive URLs/files, and content-path mappings — but it does not write `packs/`, the lockfile, or profiles.

```bash
aipack pack inspect their-pack
aipack pack inspect --url https://github.com/org/repo.git --path packs/team
aipack pack inspect https://downloads.example.com/team-pack.zip
aipack pack inspect ./team-pack.zip
aipack search --status inspected
```

The inspected pack is added to the search index as a preview. That makes `aipack search --status inspected` useful for browsing rules, skills, workflows, agents, hooks, and prompts before deciding whether to run `pack install`. Use `pack inspect --json` when automation needs the full structured inventory.

When the pack defines MCP servers, `aipack pack inspect` surfaces a warning listing the server names. MCP servers run external tools with whatever credentials the harness gives them, so the warning is a chance to check the source before sync wires them in. Inspected rows accumulate over time; they age out automatically after 30 days, and `aipack pack inspect --clear` wipes them on demand without touching installed or registered packs.

## Importing one markdown file

Use `pack import` when the source is a single gist/raw markdown file rather than a pack or repository directory. Use `--name` to create a small local pack, or `--pack` to add the file to an installed pack:

```bash
aipack pack import ./review.md --type skill --name review-pack --add
aipack pack import ./triage.md --type skill --pack example-pack
aipack pack import https://example.com/rule.md --type rule --name rules --id incident-rule
aipack pack import ./prompt.md --type prompt --name prompts
```

The command writes the file as the requested type and adds minimal YAML frontmatter if the file has none. `--add` adds the imported pack to the active profile, matching `pack install --add`. Imports into existing packs append explicit manifest lists and leave omitted lists omitted so auto-discovery keeps working.

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

Content flags require `--url` (this is a remote extraction feature) and `--name` (there's no `pack.json` to derive a name from). Available flags: `--rules`, `--skills`, `--agents`, `--workflows`, `--hooks`, `--prompts`. Each takes a directory path relative to the repository root.

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

  example-pack:
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

  team-archive:
    method: archive
    url: "https://downloads.example.com/aipack/team-archive.zip"
    description: "Static archive distribution"
```

Now anyone installs by name:

```bash
aipack pack install example-pack --add
```

The content flags, quiet hint, and description are baked into the registry entry. `pack update` re-clones and re-extracts, picking up upstream changes.

Registry entries also support `ref` (git branch/tag/commit) and `path` (subdirectory within the repo for standard packs that include a `pack.json`). For the full registry schema, see the [Pack Format Specification](./pack-format.md#112-registry).

If a foundational public pack like `aipack-core` or `essentials` is missing from your merged registries on first run, the lookup error suggests fetching the default public registry directly:

```bash
aipack registry fetch https://raw.githubusercontent.com/shrug-labs/packs/main/registry.yaml
aipack pack install essentials --add
```

Explicit `--registry` and registry-file lookups stay strict — the hint only shows up when you haven't pointed aipack at any registry yet.

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

Valid content type keys: `rules`, `agents`, `workflows`, `skills`, `hooks`, `prompts`. Values are directory paths relative to the repository root. When `--path` (subpath) is also set, paths are relative to the subpath root.

Files within mapped directories follow the same conventions as standard packs: `.md` files with YAML frontmatter for rules, agents, workflows, and prompts; `<name>/SKILL.md` subdirectories for skills; `<name>/HOOK.yaml` subdirectories for hooks.

Only declared types are discovered — omitted types have no content. This is independent of the `quiet` flag: `content_paths` controls what's *extracted*, while `quiet` controls what's *activated* in a profile.

Content path mappings, install metadata, and version pins are stored in `aipack.lock` (the lockfile) under the pack's entry:

```yaml
packs:
  example-pack:
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
```

The lockfile is machine-managed — never hand-edit it. Earlier versions of aipack stored install metadata in `sync-config.yaml` under `installed_packs`; that section is migrated to the lockfile transparently the first time `aipack doctor` runs (or on the first lockfile read in any pack command). The `resolved` block records the pack's content state at the end of the last successful sync — rule/agent/workflow IDs, skill descriptions and bundled assets, and MCP server shapes — so the next sync can diff against it and surface drift. See [Sync and Save](./sync.md#content-drift-detection) for the drift detection flow.

## Pinning to a ref

Every install takes an optional git ref — passed via `--ref`, the shorthand `@<ref>` after the pack name, or the `ref` field in the registry entry. aipack classifies the ref by shape and picks the right resolution path: exact semver, partial semver, commit hash, `latest` sentinel, namespaced tag, or any other literal git ref (branch, non-semver tag). Without a ref, `pack install` tracks the default branch's HEAD.

```bash
# Pin to a specific semver tag
aipack pack install my-pack@1.2.3
aipack pack install my-pack --ref 1.2.3

# Partial semver: resolves to the latest stable v1.x.x (or v1.2.x), pins to that exact tag
aipack pack install my-pack@v1
aipack pack install my-pack@v1.2

# Pin to a commit hash
aipack pack install my-pack@abc1234

# Pin to a namespaced tag (multi-pack monorepo)
aipack pack install my-pack@my-pack/v0.3.0
aipack pack install my-pack --ref my-pack/v0.3.0

# Track a branch (follows upstream HEAD of that branch)
aipack pack install my-pack --ref main

# Track HEAD on the default branch (no pin)
aipack pack install my-pack
```

`--version` is a Kong alias for `--ref` kept for historical scripts — every `--version X` invocation is equivalent to `--ref X`. New content should prefer `--ref`.

Version sources are git tags filtered for valid semver. The `pack.json` `version` field is author metadata for display — aipack's resolver reads git tags, not the manifest. If they disagree at install time, aipack prints a one-line warning to nudge pack authors, but the install proceeds against the tag either way. For the author-side release ritual (bump → commit → tag → push), see [Creating Packs — Releasing a new version](./creating-packs.md#releasing-a-new-version).

**URL shape matters when `--ref` is a semver.** Semver resolution requires a clone-able git repo URL — either a plain repo URL like `https://github.com/org/repo` or an SSH URL like `git@host:path.git`. GitHub blob-style URLs (`https://github.com/org/repo/tree/main/subdir`) are not supported alongside semver refs; use `--url <repo-url> --path <subdir> --ref <X>` instead. Literal refs (branches, commit hashes) bypass the semver resolver and don't have this constraint.

Partial references (`v1`, `v1.2`) query the remote tags, pick the highest matching stable tag, and pin to that exact version — they are a discovery shortcut, not a channel. The lockfile records the resolved tag (`1.5.0`), not the partial (`v1`), so `pack update` won't auto-bump when new v1.x.x tags are released; re-run `pack update my-pack --ref v1` to move the pin. Prereleases are skipped during partial matching; install them with an exact tag if needed (`my-pack@v1.5.0-beta.1`).

### Namespaced tags (multi-pack monorepos)

Repositories that ship multiple packs in subdirectories can't use flat semver tags without collision — `v1.2.3` is ambiguous across sibling packs. aipack supports namespaced tags of the form `<pack-name>/vX.Y.Z` (Go-module convention). When the installed ref is namespaced, `pack update`, `pack versions`, and the drift hint all derive the prefix automatically from the lockfile, so subsequent commands stay honest without requiring explicit repetition.

```bash
# Initial install via the namespaced tag
aipack pack install my-team-pack --ref my-team-pack/v0.3.0

# Update inherits the prefix from the installed ref — the bare semver is enough
aipack pack update my-team-pack --ref 0.3.1

# Or re-type the full namespaced form; both work
aipack pack update my-team-pack --ref my-team-pack/v0.3.1

# Discovery lists the matching tags (prefix derived from the install)
aipack pack versions my-team-pack
```

No registry schema changes are required — the pack's registry entry just sets its `ref:` field to the initial namespaced tag (or users pass `--ref <prefix>/v<X.Y.Z>` on first install). See [Creating Packs — Releasing a namespaced version](./creating-packs.md#releasing-a-namespaced-version) for the author-side release ritual.

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

`pack update` re-clones git installs from origin and re-extracts content. The `commit_hash` recorded at install time enables change detection — if the remote HEAD hasn't changed, the update is skipped. Archive installs (`method: archive` or `--archive`) are re-fetched and full-replaced because they do not have git refs or commit hashes. Updates materialize new content in staging first, apply bundled-content filters there, and only then atomically replace the installed pack. When refreshing multiple packs, updates run in parallel (up to three concurrent fetches), so total wall-clock time is bounded by the slowest pack rather than the sum. Clones for the same origin share a local bare-clone cache, so multiple packs installed from one monorepo only fetch from the remote once.

```bash
aipack pack update              # update all installed packs (default)
aipack pack update my-pack      # update one specific pack
```

**Pinned packs stay pinned.** A bare `pack update` on a pinned pack does not change the install. It checks the remote and reports the latest available version, but leaves the pin in place. Move the pin explicitly:

```bash
aipack pack update my-pack --ref 1.3.0    # move the pin to a new tag
aipack pack update my-pack --ref latest   # clear the pin, track HEAD again
aipack pack update my-pack --ref main     # switch to tracking a branch
```

For standard packs (no `content_paths`), the same re-clone-and-extract process applies. Installed packs always contain only content — no `.git/` directories, no non-pack files.

## Removing a pack

`aipack pack delete <name>` removes the pack source, lockfile entry, profile entries, and pack ledger entries. It removes only clean rendered harness files whose ledger digest still matches on-disk content and whose path is a known harness-rendered location. Modified files and unknown ledger paths are preserved on disk and made unmanaged. Shared settings files are not deleted; aipack strips only the pack-managed keys and preserves user settings.

Use `--keep-rendered` when you want to stop tracking a pack but intentionally leave rendered harness files in place as unmanaged content. `--dry-run` previews either path, and `--json` emits machine output.

## What to read next

- [Getting Started](./getting-started.md) — install packs and sync for the first time
- [Creating Packs](./creating-packs.md) — author your own pack from scratch
- [Profiles](./profiles.md) — compose and filter installed packs, define parameters, scope to roles
- [Pack Format Specification](./pack-format.md) — content format, manifest schema, MCP server definitions
