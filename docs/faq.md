# Frequently Asked Questions

Common questions about aipack — what it is, how to use it, and how to share packs.

**Contents:**
[Basics](#basics) · [Setup](#setup) · [Using packs](#using-packs) · [Creating & sharing](#creating--sharing)

## Basics

### What is aipack?

A package manager for AI agent knowledge. Rules, skills, workflows, MCP server configs, agent definitions — authored once as portable packs, composed through profiles, and synced to whatever coding assistant you use. The same pack works across Claude Code, Codex, OpenCode, and Cline without per-harness maintenance.

### What's a pack?

A versioned bundle of agent configuration: rules (always-on constraints), skills (on-demand domain knowledge), workflows (step-by-step procedures), agents (scoped sub-personas with constrained tools), MCP server configs (tool connections), and profiles (composition presets). A pack is portable — it works across harnesses and teams.

### How is this different from just copying rules files around?

Tools like block/ai-rules and ai-rulez sync personal rules across harnesses — they solve the rule fan-out problem. Community skill libraries like superpowers publish curated agent methodologies — they solve the content creation problem. Neither solves composition: taking a community skill library, your team's shared workflows, your personal rules, and a set of MCP server configs, and combining them into a coherent agent environment scoped to what you actually need right now.

aipack is the composition layer. Packs bundle more than rules — skills, workflows, agents, MCP configs, and settings travel together. Profiles control which packs are active and how they combine, so you can scope content by role (frontend-dev vs. backend-dev vs. manager), by context (feature work vs. code review), or by tool budget (stay under the 128-tool limit some models impose). And the whole thing is repeatable — a new engineer runs three commands and gets the exact same environment as a veteran.

## Setup

### Which harnesses does it support?

Cline, Codex, OpenCode, and Claude Code. Same packs, different targets. `aipack sync --harness <name>` materializes everything into the right format for that harness. Each harness has different rendering — for example, Codex flattens all rules into a single `AGENTS.override.md`, while Cline writes individual rule files — but the pack content stays identical.

### How do I get started?

Three commands to a full environment:

```bash
aipack pack install <pack-name-or-url> -w all
aipack profile set <profile> --install
aipack sync --harness <your-harness>
```

`-w all` accepts all bundled content (profiles, registries, extras) from the pack. `--install` fetches dependency packs. The full setup flow is in the [Getting Started guide](./getting-started.md).

### What happens to my existing harness config?

Sync is non-destructive by default. aipack tracks what it manages via a ledger. User-created rules and settings that aipack didn't write are left untouched. If a file aipack manages has been edited locally, the sync shows a diff rather than overwriting. `aipack restore` can undo the last sync's settings changes if needed.

### What platforms does it run on?

macOS (ARM and Intel), Linux (amd64), and Windows (amd64). Install via Homebrew (macOS/Linux), the install script, the PowerShell installer (Windows), or build from source with Go 1.25+.

### What about WSL?

aipack runs in WSL as a Linux binary. Project-scope sync works normally — rules land in the project directory on the WSL filesystem, where VS Code Remote-WSL can read them. Global-scope sync for Cline requires care: the Cline extension reads from the Windows filesystem (`C:\Users\...\Documents\Cline\Rules`), but aipack in WSL writes to the Linux filesystem. `aipack doctor` will warn about this. For global Cline sync, run aipack natively on Windows. Codex and Claude Code work fine in WSL because they read from the same filesystem aipack writes to.

## Using packs

### What are profiles?

A profile is a named composition of packs with parameters. You switch profiles to switch context — `aipack profile set frontend-dev` vs `aipack profile set backend-dev`.

Profiles solve two problems. The obvious one is context-based scoping — a frontend developer gets component libraries and design-system skills, a backend developer gets API and database workflows, and neither gets content that's irrelevant to their work. The less obvious one is tool budget management: models impose tool limits (128 on some), and loading every MCP server simultaneously blows past them. Profiles curate which MCP servers and tools are active for a given context, keeping you under the ceiling without manual config juggling.

Profiles can also carry parameters like environment-specific URLs or service names that expand into MCP server configs at sync time.

### What if I don't want all the rules/skills from a pack?

Profiles give you granular control. In the TUI (`aipack manage`), you can toggle individual rules, skills, workflows, and MCP server configs on or off within your active profile. A profile is essentially a declaration of an agent: which packs it draws from, which specific content is active, and which tools it has access to.

### What about context / token cost?

Rules are always-on — they load into every conversation, so they cost context tokens every session. That's why rules should be concise (<60 lines). Skills are on-demand — they load only when the agent determines they're relevant, so they're essentially free when not in use. Profiles let you exclude anything you don't need, so you're never paying for content that isn't active.

### Can I edit config in the harness and push changes back?

Yes — `aipack save` captures changes from harness-native config back into pack source. If you create or edit a rule in Cline, add a new skill file, or edit MCP settings, save detects the delta and writes it back to the originating pack. The pack stays the source of truth, but you can author from either direction.

### How does pack content improve over time?

A closed loop: use pack-loaded agents for real work, review what worked and what was missing, capture findings, then extract stable knowledge into pack content. One person's discovery becomes installable knowledge on the next sync — whether that's for a team, a community, or just your future self.

### What if something breaks?

`aipack doctor` checks your entire setup: sync config, active profile, installed packs, git availability, content drift, and ledger health. It reports issues and with `--fix` auto-repairs safe problems. `aipack restore` can roll back the last sync's settings changes.

## Creating & sharing

### Can I create my own pack?

Yes. A pack is a directory with a `pack.json` manifest and content files (rules, skills, workflows). `aipack pack create` scaffolds one. Publish it to a git repo and register it so others can install it by name. The [Creating Packs](./creating-packs.md) guide walks through the full authoring flow.

### How do packs get updated?

`aipack pack update --all` pulls the latest version of every installed pack from its source repo. Then `aipack sync` materializes the updates into your harness. Registry sources refresh with `aipack registry fetch`.

### Can I pin a pack to a specific version?

You can pin to a git ref at install time — `aipack pack install --url <url> --ref v1.2.0` installs from a specific tag, branch, or commit. Registry entries also carry a `ref` field. Since packs are git-native, the version mechanism is git tags: tag your pack repo at release points, and consumers can install from a specific tag.

### Who maintains the packs?

Whoever publishes them. Community packs are maintained by their authors. Org-level packs are maintained centrally. Team packs are owned by the team. Personal packs are yours.

### How do packs layer?

Packs compose on top of each other through profiles. A community pack provides a skill methodology. An org pack adds shared MCP servers and review workflows. A team pack adds project-specific conventions and deployment workflows. A personal pack carries your individual preferences. All four layers merge cleanly — the profile controls precedence and conflict resolution.

### Can I use this just for personal config?

Yes. Create a personal pack (`aipack pack create`), sync it, and you have your own portable agent configuration. No shared infrastructure required. When you want to compose with other packs later, your personal pack layers on top naturally.

### Can I use packs from other sources?

Yes — that's what registries are for. If someone has published a pack to a registry, you can discover it with `aipack registry list` or `aipack search`, install it, and add it to your profile. Packs compose regardless of where they come from — community, org, team, or personal.

### Is this open source?

Yes. The core tool is at [github.com/shrug-labs/aipack](https://github.com/shrug-labs/aipack) under the Universal Permissive License.
