# CLI Specification

Version: 0.27.0

Machine-readable output contracts for the aipack CLI. This document defines the JSON shapes that `--json` commands emit — the public interface for CI pipelines, dashboards, wrapper scripts, and automation. For command behavior, flags, and per-harness rendering details, see the [aipack reference](./aipack.md).

If this document and the code diverge, the code is authoritative. File an issue if you find a discrepancy.

## Command tree

```
aipack
├── init
├── setup
├── config
│   ├── defaults
│   │   ├── get
│   │   └── set
│   ├── env
│   │   ├── list
│   │   ├── get
│   │   ├── set
│   │   ├── unset
│   │   ├── path
│   │   └── edit
│   └── params
│       ├── list
│       ├── get
│       ├── set
│       └── unset
├── sync
├── save
├── restore
├── render
├── clean
├── status
├── trace
├── doctor
├── search
├── query
├── manage
├── version
├── update
├── pack
│   ├── create
│   ├── install
│   ├── import
│   ├── delete
│   ├── update
│   ├── rename
│   ├── add
│   ├── remove
│   ├── enable
│   ├── disable
│   ├── list
│   ├── show
│   ├── inspect
│   ├── versions
│   └── validate
├── profile
│   ├── create
│   ├── set
│   ├── show
│   ├── include
│   ├── exclude
│   ├── refs
│   ├── set-param
│   ├── unset-param
│   ├── list
│   └── delete
├── registry
│   ├── fetch
│   ├── list
│   ├── sources
│   ├── validate
│   └── delete
├── collection
│   ├── list
│   ├── show
│   └── install
├── mcp
│   └── inspect-tools
└── prompt
    ├── list
    ├── show
    └── copy
```

## JSON output conventions

Every command that supports `--json` writes a single JSON value to stdout and exits. Human-readable output goes to stderr when `--json` is active, keeping stdout machine-parseable. Exit code 0 means success; non-zero means failure regardless of output format.

All `--json` output is a single top-level JSON object or array — never NDJSON or streaming. Consumers can parse the full stdout as one `json.Unmarshal` call.

Fields marked `omitempty` are absent from the output when empty/zero. All other fields are always present.

## JSON output contracts

### `aipack sync`

Sync emits a JSON **array** with one self-contained object per target harness, in the order the harnesses were resolved. Each harness syncs independently, so each object is a complete result. A single-harness sync emits a one-element array.

```json
[
  {
    "harness":   "claudecode",
    "dry_run":   false,
    "rules":     12,
    "workflows": 8,
    "agents":    3,
    "skills":    6,
    "plugins":   2,
    "settings":  2,
    "mcp":       5,
    "warnings":  [{"message": "stale ledger migrated", "field": "ledger"}]
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `harness` | string | The harness this result describes |
| `dry_run` | bool | Whether this was a dry run |
| `rules` | int | Number of rules in the resolved profile |
| `workflows` | int | Number of workflows in the resolved profile |
| `agents` | int | Number of agents in the resolved profile |
| `skills` | int | Number of skills in the resolved profile |
| `plugins` | int | Number of plugin references in the resolved profile |
| `settings` | int | Number of settings file actions in this harness's plan |
| `mcp` | int | Number of MCP servers in the resolved profile |
| `warnings` | array | Non-fatal issues encountered during this harness's sync. Each entry has `message` (string, always present), `path` (string, optional), and `field` (string, optional). Empty array when no warnings. Profile-resolution warnings are attached to the first harness's result. |

Content counts (`rules`, `workflows`, `agents`, `skills`, `plugins`, `mcp`) are profile-level and identical across harnesses; `settings` and `warnings` are per-harness.

### `aipack restore`

```json
{
  "restored_files": [
    {
      "cache_key": "claudecode--settings.json",
      "original_path": "/Users/x/.claude/settings.json"
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `restored_files` | array | Files restored (empty array if nothing to restore) |
| `restored_files[].cache_key` | string | Key in the presync cache (`<harness>--<filename>`) |
| `restored_files[].original_path` | string | Absolute path of the restored file |

### `aipack render`

```json
{
  "output_dir": "/path/to/rendered-output"
}
```

### `aipack status`

```json
{
  "profile": "ops",
  "profile_path": "/Users/x/.config/aipack/profiles/ops.yaml",
  "config_dir": "/Users/x/.config/aipack",
  "packs": [
    {
      "name": "my-example-pack",
      "version": "2026.03.15",
      "rules": 3,
      "agents": 1,
      "workflows": 11,
      "skills": 5,
      "hooks": 0,
      "plugins": 2,
      "mcp_servers": 5,
      "settings": true
    }
  ],
  "disabled_packs": [
    {
      "name": "archived-pack",
      "version": "2026.01.10",
      "rules": 2,
      "agents": 0,
      "workflows": 0,
      "skills": 1,
      "hooks": 0,
      "plugins": 0,
      "mcp_servers": 0,
      "settings": false
    }
  ],
  "total_rules": 8,
  "total_agents": 1,
  "total_workflows": 11,
  "total_skills": 10,
  "total_hooks": 0,
  "total_plugins": 2,
  "total_mcp_servers": 5,
  "settings_packs": ["my-example-pack"]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `profile` | string | Active profile name |
| `profile_path` | string | Absolute path to the profile YAML |
| `config_dir` | string | Config directory path |
| `packs` | array | Enabled profile packs contributing content |
| `disabled_packs` | array | Disabled profile packs with the same count fields as `packs`, omitted when empty |
| `packs[].name` | string | Pack name |
| `packs[].version` | string | Pack version from manifest (omitempty) |
| `packs[].rules` | int | Rule count in this pack |
| `packs[].agents` | int | Agent count |
| `packs[].workflows` | int | Workflow count |
| `packs[].skills` | int | Skill count |
| `packs[].hooks` | int | Hook count |
| `packs[].plugins` | int | Plugin reference count |
| `packs[].mcp_servers` | int | MCP server count |
| `packs[].settings` | bool | Whether this pack provides settings |
| `disabled_packs[].error` | string | Manifest/discovery error for disabled pack diagnostics (omitempty) |
| `total_rules` | int | Sum of rules across all packs |
| `total_agents` | int | Sum of agents |
| `total_workflows` | int | Sum of workflows |
| `total_skills` | int | Sum of skills |
| `total_hooks` | int | Sum of hooks |
| `total_plugins` | int | Sum of plugin references |
| `total_mcp_servers` | int | Sum of MCP servers |
| `settings_packs` | string[] | Packs contributing settings, in profile order (omitempty) |

### `aipack trace`

Accepts either `aipack trace <type> <name>` or `aipack trace <name>`. The single-argument form resolves exact active-profile matches across traceable resource types, then inactive matches from disabled/excluded profile content and installed packs. Exit code 1 when the resource is absent or when the single-argument form is ambiguous.

```json
{
  "resource_type": "rule",
  "resource_name": "anti-slop",
  "found": true,
  "profile_state": "active",
  "source": {
    "pack": "essentials",
    "source_path": "/Users/x/.config/aipack/packs/essentials/rules/anti-slop.md",
    "category": "rules"
  },
  "destinations": [
    {
      "harness": "claudecode",
      "path": "/Users/x/.claude/rules/anti-slop.md",
      "state": "identical",
      "diff_kind": "identical"
    },
    {
      "harness": "codex",
      "path": "/Users/x/.codex/AGENTS.override.md",
      "embedded": true,
      "state": "managed",
      "diff_kind": "managed"
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `resource_type` | string | Resolved resource type |
| `resource_name` | string | Resolved resource name |
| `found` | bool | Whether the resource exists in the active profile or was diagnostically found as inactive |
| `profile_state` | string | `active`, `pack_disabled`, `content_excluded`, `installed_not_in_profile`, or `not_installed` |
| `blockers` | string[] | Reasons an inactive resource is not syncing (omitempty) |
| `remediation` | string[] | Exact next commands for inactive resources (omitempty) |
| `source` | object or null | Source location (absent when `found` is false) |
| `source.pack` | string | Pack name containing the resource |
| `source.source_path` | string | Absolute path to the source file |
| `source.category` | string | Content category: `rules`, `agents`, `workflows`, `skills`, `hooks`, `plugins`, `mcp` |
| `destinations` | array | Per-harness destination info (empty when not found or inactive) |
| `destinations[].harness` | string | Harness ID |
| `destinations[].path` | string | Absolute path where the resource lands |
| `destinations[].embedded` | bool | True when composited into a multi-resource file (omitempty) |
| `destinations[].state` | string | On-disk state (see [Enumerations](#enumerations)) |
| `destinations[].diff_kind` | string | Same as `state` (typed enum in source) |

### `aipack doctor`

Overall `ok` is false only when a critical-severity check fails. Warning-level checks do not affect the exit code.

```json
{
  "schema_version": 1,
  "ok": true,
  "status": "ok",
  "profile_path": "/Users/x/.config/aipack/profiles/developer.yaml",
  "checks": [
    {
      "name": "sync_config",
      "ok": true,
      "status": "pass",
      "severity": "critical"
    },
    {
      "name": "cli_update",
      "ok": true,
      "status": "warn",
      "severity": "warning",
      "message": "newer version available: v0.9.1",
      "remediation": "brew upgrade aipack"
    },
    {
      "name": "ledger_health",
      "ok": true,
      "status": "fixed",
      "severity": "warning",
      "message": "removed 2 orphaned ledger entries",
      "fixed": true,
      "fix_action": "removed orphaned entries"
    }
  ],
  "ecosystem": {
    "profile": "ops",
    "profile_path": "/Users/x/.config/aipack/profiles/developer.yaml",
    "config_dir": "/Users/x/.config/aipack",
    "packs": [],
    "total_rules": 8,
    "total_agents": 1,
    "total_workflows": 11,
    "total_skills": 10,
    "total_hooks": 0,
    "total_mcp_servers": 5,
    "settings_packs": ["base-pack"]
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `schema_version` | int | Always `1` (for forward compatibility) |
| `ok` | bool | True when no critical-severity check failed |
| `status` | string | `"ok"` or `"fail"` |
| `profile_path` | string | Resolved profile path (omitempty) |
| `checks` | array | Individual check results |
| `checks[].name` | string | Check identifier (see [Enumerations](#enumerations)) |
| `checks[].ok` | bool | Whether this check passed |
| `checks[].status` | string | `pass`, `fail`, `skip`, `warn`, or `fixed` |
| `checks[].severity` | string | `critical` or `warning` |
| `checks[].message` | string | Human-readable detail (omitempty) |
| `checks[].remediation` | string | Suggested fix (omitempty) |
| `checks[].details` | object | Structured check-specific data (omitempty) |
| `checks[].fixed` | bool | True when `--fix` repaired this issue (omitempty) |
| `checks[].fix_action` | string | Description of what was fixed (omitempty) |
| `ecosystem` | object or null | Same shape as `aipack status --json` (absent if profile resolution failed) |

### `aipack search`

```json
[
  {
    "pack": "essentials",
    "kind": "skill",
    "name": "deep-research",
    "description": "Methodology for thorough multi-source research",
    "owner": "",
    "category": "research",
    "snippet": "...matching text...",
    "installed": true,
    "status": "installed"
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `pack` | string | Pack containing the resource |
| `kind` | string | Resource kind |
| `name` | string | Resource name |
| `description` | string | Resource description |
| `owner` | string | Resource owner (from frontmatter metadata) |
| `last_updated` | string | Last updated date (omitempty) |
| `path` | string | Source file path (omitempty) |
| `category` | string | Category (omitempty) |
| `snippet` | string | Matching text excerpt (omitempty) |
| `installed` | bool | Whether the containing pack is installed |
| `status` | string | Discovery status: `installed`, `registered`, or `inspected` |

### `aipack query`

Always returns JSON regardless of flags. Array of row objects where keys are column names and values are SQLite-typed. Shape depends entirely on the query.

```json
[
  {"column_name": "value", "other_column": 42}
]
```

### `aipack pack list`

Always an array. Empty `[]` when no packs are installed.

```json
[
  {
    "name": "essentials",
    "path": "/Users/x/.config/aipack/packs/essentials",
    "method": "clone",
    "version": "1.2.3",
    "pin": "1.2.3",
    "origin": "https://github.com/shrug-labs/packs.git",
    "is_link": false
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Pack name |
| `path` | string | Absolute path to installed pack |
| `method` | string | Install method (see [Enumerations](#enumerations)) |
| `version` | string | Version from `pack.json` (informational, omitempty) |
| `pin` | string | Lockfile version pin: semver string, commit hash, or empty/omitted when tracking HEAD |
| `origin` | string | Source URL (omitempty for local/link installs) |
| `is_link` | bool | Whether the pack is a symlink |
| `broken_link` | bool | True for broken symlinks (omitempty) |

### `aipack pack show`

Content ID arrays are always present (empty `[]`, never null).

```json
{
  "name": "essentials",
  "version": "1.2.3",
  "pin": "1.2.3",
  "path": "/Users/x/.config/aipack/packs/essentials",
  "method": "clone",
  "origin": "https://github.com/shrug-labs/packs.git",
  "ref": "v1.2.3",
  "commit_hash": "abc123def456",
  "installed_at": "2026-03-10T08:30:00Z",
  "rules": ["anti-slop", "verification-before-completion"],
  "agents": ["code-reviewer"],
  "workflows": ["session-retro", "brainstorm"],
  "skills": ["deep-research", "writing-plans"],
  "hooks": [],
  "plugins": ["linear"],
  "prompts": [],
  "mcp_servers": [],
  "settings": ["codex/config.toml", "opencode/opencode.json"],
  "extras": ["scripts/run-server.sh"]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Pack name |
| `version` | string | Version from manifest |
| `path` | string | Absolute path |
| `method` | string | Install method |
| `origin` | string | Source URL |
| `ref` | string | Git ref used at install (omitempty) |
| `commit_hash` | string | Git commit at install time (omitempty) |
| `installed_at` | string | ISO 8601 timestamp (omitempty) |
| `rules` | string[] | Rule IDs |
| `agents` | string[] | Agent IDs |
| `workflows` | string[] | Workflow IDs |
| `skills` | string[] | Skill IDs |
| `hooks` | string[] | Hook IDs |
| `plugins` | string[] | Plugin reference IDs |
| `prompts` | string[] | Prompt IDs |
| `mcp_servers` | string[] | MCP server names |
| `settings` | string[] | Harness config files as `<harness>/<file>` IDs (omitempty) |
| `extras` | string[] | Extra bundled file paths (omitempty) |

### `aipack pack inspect`

Inspects a source without installing it. Content ID arrays are always present (empty `[]`, never null). Bundled profiles, registries, and extras are omitted when empty.

```json
{
  "name": "team-pack",
  "version": "1.2.3",
  "path": "https://github.com/org/packs.git",
  "source": "https://github.com/org/packs.git",
  "source_type": "registry",
  "status": "inspected",
  "method": "clone",
  "ref": "main",
  "path_in_source": "team-pack",
  "counts": {
    "rules": 2,
    "skills": 3,
    "hooks": 0,
    "workflows": 1,
    "agents": 0,
    "plugins": 1,
    "prompts": 0,
    "mcp": 2
  },
  "rules": ["safety"],
  "agents": [],
  "workflows": ["deploy"],
  "skills": ["triage"],
  "hooks": [],
  "plugins": ["linear"],
  "prompts": [],
  "mcp_servers": ["issue-tracker"],
  "profiles": ["oncall"],
  "registries": ["team"],
  "extras": ["scripts/check.sh"],
  "warnings": [
    "defines MCP servers (external tool access): issue-tracker"
  ],
  "registry": {
    "name": "team-pack",
    "description": "Team operating pack",
    "owner": "platform",
    "contact": "#platform",
    "quiet": true
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Resolved pack name |
| `version` | string | Version from manifest (omitempty) |
| `path` | string | Source path or URL that was inspected |
| `source` | string | Same source value used for indexing |
| `source_type` | string | `path`, `url`, or `registry` |
| `status` | string | Always `inspected` |
| `method` | string | Inspect method: `local`, `clone`, or `archive` |
| `ref` | string | Git ref used for clone inspection (omitempty) |
| `path_in_source` | string | Subdirectory within the source (omitempty) |
| `counts` | object | Content counts by vector |
| `rules`, `agents`, `workflows`, `skills`, `hooks`, `plugins`, `prompts`, `mcp_servers` | string[] | Discovered content IDs |
| `profiles`, `registries`, `extras` | string[] | Bundled content IDs or paths (omitempty) |
| `warnings` | string[] | Trust warnings surfaced by inspection, such as MCP external tool access (omitempty) |
| `registry` | object | Registry metadata when inspected by registry name (omitempty) |

`aipack pack inspect --clear` wipes inspected rows from the search index instead of inspecting a source. Cannot be combined with an input argument or `--url`. Inspected rows older than 30 days are dropped automatically the next time any source is inspected.

```json
{
  "removed": 4
}
```

### `aipack pack versions`

```json
{
  "name": "essentials",
  "origin": "https://github.com/shrug-labs/packs.git",
  "installed_version": "1.2.3",
  "versions": [
    {"version": "v2.0.0"},
    {"version": "v1.2.3", "installed": true},
    {"version": "v1.0.0"}
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Pack name |
| `origin` | string | Source URL the tags were fetched from |
| `installed_version` | string | Current pin from the lockfile (semver, commit hash, or empty/omitted when tracking HEAD) |
| `versions` | object[] | Available semver tags, sorted descending |
| `versions[].version` | string | Tag name (always v-prefixed) |
| `versions[].installed` | bool | True for the currently installed version (omitempty); only set when the pin is a semver string that matches a tag |

Versions list contains only tags that parse as valid semver. When the pack has no semver tags, `versions` is an empty array. When the pin is a commit hash, no entry is marked installed.

### `aipack pack validate`

Validates pack structure and metadata, not authored content bodies. Exit code 1 when `ok` is false.

```json
{
  "ok": true,
  "findings": [
    {
      "path": "rules/my-rule.md",
      "category": "frontmatter",
      "severity": "warning",
      "field": "description",
      "message": "missing description field"
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `ok` | bool | True when no error-severity findings |
| `findings` | array | Validation findings (absent or empty when clean) |
| `findings[].path` | string | File path relative to pack root |
| `findings[].category` | string | Finding category (see [Enumerations](#enumerations)) |
| `findings[].severity` | string | `error` or `warning` |
| `findings[].field` | string | Frontmatter field name (omitempty) |
| `findings[].message` | string | Human-readable description |

### `aipack pack delete`

Deletes an installed pack, removes safe rendered harness files, strips pack-managed keys from shared settings files, clears ledger entries, removes matching profile entries, and clears the lockfile entry. Rendered files are removed only when their ledger digest still matches the on-disk content and the path is a known harness-rendered location. Modified files and unknown ledger paths are preserved on disk and made unmanaged. Use `--keep-rendered` to clear aipack tracking while leaving rendered files unmanaged.

```json
{
  "name": "my-pack",
  "dry_run": false,
  "keep_rendered": false,
  "source_removed": true,
  "rendered_removed": 7,
  "rendered_preserved": 2,
  "shared_settings_stripped": 1,
  "ledger_cleared": 9,
  "profiles_edited": 1,
  "lockfile_cleared": true
}
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Pack name that was deleted |
| `dry_run` | bool | True when the run was a preview |
| `keep_rendered` | bool | True when rendered harness files were retained |
| `source_removed` | bool | True if `<configDir>/packs/<name>/` existed and was removed |
| `rendered_removed` | int | Count of clean, known rendered ledger paths removed (or counted in dry-run) |
| `rendered_preserved` | int | Count of rendered/shared ledger paths left on disk and made unmanaged because they were modified, unknown, or explicitly kept |
| `shared_settings_stripped` | int | Count of shared settings files rewritten to remove pack-managed keys while preserving user content |
| `ledger_cleared` | int | Count of ledger entries with `source_pack == name` cleared (or counted in dry-run) |
| `profiles_edited` | int | Count of profiles that had a matching pack entry removed (or counted in dry-run) |
| `lockfile_cleared` | bool | True if a lockfile entry existed and was removed |
| `bundled_profiles` | string[] | Bundled profile files still present after pack removal (omitempty) |

`--dry-run` reports the counts as they would be without writing. The exit code is 0 on success and non-zero only when the named pack is not installed (no lockfile entry and no source dir).

### `aipack profile show`

The fully-resolved profile object. Shape follows the `domain.Profile` struct — packs with typed content, MCP servers, and settings bundle. This is the most complex JSON output and its shape is not yet stabilized. Use `status --json` for a stable summary.

### `aipack profile include` / `aipack profile exclude`

Text-only mutating commands. They update the target profile's content selectors or MCP server `enabled` flag for exact IDs already present in the profile's enabled pack entries. There is no JSON output contract.

### `aipack profile refs`

```json
{
  "profile": "oncall",
  "profile_path": "/Users/x/.config/aipack/profiles/oncall.yaml",
  "refs": [
    {
      "kind": "param",
      "name": "tracker_url",
      "display": "{params.tracker_url}",
      "status": "missing",
      "pack": "example-pack",
      "target": "issue-tracker",
      "location": "mcp.issue-tracker.command[2]"
    },
    {
      "kind": "env",
      "name": "API_TOKEN",
      "display": "{env:API_TOKEN}",
      "status": "dotenv",
      "pack": "example-pack",
      "target": "issue-tracker",
      "location": "mcp.issue-tracker.env.API_TOKEN"
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `profile` | string | Resolved profile name |
| `profile_path` | string | Absolute path to the profile YAML |
| `refs` | array | Parameter and environment references discovered in enabled MCP servers and contributing harness settings |
| `refs[].kind` | string | `param` or `env` |
| `refs[].name` | string | Parameter or environment variable name |
| `refs[].display` | string | Original placeholder text |
| `refs[].status` | string | Params: `set`, `defaulted`, or `missing`; env refs: `dotenv`, `env`, `defaulted`, or `missing` |
| `refs[].has_default` | boolean | True when the placeholder includes an inline default (omitempty) |
| `refs[].pack` | string | Pack providing the MCP server or settings file (omitempty) |
| `refs[].target` | string | MCP server name or harness id (omitempty) |
| `refs[].location` | string | Field or config file where the placeholder was found (omitempty) |

### `aipack config params list`

Lists profile-scoped params. The `--profile` flag selects the profile; when omitted, aipack uses `sync-config.defaults.profile`, then `default`.

```json
{
  "profile": "oncall",
  "profile_path": "/Users/x/.config/aipack/profiles/oncall.yaml",
  "params": [
    {
      "key": "tracker_url",
      "value": "https://tracker.example.com"
    },
    {
      "key": "workspace",
      "value": "team-alpha"
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `profile` | string | Resolved profile name |
| `profile_path` | string | Absolute path to the profile YAML |
| `params` | array | Profile params, sorted by key |
| `params[].key` | string | Parameter key |
| `params[].value` | string | Parameter value |

### `aipack config env list`

Lists keys from the config-dir `.env` file. Values are omitted by default and present only when `--show` is also set. Entries are sorted by key.

```json
{
  "path": "/Users/x/.config/aipack/.env",
  "entries": [
    {
      "key": "API_TOKEN",
      "length": 12
    },
    {
      "key": "API_BASE_URL",
      "value": "https://api.example.com",
      "length": 23
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `path` | string | Absolute path to the config-dir `.env` file |
| `entries` | array | Keys present in the `.env` file, sorted by key |
| `entries[].key` | string | Environment variable name |
| `entries[].value` | string | Environment variable value, present only with `--show` |
| `entries[].length` | int | Character length of the stored value |

### `aipack collection list`

```json
[
  {
    "name": "team-dev",
    "description": "Team developer starter set",
    "packs": [
      {
        "name": "essentials"
      },
      {
        "name": "aipack-core",
        "ref": "aipack-core/v1.2.3",
        "with": ["profiles", "registries"]
      }
    ]
  }
]
```

`aipack collection show <name> --json` returns the same object shape for one collection instead of an array.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Collection name |
| `description` | string | Human-readable description (omitempty) |
| `packs` | array | Ordered pack install recipe |
| `packs[].name` | string | Registry pack name |
| `packs[].ref` | string | Git ref override for this collection install (omitempty) |
| `packs[].with` | string[] | Bundled content categories accepted for this pack; may contain `all` before parsing (omitempty) |

### `aipack registry list`

```json
[
  {
    "name": "essentials",
    "installed": true,
    "repo": "https://github.com/shrug-labs/packs.git",
    "path": "essentials",
    "ref": "main",
    "description": "Foundation pack for AI agent configuration",
    "owner": "shrug-labs",
    "contact": ""
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Pack name |
| `installed` | bool | Whether the pack is currently installed |
| `repo` | string | Git repository URL |
| `path` | string | Subdirectory within the repo (omitempty) |
| `ref` | string | Git ref (omitempty) |
| `description` | string | Human-readable description (omitempty) |
| `owner` | string | Maintainer or team (omitempty) |
| `contact` | string | Contact info (omitempty) |

### `aipack registry validate`

Exit code 1 when `valid` is false.

```json
{
  "path": "/Users/x/registry.yaml",
  "valid": false,
  "errors": [
    "pack \"tools\": missing required field repo"
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `path` | string | Registry file path that was validated |
| `valid` | bool | True when semantic validation found no errors |
| `errors` | string[] | Validation messages; empty on success |

### `aipack registry sources`

```json
[
  {
    "name": "default",
    "url": "https://github.com/shrug-labs/packs.git",
    "ref": "main",
    "path": "registry.yaml",
    "cached": true
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Local name for this source |
| `url` | string | Git repository URL |
| `ref` | string | Git ref (omitempty) |
| `path` | string | File path within the repo (omitempty) |
| `cached` | bool | Whether a local cache file exists |

### `aipack mcp inspect-tools` (list mode)

When invoked without a server argument or `--all`, returns the inventory of MCP servers across all installed packs.

```json
{
  "list_mode": true,
  "ok": true,
  "available_servers": [
    {
      "server_name": "my-server",
      "pack_name": "my-team-pack",
      "transport": "stdio",
      "tool_count": 24,
      "inventory_path": "/home/user/.config/aipack/packs/my-team-pack/mcp/my-server.json"
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `list_mode` | bool | Always `true` in list mode |
| `ok` | bool | Always `true` in list mode |
| `available_servers` | array | One entry per MCP server found across installed packs |
| `available_servers[].server_name` | string | Server identifier (filename without `.json`) |
| `available_servers[].pack_name` | string | Name of the pack containing this server |
| `available_servers[].transport` | string | `stdio`, `sse`, or `streamable-http` |
| `available_servers[].tool_count` | int | Number of tools in the static `available_tools` inventory |
| `available_servers[].inventory_path` | string | Absolute path to the inventory JSON file |
| `warnings` | string[] | Per-file read or parse failures encountered while scanning `packs/*/mcp/*.json`. Omitted when empty. |

### `aipack mcp inspect-tools <server>` (probe mode)

When invoked with a server name or `--all`, probes servers and returns results.

```json
{
  "ok": true,
  "results": [
    {
      "server_name": "my-server",
      "pack_name": "my-team-pack",
      "transport": "stdio",
      "status": "ok",
      "tools": ["get_item", "list_items", "search"],
      "tool_count": 3,
      "previous_tools": ["get_item", "list_items"],
      "added": ["search"],
      "removed": [],
      "saved": true,
      "inventory_path": "/home/user/.config/aipack/packs/my-team-pack/mcp/my-server.json",
      "duration": "1.2s"
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `ok` | bool | `true` if all probed servers responded successfully |
| `input_error` | bool | Present and `true` when the failure is caused by bad user input (unknown or ambiguous server name). CLI adapters map this to exit code 2 (`ExitUsage`); all other `!ok` results map to exit code 1 (`ExitFail`). |
| `results` | array | One entry per server inspected |
| `results[].server_name` | string | Server identifier |
| `results[].pack_name` | string | Pack containing this server |
| `results[].transport` | string | Transport type |
| `results[].status` | string | `ok`, `skipped`, or `error`. `error` covers probe failures and post-probe `--save` write failures. |
| `results[].tools` | string[] | Discovered tool names (sorted). Present when the live probe succeeded, even if a later `--save` failed. |
| `results[].tool_count` | int | Number of discovered tools |
| `results[].previous_tools` | string[] | Tools from the static inventory before probe |
| `results[].added` | string[] | Tools in live list but not in previous inventory |
| `results[].removed` | string[] | Tools in previous inventory but not in live list |
| `results[].saved` | bool | Whether `--save` wrote the inventory file |
| `results[].would_save` | bool | Present and `true` when `--save --dry-run` determined a write would happen but skipped it |
| `results[].inventory_path` | string | Path to inventory JSON (present when `saved` or `would_save` is true) |
| `results[].error` | string | Error message (present when status is `skipped` or `error`) |
| `results[].duration` | string | Probe duration (e.g. `"1.2s"`). Present when the live probe succeeded, even if a later `--save` failed. |

## Shared flag resolution

Several flags follow a common resolution chain across commands:

| Flag | Resolution order |
|------|-----------------|
| `--profile` | `--profile-path` → `--profile` → `sync-config defaults.profile` → `"default"` |
| `--scope` | `--scope` → `sync-config defaults.scope` → `"global"` |
| `--harness` | `--harness` → `sync-config defaults.harnesses` → `$AIPACK_DEFAULT_HARNESS` |
| `--config-dir` | `--config-dir` → `~/.config/aipack` (`%APPDATA%\aipack` on Windows) |

Commands may apply different cardinality rules after this resolution chain. `sync` accepts one or more resolved harnesses (including `all`) and syncs each independently, emitting one result object per harness; commands such as `trace`, `clean`, `save`, and `restore` may accept multiple harnesses when their command contract allows it.

## Environment variables

| Variable | Description |
|----------|-------------|
| `AIPACK_DEFAULT_HARNESS` | Default harness when none configured |
| `AIPACK_NO_UPDATE_CHECK` | Set to `1` to disable automatic update checking |

## Enumerations

**Harness IDs:** `claudecode`, `opencode`, `codex`, `cline`

**Scopes:** `project`, `global`

**Resource types (for trace):** `rule`, `agent`, `workflow`, `skill`, `hook`, `plugin`, `mcp`

**Diff kinds:** `create` (file doesn't exist on disk), `identical` (desired matches on-disk), `managed` (on-disk matches ledger — safe to update), `conflict` (user-modified since last sync), `untracked` (exists on disk but not in ledger), `error` (classification failed)

**Install methods:** `clone` (shallow git clone — default for remote git installs), `archive` (static zip/tar URL, re-fetched on update), `copy` (copied from local path), `link` (symlinked to local path), `local` (already in packs directory, registered in-place). Legacy `http-tarball` entries may appear in old lockfiles; they are transparently migrated to `clone` on the next `pack update`.

**Search statuses:** `installed` (pack is installed locally), `registered` (uninstalled registry/deep-index content), `inspected` (one-off preview from `pack inspect`). Search reconciles installed status from `aipack.lock` and installed pack directories before applying status filters.

**Finding categories:** `frontmatter`, `policy`, `consistency`, `inventory`

**Finding severities:** `error`, `warning`

**Doctor check names:** `sync_config`, `active_profile`, `profile_validated`, `packs_resolved`, `packs_registered`, `git_available`, `ledger_health`, `manifest_drift`, `cli_update`, `pack_version_drift`, `stale_ledgers`, `mcp_refs_present`, `mcp_server_paths_exist`, `lockfile_migration`, `lockfile_loaded`

**Doctor check statuses:** `pass`, `fail`, `skip`, `warn`, `fixed`

**Doctor check severities:** `critical`, `warning`
