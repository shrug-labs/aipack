# Getting Started

Install aipack, set up your first pack, and sync it to your coding assistant. This takes about five minutes.

## Install

On macOS:

```bash
brew install dfoster-oracle/tap/aipack
```

On macOS and Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/shrug-labs/aipack/main/install.sh | sh
```

On Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/shrug-labs/aipack/main/install.ps1 | iex
```

The PowerShell installer writes `aipack.exe` to `~/.local/bin`.
If you already have Go installed, you can also use:

```powershell
go install github.com/shrug-labs/aipack/cmd/aipack@latest
```

See the [README](../README.md#install) for version pinning and other install options.

## Initialize

```bash
aipack init
```

This creates the config directory (`~/.config/aipack/`), a default profile, and fetches the public pack registry so pack names are immediately discoverable.

## Install your first pack

The `aipack-core` pack provides the foundation — rules for content quality, skills for agent configuration and pack authoring, and a pack review workflow:

```bash
aipack pack install aipack-core --add
```

This clones the pack from the public registry, installs it under `~/.config/aipack/packs/aipack-core/`, and adds it to your active profile. See what's in it:

```bash
aipack pack show aipack-core
```

This prints the content inventory: 7 rules, 3 skills, 1 workflow.

## Preview and sync

Before writing anything to your harness, preview:

```bash
aipack sync --dry-run
```

This shows exactly what files would be created or updated, and where. When it looks right:

```bash
aipack sync
```

aipack renders the pack content into your harness's native format. Rules become individual files in `.claude/rules/` (Claude Code), `.opencode/rules/` (OpenCode), or `.clinerules/` (Cline). Skills become skill directories. MCP server definitions become entries in the harness config file. The same pack, rendered for whichever harness you use.

To target a specific harness or scope:

```bash
aipack sync --harness claudecode          # only Claude Code
aipack sync --scope project               # project-level config (current directory)
aipack sync --scope project --dry-run     # preview project sync
```

## See the result

After syncing, your harness picks up the new content. Some harnesses need a restart for always-on content (rules, settings, MCP config) to take effect — skills and workflows are typically discovered on demand.

Explore what's installed:

```bash
aipack status              # profile, packs, content counts
aipack search deploy       # full-text search across all pack content
aipack trace rule anti-slop # trace a rule from pack source to harness destination
```

## Add more packs

Profiles compose multiple packs. The public registry includes two more packs worth considering:

```bash
# Developer discipline — debugging, TDD, planning, code review
aipack pack install essentials --add

# Persistent memory across sessions (optional)
aipack pack install memory --add
```

After installing, re-sync to pick up the new content:

```bash
aipack sync
```

All installed packs are now active. If two packs define the same content ID, aipack raises a conflict — resolve it with override declarations in your profile. See [Profiles](./profiles.md) for selectors, layering, and conflict resolution.

You can also install packs from git URLs, local directories, or team registries. See [Installing Packs](./installing-packs.md) for all installation methods.

## One-command onboarding

Packs can bundle profiles, registry entries, and dependency declarations. When a pack includes these, installation bootstraps your entire agent environment:

```bash
aipack pack install --url https://github.com/org/team-pack.git -w all
aipack profile set frontend-dev --install
aipack sync
```

`-w all` accepts the pack's bundled content — profiles are copied to the user's profile directory, and registry entries are merged into the local registry cache so declared packs become discoverable via `aipack search` and installable by name. `--install` fetches any dependency packs referenced in the profile. Three commands to go from zero to a fully configured agent environment.

## Install from any repository

Not every repository with useful content is structured as a pack. aipack can consume content from any git repo by mapping its directories to content types:

```bash
aipack pack install \
  --url https://github.com/org/their-repo.git \
  --skills src/skills --rules docs/rules \
  --name their-content -q
aipack sync
```

The source repo doesn't need a `pack.json` or any restructuring. See [Installing Packs](./installing-packs.md) for the full guide on content flags, registry entries, and common repository layouts.

## What to read next

- [Installing Packs](./installing-packs.md) — all installation methods, content_paths, quiet packs, registry entries
- [Creating Packs](./creating-packs.md) — author your own pack from scratch or existing content
- [Profiles](./profiles.md) — compose packs, filter content, expand parameters, scope to roles
- [Sync and Save](./sync.md) — the sync workflow, save round-trips, restore, and clean
- [aipack Reference](./aipack.md) — complete CLI reference
