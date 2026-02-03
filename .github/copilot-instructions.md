## Go Version

Before reviewing, check the Go version in `Dockerfile` (the `FROM golang:` line) and
`go.mod` (the `go` directive). Assume all standard library features available in that
Go version are valid. Do **not** flag stdlib functions as non-existent based on older
Go knowledge — always defer to the version declared in the project files.

## Testing Framework

All tests use **Ginkgo + Gomega** (BDD-style). Do not suggest converting to stdlib
`testing` patterns.

## Linting

The project uses `golangci-lint` v2 with the `modernize` linter enabled, which
enforces using newer stdlib APIs (e.g., iterator-based functions) over older
alternatives when applicable. Do not suggest reverting modernize-enforced patterns.
