#!/usr/bin/env bash
set -euo pipefail

if [[ "${GITHUB_REPOSITORY}" != DataDog/ddtest || "${GITHUB_REF}" != refs/heads/main || "${GITHUB_EVENT_NAME}" != workflow_dispatch ]]; then
  echo 'Releases must be dispatched from main in DataDog/ddtest.' >&2
  exit 1
fi

if [[ ! "${VERSION}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo 'Version must have the form vMAJOR.MINOR.PATCH without leading zeroes.' >&2
  exit 1
fi

# A re-run must be authorized by both the original actor and the re-running actor.
for actor in "$GITHUB_ACTOR" "$GITHUB_TRIGGERING_ACTOR"; do
  role=$(gh api "repos/$GITHUB_REPOSITORY/collaborators/$actor/permission" --jq .role_name)
  case "$role" in
    maintain|admin) ;;
    *) echo "Release requires Maintain or Admin access: $actor has role $role." >&2; exit 1 ;;
  esac
done

# Propagate API errors instead of treating them as an absent tag.
refs=$(gh api "repos/$GITHUB_REPOSITORY/git/matching-refs/tags/$VERSION" --jq '.[].ref')
if grep -Fxq "refs/tags/$VERSION" <<< "$refs"; then
  echo "Tag $VERSION already exists. Choose an unused version." >&2
  exit 1
fi

case "${1:-}" in
  validate) exit 0 ;;
  create) ;;
  *) echo 'Usage: release.sh validate|create' >&2; exit 1 ;;
esac

# Use the checkout SHA, even if main advances during the build. Creating the ref
# explicitly fails if another process claimed this version; never move a tag.
sha=$(git rev-parse HEAD)
gh api "repos/$GITHUB_REPOSITORY/git/refs" -f "ref=refs/tags/$VERSION" -f "sha=$sha"
gh release create "$VERSION" --verify-tag --target "$sha" --title "$VERSION" --draft --generate-notes dist/*
release_url=$(gh release view "$VERSION" --json url --jq .url)
{
  echo "## Draft release $VERSION"
  echo "Commit: $sha"
  echo "Review the assets and generated notes, then publish: $release_url"
} >> "$GITHUB_STEP_SUMMARY"
