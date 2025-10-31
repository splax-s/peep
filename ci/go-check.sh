#!/usr/bin/env bash

set -euo pipefail

MODULES=("api" "builder")

for dir in "${MODULES[@]}"; do
  echo "::group::${dir} checks"
  pushd "${dir}" >/dev/null
  go vet ./...
  go test ./...
  go build ./...
  popd >/dev/null
  echo "::endgroup::"
  echo
  echo "Finished ${dir}"
  echo

done
