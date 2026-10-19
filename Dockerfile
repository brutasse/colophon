# syntax=docker/dockerfile:1
# colophon + its search indexer, ready to build a docs site from.
#
# The indexer cache inside is the same release colophon would download, and
# the same checksum is verified here. Neither build stage runs an emulator: Go
# cross-compiles natively, and the indexer stage only downloads, checks, and
# extracts files.

ARG GO_VERSION=1.27
ARG ALPINE_VERSION=3.22
ARG PAGEFIND_SOURCE=https://github.com/Pagefind/pagefind/releases/download

# ---------------------------------------------------------------- build
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
      go build -trimpath -ldflags "-s -w" -o /out/colophon .

# -------------------------------------------------------------- indexer
# Placed at the cache path colophon looks in, keyed by version and platform,
# so `colophon build` from this image needs no network. Nothing is hardcoded
# here: the version, the release target, and the checksum all come from
# pagefind.go through scripts/pagefind_pin.sh. The stage runs on the build
# machine and fetches the *target* platform's release, so the emulator stays
# out of the whole build: download, checksum, and extract are file operations.
FROM --platform=$BUILDPLATFORM alpine:${ALPINE_VERSION} AS indexer
ARG TARGETOS TARGETARCH PAGEFIND_SOURCE
COPY pagefind.go /pagefind.go
COPY scripts/pagefind_pin.sh /scripts/pagefind_pin.sh
COPY assets/PAGEFIND-LICENSE.txt /PAGEFIND-LICENSE.txt
RUN set -eu; \
    platform="$TARGETOS/$TARGETARCH"; \
    pin=/scripts/pagefind_pin.sh; \
    version=$("$pin" "$platform" version); \
    triple=$("$pin" "$platform" triple); \
    sum=$("$pin" "$platform" sha256); \
    dir=$("$pin" "$platform" dir /opt/colophon-cache); \
    mkdir -p "$dir"; \
    wget -qO /pagefind.tgz "$PAGEFIND_SOURCE/v$version/pagefind-v$version-$triple.tar.gz"; \
    echo "$sum  /pagefind.tgz" | sha256sum -c -; \
    tar xzf /pagefind.tgz -C "$dir"; \
    chmod 755 "$dir"/*; \
    cp /PAGEFIND-LICENSE.txt "$dir/LICENSE.txt"; \
    chown -R 65532:65532 /opt/colophon-cache

# ---------------------------------------------------------------- final
FROM alpine:${ALPINE_VERSION}
ARG PAGEFIND_VERSION=unknown
LABEL org.opencontainers.image.title="colophon" \
      org.opencontainers.image.description="Documentation site builder with a cached Pagefind indexer" \
      org.opencontainers.image.source="https://github.com/brutasse/colophon" \
      org.opencontainers.image.licenses="MIT" \
      io.colophon.pagefind-version="${PAGEFIND_VERSION}"
COPY LICENSE /licenses/colophon-LICENSE.txt
COPY assets/MERMAID-LICENSE.txt assets/MATHJAX-LICENSE.txt assets/PAGEFIND-LICENSE.txt /licenses/
COPY --from=build /out/colophon /usr/local/bin/
# 65532 is the unprivileged user CI systems and derived images tend to run as,
# and a writable cache directory is what lets colophon re-fetch an indexer it
# finds damaged.
COPY --from=indexer --chown=65532:65532 /opt/colophon-cache /opt/colophon-cache
ENV XDG_CACHE_HOME=/opt/colophon-cache
WORKDIR /site
ENTRYPOINT ["colophon"]
CMD ["--help"]
