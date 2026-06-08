# Profiles

A profile is a YAML file that defines an agent environment — which packs to draw from, which content is active, which plugins are enabled, which MCP servers connect, and what parameters to expand. It turns a collection of installed packs into a coherent setup.

Each selectable content vector (rules, skills, workflows, agents, hooks, plugins, MCP servers) can be filtered per pack. Rules, skills, workflows, agents, hooks, and plugins use `include` / `exclude` selectors; MCP servers use per-server entries because they also carry tool permissions. Packs marked `quiet` include nothing by default — content, plugins, MCP servers, and harness settings all activate only when explicitly listed. Parameters expand `{params.*}` placeholders in MCP configs and content, making the same profile portable across environments. Switching profiles changes what's active without reinstalling anything.

Profiles live in `~/.config/aipack/profiles/` (on Windows: `%APPDATA%\aipack\profiles\`).

## Profile structure

```yaml
schema_version: 2

params:
  tracker_url: "https://tracker.example.com"
  docs_url: "https://docs.example.com"

packs:
  - name: example-pack
    enabled: true
    settings:
      enabled: true
    rules:
      exclude: ["verbose-logging"]
    mcp:
      issue-tracker:
        enabled: true
        allowed_tools:
          - docs_search
          - docs_get_page
          - get_issue
      build-system:
        enabled: false

  - name: ext-catalog
    quiet: true
    skills:
      include: [deploy, triage]
    plugins:
      include: [linear]

  - name: personal
    overrides:
      rules: ["anti-slop"]
```

`schema_version: 2` is required. The top-level fields:

- **`params`** — key-value pairs expanded into `{params.*}` placeholders throughout pack content and MCP definitions.
- **`packs`** — ordered list of pack entries. Each entry names an installed pack and optionally filters its content, configures MCP servers, or declares overrides.

Pack entries accept `enabled` (true/false/null), `quiet` (true/false), `settings.enabled` (normal packs default to `true`; quiet packs default to `false` and need `true` to opt in), vector selectors (`rules`, `skills`, `workflows`, `agents`, `hooks`, `plugins`), `mcp` server config, and `overrides`.

## Parameters

Parameters make profiles portable across environments. Define them once; they expand wherever content uses `{params.*}` placeholders. Use `{params.KEY:-default}` when a value has a safe literal fallback; bare `{params.KEY}` stays strict and fails if the profile omits it.

```yaml
params:
  tracker_url: "https://tracker.example.com"
  team_project: "OPS"
```

In an MCP server definition (`mcp/issue-tracker.json`):

```json
{
  "command": "uvx",
  "args": ["mcp-issue-tracker", "--url", "{params.tracker_url}"]
}
```

In a rule or skill body:

```markdown
When creating Jira tickets, use project {params.team_project} unless specified otherwise.
```

Two teams using the same pack with different Jira instances need different parameter values, not different packs. Parameters are global to the profile — all packs share the same namespace.

For environment-specific values that shouldn't be committed to a profile, use `{env:VAR}` references instead. aipack checks the active config directory's `.env` file before falling back to the process environment. See the [Pack Format Specification](./pack-format.md#5-environment-references-and-parameters) for the expansion order.

To check a profile's required values, run `aipack setup`. It prints the short remediation checklist for missing strict params and env refs. `aipack profile refs` uses the same scan but shows the full reference inventory for diagnostics and JSON automation.

```bash
aipack setup oncall
aipack profile refs oncall
aipack profile set-param oncall tracker_url https://tracker.example.com
aipack profile unset-param oncall old_param
```

`aipack setup` is the shorter first-time checklist view over the same data. The TUI exposes param editing from the Profiles tab action menu. Use profile params for stable environment metadata and `{env:*}` for secrets or machine-local values.

## Vector selectors

Each profile-selectable vector (`rules`, `skills`, `workflows`, `agents`, `hooks`, `plugins`) can be filtered with `include` or `exclude`. These are mutually exclusive on the same vector — you cannot set both.

| Configuration | Behavior |
|---------------|----------|
| Omitted | All content from this vector is included |
| `include: [a, b]` | Only the listed IDs are included |
| `include: []` | All content (backward compatibility — empty list treated as omitted) |
| `exclude: [x, y]` | All content except the listed IDs |

Both `include` and `exclude` support glob patterns: `include: ["team-*"]` matches all IDs starting with `team-`.

For exact-ID changes, use `aipack profile include <id>` and `aipack profile exclude <id>` instead of editing YAML. The CLI searches the target profile's enabled pack entries across rules, agents, workflows, skills, hooks, plugins, and MCP servers, then reports ambiguity when `--kind` or `--pack` is needed. If the only match is in a disabled pack entry, enable the pack first with `aipack pack enable <pack> --profile <profile>`. The CLI writes exact IDs only; keep pattern-based selectors in YAML.

```yaml
packs:
  - name: example-pack
    rules:
      exclude: ["verbose-logging"]
    skills:
      include: ["deploy-*", "triage"]
    plugins:
      include: ["linear"]
```

## Quiet packs

A pack entry marked `quiet: true` flips the default across every delivery mechanism — content vectors, plugin references, MCP servers, and harness settings. Nothing from the pack activates unless you explicitly list it.

| Configuration | Normal pack | Quiet pack |
|---------------|------------|------------|
| Omitted | All content | **Nothing** |
| `include: []` | All content | **Nothing** |
| `include: [a, b]` | Only a, b | Only a, b |
| `exclude: [x]` | All except x | **Nothing** (nothing to subtract from) |

The same opt-in-only rule applies to plugins, MCP, and settings:

- **Plugins.** Plugin references follow normal vector selector rules. Omitted or empty `plugins:` on a quiet pack resolves to no plugins; opt in with `plugins: { include: [linear] }`.
- **MCP servers.** An omitted or empty `mcp:` map on a quiet pack resolves to no servers (a normal pack defaults to every server the manifest declares, enabled). Opt specific servers in with an explicit entry: `mcp: { srv-a: { enabled: true } }`.
- **Harness settings.** A quiet pack with `configs/` files does not contribute settings unless `settings.enabled: true` is set explicitly. A normal pack contributes its settings by default unless `settings.enabled: false` opts out.

This is the right default for large external catalogs. Install the whole pack, then selectively activate what you need:

```yaml
packs:
  - name: ext-catalog
    quiet: true
    skills:
      include: [deploy, triage]
    rules:
      include: [code-review]
    plugins:
      include: [linear]
    mcp:
      issue-tracker:
        enabled: true
    settings:
      enabled: true
```

Only the named content items, the linear plugin reference, the issue-tracker MCP server, and the pack's settings fragment sync. Everything else in the pack stays on disk but doesn't load. Use `aipack search` to discover what's available.

Three ways to get `quiet: true` on a profile entry:

- Set it directly in the profile YAML.
- Install with `aipack pack install -q --add` or `aipack pack add -q`.
- Registry hint: a registry entry with `quiet: true` propagates to the profile entry on install.

## Role-based profiles

Different profiles scope content, plugins, and MCP servers to what each context needs. Here are three profiles that draw from the same installed packs:

**`profiles/default.yaml`** — baseline, everything active:

```yaml
schema_version: 2
params:
  tracker_url: "https://tracker.example.com"
  docs_url: "https://docs.example.com"
packs:
  - name: example-pack
    enabled: true
    settings:
      enabled: true
    mcp:
      issue-tracker: { enabled: true }
      build-system: { enabled: true }
  - name: personal
```

**`profiles/frontend-dev.yaml`** — scoped to frontend work:

```yaml
schema_version: 2
params:
  tracker_url: "https://tracker.example.com"
  docs_url: "https://docs.example.com"
packs:
  - name: example-pack
    enabled: true
    settings:
      enabled: true
    skills:
      include: [react-patterns, component-review, styling]
    workflows:
      include: [design-qa]
    mcp:
      issue-tracker: { enabled: true }
      build-system: { enabled: false }
  - name: personal
```

**`profiles/oncall.yaml`** — scoped to incident response:

```yaml
schema_version: 2
params:
  tracker_url: "https://tracker.example.com"
  docs_url: "https://docs.example.com"
packs:
  - name: example-pack
    enabled: true
    settings:
      enabled: true
    skills:
      include: [triage, monitoring, escalation]
    workflows:
      include: [incident-response]
    plugins:
      include: [linear]
    mcp:
      issue-tracker: { enabled: true }
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
  - name: example-pack
  - name: personal
    overrides:
      rules: ["anti-slop"]      # personal's version replaces community-lib's
      workflows: ["deploy"]     # personal's version replaces example-pack's
```

Without the `overrides` declaration, duplicate IDs are resolved by the `defaults.collision_strategy` in sync-config.yaml. The default is `last-wins` — the later pack in profile order wins. Set it to `first-wins` for the reverse, or `error` to require explicit overrides for every collision. Explicit `overrides` always take precedence over the strategy. For rule, agent, workflow, skill, and hook collisions that should coexist, set `defaults.namespaced: true` to render names such as `deploy__aipack__team-pack`; MCP servers, plugins, and settings keys still need a single winner.

Packs with harness config files (`configs/` directory in `pack.json`) contribute base settings automatically. Multiple packs' settings are deep-merged in profile order — the first pack wins at leaf value conflicts, and a warning identifies the overlap. Set `settings.enabled: false` on a pack entry to opt it out of config contribution; quiet packs need `settings.enabled: true` to contribute configs.

Hooks are first-class content selectors. Normal packs include their hooks by default; quiet packs include none unless `hooks.include` selects them. Set `hooks.enabled: false` on a pack entry to opt out of executable hooks without disabling the pack's rules, skills, MCP servers, or settings.

## Bundled profiles

Packs can ship profile files for distribution. Drop them in the `profiles/` directory — they're auto-discovered like rules and skills:

```
my-pack/
└── profiles/
    ├── dev.yaml
    ├── frontend-dev.yaml
    └── lean.yaml
```

On install with `-w all`, bundled profiles are copied to `~/.config/aipack/profiles/` and bundled registries are merged into the user's registry configuration. A bundled profile named `default` is skipped with a warning because the user's local default profile is protected. This enables single-command team setup:

```bash
aipack pack install --url https://github.com/org/team-pack.git -w all
aipack profile set frontend-dev --install
aipack sync
```

`--install` fetches any packs referenced in the profile that aren't installed yet, looking them up in the registry.

Bundled profiles remain owned by the pack that installed them. If you customize one locally, duplicate it first; `pack update` can refresh the bundled copy. The CLI and TUI warn when a profile is known to come from installed pack content.

## MCP server overrides

Profiles control which MCP servers are active and how their tools are scoped. Each pack that defines MCP servers (in its `mcp/` directory) makes those servers available. The profile enables, disables, and configures them:

```yaml
packs:
  - name: example-pack
    mcp:
      issue-tracker:
        enabled: true
        allowed_tools:
          - docs_search
          - docs_get_page
          - get_issue
        always_allowed_tools:
          - docs_search
        disabled_tools:
          - docs_delete_page
      build-system:
        enabled: false
```

The profile is the sole source of tool permissions. `allowed_tools` limits visibility to the listed tools; `always_allowed_tools` additionally auto-approves without a per-call prompt; `disabled_tools` explicitly blocks. When both `allowed_tools` and `disabled_tools` are present, `disabled_tools` takes precedence. A tool in `always_allowed_tools` is implicitly visible, so setting it without also listing the tool in `allowed_tools` is valid and common.

For normal packs, omitting `mcp:` or setting `mcp: {}` enables every server declared by the pack. A map containing only `enabled: false` entries is treated as an exclusion overlay, so disabling one server does not drop its siblings. Once `mcp:` contains any enabled server or tool policy, it is an inclusive selection map; list sibling servers explicitly if they should remain active. For quiet packs, `mcp:` is always opt-in-only.

Leaving all three fields unset (silent) emits no allow list to the harness — the harness's native default (ask per call) applies, which is operationally equivalent to "grant all."

**Per-harness rendering of `always_allowed_tools`:**

- **Cline** renders both fields into `alwaysAllow` — no visibility/prompt distinction.
- **Claude Code** renders both fields into `permissions.allow` — tools in allow are auto-approved; unlisted tools default to per-call prompt.
- **Codex** renders `allowed_tools` and `always_allowed_tools` into `enabled_tools`, and additionally emits a `[mcp_servers.<name>.tools.<tool>] approval_mode = "approve"` stanza for each entry in `always_allowed_tools`.
- **OpenCode** unions both fields into the legacy `tools` boolean map and emits a sync-time warning that per-tool auto-approve is pending upstream syntax confirmation.

**Editing from the TUI.** The `aipack manage` TUI offers an interactive tri-state tool picker on the profiles tab. Navigate to the content tree (right-most panel), place the cursor on an MCP entry, and press `t` to open the picker. Each tool cycles through off / ask / auto on `<space>` (shortcuts: `x` off, `a` ask, `A` auto). Silent profiles render every probed tool as *ask* — matching the effective state. Probe results are cached per user at `~/.config/aipack/cache/mcp-probes.json` (24-hour TTL) so reopening the picker is instant; the header renders a "probed Nh ago" freshness hint, and pressing `r` inside the picker forces a re-probe. `aipack mcp inspect-tools` (CLI) writes the same cache as a side effect, so running it populates the TUI for next use. Pressing `.` on an MCP entry in the tree opens a small menu with "Edit file" and "Tool list" (which opens the tri-state picker, same as `t`). Inside the picker, pressing `.` opens a bulk action menu:

- **Enable all tools** — clears every tool list and enables the server (silent = grant all, matching the harness default).
- **Always allow all tools** — enumerates the probed tool list into `always_allowed_tools`. Requires a successful probe; silent has no equivalent encoding for "auto-approve everything".
- **Disable MCP server** / **Enable MCP server** — flips `enabled`; the disable variant also clears any profile tool-list overrides because they're meaningless on a disabled server.
- **Reset to pack defaults** — clears all tool-list overrides but preserves `enabled`. The resolver falls back to manifest defaults on next sync.
- **Save inventory** — rewrites the pack's `mcp/<server>.json` `available_tools` from the probe result. Only shown when a probe has completed during the current picker session.

Disabling a server in one pack and providing it from another is clean composition — for example, when a team pack's MCP server is broken and you provide a working replacement from a personal pack.

For how tool permissions render per harness, see the [Harness Reference](./harness-reference.md#mcp-tool-permissions).

## What to read next

- [Installing Packs](./installing-packs.md) — install content from any git repository
- [Creating Packs](./creating-packs.md) — author your own pack from scratch
- [Pack Format Specification](./pack-format.md) — content format, manifest schema, MCP server definitions
- [Harness Reference](./harness-reference.md) — per-harness rendering behavior, write targets, and settings merge
- [Configuration and State](./configuration.md) — config directory layout, sync-config, and profile file locations
