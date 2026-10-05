#!/usr/bin/env bash
# Check the GitHub workflows: actionlint, then the rules below, each named by
# what it guards. Every check runs; the script exits non-zero if any fails.
# Run it with `make check-workflows`, which installs pinned actionlint and yq.
# Usage: ACTIONLINT=<actionlint> YQ=<yq> hack/check-workflows.sh
set -euo pipefail
cd "$(dirname "$0")/.."

ACTIONLINT=${ACTIONLINT:-actionlint}
YQ=${YQ:-yq}
workflows=(.github/workflows/*.yml .github/workflows/*.yaml)
provision=.github/workflows/test-provision-e2e.yaml
release=.github/workflows/test-tag-helm-install.yaml

failures=0
pass() { echo "ok   $*"; }
fail() {
  echo "FAIL $*" >&2
  failures=$((failures + 1))
}
# expect_none <description> <offending lines, one per line>
expect_none() {
  if [ -z "$2" ]; then
    pass "$1"
  else
    fail "$1"
    while IFS= read -r line; do echo "       $line"; done <<<"$2" >&2
  fi
}
# jobs_where <yq filter>: "<workflow>: <job>" for each job the filter keeps.
# The filter gets each job's definition; chain select()s to combine tests.
jobs_where() {
  local workflow
  for workflow in "${workflows[@]}"; do
    "$YQ" eval ".jobs | to_entries | .[] | select(.value | [$1] | length > 0) | .key" "$workflow" \
      | sed "s|^|$workflow: |"
  done
}

if "$ACTIONLINT" "${workflows[@]}"; then pass "actionlint"; else fail "actionlint"; fi

# build-01: a tag can be moved to other code; a commit SHA cannot.
expect_none "every action is pinned to a full commit SHA with its version in a comment" \
  "$(grep -nE '^\s*(- )?uses:' "${workflows[@]}" \
    | grep -vE 'uses: [A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40} # v[0-9]+\.[0-9]+\.[0-9]+$' || true)"

# ci-01: GITHUB_TOKEN gets only what each job needs. The workflow default is
# read-only, and only a job that pushes may write packages or log in to GHCR.
pushes='([.steps[] | select((.with.push // false) == true or ((.run // "") | test("helm push|imagetools create")))] | length > 0)'
expect_none "no workflow-level packages: write" \
  "$(for workflow in "${workflows[@]}"; do
      [ "$("$YQ" eval '.permissions.packages // ""' "$workflow")" = write ] && echo "$workflow" || true
    done)"
expect_none "every job has explicit permissions (its own or the workflow's)" \
  "$(for workflow in "${workflows[@]}"; do
      [ "$("$YQ" eval 'has("permissions")' "$workflow")" = true ] && continue
      "$YQ" eval '.jobs | to_entries | .[] | select(.value | has("permissions") | not) | .key' "$workflow" \
        | sed "s|^|$workflow: |"
    done)"
expect_none "only jobs that push have packages: write" \
  "$(jobs_where "select((.permissions.packages // \"\") == \"write\") | select($pushes | not)")"
expect_none "only jobs that push log in to the registry" \
  "$(jobs_where "select([.steps[] | select((.uses // \"\") | test(\"^docker/login-action@\"))] | length > 0)
    | select($pushes | not)")"
expect_none "jobs running PR code or downloads do not keep the token in .git/config" \
  "$(for workflow in "$provision" "$release"; do
      "$YQ" eval '.jobs | to_entries | .[] | select([.value.steps[] | select((.uses // "") | test("^actions/checkout@"))
        | select(.with["persist-credentials"] != false)] | length > 0) | .key' "$workflow" | sed "s|^|$workflow: |"
    done)"

# Pinned tools: one Helm everywhere (Makefile, provision E2E, workflows) and
# the k3s the provision E2E pins.
make_helm=$(sed -n 's/^HELM_VERSION ?= *//p' Makefile)
e2e_helm=$(sed -n 's/^HELM_VERSION="\(.*\)"$/\1/p' test/e2e/provision/lib.sh)
if [ -n "$make_helm" ] && [ "$make_helm" = "$e2e_helm" ]; then
  pass "Makefile and E2E pin the same Helm ($make_helm)"
else
  fail "Makefile and E2E pin the same Helm (Makefile '$make_helm', lib.sh '$e2e_helm')"
fi
expect_none "every setup-helm installs Helm $make_helm" \
  "$(for workflow in "${workflows[@]}"; do
      "$YQ" eval ".jobs[].steps[] | select((.uses // \"\") | test(\"^azure/setup-helm@\"))
        | select(.with.version != \"$make_helm\") | .with.version // \"no version\"" "$workflow" \
        | sed "s|^|$workflow: |"
    done)"
expect_none "no workflow installs k3s from a channel" \
  "$(grep -n 'INSTALL_K3S_CHANNEL' "${workflows[@]}" || true)"

# ci-02: the release tests the images before it publishes the chart or moves
# latest; the build pushes version tags only.
expect_none "release: the chart and latest are published only after the test job" \
  "$("$YQ" eval '.jobs | to_entries | .[] | select([.value.steps[] | (.run // "") | test("helm push|:latest")] | any)
    | select([.value.needs // [] | .[]] | any_c(. == "test") | not) | .key' "$release")"
expect_none "release: the build job pushes no latest tag" \
  "$("$YQ" eval '.jobs.build.steps[] | select(.with.tags != null) | select(.with.tags | test("latest")) | .name' "$release")"
expect_none "release: the test job waits for nfsd and squid" \
  "$(for component in nfsd squid; do
      grep -q "for component in .*$component" "$release" || echo "$component"
    done)"

# ci-03: adding some other label to a PR that already has "e2e" must not
# cancel or restart the provision E2E, and fork PRs (whose token cannot push)
# run nothing. The job conditions and the concurrency group's suffix are
# evaluated for each event below, as GitHub would evaluate them.
# evaluate <expression> <context JSON>: true or false. Handles the syntax
# these conditions use: ==, !=, &&, ||, parentheses, 'strings' and
# contains(<list>.*.name, '<value>').
evaluate() {
  local expression
  expression=$(sed -E \
    -e "s/contains\(([a-z_.]+)\.\*\.name, '([^']*)'\)/([\1[].name] | any_c(. == '\2'))/g" \
    -e 's/github\./\./g' -e "s/'/\"/g" -e 's/&&/and/g' -e 's/\|\|/or/g' <<<"$1")
  "$YQ" eval -p=json "$expression" - <<<"$2"
}
rows_if=$("$YQ" eval '.jobs.rows.if' "$provision")
build_if=$("$YQ" eval '.jobs.build.if' "$provision")
group_condition=$("$YQ" eval '.concurrency.group' "$provision" | sed -E 's/.*\$\{\{ (.*) \}\}$/\1/')
# pull_request_event <action> <label added> <labels, JSON> <head repository>
pull_request_event() {
  printf '{"event_name": "pull_request", "repository": "isoboot/isoboot", "event": {"action": "%s", "label": {"name": "%s"}, "pull_request": {"labels": %s, "head": {"repo": {"full_name": "%s"}}}}}' "$@"
}
# expect_runs <description> <jobs run> <concurrency group of a real run> <context JSON>
expect_runs() {
  local actual
  actual="rows=$(evaluate "$rows_if" "$4") build=$(evaluate "$build_if" "$4") group=$(evaluate "$group_condition" "$4")"
  if [ "$actual" = "rows=$2 build=$2 group=$3" ]; then
    pass "$1"
  else
    fail "$1 (expected rows=$2 build=$2 group=$3, got $actual)"
  fi
}
expect_runs "provision E2E: runs when the e2e label is added" true true \
  "$(pull_request_event labeled e2e '[{"name": "e2e"}]' isoboot/isoboot)"
expect_runs "provision E2E: another label on an e2e PR neither runs nor cancels it" false false \
  "$(pull_request_event labeled docs '[{"name": "e2e"}, {"name": "docs"}]' isoboot/isoboot)"
expect_runs "provision E2E: runs on a push to an e2e PR" true true \
  "$(pull_request_event synchronize '' '[{"name": "e2e"}]' isoboot/isoboot)"
expect_runs "provision E2E: a push to a PR without e2e runs nothing" false false \
  "$(pull_request_event synchronize '' '[]' isoboot/isoboot)"
expect_runs "provision E2E: a fork PR runs nothing" false true \
  "$(pull_request_event labeled e2e '[{"name": "e2e"}]' someone/isoboot)"
expect_runs "provision E2E: runs on workflow_dispatch" true true \
  '{"event_name": "workflow_dispatch", "repository": "isoboot/isoboot", "event": {}}'

# ci-04: git diff ignores untracked files, so a generated file that was never
# committed would pass a git diff check. Run the workflow's check, with the
# shell options GitHub uses, in a scratch repository in each state.
verify_check=$("$YQ" eval '.jobs.verify.steps[] | select(.name == "Check for changed or new generated files") | .run' \
  .github/workflows/verify-manifests.yml)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
git -C "$scratch" init -q
echo generated >"$scratch/generated.yaml"
git -C "$scratch" add generated.yaml
git -C "$scratch" -c user.name=check -c user.email=check@example.invalid commit -q -m generated
# verify_status: the exit status of the check in the scratch repository.
verify_status() {
  (cd "$scratch" && bash --noprofile --norc -eo pipefail -c "$verify_check" >/dev/null 2>&1) && echo 0 || echo 1
}
clean=$(verify_status)
echo new >"$scratch/new-kind.yaml"
untracked=$(verify_status)
rm "$scratch/new-kind.yaml"
echo changed >"$scratch/generated.yaml"
changed=$(verify_status)
if [ -n "$verify_check" ] && [ "clean=$clean untracked=$untracked changed=$changed" = "clean=0 untracked=1 changed=1" ]; then
  pass "verify-manifests passes a clean tree and fails on changed or untracked generated files"
else
  fail "verify-manifests passes a clean tree and fails on changed or untracked generated files (clean=$clean untracked=$untracked changed=$changed)"
fi

if [ "$failures" -gt 0 ]; then
  echo "$failures workflow check(s) failed" >&2
  exit 1
fi
echo "All workflow checks passed"
