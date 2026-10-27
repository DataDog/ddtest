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
  # Custom roles (such as dd-repo-owner) can inherit Maintain permissions.
  allowed=$(gh api "repos/$GITHUB_REPOSITORY/collaborators/$actor/permission" \
    --jq '.user.permissions | .maintain == true or .admin == true')
  if [[ "$allowed" != true ]]; then
    echo "Release requires Maintain or Admin access: $actor is not authorized." >&2
    exit 1
  fi
done

# The release tags VERSION and ddto/VERSION, the version of the Go module in
# ddto/. Propagate API errors instead of treating them as an absent tag.
for tag in "$VERSION" "ddto/$VERSION"; do
  refs=$(gh api "repos/$GITHUB_REPOSITORY/git/matching-refs/tags/$tag" --jq '.[].ref')
  if grep -Fxq "refs/tags/$tag" <<< "$refs"; then
    echo "Tag $tag already exists. Choose an unused version." >&2
    exit 1
  fi
done

case "${1:-}" in
  validate) exit 0 ;;
  create) ;;
  *) echo 'Usage: release.sh validate|create' >&2; exit 1 ;;
esac

# The release commit is the checkout's commit with ddto's version and go.mod's
# requirement of it, for go install (see release-commit.sh), which made it
# locally: RELEASE_COMMIT. GitHub makes it again, signed, from the same files,
# on the checkout SHA even if main advances during the build.
sha=$(git rev-parse HEAD)
if [[ -z "${RELEASE_COMMIT:-}" || "$(git rev-parse "$RELEASE_COMMIT^")" != "$sha" ]]; then
  echo 'RELEASE_COMMIT must be the release commit on the checkout, from release-commit.sh.' >&2
  exit 1
fi
entries=()
while IFS= read -r path; do
  blob=$(printf '{"encoding":"base64","content":"%s"}' "$(git show "$RELEASE_COMMIT:$path" | base64 | tr -d '\n')" |
    gh api "repos/$GITHUB_REPOSITORY/git/blobs" --input - --jq .sha)
  mode=$(git ls-tree "$RELEASE_COMMIT" -- "$path" | cut -d' ' -f1)
  entries+=("{\"path\":\"$path\",\"mode\":\"$mode\",\"type\":\"blob\",\"sha\":\"$blob\"}")
done < <(git diff --name-only "$sha" "$RELEASE_COMMIT")
tree=$(printf '{"base_tree":"%s","tree":[%s]}' "$(git rev-parse "$sha^{tree}")" "$(IFS=,; echo "${entries[*]}")" |
  gh api "repos/$GITHUB_REPOSITORY/git/trees" --input - --jq .sha)
if [[ "$tree" != "$(git rev-parse "$RELEASE_COMMIT^{tree}")" ]]; then
  echo "GitHub's tree $tree is not the release commit's." >&2
  exit 1
fi
commit=$(printf '{"message":"Release %s","tree":"%s","parents":["%s"]}' "$VERSION" "$tree" "$sha" |
  gh api "repos/$GITHUB_REPOSITORY/git/commits" --input - --jq .sha)
# Creating the refs explicitly fails if another process claimed this version;
# never move a tag. ddto's tag comes first: VERSION needs it.
gh api "repos/$GITHUB_REPOSITORY/git/refs" -f "ref=refs/tags/ddto/$VERSION" -f "sha=$commit"
gh api "repos/$GITHUB_REPOSITORY/git/refs" -f "ref=refs/tags/$VERSION" -f "sha=$commit"
gh release create "$VERSION" --verify-tag --target "$commit" --title "$VERSION" --draft --generate-notes dist/*
release_url=$(gh release view "$VERSION" --json url --jq .url)
{
  echo "## Draft release $VERSION"
  echo "Commit: $commit, the release commit on $sha"
  echo "Review the assets and generated notes, then publish: $release_url"
} >> "$GITHUB_STEP_SUMMARY"
