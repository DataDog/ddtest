#!/usr/bin/env bash
set -euo pipefail

# Customers download these executables directly (see the README's "Direct
# download (legacy)"), and the install scripts download the same names: every
# release must publish each of them, with its line in SHA256SUMS.
dist=${1:?Usage: check-release-assets.sh DIST_DIRECTORY}
for asset in ddtest-linux-amd64 ddtest-linux-arm64 ddtest-darwin-amd64 ddtest-darwin-arm64 \
  ddtest-windows-amd64.exe ddtest-windows-arm64.exe; do
  if [[ ! -s "$dist/$asset" ]]; then
    echo "Release asset $asset is missing from $dist." >&2
    exit 1
  fi
  if ! awk -v name="$asset" '$2 == name || $2 == "*" name { found = 1 } END { exit !found }' "$dist/SHA256SUMS"; then
    echo "SHA256SUMS has no line for $asset." >&2
    exit 1
  fi
done
