#!/usr/bin/env bash
# Makes the release commit of VERSION on the checked-out commit and prints its
# SHA. go install github.com/DataDog/ddtest@VERSION builds that commit: ddto,
# the module in ddto/, reports VERSION, and go.mod requires it at VERSION,
# which tags ddto/VERSION, instead of replacing it with ./ddto. release.sh
# creates the commit in GitHub and tags both VERSION and ddto/VERSION on it.
#
# Nothing leaves the checkout: the commit is made in a temporary worktree, and
# go install is checked against this repository, whose tags Go reads through
# local tags that are removed afterwards.
set -euo pipefail

if [[ ! "${VERSION:-}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo 'Version must have the form vMAJOR.MINOR.PATCH without leading zeroes.' >&2
  exit 1
fi

module=github.com/DataDog/ddtest
repo=$(git rev-parse --show-toplevel)
for tag in "$VERSION" "ddto/$VERSION"; do
  if git -C "$repo" rev-parse -q --verify "refs/tags/$tag" > /dev/null; then
    echo "Tag $tag already exists in the checkout." >&2
    exit 1
  fi
done

work=$(mktemp -d)
tree="$work/tree"
cleanup() {
  git -C "$repo" update-ref -d "refs/tags/$VERSION" 2> /dev/null || true
  git -C "$repo" update-ref -d "refs/tags/ddto/$VERSION" 2> /dev/null || true
  git -C "$repo" worktree remove --force "$tree" 2> /dev/null || true
  chmod -R u+w "$work" 2> /dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

export GIT_AUTHOR_NAME='ddtest release' GIT_AUTHOR_EMAIL='ddtest-release@users.noreply.github.com'
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME" GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"
# Go fetches this module's versions from this repository, with the local
# tags, and the others through the module cache and the proxy. Neither the
# proxy nor the checksum database knows the new versions yet.
cache=$(go env GOMODCACHE)
export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0="url.$repo.insteadOf" GIT_CONFIG_VALUE_0="https://$module"
export GOPROXY="file://$cache/cache/download,https://proxy.golang.org,direct" GONOPROXY="$module" GONOSUMDB="$module"
export GOFLAGS= GOWORK=off

git -C "$repo" worktree add -q --detach "$tree" HEAD
go -C "$tree/ddto" run ./scripts/release -version "$VERSION" -stamp
# Local commits, unsigned whatever the configuration: GitHub makes the one
# that the release tags.
git -C "$tree" -c commit.gpgSign=false commit -q -am "ddto $VERSION"
# go get records ddto's checksums from the tag; ddto/ is the same in the
# release commit, which only changes go.mod and go.sum beside it.
git -C "$repo" update-ref "refs/tags/ddto/$VERSION" "$(git -C "$tree" rev-parse HEAD)"
go -C "$tree" mod edit -dropreplace="$module/ddto" -require="$module/ddto@$VERSION"
GOMODCACHE="$work/sum-cache" go -C "$tree" get "$module/ddto@$VERSION"
git -C "$tree" -c commit.gpgSign=false commit -q --amend -am "Release $VERSION"
commit=$(git -C "$tree" rev-parse HEAD)

# A fresh module cache downloads both modules from the release commit's tags
# and checks ddto against the checksums in its go.sum.
git -C "$repo" update-ref "refs/tags/ddto/$VERSION" "$commit"
git -C "$repo" update-ref "refs/tags/$VERSION" "$commit"
GOMODCACHE="$work/install-cache" GOBIN="$work/bin" go install "$module@$VERSION"
ddtest="$work/bin/ddtest"
if [[ "$("$ddtest" --version)" != "$VERSION" || "$("$ddtest" instrument --version)" != "ddtest instrument $VERSION" ]]; then
  echo "go install $module@$VERSION does not report $VERSION." >&2
  exit 1
fi
echo "$commit"
