# CLAUDE.md

## Git

- Commit messages must be 40 characters or fewer.
- Always add `Co-Authored-By: Claude Code <noreply@anthropic.com>` to commit messages.

## Helm Templates

- Quote interpolated values in templates: `"{{ .Values.foo }}"` for YAML fields and inline shell/config args.

## Project Guide

- `AGENTS.md` has the layout, the generated files and the conventions; `README.md` explains the system.
- User docs live in `docs/` (index: `docs/README.md`). `make test` creates every isoboot object, ConfigMap and Secret in their `yaml` blocks against the CRDs and checks their relative links (`test/docs/`); mark a deliberately partial block with `<!-- docs-test: skip (why) -->`.

## E2E Tests

- Don't use loops to verify downloads — just duplicate the check per artifact (max 3). A little duplication is clearer than loop + if/else mapping.
- Provision E2E (`test/e2e/provision/`): `rows.json` is the only list of rows, for CI and local runs. After changing it or an example it uses, run `test/e2e/provision/check-rows.sh`.
- Never run `test/e2e/provision/run.sh` or a phase script on your machine or a shared host: they uninstall k3s, delete `/data/isoboot` and change the network. They refuse to run unless `GITHUB_ACTIONS=true`, the host has `/etc/isoboot-e2e-vm`, or `E2E_ALLOW_THIS_HOST=1` is set. Don't set that to get around the guard.
- Run rows locally with `hack/e2e-local.sh [--row <id>]... [--keep] [--reuse]` (x86-64 Linux host with KVM, nested virtualisation and multipass). It runs everything in the throwaway VM `isoboot-e2e-local` and copies logs to `e2e-logs/<time>/<row>/`. `--keep` leaves the VM and the last row's state; inside it, `~/isoboot/test/e2e/provision/run.sh <row> <phase>` re-runs one phase. `--reuse` runs in that VM again, keeping its downloads. The guest's 4K screen is on VNC at `127.0.0.1:5900` inside the VM (tunnel it with `ssh -L`; see README, Provision E2E) and saved as `screen-*.png` in the row's logs.
- In CI the provision E2E runs on PRs labelled `e2e` (`.github/workflows/test-provision-e2e.yaml`), one phase script per step.
- After changing the harness, run `test/e2e/provision/selftest.sh` (bash 4+, jq, docker; no KVM). New checks in the harness go into `lib.sh` as functions with a selftest case that fails without them.

## Before Pushing

- Run `make lint` to check for linting errors.
- Run `make test` to run unit tests.
- Update the PR description (if any) to match the branch content.

## Pull Requests

- After pushing, check for `This branch has conflicts that must be resolved`. If conflicts exist, resolve them and push again.
- After creating a new PR **or pushing to an existing PR**, post a comment: `@claude please review this PR`.
- After posting the review request, watch the PR for 5 minutes using `gh pr view <number> --json comments` (not `--json reviews`). If Claude has not started the review, post `@claude please review this PR` again.
- Repeat until you have posted 5 review requests in a row with no response. Then comment that Claude is not responding and `@twdamhore` should take a look.
- When Claude posts a review, address all feedback (blocking, non-blocking, suggestions, and issues), push fixes, and request another review.
- Repeat this review-fix loop until Claude posts a review with nothing actionable. Then request one more review to get 2 clean reviews in a row before stopping.
