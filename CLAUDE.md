# CLAUDE.md — sv4git Codebase Guide

## Project Overview

**sv4git** (`git-sv`) is a Go CLI tool for semantic versioning with git. It validates commit messages (Conventional Commits spec), auto-bumps semver versions based on commit history, creates git tags, and generates changelogs/release notes using Go templates.

- Module: `github.com/bvieira/sv4git/v2`
- Binary: `git-sv` (installed on PATH, invoked as `git sv` or `git-sv`)
- Go version: 1.19+

---

## Repository Structure

```
sv4git/
├── cmd/git-sv/              # CLI entry point (package main)
│   ├── main.go              # App setup, command registration, config loading
│   ├── handlers.go          # Command handler functions
│   ├── config.go            # Config types, loading, merging, env vars
│   ├── prompt.go            # Interactive prompts (promptui wrappers)
│   ├── log.go               # Logging helpers (warnf)
│   └── resources/
│       └── templates/       # Embedded Go templates (via go:embed)
│           ├── changelog-md.tpl
│           ├── releasenotes-md.tpl
│           ├── rn-md-section-commits.tpl
│           └── rn-md-section-breaking-changes.tpl
├── sv/                      # Core library (package sv)
│   ├── config.go            # Config struct types
│   ├── git.go               # Git interface and GitImpl (executes git commands)
│   ├── message.go           # CommitMessage, MessageProcessor (parse/validate/format)
│   ├── semver.go            # SemVerCommitsProcessor (version bumping logic)
│   ├── releasenotes.go      # ReleaseNote, ReleaseNoteProcessor
│   ├── formatter.go         # OutputFormatter (renders Go templates)
│   ├── formatter_functions.go # Template helper functions (timefmt, getsection)
│   └── *_test.go            # Unit tests
├── .sv4git.yml              # Repository-level sv4git config (used by this repo itself)
├── .golangci.yml            # golangci-lint config (enables tagliatelle linter)
├── Makefile                 # Build, test, lint, release targets
├── go.mod / go.sum          # Go module files
└── .github/workflows/       # CI: lint + build + tag + release on push to master
```

---

## Development Commands

All common tasks are via `make`. Run `make` with no args to list targets.

| Command | Description |
|---|---|
| `make build` | Run tests then build binary to `bin/linux_amd64/git-sv` |
| `make test` | Run all unit tests (`go test ./...`) |
| `make test-coverage` | Run tests with race detector and coverage report |
| `make lint` | Run `golangci-lint` (no autofix) |
| `make lint-autofix` | Run `golangci-lint --fix` |
| `make run args="-h"` | Run the built binary with optional args |
| `make tidy` | Run `go mod tidy` |
| `make release` | Build + package binary as `.tar.gz` (or `.zip` on Windows) |
| `make release-all` | Build for all platforms (linux/darwin/windows, amd64/arm64) |

### Build Variables

```bash
BUILDOS="linux" BUILDARCH="amd64" make build   # target OS/arch
VERSION="1.2.3" make build                      # embed version string
```

The binary is output to `bin/${BUILDOS}_${BUILDARCH}/git-sv`.

### Running Tests

```bash
make test
# or directly:
go test ./...
go test -race -covermode=atomic -coverprofile coverage.out ./...
```

---

## Architecture & Key Concepts

### Dependency Flow

```
main.go
  └─ loads Config (3-level merge: default → user ($SV4GIT_HOME) → repo (.sv4git.yml))
  └─ constructs:
       ├─ MessageProcessor  (sv.NewMessageProcessor)
       ├─ Git               (sv.NewGit)
       ├─ SemVerCommitsProcessor (sv.NewSemVerCommitsProcessor)
       ├─ ReleaseNoteProcessor   (sv.NewReleaseNoteProcessor)
       └─ OutputFormatter        (sv.NewOutputFormatter)
  └─ registers CLI commands → handlers.go
```

### Core Interfaces (in `sv/`)

- **`Git`** (`sv/git.go`): Wraps `git` subprocess calls. Methods: `LastTag`, `Log`, `Commit`, `Tag`, `Tags`, `Branch`, `IsDetached`.
- **`MessageProcessor`** (`sv/message.go`): Parses, validates, formats, and enhances commit messages per Conventional Commits. Auto-extracts issue IDs from branch names.
- **`SemVerCommitsProcessor`** (`sv/semver.go`): Given a list of commits, determines the next semver bump (major/minor/patch/none).
- **`ReleaseNoteProcessor`** (`sv/releasenotes.go`): Groups commits into sections and produces `ReleaseNote` structs.
- **`OutputFormatter`** (`sv/formatter.go`): Renders `ReleaseNote`/`[]ReleaseNote` using Go `text/template`.

### Config Loading (`cmd/git-sv/config.go`)

Config is merged in priority order: **repository > user > default**.

1. **Default**: hardcoded in `defaultConfig()` in `config.go`
2. **User**: `$SV4GIT_HOME/config.yml` (optional)
3. **Repository**: `.sv4git.yml` in the repo root (optional)

Slices and pointers are always overwritten (not merged) due to a custom `mergeTransformer`. `ReleaseNotes.Headers` (deprecated) is also handled specially.

### CLI Commands

Defined in `cmd/git-sv/main.go`, handlers in `cmd/git-sv/handlers.go`:

| Command (alias) | What it does |
|---|---|
| `config show` / `cfg show` | Print merged config as YAML |
| `config default` / `cfg default` | Print default config as YAML |
| `current-version` / `cv` | Print last git tag parsed as semver |
| `next-version` / `nv` | Print next version based on commits since last tag |
| `commit-log` / `cl` | Print commit log as JSON lines, supports `--range tag\|date\|hash` |
| `commit-notes` / `cn` | Generate commit notes (release notes without version) for a range |
| `release-notes` / `rn` | Generate formatted release notes for a tag or next version |
| `changelog` / `cgl` | Generate full changelog across multiple tags |
| `tag` / `tg` | Create and push a git tag for the next version |
| `commit` / `cmt` | Interactive conventional commit helper |
| `validate-commit-message` / `vcm` | Used as `prepare-commit-msg` git hook to validate/enhance commit messages |

---

## Code Conventions

### Struct Tags

The `tagliatelle` linter enforces tag naming conventions (`.golangci.yml`):
- `json`: **camelCase**
- `yaml`: **kebab-case**
- `xml`, `bson`: camelCase
- `mapstructure`: kebab-case

Example from `sv/git.go`:
```go
type GitCommitLog struct {
    Date       string        `json:"date,omitempty"`
    AuthorName string        `json:"authorName,omitempty"`  // camelCase json
    ...
}
```

Example from `sv/config.go`:
```go
type BranchesConfig struct {
    DisableIssue bool     `yaml:"disable-issue"`  // kebab-case yaml
    SkipDetached *bool    `yaml:"skip-detached"`
    ...
}
```

### Handler Pattern

Handlers are constructor functions returning `func(c *cli.Context) error`. Dependencies are closed over:

```go
func currentVersionHandler(git sv.Git) func(c *cli.Context) error {
    return func(c *cli.Context) error {
        // use git here
    }
}
```

### Error Handling

- Errors from git subprocess calls include combined stdout+stderr via `combinedOutputErr`.
- Fatal errors (config load failure, git path discovery) use `log.Fatal`.
- Handler errors are returned as `fmt.Errorf` and printed by the CLI framework.

### Templates

- Default templates are embedded at compile time via `//go:embed resources/templates/*.tpl`.
- Repository-level overrides: place files in `.sv4git/templates/` at the repo root. The CLI loads all files from that directory, so partial overrides work as long as both `changelog-md.tpl` and `releasenotes-md.tpl` exist.
- Template functions available: `timefmt`, `getsection`, `getenv` (from `sv/formatter_functions.go` and `os.Getenv`).

---

## Monorepo Feature Architecture

The monorepo feature (`monorepo-*` commands) lets each component in a monorepo maintain its own semver stored in a YAML/JSON file. All design is in `sv/monorepo.go` and `cmd/git-sv/handlers.go`.

### Config

```yaml
monorepo:
  versioning-file: "services/*/version.yml"   # filepath.Glob pattern — single * only, no **
  path: '.metadata.annotations["backstage.io/template-version"]'  # jq/yq-style dot/bracket path
```

`path` is parsed by `parsePath()` in `sv/monorepo.go`. Bracket notation (`["key.with.dots"]`) handles keys that contain dots or special characters. Leading `.` is optional.

### Git tag convention

Component tags follow the Go module proxy format: `<component-relative-path>/vX.Y.Z`
(e.g. `services/payments/v1.3.0`). This scopes tags by directory, avoiding collisions.

### Two commit-baseline functions

These two functions serve different purposes and must not be swapped:

| Function | Used by | Baseline |
|---|---|---|
| `componentCommits()` | `monorepo-next-version`, `monorepo-tag`, `monorepo-changelog` (tag loop) | Last component tag (`LastComponentTag`) → falls back to all dir commits |
| `componentBaseVersionAndCommits()` | `monorepo-bump`, `monorepo-changelog --add-next-version` | 3-tier (see below) |

### 3-tier baseline (`componentBaseVersionAndCommits`)

Used by `monorepo-bump` and the `--add-next-version` block of `monorepo-changelog`. Anchors both the commit range and base version to git-committed state to guarantee idempotency:

1. **Last component tag** — version parsed from tag name; commits since that tag.
2. **Last commit that touched the versioning file** (`LastFileCommit`) — file content at that commit (`ShowFile`) parsed via `ReadVersionFromBytes`; commits since that hash.
3. **All dir commits** — fallback for brand-new components; uses current file version as base.

After computing `nextVer`, `monorepoUpdateVersionHandler` skips writing if `nextVer == component.CurrentVersion` — this makes `monorepo-bump` idempotent even when the file was bumped but not yet committed.

### `monorepo-changelog` flags mirror `changelog`

`--size N` (default 10), `--all`, `--add-next-version`, `--semantic-version-only`. The `--add-next-version` block calls `componentBaseVersionAndCommits` + `semverProcessor.NextVersion` (not `componentCommits` + `monorepoProcessor.NextVersion`) so the version shown is identical to what `monorepo-bump` would compute.

### New Git interface methods (sv/git.go)

- `LastComponentTag(componentPath string) string` — most recent `<path>/v*` tag
- `ComponentTags(componentPath string) ([]GitTag, error)` — all `<path>/v*` tags sorted ascending
- `LastFileCommit(relPath string) string` — hash of last commit touching a file
- `ShowFile(commit, relPath string) ([]byte, error)` — raw file content at a commit

### File I/O design

Version files are parsed into `map[string]interface{}` (YAML or JSON detected by extension), the version field is mutated in-place, then marshalled back. **YAML comments are not preserved** — acceptable trade-off.

### Limitations

- `filepath.Glob` supports only single `*`, not `**`. Deep patterns won't work.
- YAML comments in versioning files are silently dropped on write.

---

## Commit Message Convention

This repository uses **Conventional Commits** enforced by sv4git itself (via `.sv4git.yml`).

Format: `<type>(<scope>): <description>`

Supported types (from `.sv4git.yml` default): `build`, `ci`, `chore`, `docs`, `feat`, `fix`, `perf`, `refactor`, `revert`, `style`, `test`

Version bumping rules (from this repo's `.sv4git.yml`):
- `feat` → minor bump
- `build`, `ci`, `chore`, `fix`, `perf`, `refactor`, `test` → patch bump
- Breaking change (`BREAKING CHANGE:` footer or `!` in header) → major bump

Tag pattern: `v%d.%d.%d` (e.g., `v2.7.0`)

Issue footer format: `issue: #<number>` (auto-extracted from branch names matching `#?[0-9]+`).

---

## CI/CD

### Pull Requests (`pull-request.yml`)

Triggered on PRs to `master`. Runs:
1. `golangci-lint`
2. `make build` (which includes `make test`)

### Merges to Master (`ci.yml`)

Triggered on pushes to `master` (ignores `.md` and `.gitignore` changes). Pipeline:
1. **Lint** — `golangci-lint`
2. **Build** — `make build`
3. **Tag** — runs `git sv tag` to create and push a new semver tag
4. **Release** — builds binaries for all platforms, creates GitHub release with release notes

---

## Testing Approach

- Tests live alongside source files as `*_test.go` in `sv/` and `cmd/git-sv/`.
- The `sv/` package has comprehensive table-driven unit tests.
- Test files in `cmd/git-sv/` use `resources_test.go` for config/resource loading tests.
- The `tagliatelle`, `gocyclo`, `errcheck`, `dupl`, `gosec`, `gochecknoglobals`, and `testpackage` linters are suppressed for `_test.go` files (see `.golangci.yml`).
- `gochecknoglobals` and `funlen` are suppressed for `cmd/git-sv/main.go`.

---

## Key Files Quick Reference

| File | Purpose |
|---|---|
| `cmd/git-sv/main.go` | Entry point; wires all components together |
| `cmd/git-sv/handlers.go` | One handler function per CLI command |
| `cmd/git-sv/config.go` | `Config` struct, loading, merging, defaults |
| `cmd/git-sv/prompt.go` | `promptui`-based interactive prompts |
| `sv/git.go` | Git subprocess interface + `GitImpl` |
| `sv/message.go` | `CommitMessage` struct, parse/validate/format logic |
| `sv/semver.go` | Next-version calculation from commits |
| `sv/releasenotes.go` | Release note data model and processor |
| `sv/formatter.go` | Template-based output formatting |
| `sv/monorepo.go` | `MonorepoComponent`, `MonorepoProcessor`, file I/O helpers, `parsePath` |
| `sv/config.go` | Config type definitions (shared by CLI and library) |
| `.sv4git.yml` | This repo's sv4git configuration |
| `.golangci.yml` | Linter configuration |
| `Makefile` | All dev workflow targets |
