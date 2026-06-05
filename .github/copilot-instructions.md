# Copilot Code Review Instructions for sv4git

This repository implements semantic versioning and conventional commit tooling in Go.

## Focus areas

### Correctness over style
golangci-lint handles formatting and style. Focus reviews on logic, correctness, and edge cases — not on variable naming, comment wording, or formatting.

### Error handling
- Errors must be returned or wrapped with context (`fmt.Errorf("... : %v", err)`). Silently discarding errors is a bug.
- Functions that can fail should return `error` as the last return value.

### Monorepo versioning logic
The monorepo versioning commands (`monorepo-tag`, `monorepo-update-version`, `monorepo-bump`) must anchor their base version to the **last git tag**, not to the on-disk file version. Using the file version as a base will cause a double-bump when the file is ahead of the last tag (e.g., after a prepare-release step). The correct helper is `componentBaseVersionAndCommits`, not `componentCommits`.

### Conventional commits compliance
This project uses conventional commits. Commit type must be one of: `build`, `ci`, `chore`, `docs`, `feat`, `fix`, `perf`, `refactor`, `revert`, `style`, `test`.

### Test coverage
New logic must have unit tests. Prefer table-driven tests. Mock interfaces rather than using real git operations in unit tests.

### Interface design
Keep interfaces small. Functions that accept an interface (e.g., `sv.Git`, `sv.MonorepoProcessor`) should only declare the methods they actually use.
