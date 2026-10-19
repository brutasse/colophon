package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildFixture renders a testdata site into a temp dir the way `colophon
// build` does — pages, embedded assets, statics — minus the search index,
// which is exercised by the end-to-end CI run rather than here.
func buildFixture(t *testing.T, fixture, tags string) (string, *Site) {
	t.Helper()
	dir := filepath.Join("testdata", fixture)
	s, err := NewSite(filepath.Join(dir, "source"), tags, dir)
	if err != nil {
		t.Fatalf("NewSite(%s): %v", fixture, err)
	}
	if err := s.BuildAll(); err != nil {
		t.Fatalf("BuildAll(%s): %v", fixture, err)
	}
	out := t.TempDir()
	if err := s.WriteOut(out); err != nil {
		t.Fatalf("WriteOut(%s): %v", fixture, err)
	}
	return out, s
}

func readPage(t *testing.T, out, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func exists(out, rel string) bool {
	_, err := os.Stat(filepath.Join(out, filepath.FromSlash(rel)))
	return err == nil
}

func want(t *testing.T, page, html string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(html, n) {
			t.Errorf("%s: missing %q", page, n)
		}
	}
}

func wantNot(t *testing.T, page, html string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if strings.Contains(html, n) {
			t.Errorf("%s: unexpected %q", page, n)
		}
	}
}

// TestBuildClean is the smoke test: every documented construct in
// testdata/site renders, and the link gate stays silent about it.
func TestBuildClean(t *testing.T) {
	out, s := buildFixture(t, "site", "")

	// Fragment files are declared by front matter, inlined into the pages that
	// include them, and the tag-gated subtree is excluded until its tag is
	// passed: 5 pages, not 7.
	if len(s.Pages) != 5 {
		t.Errorf("built %d pages, want 5 (%v)", len(s.Pages), rels(s))
	}
	if exists(out, "guide/basics.html") {
		t.Error("fragment page was built standalone")
	}
	if exists(out, "gated/index.html") {
		t.Error("tag-gated page was built without its tag")
	}

	index := readPage(t, out, "index.html")
	want(t, "index.html", index,
		"<title>Test docs home · Colophon test site</title>",
		`<img src="/_static/logo.svg" alt="Colophon test site">`,
		`<link rel="icon" href="/_static/logo.svg">`,
		`--accent: #0b7285;`,
		`<a href="/guide/index.html">Guide</a>`, // sidebar
		`href="guide/first.html"`,               // .md link rewritten to .html
		`© 2024-`,                               // {year} substituted in copyright
		`<a href="https://example.com/edit/main/testdata/site/source/index.md">Edit this page</a>`,
	)
	wantNot(t, "index.html", index, "pagefind-modal", `href="guide/first.md"`)
	// The gated subtree is listed in the home nav but is not built without its
	// tag, so it must stay out of the sidebar too.
	wantNot(t, "index.html", index, `<a href="/gated/index.html"`)

	guide := readPage(t, out, "guide/index.html")
	want(t, "guide/index.html", guide,
		`<title>Guide · Colophon test site</title>`, // title from the h1
		`<nav class="page-toc">`,                    // toc: true
		`<nav class="toc" aria-label="On this page">`,
		`<a href="/guide/first.html">`, // nav: list children
		`<a href="/guide/second.html">`,
		`<span class="here">Guide</span>`, // breadcrumbs end on the page itself
		`<a class="next" href="/guide/first.html">`,
	)

	first := readPage(t, out, "guide/first.html")
	want(t, "guide/first.html", first,
		"keep steps",      // fragment content, inlined
		"request_timeout", // config fragment, fenced as yaml
		`markdown-alert-note`,
		`<strong>Shared warning</strong>`,
		`class="chroma"`, // highlighted fence
		`<div class="table-wrap"><table>`,
		`<div id="my-target"></div>`, // raw HTML keeps a legacy anchor
		`src="../_static/logo.svg"`,  // asset links stay file-relative
		`href="#target-section"`,
		`<a class="prev" href="/guide/index.html">`,
	)
	wantNot(t, "guide/first.html", first,
		"colophon:include", // every include comment was consumed, fenced fragment included
	)

	second := readPage(t, out, "guide/second.html")
	want(t, "guide/second.html", second,
		`/_assets/vendor/tex-svg.js`, // math: true ships MathJax
		`<pre class="mermaid">`,
		`/_assets/vendor/mermaid.min.js`,
		`href="first.html#target-section"`,
	)

	embeds := readPage(t, out, "embeds.html")
	want(t, "embeds.html", embeds, `src="https://embed.example.com/doc/1"`)
	wantNot(t, "embeds.html", embeds, "not-allowed.example.net")

	// Assets are extracted from the binary, statics copied verbatim.
	for _, f := range []string{"_assets/docs.css", "_assets/chroma.css", "_assets/vendor/mermaid.min.js", "_static/logo.svg"} {
		if !exists(out, f) {
			t.Errorf("%s was not written", f)
		}
	}

	errs, info, hints := s.collectLinkProblems()
	if len(errs) != 0 || len(info) != 0 || len(hints) != 0 {
		t.Errorf("clean fixture reported problems: errs=%v info=%v hints=%v", errs, info, hints)
	}
}

// TestGatedTagBuild covers the --tags subtree: built on demand, and reported
// as informational by the link gate because upstream owns it.
func TestGatedTagBuild(t *testing.T) {
	out, s := buildFixture(t, "site", "gated")
	if len(s.Pages) != 7 {
		t.Errorf("built %d pages with the gated tag, want 7 (%v)", len(s.Pages), rels(s))
	}
	if !exists(out, "gated/details.html") {
		t.Fatal("gated subtree was not built with its tag")
	}
	gated := readPage(t, out, "gated/details.html")
	want(t, "gated/details.html", gated,
		`<a href="/index.html">Home</a>`, // home-label
		`<a href="/gated/index.html">Gated section</a>`,
	)
	// The subtree is upstream-owned: its problems are informational only, so
	// it must not add gating errors either way.
	errs, info, _ := s.collectLinkProblems()
	if len(errs) != 0 {
		t.Errorf("gating errors with the gated tag: %v", errs)
	}
	if len(info) != 0 {
		t.Errorf("informational problems in a clean gated subtree: %v", info)
	}
	if got := s.gatedDirs(); got != "gated/" {
		t.Errorf("gatedDirs() = %q, want %q", got, "gated/")
	}
}

// TestBrokenSiteProblems checks that the gate's rules fire on the link shapes
// it exists to catch.
func TestBrokenSiteProblems(t *testing.T) {
	out, s := buildFixture(t, "broken", "")
	errs, _, _ := s.collectLinkProblems()
	joined := strings.Join(errs, "\n")
	for _, wantMsg := range []string{
		"broken link nowhere.html",
		"missing anchor one.html#nope",
		"link to a built page uses .html; use .md",
		"root-absolute page link; use a file-relative .md path",
		"link to this site; use a relative .md path (./one.md)",
		"orphan page: no nav: list contains it",
		"undeclared fragment: an include comment in one.md inlines this file",
	} {
		if !strings.Contains(joined, wantMsg) {
			t.Errorf("problems missing %q, got:\n%s", wantMsg, joined)
		}
	}
	// orphan.md is the unreachable page and inlined.md is the fragment the
	// source forgot to declare. one.md and two.md are unlisted too, but the home
	// page links them from prose, which keeps them reachable and out of the
	// report.
	if len(errs) != 7 {
		t.Errorf("got %d problems, want 7:\n%s", len(errs), joined)
	}

	// Math without `math: true` is what the gate fails a build on; RunCheck
	// turns the flag into exit 1, so assert the flag itself here.
	if p, ok := s.Pages["two.md"]; !ok || !p.Flags.Math {
		t.Error("two.md was not detected as a math page")
	}

	home := readPage(t, out, "index.html")
	want(t, "index.html", home, `<title>Broken test site · Broken test site</title>`)
	// No edit-base in colophon.conf means no footer link, and with no
	// iframe-hosts configured no frame survives at all.
	wantNot(t, "index.html", home, "Edit this page", "iframe", "embed.example.com")
	// Default theme, not the test site's accent override.
	wantNot(t, "index.html", home, "--accent:")
}

// TestNewSiteConfigErrors covers config mistakes that must fail the build
// rather than ship a broken or looser site.
func TestNewSiteConfigErrors(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Home\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, conf, tags, wantMsg string
	}{
		{"bad iframe host", "iframe-hosts: https://embed.example.com/\n", "", "is not a domain"},
		{"empty iframe host", "iframe-hosts: ,,\n", "", ""}, // ignored, no error
		{"missing logo", "logo: _static/nope.png\n", "", "not found under"},
		{"bad accent", "accent: reddish\n", "", "not a hex color"},
		{"bad tags value", "tags: nodir\n", "", "expected name=dir pairs"},
		{"unknown tag", "tags: projects=projects\n", "nope", "unknown tag"},
	}
	for _, c := range cases {
		conf := filepath.Join(dir, "colophon.conf")
		if err := os.WriteFile(conf, []byte(c.conf), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := NewSite(src, c.tags, dir)
		switch {
		case c.wantMsg == "" && err != nil:
			t.Errorf("%s: unexpected error: %v", c.name, err)
		case c.wantMsg != "" && err == nil:
			t.Errorf("%s: expected an error mentioning %q", c.name, c.wantMsg)
		case c.wantMsg != "" && !strings.Contains(err.Error(), c.wantMsg):
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.wantMsg)
		}
	}
}

// TestIframeHostsDefaultDenies pins the sanitizer's default: with no
// iframe-hosts configured, petroglyph's DisplayPolicy applies unchanged.
func TestIframeHostsDefaultDenies(t *testing.T) {
	hosts, err := iframeHosts("")
	if err != nil || len(hosts) != 0 {
		t.Fatalf("iframeHosts(\"\") = %v, %v", hosts, err)
	}
	hosts, err = iframeHosts(" Embed.Example.com , example.org ")
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 || hosts[0] != "embed.example.com" || hosts[1] != "example.org" {
		t.Errorf("iframeHosts parsed %v", hosts)
	}
	for _, bad := range []string{"exa mple.com", "example.com/path", "*", "http://example.com"} {
		if _, err := iframeHosts(bad); err == nil {
			t.Errorf("iframeHosts(%q) accepted a non-domain", bad)
		}
	}
}

// TestOrphanRule pins the reachability rule and the fragment declaration that
// replaces the old `-fragments-` file name: a `nav: list` entry or a prose link
// rescues a page, `orphan: true` opts out, `fragment: true` keeps a file out of
// the build, and one unlisted section is reported once, on the page whose nav
// entry is missing.
func TestOrphanRule(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	pages := map[string]string{
		"index.md":       "---\nnav: list\n---\n\n# Home\n\n- [Home page](home.md)\n\nA prose link to [kept](notes/kept.md).\n",
		"home.md":        "# Home page\n",
		"deliberate.md":  "---\norphan: true\n---\n\n# Deliberate\n",
		"inlined.md":     "---\nfragment: true\n---\n\n# Inlined\n",
		"notes/index.md": "---\nnav: list\n---\n\n# Notes\n\n- [Child](child.md)\n",
		"notes/child.md": "# Child\n",
		"notes/kept.md":  "# Kept\n",
		"notes/stray.md": "# Stray\n\nUnlisted and unlinked inside an unlisted section.\n",
	}
	for rel, body := range pages {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewSite(src, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BuildAll(); err != nil {
		t.Fatal(err)
	}
	if len(s.Pages) != len(pages)-1 {
		t.Errorf("built %d pages, want %d (%v)", len(s.Pages), len(pages)-1, rels(s))
	}
	errs, info, _ := s.collectLinkProblems()
	joined := strings.Join(errs, "\n")
	if len(errs) != 2 || len(info) != 0 {
		t.Fatalf("got errs=%v info=%v", errs, info)
	}
	if !strings.Contains(joined, "notes/index.md: orphan page") {
		t.Errorf("the unlisted section index was not reported:\n%s", joined)
	}
	if !strings.Contains(joined, "notes/stray.md: orphan page") ||
		!strings.Contains(joined, "notes/index.md is not in the navigation either") {
		t.Errorf("the stray page does not point at its section index:\n%s", joined)
	}
	for _, silent := range []string{"child.md", "kept.md", "deliberate.md", "inlined.md"} {
		if strings.Contains(joined, silent) {
			t.Errorf("%s was reported as unreachable:\n%s", silent, joined)
		}
	}
}

func rels(s *Site) []string {
	out := make([]string, 0, len(s.Pages))
	for rel := range s.Pages {
		out = append(out, rel)
	}
	return out
}
