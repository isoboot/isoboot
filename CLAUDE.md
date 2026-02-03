# CLAUDE.md - AI Assistant Context for isoboot

## Current State

This repository is in a **scaffolding transition phase**. All prior Go code, proto
definitions, controllers, and handlers have been removed in preparation for a fresh
`kubebuilder init`. The only files remaining are:

- `LICENSE` - Apache 2.0
- `Makefile` - Minimal build/test/clean targets

The previous codebase implemented a Kubernetes controller for network boot
orchestration (PXE/iPXE), managing BootSource and BootTarget custom resources with
gRPC services for boot media delivery.

## What's Next

1. Run `kubebuilder init` to scaffold a new project structure
2. Define CRDs (BootSource, BootTarget) using kubebuilder markers
3. Implement controllers with the new kubebuilder-generated scaffolding
4. Re-introduce gRPC services for boot media delivery
5. Add comprehensive test coverage from the start

## Build Commands

```bash
make build   # go build ./...
make test    # go test ./...
make lint    # golangci-lint run
make clean   # rm -f bin/*
```

These targets will expand as kubebuilder scaffolding is added (e.g., `make manifests`,
`make generate`, `make docker-build`).

## Pre-PR Checklist

Before creating a pull request, **always** run:

```bash
make build && make test && make lint
```

All three must pass. The CI will reject PRs that fail lint, so catching issues locally
saves time.

## Testing Convention

All tests use **Ginkgo + Gomega**, consistent with controller-runtime and kubebuilder
conventions. This applies to both controller integration tests (with envtest) and
standalone library packages (with `httptest`, temp files, etc.).

- Each package has a `suite_test.go` with `RunSpecs` as the entry point
- Set `reporterConfig.Verbose = true` in `RunSpecs` so individual spec names appear
  in CI output (do **not** pass `-ginkgo.v` via CLI — it breaks non-ginkgo binaries
  if any exist)
- Use `Describe`/`Context`/`It` blocks for readable spec names
- Use `Eventually`/`Consistently` for async assertions in controller tests
- Use `BeforeEach`/`AfterEach` for per-test setup/teardown

## Git Conventions

- **No force pushes** to `main`
- **Pull requests required** for all changes to `main`
- **All PRs must target `main`** — do not create PRs that merge into non-main branches
  (no stacked PRs, no intermediate base branches like `pr/01-scaffold-v2`)
- **Squash merge** is the preferred merge strategy
- Commit messages should be concise and descriptive (imperative mood)
- Branch naming: `feat/`, `fix/`, `docs/`, `refactor/`, `pr/` prefixes

## AI-to-AI Conversation Loop

This project uses an automated review workflow where Claude Code and GitHub Copilot
collaborate on pull requests:

1. **Claude Code** authors changes and opens a PR
2. **GitHub Copilot** (`copilot-pull-request-reviewer`) automatically reviews via
   `review_on_push: true`
3. **Claude Code** reads Copilot's review threads and for each:
   - **Agrees**: Makes the suggested change, replies explaining the fix, resolves the thread
   - **Disagrees**: Replies with rationale, resolves the thread
   - **Out of scope**: Creates a GitHub issue to track it, replies with issue reference, resolves the thread
4. If changes were made, Claude commits, pushes, and **explicitly re-requests Copilot review**
5. The loop ends when Copilot's review has no addressable comments
6. After **10 cycles**, the automated loop pauses. A human maintainer can comment
   `"Initiate AI review feedback loop"` to trigger another 10 cycles

All Claude replies in PR threads are prefixed with `"From Claude:"` on their own line
to clearly distinguish AI-to-AI conversation from human comments.

### Resolving Review Threads

After replying to a review comment, Claude must resolve the thread using the GraphQL
`resolveReviewThread` mutation. First query the thread IDs:

```
gh api graphql -f query='query {
  repository(owner: "isoboot", name: "isoboot") {
    pullRequest(number: <PR#>) {
      reviewThreads(first: 50) {
        nodes { id isResolved comments(first: 1) { nodes { body } } }
      }
    }
  }
}'
```

Then resolve each unresolved thread:

```
gh api graphql -f query='mutation {
  resolveReviewThread(input: {threadId: "<THREAD_NODE_ID>"}) {
    thread { isResolved }
  }
}'
```

Do **not** use `minimizeComment` — that hides comments instead of resolving the thread.

### Re-requesting Copilot Review

After pushing changes that address review feedback, Claude must **always** explicitly
re-request Copilot review using:

```
gh pr edit <PR#> --add-reviewer 'copilot-pull-request-reviewer[bot]'
```

The `[bot]` suffix is required. Do **not** use any other method (closing/reopening the
PR, `@copilot review` comments, or the GraphQL `requestReviews` mutation).

### Monitoring for Copilot Reviews

After pushing changes and re-requesting Copilot review, Claude must run a background
monitor that:

1. Polls for new Copilot reviews every **30 seconds**
2. If Copilot responds with an error (`"Copilot encountered an error and was unable to
   review this pull request"`), re-requests the review immediately and resets the timer
3. If Copilot posts a review with comments, reports any unresolved threads
4. If Copilot does not respond within **25 minutes**, posts a PR comment:
   `"From Claude: Copilot did not respond within 25 minutes of the review request."`

**Important pitfalls when polling reviews:**

- Use separate `--jq` queries per field (`.[-1].submitted_at`, `.[-1].user.login`,
  `.[-1].body`). Do **not** combine into a delimited string with `cut -d'|'` — Copilot
  review bodies contain `|` from markdown tables which corrupts parsing.
- Always use `?per_page=100` when fetching reviews — the GitHub API defaults to 30
  results per page, so `.[-1]` may not return the actual latest review on PRs with
  many review rounds.
- When checking for Copilot errors, match the **exact** string `"Copilot encountered
  an error"` — do **not** use a generic grep for "error" because legitimate reviews
  often contain phrases like "error handling" or "error paths" which cause false
  positives.
- When a new Copilot review is detected, **always fetch the review's comments using
  its review ID** (`/reviews/<ID>/comments`). Do **not** use date-based filtering on
  `/pulls/<PR>/comments` — timestamps can be unreliable and cause comment counts to
  show as zero even when comments exist. After fetching, Claude must immediately
  process any comments (agree/disagree/out-of-scope) before waiting for the next
  review cycle.

### CI Checks

After pushing changes, Claude must check CI status with `gh pr checks <PR#>` and
inspect any failures using `gh run view <RUN_ID> --log-failed`. Common CI jobs:

- **Lint** (`lint.yml`): Runs `golangci-lint`. Fix all reported issues (errcheck,
  goconst, modernize, prealloc, etc.) before pushing again.
- **Tests** (`test.yml`): Runs `make test`. Ensure all packages compile and all tests
  pass.
- **E2E Tests** (`test-e2e.yml`): Runs `make test-e2e` with a Kind cluster.

If CI fails, Claude must fix the issues, commit, push, and re-request Copilot review.

### PR Summary

After each push to a PR branch, Claude must update the PR description (`gh pr edit
--body`) to reflect the current state of the PR. The summary must include:

- What changed (packages added/modified, key functions)
- A full test table listing every test with its type (Positive/Negative) and what it validates
- Test plan with current pass counts
- Links to any follow-up issues created during review
