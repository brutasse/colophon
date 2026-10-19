#!/bin/sh
# Print the Pagefind release pinned for one platform, as recorded by the
# pagefindReleases table in pagefind.go.
#
# colophon downloads that release at site build time, and the container image
# stages the same file into its cache, so both read one pin file rather than
# carrying a copy of it. A Go test checks this script against the table.
#
#   scripts/pagefind_pin.sh linux/amd64            # release triple
#   scripts/pagefind_pin.sh linux/amd64 sha256     # of the release tarball
#   scripts/pagefind_pin.sh linux/amd64 version    # pinned pagefindVersion
#   scripts/pagefind_pin.sh linux/amd64 dir [root] # cache dir colophon reads
set -eu

src=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)/pagefind.go
platform=${1:?usage: pagefind_pin.sh GOOS/GOARCH [triple|sha256|version|dir]}
field=${2:-triple}
root=${3:-/opt/colophon-cache}

version=$(awk -F'"' '/^const pagefindVersion/ {print $2; exit}' "$src")
[ -n "$version" ] || { echo "pagefind_pin.sh: no pagefindVersion in $src" >&2; exit 1; }

if [ "$field" = version ]; then
  echo "$version"
  exit 0
fi

# A table row reads: "linux/amd64":   {"x86_64-unknown-linux-musl", "<64 hex>"},
row=$(awk -F'"' -v k="$platform" '$2 == k { print $4 " " $6; exit }' "$src")
[ -n "$row" ] || { echo "pagefind_pin.sh: no pinned release for $platform" >&2; exit 1; }
triple=${row% *}
sha256=${row#* }

case $field in
  triple) echo "$triple" ;;
  sha256) echo "$sha256" ;;
  dir)    printf '%s/colophon/pagefind-%s-%s\n' "$root" "$version" "$triple" ;;
  *)      echo "pagefind_pin.sh: unknown field $field" >&2; exit 1 ;;
esac
