# CLAUDE.md — sv4git

**sv4git** (`git-sv`) is a Go CLI tool for semantic versioning with git. Validates Conventional Commits, bumps semver, creates git tags, generates changelogs via Go templates.

- Module: `github.com/bvieira/sv4git/v2`
- Binary: `git-sv` (on PATH, invoked as `git sv` or `git-sv`)
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
│   └── resources/templates/ # Embedded Go templates (via go:embed)
├── sv/                      # Core library (package sv)
│   ├── config.go            # Config struct types
│   ├── git.go               # Git interface and GitImpl (executes git commands)
│   ├── message.go           # CommitMessage, MessageProcessor (parse/validate/format)
│   ├── semver.go            # SemVerCommitsProcessor (version bumping logic)
│   ├── releasenotes.go      # ReleaseNote, ReleaseNoteProcessor
│   ├── formatter.go         # OutputFormatter (renders Go templates)
│   └── monorepo.go          # MonorepoComponent, MonorepoProcessor, parsePath
├── .sv4git.yml              # This repo's sv4git config (authoritative for commit rules)
├── .golangci.yml            # golangci-lint config (enables tagliatelle linter)
├── Makefile                 # All dev targets — run `make` to list them
└── .github/workflows/       # CI: lint + build + tag + release
```

---

## Development

All tasks use `make`. Run `make` to list all targets. Key commands:

- `make build` — run tests then build to `bin/linux_amd64/git-sv`
- `make test` — `go test ./...`
- `make lint` / `make lint-autofix` — golangci-lint

---

## Code Conventions

### Struct Tags

`tagliatelle` linter enforces (see `.golangci.yml`): `json` → **camelCase**, `yaml`/`mapstructure` → **kebab-case**.

```go
AuthorName   string `json:"authorName"`       // camelCase
SkipDetached *bool  `yaml:"skip-detached"`    // kebab-case
```

### Error Handling

- Git subprocess errors: combined stdout+stderr via `combinedOutputErr`.
- Fatal errors (config, git path): `log.Fatal`.
- Handler errors: returned as `fmt.Errorf`, printed by CLI framework.

---

## Commit Message Convention

Format: `<type>(<scope>): <description>` — authoritative rules in `.sv4git.yml`.

Bumps (this repo): `feat` → minor · `fix`/`build`/`ci`/`chore`/`perf`/`refactor`/`test` → patch · breaking change (`BREAKING CHANGE:` footer or `!`) → major.

Tag pattern: `v%d.%d.%d`. Issue footer: `issue: #<number>` (auto-extracted from branch names matching `#?[0-9]+`).

---

## CI/CD

PRs to `master`: lint + build. Merges to `master`: lint → build → `git sv tag` → multi-platform release. See `.github/workflows/`.

---

## Supplementary Context

Read these files when working on the relevant area — they are not loaded automatically:

| File | When to read |
|---|---|
| `docs/architecture.md` | Working on CLI commands, config loading, handler/template patterns, or the dependency wiring in `main.go` |
| `docs/monorepo.md` | Working on any `monorepo-*` command or `sv/monorepo.go` |
| `docs/testing.md` | Writing or debugging tests, or adjusting linter config |
