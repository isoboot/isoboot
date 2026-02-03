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
make clean   # rm -f bin/*
```

These targets will expand as kubebuilder scaffolding is added (e.g., `make manifests`,
`make generate`, `make docker-build`).

## Git Conventions

- **No force pushes** to `main`
- **Pull requests required** for all changes to `main`
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

### Re-requesting Copilot Review

After pushing changes that address review feedback, Claude must **always** explicitly
re-request Copilot review using:

```
gh pr edit <PR#> --add-reviewer 'copilot-pull-request-reviewer[bot]'
```

The `[bot]` suffix is required. Do **not** use any other method (closing/reopening the
PR, `@copilot review` comments, or the GraphQL `requestReviews` mutation).

### Handling Copilot Review Errors

If Copilot responds with `"Copilot encountered an error and was unable to review this
pull request"`, Claude must immediately re-request the review using the same command
above. Retry up to 3 times with a short delay between attempts.
