# CLAUDE.md

## Git

- Commit messages must be 40 characters or fewer.
- Always add `Co-Authored-By: Claude Code <noreply@anthropic.com>` to commit messages.

## Before Creating a PR

- Switch to `main` and pull latest before creating a new branch.

## Before Pushing

- Run `make lint` to check for linting errors.
- Run `make test` to run unit tests.
- Update the PR description (if any) to match the branch content.
