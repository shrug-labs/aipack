# Frequently Asked Questions

Common questions about aipack — what it is, how to use it, and how to share packs with your team.

**Contents:**
[Basics](#basics) · [Setup](#setup) · [Using packs](#using-packs) · [Creating & sharing](#creating--sharing)

## Basics

### What is aipack?

A CLI that packages and syncs AI agent configuration — rules, skills, workflows, MCP server configs — to whatever harness you use (Cline, Codex, OpenCode, Claude Code). Think of it as a package manager for agent knowledge. You author config once, and aipack materializes it into the right format for each harness.

### What's a pack?

A versioned bundle of agent configuration: rules (always-on constraints), skills (on-demand domain knowledge), workflows (step-by-step procedures), agents (scoped sub-personas with constrained tools), MCP server configs (tool connections), and profiles (composition presets). A pack is portable — it works across harnesses and teams.

### How is this different from just copying rules files around?

Tools like block/ai-rules and ai-rulez sync personal rules across harnesses. That's useful but limited — they solve the personal rule-sync problem. aipack solves the harder problem: how does a team package operational knowledge — triage workflows, MCP tool configs, agent definitions, role-based profiles — and distribute it as installable, composable, versionable units?

Three specific differences. First, packs contain more than rules — skills, workflows, agents, MCP configs, and settings are all managed together as one bundle. Second, aipack handles layered composition: org, team, and personal packs merge cleanly with explicit conflict resolution. Third, it's repeatable — a new engineer runs three commands and gets the exact same environment as a veteran. No tribal knowledge about which JSON to edit where.

## Setup

### Which harnesses does it support?

Cline, Codex, OpenCode, and Claude Code. Same packs, different targets. `aipack sync --harness <name>` materializes everything into the right format for that harness. Each harness has different rendering — for example, Codex flattens all rules into a single `AGENTS.override.md`, while Cline writes individual rule files — but the pack content stays identical.

### How do I get started?

Three commands to a full environment:

```bash
aipack pack install <pack-name-or-url> --seed
aipack profile set <profile> --install
aipack sync --harness <your-harness>
```

`--seed` applies bundled profiles and registry entries from the pack. `--install` fetches dependency packs. The full setup flow is in the [Getting Started guide](./getting-started.md).

### What happens to my existing harness config?

Sync is non-destructive by default. aipack tracks what it manages via a ledger. User-created rules and settings that aipack didn't write are left untouched. If a file aipack manages has been edited locally, the sync shows a diff rather than overwriting. `aipack restore` can undo the last sync's settings changes if needed.

### What platforms does it run on?

macOS (ARM and Intel), Linux (amd64), and Windows (amd64). Install via Homebrew (macOS/Linux), the install script, the PowerShell installer (Windows), or build from source with Go 1.24+.

## Using packs

### What are profiles?

A profile is a named composition of packs with parameters. You switch profiles to switch context — `aipack profile set frontend` vs `aipack profile set ops`.

Profiles enable role-based configuration. A team pack can ship multiple profiles — general operator, primary oncall, manager, service owner, new engineer — each scoping different content and MCP servers to what that role needs. A new engineer gets onboarding and reference materials; a primary oncall gets incident response and monitoring tools.

Profiles can also carry parameters like environment-specific URLs or service names that expand into MCP server configs at sync time.

### What if I don't want all the rules/skills from a pack?

Profiles give you granular control. In the TUI (`aipack manage`), you can toggle individual rules, skills, workflows, and MCP server configs on or off within your active profile. A profile is essentially a declaration of an agent: which packs it draws from, which specific content is active, and which tools it has access to.

### What about context / token cost?

Rules are always-on — they load into every conversation, so they cost context tokens every session. That's why rules should be concise (<60 lines). Skills are on-demand — they load only when the agent determines they're relevant, so they're essentially free when not in use. Profiles let you exclude anything you don't need, so you're never paying for content that isn't active.

### Can I edit config in the harness and push changes back?

Yes — `aipack save` captures changes from harness-native config back into pack source. If you create or edit a rule in Cline, add a new skill file, or edit MCP settings, save detects the delta and writes it back to the originating pack. The pack stays the source of truth, but you can author from either direction.

### How does pack content improve over time?

A closed loop: use pack-loaded agents for real work, review what worked and what was missing, capture findings in a persistent memory bank, then extract stable knowledge into pack content for the whole team. One person's discovery becomes the team's knowledge on the next sync.

### What if something breaks?

`aipack doctor` checks your entire setup: sync config, active profile, installed packs, git availability, content drift, and ledger health. It reports issues and with `--fix` auto-repairs safe problems. `aipack restore` can roll back the last sync's settings changes.

## Creating & sharing

### Can I create my own pack?

Yes. A pack is a directory with a `pack.json` manifest and content files (rules, skills, workflows). `aipack pack create` scaffolds one. Publish it to a git repo and register it so others on your team can install it by name. The [Getting Started guide](./getting-started.md) walks through the full authoring flow.

### How do packs get updated?

`aipack pack update --all` pulls the latest version of every installed pack from its source repo. Then `aipack sync` materializes the updates into your harness. Registry sources refresh with `aipack registry fetch`.

### Can I pin a pack to a specific version?

You can pin to a git ref at install time — `aipack pack install --url <url> --ref v1.2.0` installs from a specific tag, branch, or commit. Registry entries also carry a `ref` field. Since packs are git-native, the version mechanism is git tags: tag your pack repo at release points, and consumers can install from a specific tag.

### Who maintains the packs?

Teams own their packs. Each team maintains their own, and org-level packs are maintained centrally. Personal packs are yours.

### What's the org pack vs team pack vs personal pack?

Three layers that compose on top of each other. An org pack provides baseline config everyone needs — shared MCP servers, common review workflows. Team packs add domain-specific knowledge — triage runbooks, deployment workflows, team-specific tools. Personal packs are your individual preferences and custom workflows — your AI dotfiles. All three layers merge cleanly.

### Can I use this without team packs — just for personal config?

Yes. Install a personal pack (or create one with `aipack pack create`), sync it, and you have your own portable agent configuration. No team infrastructure required. When your team is ready to share, your personal pack layers on top of team packs naturally.

### Can I use packs from another team?

Yes — that's what registries are for. If another team has published their pack to a registry, you can discover it with `aipack registry list` or `aipack search`, install it, and either add it to your profile or switch to a profile that includes it.

### Is this open source?

Yes. The core tool is at [github.com/shrug-labs/aipack](https://github.com/shrug-labs/aipack) under the Universal Permissive License.
