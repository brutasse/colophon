# colophon

A documentation site builder. One Go binary turns a Markdown corpus into a
static HTML site with sidebar navigation, breadcrumbs, prev/next links, a
right-hand "On this page" rail, and full-text search. It renders with
[petroglyph](https://github.com/brutasse/petroglyph), ships its stylesheet,
[Mermaid](https://mermaid.js.org), and
[MathJax](https://www.mathjax.org) inside the binary, and downloads the
[Pagefind](https://pagefind.app) indexer for your platform on the first build
that needs it. The binary is all you need to build a site. Once colophon has
cached the indexer, the whole build works offline.

## Features

- Renders a directory tree of Markdown into the same tree of HTML, and keeps
  every URL relative so the corpus stays browsable and editable on GitHub.
- Reads plain GitHub Flavored Markdown: alerts, tables, fenced code, Mermaid,
  and math need no special syntax.
- Builds navigation from the `nav: list` pages, then derives breadcrumbs, the
  sidebar, and prev/next links from that one tree.
- Gates the build on a link check: broken links, dead anchors, unreachable
  pages, and link forms that break GitHub navigation all fail `build --check`.
- Ships the search index: Pagefind runs after the render, so search works on a
  static site with no backend.
- Installs as one binary on Linux, macOS, and Windows, verifies each download
  against a published checksum, and updates itself with `colophon update`.
- Publishes a multi-arch container base image with that indexer already cached,
  so a site build in CI needs no network.
- Serves a dev loop that rebuilds one page per save and reloads the browser.
- Sanitizes raw HTML on every page with petroglyph's display policy.
- Loads Mermaid and MathJax only on pages that use them.

## Install

```
curl -fsSL https://raw.githubusercontent.com/brutasse/colophon/main/scripts/install.sh | sh
```

The script picks the release for your operating system and CPU, checks it
against the release checksums file, and installs it in `/usr/local/bin`. When
that directory is not writable, it installs in `~/.local/bin` and prints the
`PATH` entry you need. Run `colophon version` to see which release you
installed.

Three environment variables change those defaults: `COLOPHON_VERSION` names a
release tag, `COLOPHON_INSTALL_DIR` names the directory, and
`COLOPHON_URL_BASE` names a mirror that serves the same file names. Assign them
to the shell that runs the script, because a pipe passes only the script text:

```
curl -fsSL https://raw.githubusercontent.com/brutasse/colophon/main/scripts/install.sh \
  | COLOPHON_VERSION=v0.1.0 COLOPHON_INSTALL_DIR="$HOME/bin" sh
```

Release binaries exist for `linux/amd64`, `linux/arm64`, `darwin/amd64`,
`darwin/arm64`, and `windows/amd64`. The
[releases](https://github.com/brutasse/colophon/releases) page lists every
archive and its checksum. On Windows, run the same line in Git Bash, which
supplies `curl` and `unzip`. Install with Go on another platform, or when you
build from this repository, which needs Go 1.27.2 or later:

```
go install github.com/brutasse/colophon@latest
```

### Staying current

```
colophon update
```

The command asks the GitHub releases API which release is newest. It downloads
the archive for your platform and checks it against the checksum GitHub
publishes for that asset. Then it moves the new binary into place.
`colophon update --check` reports what is available and installs nothing.
`colophon update --to 1.4.0` names a release, which is also how you go back to
an older one. The directory holding the binary must be writable, so an install
in `/usr/local/bin` normally needs an administrator.

Every other command prints one line when a newer release exists. That check runs
at most once a day, waits at most two seconds, and prints nothing when it fails.
It never delays or fails a build. Colophon skips it in CI and when stderr is a
pipe. A source build never asks, and neither does a run with
`COLOPHON_NO_UPDATE_CHECK` or `--no-update-check` set.

GitHub allows 60 anonymous API requests an hour per address. Set `GITHUB_TOKEN`
or `GH_TOKEN` to raise that limit on a shared build host.
`COLOPHON_RELEASES_API` names a different endpoint, for GitHub Enterprise or an
internal mirror.

Two installs have another owner. Go owns a binary that
`go install github.com/brutasse/colophon@latest` placed in your Go bin
directory, and `colophon update` refuses a source build for the same reason:
rebuild those. The container image sets `COLOPHON_NO_UPDATE_CHECK=1`, because
its version is its image tag and you move to a newer one by rebuilding.

## Quick start

A build runs from `colophon.conf`, which holds the site settings and names the
directory holding the pages:

```
colophon.conf       # source: source
source/
  index.md          # the root page: its nav: list defines the navigation
  guide/
    index.md
    install.md
```

Then run, in the directory holding that file:

```
colophon build                       # render into _build/html
colophon build --check               # the same build, plus the CI gate
colophon serve                       # http://127.0.0.1:8000 with live reload
```

Without a `colophon.conf` to read, `build` and `serve` stop and say so: nothing
is guessed about where your pages are. `colophon version` and
`colophon update` read no config, so they run anywhere. From another directory,
name the file: `colophon build --config docs/colophon.conf --out site`.

## Site config

`colophon.conf` holds one `key: value` pair per line. Lines that start with
`#` are comments. Every key but `source` has a default, and colophon ignores
a key it does not know, which keeps a config shared with another tool usable.

| Key | Default | Meaning |
| --- | --- | --- |
| `source` | none | Directory holding the Markdown corpus; required, resolved against this file |
| `title` | `Docs` | Site name in the `<title>` tag and the sidebar brand |
| `home-label` | `Docs` | Text of the first breadcrumb |
| `edit-base` | none | Base URL of the "Edit this page" link; no key means no link |
| `base-url` | none | Public URL of this site; links to it are reported as non-portable |
| `copyright` | none | Footer line; `{year}` becomes the build year |
| `logo` | none | File under the source directory, served at the same root-relative path |
| `favicon` | none | File under the source directory, served at the same root-relative path |
| `accent` | theme blue | Hex colour override for the light theme |
| `accent-dark` | theme blue | Hex colour override for the dark theme |
| `tags` | none | Comma-separated `name=dir` pairs of gated subtrees |
| `iframe-hosts` | none | Comma-separated domains whose `<iframe>` embeds keep their `src` |

An invalid value fails the build instead of shipping a broken page or a looser
sanitizer. Colophon rejects a `source` directory that is not there, a `logo` or
`favicon` file that is not under it, an `accent` that is not a hex colour, and
an `iframe-hosts` entry that is not a domain.

### Gated subtrees

A gated subtree holds content that another repository owns, so its link
problems never fail your build. Colophon excludes it from the build unless you
pass its tag: `--tags internal` builds the directory that `tags` maps to
`internal`. The `tags` key is what declares a subtree, so a site with no `tags`
key gates nothing.

## Pages

Pages carry optional front matter between `---` lines:

| Key | Meaning |
| --- | --- |
| `title` | Page title; the first heading is the default |
| `toc` | `true` or a depth: insert a table of contents after the first `<h1>` |
| `math` | `true`: this page uses math, so ship MathJax |
| `nav` | `list`: the top-level bullet list of this page is its child navigation |
| `fragment` | `true`: the file is inlined by include comments and never built as a page; implies `orphan` |
| `orphan` | `true`: nothing links to this page on purpose, so the check gate accepts it |

Colophon ignores every other key, which keeps front matter copied from another
generator harmless.

Navigation comes from those lists and from headings. The pages in a page's
top-level bullet list are its children, and `index.md` is the root of the tree. A
page with no list has no page children. Colophon reports a page that is outside
the tree and that no other page links to: it builds, but no reader reaches it.
`build --check` fails on it, and `orphan: true` says a page stays unlinked on
purpose.

A page also parents its own top-level sections: every `#` heading below its title
is a child of it in the tree. That is what a file holding several documents glued
into one page needs, because one page title would otherwise be the only row for
the whole file; each glued document gets a sidebar row and a rail entry, while
`##` and deeper stay in the "On this page" rail alone. Heading levels decide it, so
nothing declares it, and the rows point at anchors the page already had.

On the page that lists such a child, the list gains those sections as a sub-list
under its bullet, so an index of glued files reads as the documents inside it
instead of one line each. Nothing to write for that either, and a list whose items
the author already annotated with sub-bullets stays exactly as written, because a
list like that is prose and not the child navigation.

An item may end its link in `#all` — `[Numbers allocation](numbers_allocation.md#all)`
— to say that every h1 of that file is a document, including the heading the page
would take its title from, which is what a set of documents glued into one file is.
Give the item the name of the file, and the page its own name with `title:`, so the
bullet, the sidebar row and the tab all say what the file is and every document in
it is listed under it. Colophon drops the marker from the rendered link, and the
page itself keeps one row in the navigation.

### Links

Write a link to another page as a file-relative `.md` path, with an optional
`#fragment`: `[install](install.md#linux)`. The builder rewrites it to `.html`
at render time. The same file on GitHub then navigates to the source page. Link
to an asset with a file-relative path too, never a root-absolute one.

### Fragments

A file whose front matter declares `fragment: true` is a fragment: colophon
inlines it into the pages that include it and never builds it as a page. Being a
fragment implies `orphan: true`, so you do not write both. The include comment is
invisible on GitHub:

```markdown
<!--colophon:include steps.md-->
```

Options may follow the path. Colophon strips front matter from a Markdown
fragment and inlines the rest as prose; `literal`, or `language=` naming a
fence, ships a file as highlighted code instead:

```markdown
<!--colophon:include timeout.yaml language=yaml-->
```

Colophon reports a malformed or unknown option in the page as
`include unavailable`, so a typo cannot silently drop content.

A Markdown file that an include comment inlines must declare `fragment: true`.
Without it, colophon also builds the file as a page, so the same prose appears at
two URLs, and `build --check` fails on it.

### Alerts, code, and diagrams

Notes and warnings are GitHub alerts, and a bold first line gives the alert a
title:

```markdown
> [!WARNING]
> **Restart required**
>
> Restart the node.
```

Everything else is plain GitHub Flavored Markdown. Colophon highlights a fenced
code block that names a language, and turns a `mermaid` fence into a diagram. A
diagram renders fitted to the page width inside a scroll box, with a chip over it
to zoom in, zoom out, reset, and expand, and drags to pan; a wide diagram stays
readable that way instead of shrinking to unreadable text, and a page without
JavaScript still shows it. Expand moves the box onto the full screen at its
current size, and Escape or the same control puts it back. `$...$` math needs
the `math: true` front matter, which ships MathJax with the page. To keep an old
link target alive when you move a heading, write the anchor as raw HTML:

```markdown
<div id="upgrade-notes"></div>
```

## The check gate

`colophon build` renders, writes, and reports link problems as warnings, then
exits 0: a broken link should not stop you from reading the site.

`colophon build --check` turns that report into a gate and exits 1 on:

- a link to a page that no build produces,
- a link to an anchor that the target page does not define,
- a page link that uses `.html`, a root-absolute path, or no extension,
- an absolute link to this same site,
- a page that no `nav: list` contains and that no other page links to,
- a file that an include comment inlines but that does not declare
  `fragment: true`,
- a page with math that does not declare `math: true`,
- a `nav: list` item that links to a section of a page (`[text](page.md#anchor)`).
  Children come only from items that are one plain page link, so that one item
  quietly leaves the entire list, and every page in it, out of the navigation. The
  one accepted fragment is `#all`, which asks for every section of the page, the
  title heading included, under the item.

For a missing anchor, the report can also print the rewrite it suggests, where
the fragment differs only by dash collapsing (`old --- anchor` to the slug the
renderer builds).

## Search

The binary drives the Pagefind indexer, so `colophon build` renders the
pages, indexes them into `_build/html/pagefind/`, and only then writes the
search markup into the pages. A page that never gets a bundle ships no search
UI, and a failing indexer is a warning, not a failed build.

Pagefind is a platform binary, so colophon does not carry one. The first build
downloads the release for your platform, checks it against a checksum pinned
in the source, and caches it under the user cache directory
(`~/.cache/colophon/`, `~/Library/Caches/colophon/` on macOS) for every build
after that. Nothing is fetched again until the pinned release changes.

Two ways to keep search without reaching for GitHub:

```
COLOPHON_PAGEFIND=/opt/mirrors/pagefind colophon build  # run this binary instead
cp -r ~/.cache/colophon <build-host>                    # move the cache itself
```

Copying the cache only helps a machine with the same OS and CPU, since the
binary inside it is a platform binary.

A platform with no Pagefind release and a network that will not answer both
say so and build the site without search. A damaged cache entry is thrown away
and fetched again. Run `scripts/update_pagefind.sh v1.5.3` to move to a newer
release; it prints the pinned checksums for `pagefind.go`.

## Container image

`ghcr.io/brutasse/colophon` is a small Alpine image holding `colophon` and the
Pagefind release for its own architecture at the cache path colophon reads.
Nothing is downloaded when you build from it, so it works behind a network
policy that blocks releases-hosting sites:

```
FROM ghcr.io/brutasse/colophon:v0.1.0
COPY docs/ /site/          # colophon.conf plus the source directory it names
RUN colophon build --check --out _site
```

Or run it against a checkout:

```
docker run --rm -v "$PWD:/site" -w /site ghcr.io/brutasse/colophon build --check
```

The image is built for `linux/amd64` and `linux/arm64`, which is as far as
Pagefind publishes releases; `io.colophon.pagefind-version` labels which
indexer went in. The image tag follows the git ref: `latest` and `sha-<sha>`
on `main`, and the tag name on a `v*` tag. A `FROM` line and the release tag
are then the same string. The version, release target, and checksum come from
`pagefind.go` through `scripts/pagefind_pin.sh`, so bumping the indexer means
editing that one table and rebuilding the image. The image can never ship an
indexer its own `colophon` would reject.

The image runs as root, which suits a build stage; pass `--user` or a `USER`
line to run unprivileged, which still reads the cached indexer.

## Dev server

`colophon serve` watches the source directory and keeps the site in memory:

- a page rebuild takes milliseconds, and the browser reloads over Server-Sent
  Events,
- a fragment change or a navigation change rebuilds the whole site,
- the checker reports the problems of each saved file on the terminal,
- Pagefind indexes a mirror of the pages in the background, so a reload never
  waits on search.

## Development

```
go build ./...                  # compile check
go build -o colophon .          # the builder
go test ./...                   # renders testdata/site and testdata/broken
gofmt -l .
docker buildx build --platform linux/amd64 --load -t colophon:local .
```

`testdata/site` is a small corpus that uses every feature above, and
`build_test.go` asserts on the rendered HTML. `testdata/broken` holds one
problem per check rule, so a rule that stops firing fails a test. Three
workflows run:

- `ci.yml` builds the fixture with `colophon build --check` on linux and on
  macOS, which runs the CLI, the check gate, and the indexer download end to
  end,
- `image.yml` builds the container image for both architectures, pushes it on
  `main` and on tags, and then builds the fixture from the pushed image with
  `--network=none`, so a cache path that stops matching colophon fails CI
  rather than quietly shipping search-less sites,
- `release.yml` builds the five release archives on a `v*` tag and publishes
  them as release assets. Its install job then installs one on linux and on
  macOS through `scripts/install.sh` and builds the fixture from that binary. A
  wrong asset name or a broken installer therefore fails CI rather than a reader
  who copied the install line.

`ci.yml` also installs a locally packed archive through the same script, so a
broken installer fails its own pull request rather than a tag push.

The release archives and the installer are both runnable from a checkout. Build
the binaries with `-ldflags "-X main.version=<version>"` and pack them as
`colophon-<os>-<arch>.tar.gz`. Put a `checksums.txt` beside them and point
`COLOPHON_URL_BASE` at that directory with a `file://` URL.

The first push to ghcr.io creates the package as private: flip it to public in
the repository's package settings before the image is usable from another
repository.

## Vendored assets

| File | Upstream | Licence |
| --- | --- | --- |
| `assets/vendor/mermaid.min.js` | [Mermaid](https://mermaid.js.org) | MIT (`assets/MERMAID-LICENSE.txt`) |
| `assets/vendor/tex-svg.js` | [MathJax](https://www.mathjax.org) | MIT (`assets/MATHJAX-LICENSE.txt`) |

The Pagefind indexer is not vendored; colophon keeps
`assets/PAGEFIND-LICENSE.txt` and writes it into the download cache beside the
binary it describes.

`assets/docs.css` and `assets/chroma.css` are part of this project. This
repository commits the two vendored files, so `go build` needs no download
step, and `go test` needs no network either: the indexer is fetched by
`colophon build`, not by the toolchain.

## Licence

MIT. See [LICENSE](LICENSE).
