# Testing

## Structure

- Tests live as `*_test.go` alongside source files in `sv/` and `cmd/git-sv/`.
- `sv/` uses comprehensive table-driven unit tests.
- `cmd/git-sv/` uses `resources_test.go` for config/resource loading tests.

## Running Tests

```bash
make test                                                      # go test ./...
make test-coverage                                             # race detector + coverage report
go test -race -covermode=atomic -coverprofile coverage.out ./...
```

## Linter Exclusions (see `.golangci.yml`)

These linters are suppressed for `_test.go` files: `tagliatelle`, `gocyclo`, `errcheck`, `dupl`, `gosec`, `gochecknoglobals`, `testpackage`.

`gochecknoglobals` and `funlen` are also suppressed for `cmd/git-sv/main.go`.
