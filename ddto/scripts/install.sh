#!/usr/bin/env bash
# Installs ddto from its GitHub releases:
#
#   curl -fsSL https://github.com/tonyredondo/dd-ci-testing-poc/releases/latest/download/install.sh | bash
#   curl -fsSL .../install.sh | bash -s -- --version v1.2.3 --dir /usr/local/bin
#
# It downloads the release executable for this host and checks it against the
# release's SHA256SUMS. Without an executable for the host, or when the
# download fails, it builds ddto with go install instead. A checksum mismatch
# stops it. An installed executable of the same version stays: a requested
# version needs no download, and latest only needs SHA256SUMS. In GitHub
# Actions it adds the directory to PATH for later steps.
#
# Settings, as options or environment variables:
#   --version, DDTO_VERSION       release tag, or latest (the default)
#   --dir, DDTO_INSTALL_DIR       destination directory ($HOME/.local/bin)
#   DDTO_DOWNLOAD_URL             base URL of a mirror of the release's files
set -euo pipefail

repo=tonyredondo/dd-ci-testing-poc
version=${DDTO_VERSION:-latest}
dir=${DDTO_INSTALL_DIR:-$HOME/.local/bin}
while [ $# -gt 0 ]; do
  case $1 in
    --version) version=${2:?--version needs a value}; shift 2 ;;
    --dir) dir=${2:?--dir needs a value}; shift 2 ;;
    *) echo "ddto install: unknown option $1" >&2; exit 2 ;;
  esac
done

say() { echo "ddto install: $*" >&2; }

fetch() { # url file
  if command -v curl > /dev/null; then
    curl -fsSL --retry 3 -o "$2" "$1"
  elif command -v wget > /dev/null; then
    wget -q -O "$2" "$1"
  else
    say "curl or wget is required"
    return 1
  fi
}

sha256() { # file
  # From standard input: sha256sum and shasum prefix the digest with a
  # backslash for a file name that has one.
  if command -v sha256sum > /dev/null; then
    sha256sum < "$1" | cut -d ' ' -f 1
  elif command -v shasum > /dev/null; then
    shasum -a 256 < "$1" | cut -d ' ' -f 1
  elif command -v openssl > /dev/null; then
    openssl dgst -sha256 < "$1" | sed 's/.* //'
  else
    return 1
  fi
}

case $(uname -s) in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  MINGW* | MSYS* | CYGWIN*) os=windows ;;
  *) os= ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) arch= ;;
esac
exe=
if [ "$os" = windows ]; then exe=.exe; fi
if [ "$version" = latest ]; then
  base=https://github.com/$repo/releases/latest/download
else
  base=https://github.com/$repo/releases/download/$version
fi
base=${DDTO_DOWNLOAD_URL:-$base}
# Messages show the URL without the user information or query that a
# mirror's URL can carry.
shown=$(printf '%s\n' "$base" | sed -e 's|^\([A-Za-z][A-Za-z0-9+.-]*://\)[^/@]*@|\1|' -e 's|[?#].*||')

mkdir -p "$dir"
dir=$(cd "$dir" && pwd)
# Windows programs, such as go and later steps of a workflow, need C:\ paths,
# not the /c/ or /cygdrive/c/ forms of Git Bash and Cygwin.
native_dir=$dir
if [ "$os" = windows ] && command -v cygpath > /dev/null; then
  native_dir=$(cygpath -w "$dir")
fi
temp=$(mktemp -d)
# Also the partial copy beside the target, after a signal during publish.
partial=
trap 'rm -rf "$temp" ${partial:+"$partial"}' EXIT
# A relative TMPDIR gives a relative name, which go install refuses for GOBIN.
temp=$(cd "$temp" && pwd)

publish() { # file
  # Beside the target, in a file that mktemp creates, then renamed over it: a
  # copy that fails, as on a full disk, keeps the installed ddto.
  if ! partial=$(mktemp "$target.tmp.XXXXXX"); then
    say "cannot write in $dir"
    exit 1
  fi
  if ! { cp "$1" "$partial" && chmod 0755 "$partial" && mv -f "$partial" "$target"; }; then
    rm -f "$partial"
    say "cannot write $target"
    exit 1
  fi
}
target=$dir/ddto$exe
current=
if [ -x "$target" ]; then
  current=$("$target" --version 2> /dev/null | awk '{ print $2 }' || true)
fi
installed=
# On a persistent runner, keep an installed executable of the same version: a
# requested version needs no download, and latest only needs SHA256SUMS.
if [ "$version" != latest ] && [ "$current" = "$version" ]; then
  installed="already installed"
elif [ -n "$os" ] && [ -n "$arch" ]; then
  asset=ddto-$os-$arch$exe
  want=
  if ! fetch "$base/SHA256SUMS" "$temp/SHA256SUMS"; then
    say "no SHA256SUMS at $shown"
  else
    want=$(awk -v name="$asset" '$2 == name || $2 == "*" name { print $1 }' "$temp/SHA256SUMS")
  fi
  if [ -z "$want" ]; then
    say "no release executable $asset at $shown"
  elif [ -n "$current" ] && [ "$(sha256 "$target" || true)" = "$want" ]; then
    installed="already installed"
  elif ! fetch "$base/$asset" "$temp/$asset"; then
    say "no release executable $asset at $shown"
  else
    if ! got=$(sha256 "$temp/$asset"); then
      say "sha256sum, shasum or openssl is required to check $asset"
      exit 1
    fi
    if [ "$want" != "$got" ]; then
      say "$asset does not match SHA256SUMS"
      exit 1
    fi
    publish "$temp/$asset"
    installed="release $version"
  fi
else
  say "no release executable for $(uname -s) $(uname -m)"
fi
if [ -z "$installed" ]; then
  if ! command -v go > /dev/null; then
    say "install Go 1.25 or newer to build ddto, or download it from https://github.com/$repo/releases"
    exit 1
  fi
  say "building ddto with go install"
  # For the host, whatever GOOS and GOARCH the project selects. go install
  # replaces its destination in place, so it builds in the temporary
  # directory and publish moves the result over the target.
  mkdir "$temp/bin"
  native_bin=$temp/bin
  if [ "$os" = windows ] && command -v cygpath > /dev/null; then
    native_bin=$(cygpath -w "$temp/bin")
  fi
  GOOS=$(go env GOHOSTOS) GOARCH=$(go env GOHOSTARCH) GOBIN=$native_bin go install "github.com/$repo/cmd/ddto@$version"
  # The project's settings, such as GOAMD64, can build for another CPU: keep
  # the installed ddto unless the new one runs here.
  if ! "$temp/bin/ddto$exe" --version > /dev/null 2>&1; then
    say "the ddto that go install built does not run on this host"
    exit 1
  fi
  publish "$temp/bin/ddto$exe"
  installed="go install @$version"
fi
"$target" --help > /dev/null 2>&1
say "$("$target" --version) in $target ($installed)"
# A step's own PATH does not reach the next steps: GitHub Actions adds what
# GITHUB_PATH lists, even when this step's PATH already has the directory.
if [ -n "${GITHUB_PATH:-}" ]; then
  echo "$native_dir" >> "$GITHUB_PATH"
  say "added $dir to PATH for the next steps"
else
  case ":$PATH:" in
    *":$dir:"*) ;;
    *) say "add $dir to PATH" ;;
  esac
fi
