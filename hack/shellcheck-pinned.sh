#!/usr/bin/env bash
# The shellcheck version this repository checks against, pinned by digest.
# hack/check-workflows.sh hands it to actionlint, which otherwise uses
# whatever shellcheck is on the PATH (none on a Mac, the runner's own in CI),
# so a workflow could pass in one place and fail in the other. actionlint
# passes each script on stdin.
set -euo pipefail
exec docker run --rm -i -v "$PWD:$PWD:ro" -w "$PWD" \
  koalaman/shellcheck:v0.11.0@sha256:61862eba1fcf09a484ebcc6feea46f1782532571a34ed51fedf90dd25f925a8d "$@"
