# Config

Config parsing, manifest/profile loading, pack discovery, and profile resolution. No upward imports — callers are `internal/app/` and `internal/engine/`.

## Profile resolution

`ResolveProfile` in `profile_resolve.go` turns a profile + pack manifests into a concrete set of content IDs per pack. Designing drift or sync features on top of it requires understanding which inputs produce fatal errors, warnings, or silent drops.

### Selector kinds

Profile pack entries reference content via `rules.include`, `rules.exclude`, `overrides.rules` (similarly for `agents`, `workflows`, `skills`). Each entry is either an **exact ID** (no glob metacharacters `*?[`) or a **glob**.

### Exact-ID references

**Default (`prevInventories == nil`):** unknown exact IDs are fatal. Error shape:

- selectors: `pack %q %s %s references unknown id %q`
- overrides: `pack %q overrides.%s references %q, but no pack provides that %s`

Sync aborts entirely.

**Lockfile-aware (`prevInventories != nil`):** if the ID is in the current inventory → resolves normally. If it's not in current but IS in the previous inventory (the pack removed content the profile references) → `domain.BrokenRef` emitted, ID dropped, resolution continues. If it's in neither (a typo) → still fatal. This preserves fat-finger detection while allowing graceful degradation through pack updates.

### Glob references

Selectors with glob metacharacters (e.g. `include: ["jira_*"]`) **silently match zero** — no error, no warning, regardless of `prevInventories`. A glob that used to match items but now matches nothing produces no drift signal through the resolver. Drift detection above the resolver must compare pre/post resolved-set sizes to catch this.

### Override ownership

Overrides are pre-scanned (not order-dependent): the pack whose entry declares an override owns the ID regardless of where the referenced content lives. If pack B declares `overrides.rules: [shared-rule]` and both A and B provide `shared-rule`, B wins. The owner-map is separate from the seen-map used to resolve which pack ultimately provides the content.

Unmatched override IDs go through the same drift-aware path: fatal by default, `BrokenRef` with `Direction: "overrides"` when present in a previous inventory.

### Collision strategies

Three strategies for content appearing in multiple packs:

- `CollisionError` — all collisions collected and reported in one error with a remediation YAML snippet. Strictest; useful for CI.
- `CollisionFirstWins` — earliest pack in profile order wins. Losers stripped from their pack's resolved content. Warning emitted.
- `CollisionLastWins` — latest pack wins. Warning emitted. Fallback when strategy is unset.

Overrides pre-empt collision strategy: if either pack has an `overrides.X` entry for the ID, the owner-map decision wins regardless of strategy.

### Quiet packs

`quiet: true` on a profile pack entry inverts the "empty include means all" default. Quiet packs include **nothing** unless an explicit non-empty include list is provided; their exclude selectors have no effect (nothing to subtract from). Library packs typically flip this; team and personal packs typically don't.

### Empty include = include all

For non-quiet packs, `rules.include: []` (empty list, not missing) includes every rule the pack provides. Missing `rules:` entirely also defaults to all. Only a non-empty include filters. This is the mechanism that lets profiles gracefully absorb new content a pack adds — unless the pack is quiet, in which case new content is invisible until the profile explicitly includes it.

### What the resolver does NOT check

- Whether MCP server `{params.*}` / `{env:*}` refs are actually set. That's render time in `expandMCPServer` (`internal/engine/mcp.go`).
- Whether the binary pointed to by an MCP server command exists. That's `doctor`'s job.
- Whether skill assets exist beyond `SKILL.md`. Parse-time check; failures are warnings.
- Whether pack content is consistent with `pack.json` declarations. That's `manifest_drift` in doctor.

### Implication for drift/sync features

Classify the signal you want to surface:

- **Fatal-error territory** (exact-ID refs, override refs): choose between crashing on bad data, wrapping resolution in lockfile-aware logic that converts fatal → warning for drift cases, or pre-validating before calling the resolver.
- **Silent-drop territory** (glob zero-matches, missing optional frontmatter): the resolver won't surface it. Compare before/after state explicitly.
- **Warning territory** (collisions, skipped MCP servers): already flows through `[]domain.Warning`. Read the returned warnings slice and filter by `Field`.

## Content discovery

`pack_discover.go`:

- `DiscoverIDs(dir, suffix)` — recursive walk; id preserves the slashed relative path. Used for rules / prompts / mcp / profiles / registries — categories whose ids may carry directory structure.
- `DiscoverIDsByLeaf(dir, dirName, suffix)` — recursive walk, leaf as id; returns an `id → relpath` map and errors on within-pack same-leaf collisions. Used for agents and workflows.
- `DiscoverSkills(skillsDir)` — recursive walk for `SKILL.md`, leaf (parent dir name) as id; returns `id → relpath` map and errors on collisions. SkipDir at first match keeps bundled-asset SKILL.md inside an outer skill from emitting as its own.
- `DiscoverContent` orchestrates all three and records the resolved paths via `PackManifest.SetResolvedPath` so `RelPath(cat, id)` returns the actual on-disk location.

`resolveWalkRoot` follows the root-level symlink so packs whose category directories were symlinked in by `pack_create` get descended into correctly. Deeper symlinks are not followed.

Manifest fields win over autodiscovery. `validatePackInventory` rejects literal `__` in rule ids (reserved as the harness escape for `/`) and rejects `/` in agent / workflow / skill ids — those ids are the leaf only; subdirectories are filesystem-only organization.

Consumers that map (category, id) → on-disk path must call `PackManifest.RelPath(cat, id)` rather than `PackCategory.PrimaryRelPath(id)` directly. `RelPath` consults `resolvedPaths` first and falls back to `PrimaryRelPath` for ids without recorded paths (rules, or unauthored content).

User-facing convention: `docs/pack-format.md`.
