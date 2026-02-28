# sv/ — Core Library

This package contains all business logic. The CLI in `cmd/git-sv/` depends on these interfaces.

## Core Interfaces

- **`Git`** (`git.go`): Wraps `git` subprocess calls. Methods: `LastTag`, `Log`, `Commit`, `Tag`, `Tags`, `Branch`, `IsDetached`.
- **`MessageProcessor`** (`message.go`): Parses, validates, formats commit messages per Conventional Commits. Auto-extracts issue IDs from branch names.
- **`SemVerCommitsProcessor`** (`semver.go`): Given commits, determines next semver bump (major/minor/patch/none).
- **`ReleaseNoteProcessor`** (`releasenotes.go`): Groups commits into sections → `ReleaseNote` structs.
- **`OutputFormatter`** (`formatter.go`): Renders `ReleaseNote`/`[]ReleaseNote` via Go `text/template`. Functions: `timefmt`, `getsection`, `getenv`.

---

## Monorepo Feature Architecture

The `monorepo-*` commands let each component maintain its own semver in a YAML/JSON file. All logic is in `monorepo.go`.

### Config

```yaml
monorepo:
  versioning-file: "services/*/version.yml"   # filepath.Glob — single * only, no **
  path: '.metadata.annotations["backstage.io/template-version"]'  # jq/yq-style dot/bracket path
```

`path` is parsed by `parsePath()`. Bracket notation handles keys with dots/special chars. Leading `.` is optional.

### Git Tag Convention

Component tags: `<component-relative-path>/vX.Y.Z` (e.g. `services/payments/v1.3.0`), following Go module proxy format.

### Two Commit-Baseline Functions — Do Not Swap

| Function | Used by | Baseline |
|---|---|---|
| `componentCommits()` | `monorepo-tag` only | Last component tag → falls back to all dir commits |
| `componentBaseVersionAndCommits()` | `monorepo-next-version`, `monorepo-bump`, `monorepo-changelog --add-next-version` | 3-tier (see below) |

`componentCommits` is correct for `monorepo-tag` because it always tags immediately, so on-disk version never leads committed state. The others need `componentBaseVersionAndCommits` for idempotency.

### 3-tier Baseline (`componentBaseVersionAndCommits`)

Anchors commit range and base version to git-committed state:

1. **Last component tag** — version from tag name; commits since that tag.
2. **Last commit touching the versioning file** (`LastFileCommit`) — file content at that commit via `ShowFile` + `ReadVersionFromBytes`; commits since that hash.
3. **All dir commits** — fallback for new components; uses current file version.

`monorepoUpdateVersionHandler` skips writing if `nextVer == component.CurrentVersion` — makes `monorepo-bump` idempotent.

### `monorepo-changelog` and `--add-next-version`

The `--add-next-version` block calls `componentBaseVersionAndCommits` + `semverProcessor.NextVersion` (not `componentCommits` + `monorepoProcessor.NextVersion`) so the version matches what `monorepo-bump` computes.

Flags mirror `changelog`: `--size N` (default 10), `--all`, `--add-next-version`, `--semantic-version-only`.

### File I/O

Version files parsed into `map[string]interface{}` (YAML/JSON by extension), mutated, marshalled back. **YAML comments are lost on write** — known trade-off.

### Limitations

- `filepath.Glob` supports only single `*`, not `**`.
- YAML comments in versioning files are dropped on write.
