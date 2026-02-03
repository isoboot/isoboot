## Go Version

This project uses **Go 1.25** (see `Dockerfile` and `go.mod`). When reviewing code,
assume all standard library features available in Go 1.25 are valid. This includes
features added in Go 1.24 and Go 1.25 such as:

- `strings.SplitSeq`, `strings.SplitAfterSeq`, `strings.FieldsSeq`, `strings.LinesSeq`
  (iterator-based string splitting)
- `bytes.SplitSeq`, `bytes.SplitAfterSeq`, `bytes.FieldsSeq`, `bytes.LinesSeq`
- Range-over-func (iterator protocol via `iter.Seq` / `iter.Seq2`)
- `slices.All`, `slices.Values`, `slices.Collect` and other iterator helpers

Do **not** flag these functions as non-existent or suggest replacing them with older
alternatives like `strings.Split`.

## Testing Framework

All tests use **Ginkgo + Gomega** (BDD-style). Do not suggest converting to stdlib
`testing` patterns.

## Linting

The project uses `golangci-lint` v2 with the `modernize` linter enabled, which
enforces using iterator-based functions (e.g., `SplitSeq`) over slice-returning ones
when the result is only iterated. Do not suggest reverting modernize-enforced patterns.
