# CLAUDE.md

## Git

- Commit messages must be 40 characters or fewer.
- Always add `Co-Authored-By: Claude Code <noreply@anthropic.com>` to commit messages.

## Helm Templates

- Quote interpolated values: use `{{ .Values.foo | quote }}` for YAML fields, `"{{ .Values.foo }}"` for inline shell args.

## E2E Tests

- Don't use loops to verify downloads — just duplicate the check per artifact (max 3). A little duplication is clearer than loop + if/else mapping.

## Before Pushing

- Run `make lint` to check for linting errors.
- Run `make test` to run unit tests.
- Update the PR description (if any) to match the branch content.

## Pull Requests

- After creating a new PR, post a comment: `@claude please review this PR`.
