#!/bin/sh
# Install a colophon release binary for this machine.
#
# The release archives keep one fixed name per platform across every release,
# so `latest` resolves through the stable GitHub download URL: no API call, no
# access token, and no rate limit. Nothing is written before the archive's
# sha256 matches the release's own checksums file, which is fetched from the
# same download root.
#
# The way to run this file is a pipe, so it takes no arguments and reads
# nothing from stdin; the three knobs below are environment variables.
#
#   COLOPHON_VERSION      release tag to install; default: latest
#   COLOPHON_INSTALL_DIR  directory to write to; default: /usr/local/bin,
#                         or ~/.local/bin when that is not writable
#   COLOPHON_URL_BASE     download root serving the release asset names, for a
#                         mirror; default: the GitHub release download URL
#
# Example:
#
#   curl -fsSL https://raw.githubusercontent.com/brutasse/colophon/main/scripts/install.sh \
#     | COLOPHON_VERSION=v0.1.0 sh
set -eu

say() {
  printf 'install.sh: %s\n' "$*" >&2
}

die() {
  say "$*"
  exit 1
}

command -v curl >/dev/null 2>&1 || die "curl is required"

# Verifying the download needs one hex-digest tool. The three names cover GNU
# coreutils, macOS, and OpenSSL, so no install needs an extra package.
if command -v sha256sum >/dev/null 2>&1; then
  digest() { sha256sum -- "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  digest() { shasum -a 256 -- "$1" | cut -d' ' -f1; }
elif command -v openssl >/dev/null 2>&1; then
  digest() { openssl dgst -sha256 -- "$1" | awk '{print $NF}'; }
else
  die "sha256sum, shasum, or openssl is required to verify the download"
fi

# The platform pair the release is keyed on, the same GOOS/GOARCH names the
# build uses.
os=$(uname -s)
arch=$(uname -m)
# A shell running under Rosetta reports an Intel CPU on Apple Silicon hardware,
# where the arm64 release is the right one.
if [ "$os" = Darwin ] &&
  [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
  arch=arm64
fi

case $os in
  Linux) platform=linux ;;
  Darwin) platform=darwin ;;
  # Git Bash and MSYS2 on Windows: the release is a Windows .exe, and the
  # script installs it into a directory on PATH.
  MINGW* | MSYS* | CYGWIN*) platform=windows ;;
  *)
    die "no colophon release for $os; install with go install github.com/brutasse/colophon@latest"
    ;;
esac

case $arch in
  x86_64 | amd64) cpu=amd64 ;;
  aarch64 | arm64) cpu=arm64 ;;
  *)
    die "no colophon release for $arch; supported: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64"
    ;;
esac

# The archive name and the binary inside it: Windows needs the .exe suffix to
# be runnable from PATH.
ext=tar.gz
binary=colophon
if [ "$platform" = windows ]; then
  if [ "$cpu" != amd64 ]; then
    die "the Windows release is published for amd64 only, not $arch"
  fi
  ext=zip
  binary=colophon.exe
fi

version=${COLOPHON_VERSION:-latest}
base=${COLOPHON_URL_BASE:-}
if [ -z "$base" ]; then
  if [ "$version" = latest ]; then
    base=https://github.com/brutasse/colophon/releases/latest/download
  else
    # Accept a tag with or without the leading v, so a version copied from the
    # README or from `colophon version` both work.
    case $version in v*) ;; *) version=v$version ;; esac
    base=https://github.com/brutasse/colophon/releases/download/$version
  fi
fi

asset=colophon-$platform-$cpu.$ext
tmp=$(mktemp -d) || die "cannot create a temporary directory"
trap 'rm -rf "$tmp"' EXIT
# Interrupted leaves no half-written download behind: the EXIT trap still runs.
trap 'exit 1' INT TERM

fetch() {
  # -f makes a missing asset an error instead of a saved 404 page.
  curl -f -sSL --retry 3 "$1" -o "$2" || die "cannot download $1"
}

# Named first, so a reader of the piped output can see which release is going
# where, and which file the checksum below belongs to.
say "installing $asset from $base"
fetch "$base/checksums.txt" "$tmp/checksums.txt"
fetch "$base/$asset" "$tmp/$asset"

# The checksums file lists every asset of the release, one per line, and the
# archive is only ever installed after its own line matches it. A file written
# in binary mode or on Windows names the asset with a leading * or a trailing
# carriage return, so both are dropped before the comparison.
want=$(awk -v asset="$asset" '
  { name = $2; sub(/^\*/, "", name); sub(/\r$/, "", name)
    if (name == asset) { print $1; exit } }' "$tmp/checksums.txt")
[ -n "$want" ] || die "checksums.txt of $version has no entry for $asset"
have=$(digest "$tmp/$asset")
[ "$have" = "$want" ] || die "$asset checksum mismatch: got $have, expected $want"

mkdir -p "$tmp/unpacked"
if [ "$ext" = zip ]; then
  command -v unzip >/dev/null 2>&1 || die "unzip is required to install the Windows release"
  unzip -q -o "$tmp/$asset" -d "$tmp/unpacked"
else
  tar -xzf "$tmp/$asset" -C "$tmp/unpacked"
fi
[ -f "$tmp/unpacked/$binary" ] || die "$asset does not contain $binary"

# A directory the caller named must be usable; the default falls back to the
# per-user bin directory rather than failing in the middle of a piped install.
if [ -n "${COLOPHON_INSTALL_DIR:-}" ]; then
  dir=$COLOPHON_INSTALL_DIR
  mkdir -p "$dir" 2>/dev/null || die "cannot create $dir"
  [ -w "$dir" ] || die "$dir is not writable; run as root or set COLOPHON_INSTALL_DIR"
else
  dir=/usr/local/bin
  if ! mkdir -p "$dir" 2>/dev/null || [ ! -w "$dir" ]; then
    dir=${HOME:?HOME must be set}/.local/bin
    mkdir -p "$dir" || die "cannot create $dir"
    say "/usr/local/bin is not writable; installing to $dir, which must be on PATH"
  fi
fi

mv -f "$tmp/unpacked/$binary" "$dir/$binary" 2>/dev/null ||
  die "cannot write $dir/$binary"
chmod 755 "$dir/$binary"

# The version line is the only record of which release this archive came from.
if got=$("$dir/$binary" version 2>/dev/null); then
  printf 'installed %s (%s)\n' "$dir/$binary" "$got"
else
  say "installed $dir/$binary, but it did not run; check that $platform/$cpu matches this machine"
fi
