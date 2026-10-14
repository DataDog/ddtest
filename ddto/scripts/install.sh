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
  if command -v sha256sum > /dev/null; then
    sha256sum "$1" | cut -d ' ' -f 1
  elif command -v shasum > /dev/null; then
    shasum -a 256 "$1" | cut -d ' ' -f 1
  elif command -v openssl > /dev/null; then
    openssl dgst -sha256 "$1" | sed 's/.* //'
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

mkdir -p "$dir"
dir=$(cd "$dir" && pwd)
# Windows programs, such as go and later steps of a workflow, need C:\ paths,
# not the /c/ or /cygdrive/c/ forms of Git Bash and Cygwin.
native_dir=$dir
if [ "$os" = windows ] && command -v cygpath > /dev/null; then
  native_dir=$(cygpath -w "$dir")
fi
temp=$(mktemp -d)
trap 'rm -rf "$temp"' EXIT
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
    say "no SHA256SUMS at $base"
  else
    want=$(awk -v name="$asset" '$2 == name || $2 == "*" name { print $1 }' "$temp/SHA256SUMS")
  fi
  if [ -z "$want" ]; then
    say "no release executable $asset at $base"
  elif [ -n "$current" ] && [ "$(sha256 "$target" || true)" = "$want" ]; then
    installed="already installed"
  elif ! fetch "$base/$asset" "$temp/$asset"; then
    say "no release executable $asset at $base"
  else
    if ! got=$(sha256 "$temp/$asset"); then
      say "sha256sum, shasum or openssl is required to check $asset"
      exit 1
    fi
    if [ "$want" != "$got" ]; then
      say "$asset does not match SHA256SUMS"
      exit 1
    fi
    cp "$temp/$asset" "$target"
    chmod 0755 "$target"
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
  # For the host, whatever GOOS and GOARCH the project selects.
  GOOS=$(go env GOHOSTOS) GOARCH=$(go env GOHOSTARCH) GOBIN=$native_dir go install "github.com/$repo/cmd/ddto@$version"
  installed="go install @$version"
fi
"$target" --help > /dev/null 2>&1
say "$("$target" --version) in $target ($installed)"
case ":$PATH:" in
  *":$dir:"*) ;;
  *)
    if [ -n "${GITHUB_PATH:-}" ]; then
      echo "$native_dir" >> "$GITHUB_PATH"
      say "added $dir to PATH for the next steps"
    else
      say "add $dir to PATH"
    fi
    ;;
esac
