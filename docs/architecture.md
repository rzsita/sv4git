# Architecture & Key Concepts

## Dependency Flow

```
main.go
  └─ loads Config (3-level merge: default → user ($SV4GIT_HOME) → repo (.sv4git.yml))
  └─ constructs:
       ├─ MessageProcessor       (sv.NewMessageProcessor)
       ├─ Git                    (sv.NewGit)
       ├─ SemVerCommitsProcessor (sv.NewSemVerCommitsProcessor)
       ├─ ReleaseNoteProcessor   (sv.NewReleaseNoteProcessor)
       └─ OutputFormatter        (sv.NewOutputFormatter)
  └─ registers CLI commands → handlers.go
```

## Core Interfaces (`sv/`)

- **`Git`** (`sv/git.go`): Wraps `git` subprocess calls. Methods: `LastTag`, `Log`, `Commit`, `Tag`, `Tags`, `Branch`, `IsDetached`.
- **`MessageProcessor`** (`sv/message.go`): Parses, validates, formats commit messages per Conventional Commits. Auto-extracts issue IDs from branch names.
- **`SemVerCommitsProcessor`** (`sv/semver.go`): Given commits, determines next semver bump (major/minor/patch/none).
- **`ReleaseNoteProcessor`** (`sv/releasenotes.go`): Groups commits into sections → `ReleaseNote` structs.
- **`OutputFormatter`** (`sv/formatter.go`): Renders `ReleaseNote`/`[]ReleaseNote` via Go `text/template`. Functions: `timefmt`, `getsection`, `getenv`.

## Config Loading (`cmd/git-sv/config.go`)

Priority order: **repository > user > default**.

1. **Default** — hardcoded in `defaultConfig()`
2. **User** — `$SV4GIT_HOME/config.yml` (optional)
3. **Repository** — `.sv4git.yml` in repo root (optional)

Slices and pointers are always **overwritten** (not merged) via a custom `mergeTransformer`. `ReleaseNotes.Headers` (deprecated) is handled specially.

## CLI Commands

Defined in `cmd/git-sv/main.go`, handlers in `cmd/git-sv/handlers.go`:

| Command / alias | What it does |
|---|---|
| `config show` / `cfg show` | Print merged config as YAML |
| `config default` / `cfg default` | Print default config as YAML |
| `current-version` / `cv` | Print last git tag parsed as semver |
| `next-version` / `nv` | Print next version based on commits since last tag |
| `commit-log` / `cl` | Print commit log as JSON lines; `--range tag\|date\|hash` |
| `commit-notes` / `cn` | Commit notes (release notes without version) for a range |
| `release-notes` / `rn` | Formatted release notes for a tag or next version |
| `changelog` / `cgl` | Full changelog across multiple tags |
| `tag` / `tg` | Create and push a git tag for the next version |
| `commit` / `cmt` | Interactive conventional commit helper |
| `validate-commit-message` / `vcm` | `prepare-commit-msg` git hook — validate/enhance messages |

## Handler Pattern

Handlers are constructor functions returning `func(c *cli.Context) error`. Dependencies closed over:

```go
func currentVersionHandler(git sv.Git) func(c *cli.Context) error {
    return func(c *cli.Context) error {
        // use git here
    }
}
```

## Templates

- Default templates embedded at compile time via `//go:embed resources/templates/*.tpl`.
- Repository overrides: `.sv4git/templates/` at repo root. Partial overrides require **both** `changelog-md.tpl` and `releasenotes-md.tpl` present.
- Available template functions: `timefmt`, `getsection`, `getenv`.
