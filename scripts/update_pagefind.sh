#!/usr/bin/env bash
# Print the pinned release sums for pagefindReleases in pagefind.go.
#
# colophon downloads Pagefind at site build time and checks it against these
# sums, so bumping the indexer means editing pagefind.go, not vendoring a
# binary. Usage: scripts/update_pagefind.sh v1.5.3
#
# Paste the map over the one in pagefind.go and set pagefindVersion in the
# same file to the same tag without the leading v.
set -euo pipefail
v="${1:?usage: update_pagefind.sh vX.Y.Z}"

# GOOS/GOARCH pairs, matching what pagefind.go keys the map on.
platforms=(
  "linux/amd64 x86_64-unknown-linux-musl"
  "linux/arm64 aarch64-unknown-linux-musl"
  "darwin/amd64 x86_64-apple-darwin"
  "darwin/arm64 aarch64-apple-darwin"
  "windows/amd64 x86_64-pc-windows-msvc"
  "windows/arm64 aarch64-pc-windows-msvc"
  "freebsd/amd64 x86_64-unknown-freebsd"
)

echo 'var pagefindReleases = map[string]pagefindRelease{'
for p in "${platforms[@]}"; do
  go="${p%% *}"
  triple="${p##* }"
  url="https://github.com/Pagefind/pagefind/releases/download/$v/pagefind-$v-$triple.tar.gz"
  sum=$(curl -fsSL --retry 3 "$url.sha256" | awk '{print $1}')
  [[ "$sum" =~ ^[0-9a-f]{64}$ ]] || { echo "no sha256 for $url.sha256" >&2; exit 1; }
  # The release page also has to serve the tarball, or the build breaks later.
  curl -fsSL --retry 3 -o /dev/null -r 0-0 "$url" || { echo "missing asset $url" >&2; exit 1; }
  printf '\t"%s": {"%s", "%s"},\n' "$go" "$triple" "$sum"
done
echo '}'
echo "# pagefind.go: pagefindVersion = ${v#v}" >&2
