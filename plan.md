# Plan: `monorepo-bump` — bump version files without tagging or committing

## Goal

Add a new CLI command `monorepo-bump` (alias `mbu`) that writes the next
semver to each component's versioning file and stops there — no `git commit`,
no `git tag`, no push.  This gives teams a "prepare for release" step they
can inspect and amend before running `monorepo-tag`.

---

## What changes

### 1. `cmd/git-sv/handlers.go` — new handler

Add `monorepoUpdateVersionHandler` immediately after `monorepoTagHandler`
(~line 597).  The logic is a strict subset of `monorepoTagHandler`:

```
monorepoTagHandler flow          monorepoUpdateVersionHandler flow
─────────────────────────────    ──────────────────────────────────
FindComponents                   FindComponents
  for each component:              for each component:
    componentCommits                 componentCommits
    NextVersion                      NextVersion
    if !updated → skip               if !updated → skip (print "no changes")
    UpdateVersion          ✓         UpdateVersion          ✓
    TagForComponent        ✗         (omitted)
    print tag name                   print "<name>: <version> written to <file>"
```

Signature mirrors the others — all dependencies closed over, no new params:

```go
func monorepoUpdateVersionHandler(
    git sv.Git,
    semverProcessor sv.SemVerCommitsProcessor,
    monorepoProcessor sv.MonorepoProcessor,
    cfg Config,
    repoPath string,
) func(c *cli.Context) error
```

### 2. `cmd/git-sv/main.go` — register the command

Insert after the existing `monorepo-tag` entry (~line 173):

```go
{
    Name:    "monorepo-bump",
    Aliases: []string{"mbu"},
    Usage:   "bump version files for all changed components in a monorepo without tagging or committing",
    Action:  monorepoUpdateVersionHandler(git, semverProcessor, monorepoProcessor, cfg, repoPath),
},
```

### 3. `cmd/git-sv/handlers_monorepo_test.go` — three new unit tests

Reusing the existing mock infrastructure, add:

| Test | Scenario |
|---|---|
| `Test_monorepoUpdateVersionHandler_SkipsNoUpdate` | `updated=false` → `UpdateVersion` must NOT be called; no error |
| `Test_monorepoUpdateVersionHandler_WritesVersion` | `updated=true` → `UpdateVersion` called with the right version; no tag created |
| `Test_monorepoUpdateVersionHandler_FindComponentsError` | `FindComponents` error propagated |

---

## Implementation steps

1. **Add handler** — implement `monorepoUpdateVersionHandler` in `handlers.go`
2. **Register command** — add entry in `main.go` command slice
3. **Add tests** — add the three test cases to `handlers_monorepo_test.go`
4. **Run `make test`** — verify all tests pass
5. **Commit and push** to `claude/add-claude-documentation-a0NlY`

---

## Out of scope

- No new config fields are needed.
- No changes to the `sv/` library — `UpdateVersion` already exists on
  `MonorepoProcessor`.
- Committing the changed file is intentionally left to the developer
  (or a follow-up CI step), keeping the command single-responsibility.
