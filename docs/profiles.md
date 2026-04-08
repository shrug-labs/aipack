# Profiles

A profile is a YAML file that defines an agent environment — which packs to draw from, which content is active, which MCP servers connect, and what parameters to expand. It turns a collection of installed packs into a coherent setup.

Each content vector (rules, skills, workflows, agents, MCP servers) can be filtered per pack with `include` and `exclude` selectors. Packs marked `quiet` include nothing by default — content activates only when explicitly listed. Parameters expand `{params.*}` placeholders in MCP configs and content, making the same profile portable across environments. Switching profiles changes what's active without reinstalling anything.

Profiles live in `~/.config/aipack/profiles/` (on Windows: `%APPDATA%\aipack\profiles\`).

## Profile structure

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
    rules:
      exclude: ["verbose-logging"]
    mcp:
      atlassian:
        enabled: true
        allowed_tools:
          - confluence_search
          - confluence_get_page
          - jira_get_issue
      build-system:
        enabled: false

  - name: ext-catalog
    quiet: true
    skills:
      include: [deploy, triage]

  - name: personal
    overrides:
      rules: ["anti-slop"]
```

`schema_version: 2` is required. The top-level fields:

- **`params`** — key-value pairs expanded into `{params.*}` placeholders throughout pack content and MCP definitions.
- **`packs`** — ordered list of pack entries. Each entry names an installed pack and optionally filters its content, configures MCP servers, or declares overrides.

Pack entries accept `enabled` (true/false/null), `quiet` (true/false), `settings.enabled` (false to opt out), vector selectors (`rules`, `skills`, `workflows`, `agents`), `mcp` server config, and `overrides`.

## Parameters

Parameters make profiles portable across environments. Define them once; they expand wherever content uses `{params.*}` placeholders.

```yaml
params:
  jira_url: "https://jira.example.com"
  team_project: "OPS"
```

In an MCP server definition (`mcp/atlassian.json`):

```json
{
  "command": "uvx",
  "args": ["mcp-atlassian", "--jira-url", "{params.jira_url}"]
}
```

In a rule or skill body:

```markdown
When creating Jira tickets, use project {params.team_project} unless specified otherwise.
```

Two teams using the same pack with different Jira instances need different parameter values, not different packs. Parameters are global to the profile — all packs share the same namespace.

For environment-specific values that shouldn't be committed to a profile, use `{env:VAR}` references instead. See the [Pack Format Specification](./pack-format.md#5-environment-references-and-parameters) for the expansion order.

## Vector selectors

Each content vector (`rules`, `skills`, `workflows`, `agents`) can be filtered with `include` or `exclude`. These are mutually exclusive on the same vector — you cannot set both.

| Configuration | Behavior |
|---------------|----------|
| Omitted | All content from this vector is included |
| `include: [a, b]` | Only the listed IDs are included |
| `include: []` | All content (backward compatibility — empty list treated as omitted) |
| `exclude: [x, y]` | All content except the listed IDs |

Both `include` and `exclude` support glob patterns: `include: ["team-*"]` matches all IDs starting with `team-`.

```yaml
packs:
  - name: team-ops
    rules:
      exclude: ["verbose-logging"]
    skills:
      include: ["deploy-*", "triage"]
```

## Quiet packs

A pack entry marked `quiet: true` flips the default: omitted or empty selectors resolve to nothing instead of everything. Content activates only when you explicitly list it with a non-empty `include`.

| Configuration | Normal pack | Quiet pack |
|---------------|------------|------------|
| Omitted | All content | **Nothing** |
| `include: []` | All content | **Nothing** |
| `include: [a, b]` | Only a, b | Only a, b |
| `exclude: [x]` | All except x | **Nothing** (nothing to subtract from) |

This is the right default for large external catalogs. Install the whole pack, then selectively activate what you need:

```yaml
packs:
  - name: ext-catalog
    quiet: true
    skills:
      include: [deploy, triage]
    rules:
      include: [code-review]
```

Only the three named items sync. Everything else in the pack stays on disk but doesn't load. Use `aipack search` to discover what's available.

Three ways to get `quiet: true` on a profile entry:

- Set it directly in the profile YAML.
- Install with `aipack pack install -q --add` or `aipack pack add -q`.
- Registry hint: a registry entry with `quiet: true` propagates to the profile entry on install.

## Role-based profiles

Different profiles scope content and MCP servers to what each context needs. Here are three profiles that draw from the same installed packs:

**`profiles/default.yaml`** — baseline, everything active:

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
      atlassian: { enabled: true }
      build-system: { enabled: true }
  - name: personal
```

**`profiles/frontend-dev.yaml`** — scoped to frontend work:

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
    skills:
      include: [react-patterns, component-review, styling]
    workflows:
      include: [design-qa]
    mcp:
      atlassian: { enabled: true }
      build-system: { enabled: false }
  - name: personal
```

**`profiles/oncall.yaml`** — scoped to incident response:

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
    skills:
      include: [triage, monitoring, escalation]
    workflows:
      include: [incident-response]
    mcp:
      atlassian: { enabled: true }
      build-system: { enabled: true }
      monitoring: { enabled: true }
  - name: personal
```

Switch between them:

```bash
aipack profile set oncall
aipack sync
```

## Layering multiple packs

Profiles compose packs from different sources. Packs are processed in order (first to last). If two packs declare the same content ID — for example, both have `rules/anti-slop.md` — the sync engine resolves the collision based on `defaults.collision_strategy` in sync-config.yaml (default: `last-wins`). You can also resolve collisions explicitly by declaring overrides:

```yaml
packs:
  - name: community-lib
  - name: team-ops
  - name: personal
    overrides:
      rules: ["anti-slop"]      # personal's version replaces community-lib's
      workflows: ["deploy"]     # personal's version replaces team-ops's
```

Without the `overrides` declaration, duplicate IDs are resolved by the `defaults.collision_strategy` in sync-config.yaml. The default is `last-wins` — the later pack in profile order wins. Set it to `first-wins` for the reverse, or `error` to require explicit overrides for every collision. Explicit `overrides` always take precedence over the strategy.

Packs with harness config files (`configs/` directory in `pack.json`) contribute base settings automatically. Multiple packs' settings are deep-merged in profile order — the first pack wins at leaf value conflicts, and a warning identifies the overlap. Set `settings.enabled: false` on a pack entry to opt it out of settings contribution.

## Bundled profiles

Packs can ship profile files for distribution. Drop them in the `profiles/` directory — they're auto-discovered like rules and skills:

```
my-pack/
└── profiles/
    ├── dev.yaml
    ├── frontend-dev.yaml
    └── lean.yaml
```

On install with `-w all`, bundled profiles are copied to `~/.config/aipack/profiles/` and bundled registries are merged into the user's registry configuration. This enables single-command team setup:

```bash
aipack pack install --url https://github.com/org/team-pack.git -w all
aipack profile set frontend-dev --install
aipack sync
```

`--install` fetches any packs referenced in the profile that aren't installed yet, looking them up in the registry.

## MCP server overrides

Profiles control which MCP servers are active and how their tools are scoped. Each pack that defines MCP servers (in its `mcp/` directory) makes those servers available. The profile enables, disables, and configures them:

```yaml
packs:
  - name: team-ops
    mcp:
      atlassian:
        enabled: true
        allowed_tools:
          - confluence_search
          - confluence_get_page
          - jira_get_issue
        disabled_tools:
          - confluence_delete_page
      build-system:
        enabled: false
```

`allowed_tools` overrides the pack manifest's `default_allowed_tools` for this server. `disabled_tools` explicitly blocks specific tools. When both are present, `disabled_tools` takes precedence.

Disabling a server in one pack and providing it from another is clean composition — for example, when a team pack's MCP server is broken and you provide a working replacement from a personal pack.

For how tool permissions render per harness, see the [Harness Reference](./harness-reference.md#mcp-tool-permissions).

## What to read next

- [Installing Packs](./installing-packs.md) — install content from any git repository
- [Creating Packs](./creating-packs.md) — author your own pack from scratch
- [Pack Format Specification](./pack-format.md) — content format, manifest schema, MCP server definitions
- [Harness Reference](./harness-reference.md) — per-harness rendering behavior, write targets, and settings merge
- [Configuration and State](./configuration.md) — config directory layout, sync-config, and profile file locations
