# CLAUDE.md

## Git

- Commit messages must be 40 characters or fewer.
- Always add `Co-Authored-By: Claude Code <noreply@anthropic.com>` to commit messages.

## E2E Tests

- Don't use loops to verify downloads — just duplicate the check per artifact (max 3). A little duplication is clearer than loop + if/else mapping.

## Before Pushing

- Run `make lint` to check for linting errors.
- Run `make test` to run unit tests.
- Update the PR description (if any) to match the branch content.
