# CLI Specification

Version: 0.9.0

Machine-readable output contracts for the aipack CLI. This document defines the JSON shapes that `--json` commands emit — the public interface for CI pipelines, dashboards, wrapper scripts, and automation. For command behavior, flags, and per-harness rendering details, see the [aipack reference](./aipack.md).

If this document and the code diverge, the code is authoritative. File an issue if you find a discrepancy.

## Command tree

```
aipack
├── init
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
├── pack
│   ├── create
│   ├── install
│   ├── list
│   ├── show
│   ├── update
│   ├── delete
│   ├── rename
│   ├── enable
│   └── disable
├── profile
│   ├── create
│   ├── set
│   ├── show
│   ├── list
│   └── delete
├── registry
│   ├── fetch
│   ├── list
│   ├── sources
│   └── remove
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

```json
{
  "dry_run":   false,
  "rules":     12,
  "workflows": 8,
  "agents":    3,
  "skills":    6,
  "settings":  2,
  "mcp":       5
}
```

| Field | Type | Description |
|-------|------|-------------|
| `dry_run` | bool | Whether this was a dry run |
| `rules` | int | Number of rule files in the plan |
| `workflows` | int | Number of workflow files in the plan |
| `agents` | int | Number of agent files in the plan |
| `skills` | int | Number of skill directories in the plan |
| `settings` | int | Number of settings file actions in the plan |
| `mcp` | int | Number of MCP server actions in the plan |

### `aipack restore`

```json
{
  "restored_files": [
    {
      "cache_key": "claudecode--settings.local.json",
      "original_path": "/Users/x/.claude/settings.local.json"
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
  "profile": "ocm",
  "profile_path": "/Users/x/.config/aipack/profiles/ocm.yaml",
  "config_dir": "/Users/x/.config/aipack",
  "packs": [
    {
      "name": "ocm-ai-runbooks",
      "version": "2026.03.15",
      "rules": 3,
      "agents": 1,
      "workflows": 11,
      "skills": 5,
      "mcp_servers": 5,
      "settings": true
    }
  ],
  "total_rules": 8,
  "total_agents": 1,
  "total_workflows": 11,
  "total_skills": 10,
  "total_mcp_servers": 5,
  "settings_pack": "ocm-ai-runbooks"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `profile` | string | Active profile name |
| `profile_path` | string | Absolute path to the profile YAML |
| `config_dir` | string | Config directory path |
| `packs` | array | Per-pack content inventories |
| `packs[].name` | string | Pack name |
| `packs[].version` | string | Pack version from manifest (omitempty) |
| `packs[].rules` | int | Rule count in this pack |
| `packs[].agents` | int | Agent count |
| `packs[].workflows` | int | Workflow count |
| `packs[].skills` | int | Skill count |
| `packs[].mcp_servers` | int | MCP server count |
| `packs[].settings` | bool | Whether this pack provides settings |
| `total_rules` | int | Sum of rules across all packs |
| `total_agents` | int | Sum of agents |
| `total_workflows` | int | Sum of workflows |
| `total_skills` | int | Sum of skills |
| `total_mcp_servers` | int | Sum of MCP servers |
| `settings_pack` | string | Name of the pack providing settings (omitempty) |

### `aipack trace`

Exit code 1 when the resource is not found.

```json
{
  "resource_type": "rule",
  "resource_name": "anti-slop",
  "found": true,
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
| `resource_type` | string | Echoed input type |
| `resource_name` | string | Echoed input name |
| `found` | bool | Whether the resource exists in the active profile |
| `source` | object or null | Source location (absent when `found` is false) |
| `source.pack` | string | Pack name containing the resource |
| `source.source_path` | string | Absolute path to the source file |
| `source.category` | string | Content category: `rules`, `agents`, `workflows`, `skills`, `mcp` |
| `destinations` | array | Per-harness destination info (empty when not found) |
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
      "message": "pruned 2 orphaned ledger entries",
      "fixed": true,
      "fix_action": "pruned orphaned entries"
    }
  ],
  "ecosystem": {
    "profile": "ocm",
    "profile_path": "/Users/x/.config/aipack/profiles/developer.yaml",
    "config_dir": "/Users/x/.config/aipack",
    "packs": [],
    "total_rules": 8,
    "total_agents": 1,
    "total_workflows": 11,
    "total_skills": 10,
    "total_mcp_servers": 5,
    "settings_pack": "base-pack"
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
    "installed": true
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
    "method": "archive",
    "version": "2026.03.07",
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
| `version` | string | Version from manifest (omitempty) |
| `origin` | string | Source URL (omitempty for local/link installs) |
| `is_link` | bool | Whether the pack is a symlink |
| `broken_link` | bool | True for broken symlinks (omitempty) |

### `aipack pack show`

Content ID arrays are always present (empty `[]`, never null).

```json
{
  "name": "essentials",
  "version": "2026.03.07",
  "path": "/Users/x/.config/aipack/packs/essentials",
  "method": "archive",
  "origin": "https://github.com/shrug-labs/packs.git",
  "ref": "main",
  "commit_hash": "abc123def456",
  "installed_at": "2026-03-10T08:30:00Z",
  "rules": ["anti-slop", "verification-before-completion"],
  "agents": ["code-reviewer"],
  "workflows": ["session-retro", "brainstorm"],
  "skills": ["deep-research", "writing-plans"],
  "prompts": [],
  "mcp_servers": []
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
| `prompts` | string[] | Prompt IDs |
| `mcp_servers` | string[] | MCP server names |

### `aipack pack validate`

Exit code 1 when `ok` is false.

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

### `aipack profile show`

The fully-resolved profile object. Shape follows the `domain.Profile` struct — packs with typed content, MCP servers, and settings bundle. This is the most complex JSON output and its shape is not yet stabilized. Use `status --json` for a stable summary.

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

### `aipack registry sources`

```json
[
  {
    "name": "default",
    "url": "https://github.com/shrug-labs/aipack.git",
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

## Shared flag resolution

Several flags follow a common resolution chain across commands:

| Flag | Resolution order |
|------|-----------------|
| `--profile` | `--profile-path` → `--profile` → `sync-config defaults.profile` → `"default"` |
| `--scope` | `--scope` → `sync-config defaults.scope` → `"project"` |
| `--harness` | `--harness` → `sync-config defaults.harnesses` → `$AIPACK_DEFAULT_HARNESS` |
| `--config-dir` | `--config-dir` → `$AIPACK_CONFIG_DIR` → `~/.config/aipack` |

## Environment variables

| Variable | Description |
|----------|-------------|
| `AIPACK_DEFAULT_HARNESS` | Default harness when none configured |
| `AIPACK_CONFIG_DIR` | Override config directory |
| `AIPACK_NO_UPDATE_CHECK` | Set to `1` to disable automatic update checking |

## Enumerations

**Harness IDs:** `claudecode`, `opencode`, `codex`, `cline`

**Scopes:** `project`, `global`

**Resource types (for trace):** `rule`, `agent`, `workflow`, `skill`, `mcp`

**Diff kinds:** `create` (file doesn't exist on disk), `identical` (desired matches on-disk), `managed` (on-disk matches ledger — safe to update), `conflict` (user-modified since last sync), `untracked` (exists on disk but not in ledger), `error` (classification failed)

**Install methods:** `archive` (git archive fetch), `clone` (shallow git clone), `copy` (copied from local path), `link` (symlinked to local path), `local` (already in packs directory, registered in-place)

**Finding categories:** `frontmatter`, `policy`, `consistency`, `inventory`

**Finding severities:** `error`, `warning`

**Doctor check names:** `sync_config`, `active_profile`, `profile_validated`, `packs_resolved`, `packs_registered`, `git_available`, `ledger_health`, `manifest_drift`, `cli_update`, `pack_version_drift`, `stale_ledgers`

**Doctor check statuses:** `pass`, `fail`, `skip`, `warn`, `fixed`

**Doctor check severities:** `critical`, `warning`
