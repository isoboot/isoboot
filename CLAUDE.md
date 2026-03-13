# CLAUDE.md

## Git

- Commit messages must be 40 characters or fewer.
- Always add `Co-Authored-By: Claude Code <noreply@anthropic.com>` to commit messages.

## Helm Templates

- Quote interpolated values in templates: `"{{ .Values.foo }}"` for YAML fields and inline shell/config args.

## E2E Tests

- Don't use loops to verify downloads — just duplicate the check per artifact (max 3). A little duplication is clearer than loop + if/else mapping.

## Before Pushing

- Run `make lint` to check for linting errors.
- Run `make test` to run unit tests.
- Update the PR description (if any) to match the branch content.

## Pull Requests

- After pushing, check for `This branch has conflicts that must be resolved`. If conflicts exist, resolve them and push again.
- After creating a new PR **or pushing to an existing PR**, post a comment: `@claude please review this PR`.
- After posting the review request, watch the PR for 5 minutes. If Claude has not started the review, post `@claude please review this PR` again.
- Repeat until you have posted 5 review requests in a row with no response. Then comment that Claude is not responding and `@twdamhore` should take a look.
- When Claude posts a review, address all feedback (blocking, non-blocking, suggestions, and issues), push fixes, and request another review.
- Repeat this review-fix loop until Claude posts a review with nothing actionable. Then request one more review to get 2 clean reviews in a row before stopping.
