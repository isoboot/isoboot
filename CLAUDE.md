# isoboot Project Notes

## Pre-Planning
- Always pull latest main before starting a new branch/PR: `git fetch origin && git checkout main && git pull`

## Pre-PR Checklist
- Always run `gofmt -w .` on changed files before committing
- Always run `make lint` before pushing (CI runs a Lint job that will fail otherwise)
- Always run `make helm-sync` and `make verify` before creating a PR
- The Helm chart CRD (`charts/isoboot/templates/crd.yaml`) must be in sync with `config/crd/bases/`
- CI runs a `verify` job that fails if these are out of date

## PRs and Issues
- When a GitHub issue is referenced (e.g. "Closes #205"), link the PR to the issue using `--body "Closes #NNN"` in `gh pr create`

## Post-Push
- After pushing, monitor CI with `gh run list -b <branch> --limit 3` and `gh run watch <run-id>`
- Check for failures and fix before moving on
