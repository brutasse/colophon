package main

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// buildFixture renders a testdata site into a temp dir the way `colophon build` does —
// pages, embedded assets, statics — minus the search index, which needs the network.
func buildFixture(t *testing.T, fixture, tags string) (string, *Site) {
	t.Helper()
	dir := filepath.Join("testdata", fixture)
	s, err := NewSite(filepath.Join(dir, "colophon.conf"), tags)
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

// sliceBetween returns the first span of a built page that starts at one marker
// and stops at the next closing marker, which is how a test looks at the rail or
// the sidebar without matching markup the other one also writes.
func sliceBetween(t *testing.T, page, html, start, end string) string {
	t.Helper()
	i := strings.Index(html, start)
	if i < 0 {
		t.Fatalf("%s: no %q", page, start)
	}
	rest := html[i:]
	if j := strings.Index(rest, end); j >= 0 {
		return rest[:j]
	}
	return rest
}

// Every documented construct in testdata/site renders, and the link gate stays silent
// about it.
func TestBuildClean(t *testing.T) {
	out, s := buildFixture(t, "site", "")

	// Fragment files are declared by front matter, inlined into the pages that
	// include them, and the tag-gated subtree is excluded until its tag is
	// passed: 7 pages, not 9.
	if len(s.Pages) != 7 {
		t.Errorf("built %d pages, want 7 (%v)", len(s.Pages), rels(s))
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

	// A page parents its own top-level sections, so each glued document gets a
	// sidebar row and a rail entry pointing at the anchor the page already had.
	composite := readPage(t, out, "guide/composite.html")
	want(t, "guide/composite.html", composite,
		`<a href="/guide/composite.html#second-glued-document">Second glued document</a>`, // sidebar
		`<a href="#third-glued-document">Third glued document</a>`,                        // rail
	)
	// On the index, the sections are a sub-list of the item that holds them. What
	// `#all` decides is which headings are in that list: composite.md is a set of
	// documents, so the heading its title came from is one of them and joins the list,
	// while glued.md keeps that heading as the name of the item alone.
	want(t, "guide/index.html", guide,
		`<a href="composite.html" rel="nofollow">Composite guide pages</a><ul class="nav-sections">`+
			`<li><a href="composite.html#glued-guide-pages">Glued guide pages</a></li>`+
			`<li><a href="composite.html#second-glued-document">Second glued document</a></li>`, // #all
		`<a href="glued.html" rel="nofollow">Glued appendix</a><ul class="nav-sections">`+
			`<li><a href="glued.html#first-appendix-document">First appendix document</a></li>`, // without it
	)
	if n := strings.Count(guide, `class="nav-sections"`); n != 2 {
		t.Errorf("guide/index.html nests %d child lists, want 2", n)
	}
	wantNot(t, "guide/index.html", guide, `composite.md#all`) // the marker is for the builder, not the link
	wantNot(t, "guide/index.html sidebar", sliceBetween(t, "guide/index.html", guide, `<nav class="sidebar"`, "</nav>"),
		`#second-glued-document`)
	// A page's own title heading is not one of its sections, and an h2 stays in the
	// rail alone. The inline `toc:` list is a separate construct, and it does name
	// the title, so look at the rail only.
	rail := sliceBetween(t, "guide/index.html", guide, `<nav class="toc"`, "</nav>")
	wantNot(t, "guide/index.html rail", rail, `href="#guide"`)
	second := readPage(t, out, "guide/second.html")
	want(t, "guide/second.html", second,
		`<pre class="mermaid">`, // the fence becomes the hook the viewer wraps
		`href="first.html#target-section"`,
	)

	embeds := readPage(t, out, "embeds.html")
	want(t, "embeds.html", embeds, `src="https://embed.example.com/doc/1"`)
	wantNot(t, "embeds.html", embeds, "not-allowed.example.net")

	// Assets are extracted from the binary, statics copied verbatim.
	for _, f := range []string{"_assets/docs.css", "_assets/chroma.css", "_assets/site.js", "_assets/toc.js", "_assets/mermaid-viewer.js", "_assets/vendor/mermaid.min.js", "_static/logo.svg"} {
		if !exists(out, f) {
			t.Errorf("%s was not written", f)
		}
	}

	errs, info, hints := s.collectLinkProblems()
	if len(errs) != 0 || len(info) != 0 || len(hints) != 0 {
		t.Errorf("clean fixture reported problems: errs=%v info=%v hints=%v", errs, info, hints)
	}
}

// The --tags subtree is built on demand and reported as informational by the link gate,
// because upstream owns it.
func TestGatedTagBuild(t *testing.T) {
	out, s := buildFixture(t, "site", "gated")
	if len(s.Pages) != 9 {
		t.Errorf("built %d pages with the gated tag, want 9 (%v)", len(s.Pages), rels(s))
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

// The tags key is the only source of gated subtrees, so a projects/ directory is
// ordinary content until colophon.conf declares it, and its pages are ours to check.
func TestNoDefaultTags(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	pages := map[string]string{
		"index.md":            "---\nnav: list\n---\n\n# Home\n\n- [Project](projects/index.md)\n",
		"projects/index.md":   "---\nnav: list\n---\n\n# Project\n\n- [Details](details.md)\n",
		"projects/details.md": "# Details\n",
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
	conf := filepath.Join(dir, "colophon.conf")
	if err := os.WriteFile(conf, []byte("source: source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSite(conf, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BuildAll(); err != nil {
		t.Fatal(err)
	}
	if len(s.Pages) != len(pages) {
		t.Errorf("built %d pages, want %d (%v)", len(s.Pages), len(pages), rels(s))
	}
	if got := s.gatedDirs(); got != "no gated subtrees" {
		t.Errorf("gatedDirs() = %q, want %q", got, "no gated subtrees")
	}
	// Nothing is exempt from the link gate either, so a clean subtree still
	// reports nothing and a broken link there would be an error.
	errs, info, _ := s.collectLinkProblems()
	if len(errs) != 0 || len(info) != 0 {
		t.Errorf("clean site reported problems: errs=%v info=%v", errs, info)
	}
}

// The gate's rules have to fire on the link shapes they exist to catch.
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

	// Math is detected, never declared: two.md carries the flag that ships
	// MathJax with it, and no front matter asked for it.
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

// Config mistakes have to fail the build rather than ship a broken or looser site.
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
		{"unknown tag", "tags: internal=internal\n", "nope", "unknown tag"},
		{"tag with none declared", "", "projects", "declares no tags"},
	}
	for _, c := range cases {
		conf := filepath.Join(dir, "colophon.conf")
		if err := os.WriteFile(conf, []byte("source: source\n"+c.conf), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := NewSite(conf, c.tags)
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

// colophon.conf is the entry point and the file that says where the pages are, so
// neither a missing file nor one naming no source directory builds anything.
func TestNewSiteRequiresConf(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Home\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "colophon.conf")

	if _, err := NewSite(conf, ""); err == nil || !strings.Contains(err.Error(), "source:") {
		t.Errorf("a missing config built a site or did not name the key: %v", err)
	}

	cases := []struct{ name, conf, wantMsg string }{
		{"no source key", "title: No source\n", "no source key"},
		{"source that is not there", "source: pages\n", "is not a directory"},
		{"source that is a file", "source: colophon.conf\n", "is not a directory"},
	}
	for _, c := range cases {
		if err := os.WriteFile(conf, []byte(c.conf), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := NewSite(conf, "")
		if err == nil || !strings.Contains(err.Error(), c.wantMsg) {
			t.Errorf("%s: error %v does not mention %q", c.name, err, c.wantMsg)
		}
	}

	// The source directory is resolved against the config file, so a run from
	// elsewhere still builds the site that file describes.
	if err := os.WriteFile(conf, []byte("source: source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSite(conf, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Src != src {
		t.Errorf("source resolved to %s, want %s", s.Src, src)
	}
}

// With no iframe-hosts configured, petroglyph's DisplayPolicy applies unchanged.
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

// The reachability rule and the fragment declaration: a `nav: list` entry or a prose
// link rescues a page, `orphan: true` opts out, `fragment: true` keeps a file out of
// the build, and one unlisted section is reported once, on the page whose nav entry is
// missing.
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
	if err := os.WriteFile(filepath.Join(dir, "colophon.conf"), []byte("source: source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSite(filepath.Join(dir, "colophon.conf"), "")
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

// A moved heading keeps its old fragment through <a name>, which is the anchor
// idiom GitHub documents, and a fragment written before the anchors followed
// GitHub's rule differs from its target only by hyphen runs. The gate accepts
// the first and names the fix for the second.
func TestAnchorTargetsAndDashRewrites(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	pages := map[string]string{
		"index.md": "---\nnav: list\n---\n\n# Home\n\n- [Notes](notes.md)\n\n" +
			"[Old target](notes.md#upgrade-notes) and [punctuation](notes.md#foo-bar--baz).\n" +
			"[Stale fragment](notes.md#foo-bar-baz).\n",
		"notes.md": "# Notes\n\n## Foo: Bar — baz!!!\n\n<a name=\"upgrade-notes\"></a>\n",
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
	if err := os.WriteFile(filepath.Join(dir, "colophon.conf"), []byte("source: source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSite(filepath.Join(dir, "colophon.conf"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BuildAll(); err != nil {
		t.Fatal(err)
	}
	errs, info, hints := s.collectLinkProblems()
	if len(errs) != 1 || len(info) != 0 {
		t.Fatalf("got errs=%v info=%v", errs, info)
	}
	if !strings.Contains(errs[0], "missing anchor notes.html#foo-bar-baz") {
		t.Errorf("the stale fragment was not reported: %v", errs)
	}
	if len(hints) != 1 || hints[0] != "index.md: notes.html#foo-bar-baz -> foo-bar--baz" {
		t.Errorf("the fragment rewrite was not suggested: %v", hints)
	}
}

// A link to a section of a page reads like a nav entry to its author, but a `nav: list`
// page takes children only from items that are one plain page link, so the item leaves
// the whole list as prose and the pages beside it lose their breadcrumbs, their sidebar
// row, and their place in prev/next with nothing said about it.
func TestNavAnchorRule(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	pages := map[string]string{
		"index.md": "---\nnav: list\n---\n\n# Home\n\n- [Home page](home.md)\n- [A section of it](home.md#second-thing)\n- [All of it](home.md#all)\n\n" +
			"An anchor in prose is an ordinary link, and stays one: [second thing](home.md#second-thing). " +
			"The notes are [elsewhere](notes.md).\n\n" +
			"```\n- [Inside a fence](home.md#second-thing)\n```\n",
		"home.md":  "# Home page\n\nProse.\n\n## Second thing\n\nMore prose.\n",
		"notes.md": "# Notes\n\nNot a nav page, so this item is just a link:\n\n- [Second thing](home.md#second-thing)\n",
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
	if err := os.WriteFile(filepath.Join(dir, "colophon.conf"), []byte("source: source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSite(filepath.Join(dir, "colophon.conf"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BuildAll(); err != nil {
		t.Fatal(err)
	}
	errs, info, _ := s.collectLinkProblems()
	joined := strings.Join(errs, "\n")
	if len(errs) != 2 || len(info) != 0 {
		t.Fatalf("got errs=%v info=%v", errs, info)
	}
	if !strings.Contains(joined, "index.md: nav list item - [A section of it](home.md#second-thing) links to a section, not a page") {
		t.Errorf("the nav list item was not reported:\n%s", joined)
	}
	// `#all` is the marker, so the nav item rule stays quiet about it. It reached
	// the page as a link here only because this list is prose, and the missing
	// anchor says so, which is what keeps a misplaced marker from rotting silently.
	if !strings.Contains(joined, "index.md: missing anchor home.html#all") {
		t.Errorf("the misplaced #all marker was not reported as the link it is:\n%s", joined)
	}
	if strings.Contains(joined, "nav list item - [All of it]") {
		t.Errorf("the #all marker was reported as a nav list item:\n%s", joined)
	}
	// The report is worth having precisely because the list really was lost.
	if s.inNav("home.md") {
		t.Error("home.md is in the nav tree, so the anchor item did not break the list")
	}
	for _, silent := range []string{"notes.md", "notes.md#", "Inside a fence", "An anchor in prose"} {
		if strings.Contains(joined, silent) {
			t.Errorf("%q was reported, but only a nav list item is a problem:\n%s", silent, joined)
		}
	}
}

// The dev reload channel is a WebSocket, not a stream; serve.go's Serve comment holds
// why.
func TestDevReloadUsesWebSocket(t *testing.T) {
	out, s := buildFixture(t, "site", "")
	// The shipped site has no reload endpoint at all, and no client for one.
	wantNot(t, "index.html", readPage(t, out, "index.html"),
		"EventSource", "/sse", "WebSocket", "/reload")

	s.Dev = true
	b, err := s.wrapPage(s.Pages["index.md"])
	if err != nil {
		t.Fatalf("wrapPage: %v", err)
	}
	dev := string(b)
	want(t, "dev index.html", dev, "new WebSocket(", "/reload?path=")
	wantNot(t, "dev index.html", dev, "EventSource", "/sse")

	// A tab announces the document it shows by URL, and a rebuild reports the
	// pages it changed by output path. Both must land on the same key, or the
	// ping reaches no tab, or reaches every one.
	for rel, p := range s.Pages {
		if k := renderedKey("/" + p.OutPath); k != p.OutPath {
			t.Errorf("%s: renderedKey(%q) = %q, want the page's own output path", rel, "/"+p.OutPath, k)
		}
	}
}

// assetRefRe matches an asset URL a page points at: src="/_assets/site.js".
var assetRefRe = regexp.MustCompile(`(?:src|href)="/(_assets/[^"]+)"`)

// builtPages lists every page of a built site.
func builtPages(out string) ([]string, error) {
	var paths []string
	var walkErr error
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			switch {
			case e.IsDir():
				if err := walk(p); err != nil {
					return err
				}
			case strings.HasSuffix(e.Name(), ".html"):
				paths = append(paths, p)
			}
		}
		return nil
	}
	if walkErr = walk(out); walkErr != nil {
		return nil, walkErr
	}
	return paths, nil
}

// scriptTagRe matches a script's opening tag and srcAttrRe its src. Reading the tags is
// what keeps this test off the scripts' bytes: which page pays for a script is a
// question about the template, and what the script then does is tested in JavaScript.
var scriptTagRe = regexp.MustCompile(`(?s)<script\b([^>]*)>`)
var srcAttrRe = regexp.MustCompile(`src="([^"]*)"`)

// pageScript is one external script tag. The deferred flag is part of what a tag means:
// deferred scripts run in document order, which is the only order a page's scripts have.
type pageScript struct {
	src      string
	deferred bool
}

func pageScripts(page string) []pageScript {
	var scripts []pageScript
	for _, tag := range scriptTagRe.FindAllStringSubmatch(page, -1) {
		m := srcAttrRe.FindStringSubmatch(tag[1])
		if m == nil {
			continue // an inline tag: the theme head hook, the MathJax config, the dev reload client
		}
		scripts = append(scripts, pageScript{src: m[1], deferred: strings.Contains(tag[1], "defer")})
	}
	return scripts
}

// sortedSrcs lists the scripts in order by name, so a second copy of one or a src that
// arrived by a relative path — a different URL on a page in a subdirectory — cannot
// pass for the tag it copies.
func sortedSrcs(scripts []pageScript) []string {
	out := make([]string, len(scripts))
	for i, sc := range scripts {
		out[i] = sc.src
	}
	slices.Sort(out)
	return out
}

func indexSrc(scripts []pageScript, src string) int {
	for i, sc := range scripts {
		if sc.src == src {
			return i
		}
	}
	return -1
}

const (
	mermaidVendorURL = "/_assets/vendor/mermaid.min.js"
	mermaidViewerURL = "/_assets/mermaid-viewer.js"
	texURL           = "/_assets/vendor/tex-svg.js"
	siteURL          = "/_assets/site.js"
	tocURL           = "/_assets/toc.js"
	searchURL        = "/pagefind/pagefind-component-ui.js"
)

// wantScripts are the scripts a page earns its tags on, from the same values wrapPage
// hands the template. Deriving them from the site rather than from the page's own bytes
// is what makes a tag the template dropped a failure: a page can be missing the spy it
// needs as quietly as it can carry one it has no rail for.
func wantScripts(s *Site, p *Page, page string) []string {
	var want []string
	if s.Search {
		want = append(want, searchURL)
	}
	if p.Flags.Math {
		want = append(want, texURL)
	}
	if p.Flags.Mermaid {
		want = append(want, mermaidVendorURL, mermaidViewerURL)
	}
	want = append(want, siteURL) // the theme control and the mobile nav, one file for the whole site
	// The rail is toc.js's whole subject — its first line gives up without one — so the
	// tag follows the <nav> and no flag decides either of them.
	if strings.Contains(page, `<nav class="toc"`) {
		want = append(want, tocURL)
	}
	return want
}

// TestBuiltPageScriptTags asks every page of the built fixture one question: does it load
// exactly the scripts its own flags and markup call for? A script a page has no use for
// costs a download on every visit to it, and a missing one leaves the reader a control
// that does nothing. What the scripts do with the markup they find is tested in
// JavaScript under test/js, against this same built site.
func TestBuiltPageScriptTags(t *testing.T) {
	out, s := buildFixture(t, "site", "")
	byOut := make(map[string]*Page, len(s.Pages))
	for _, p := range s.Pages {
		byOut[p.OutPath] = p
	}

	check := func(t *testing.T, rel string, p *Page, page string) {
		t.Helper()
		got := pageScripts(page)
		want := wantScripts(s, p, page)
		wantSrcs := slices.Clone(want)
		slices.Sort(wantSrcs)
		if gotSrcs := sortedSrcs(got); !slices.Equal(gotSrcs, wantSrcs) {
			t.Errorf("%s loads %v, want %v", rel, gotSrcs, wantSrcs)
		}

		// The one dependency among the scripts, and the reason the vendor file needs its
		// own tag rather than an onload handler: only deferred tags have a document order to
		// run in, and the viewer calls mermaid.initialize.
		if p.Flags.Mermaid {
			vendor, viewer := indexSrc(got, mermaidVendorURL), indexSrc(got, mermaidViewerURL)
			if vendor >= 0 && viewer >= 0 {
				if !got[vendor].deferred || !got[viewer].deferred {
					t.Errorf("%s: a mermaid tag is not deferred, so the two run in the order the parser reaches them", rel)
				}
				if vendor > viewer {
					t.Errorf("%s: the mermaid vendor tag follows the viewer, so the viewer would run first", rel)
				}
			}
		}
		// MathJax is configured by an inline tag, which the list above says nothing about,
		// so its position against the runtime is checked in the document itself.
		if tex := strings.Index(page, texURL); p.Flags.Math && tex >= 0 && strings.Index(page, "window.MathJax") > tex {
			t.Errorf("%s: the MathJax configuration follows the runtime that reads it", rel)
		}
		// A reference is a promise the build has to keep: every asset a page names, script
		// or stylesheet, has to be a file WriteAssets wrote.
		for _, m := range assetRefRe.FindAllStringSubmatch(page, -1) {
			if !exists(out, m[1]) {
				t.Errorf("%s references /%s, which was not written", rel, m[1])
			}
		}
	}

	pages, err := builtPages(out)
	if err != nil {
		t.Fatalf("walk the built site: %v", err)
	}
	var checked int
	for _, path := range pages {
		rel, err := filepath.Rel(out, path)
		if err != nil {
			t.Fatalf("relative path of %s: %v", path, err)
		}
		rel = filepath.ToSlash(rel)
		p, ok := byOut[rel]
		if !ok {
			t.Errorf("%s is in the output tree but is not a page of the site", rel)
			continue
		}
		checked++
		check(t, rel, p, readPage(t, out, rel))
	}
	if checked != len(s.Pages) {
		t.Errorf("%d of the site's %d pages are in the output tree", checked, len(s.Pages))
	}

	// Every page above agreeing that none of them loads the search bundle says nothing on
	// its own, because Search is set by `colophon build` with a pagefind bundle and by
	// serve, and this fixture does neither. Turning it on is what gives that row an edge.
	t.Run("with search on", func(t *testing.T) {
		s.Search = true
		p := s.Pages["index.md"]
		b, err := s.wrapPage(p)
		if err != nil {
			t.Fatalf("wrapPage: %v", err)
		}
		check(t, "index.html with search on", p, string(b))
	})
}

// Goes through WriteAssets rather than checkASCII alone: the vendored scripts hold
// literal glyphs and have to stay outside the rule.
func TestWriteAssetsRefusesNonASCIIScripts(t *testing.T) {
	out := t.TempDir()
	if err := WriteAssets(out); err != nil {
		t.Fatalf("WriteAssets: %v", err)
	}
	for _, f := range []string{"_assets/site.js", "_assets/mermaid-viewer.js", "_assets/vendor/mermaid.min.js"} {
		if !exists(out, f) {
			t.Errorf("%s was not written", f)
		}
	}

	if err := checkASCII("site.js", []byte("var s = '\u00e9';\nother = 1;\n")); err == nil {
		t.Fatal("a literal glyph was accepted")
	} else if !strings.Contains(err.Error(), "site.js:1:") {
		t.Errorf("the error should name file and line, got %v", err)
	}
	if err := checkASCII("toc.js", []byte("ok\nvar s = '\u2014';\n")); err == nil ||
		!strings.Contains(err.Error(), "toc.js:2:") {
		t.Errorf("a glyph on the second line should report line 2, got %v", err)
	}
}

// cssThemeBlock is one of the three theme blocks docs.css and chroma.css are
// written in — the light defaults, the system preference, and the stored choice
// that beats it — which is also the shape accentCSS writes for --accent.
type cssThemeBlock struct {
	which string
	re    *regexp.Regexp
}

var cssThemeBlocks = []cssThemeBlock{
	{"light", regexp.MustCompile(`(?s):root,\s*\[data-theme="light"\]\s*\{([^}]*)\}`)},
	{"system dark", regexp.MustCompile(`(?s)@media \(prefers-color-scheme: dark\) \{\s*:root:not\(\[data-theme="light"\]\)\s*\{([^}]*)\}`)},
	{"stored dark", regexp.MustCompile(`(?s)\[data-theme="dark"\]\s*\{([^}]*)\}`)},
}

var (
	cssDeclRe       = regexp.MustCompile(`(--[a-z-]+):\s*([^;}]+)`)
	cssVarRe        = regexp.MustCompile(`var\((--[a-z-]+)\)`)
	cssHexRe        = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	cssRGBARe       = regexp.MustCompile(`rgba\(\s*(\d+),\s*(\d+),\s*(\d+),\s*([\d.]+)\s*\)`)
	cssCommentRe    = regexp.MustCompile(`(?s)/\*.*?\*/`)
	codeContrastMin = 4.5 // WCAG's AA bar for the .85rem text a code block holds
)

// embeddedAsset reads a file out of the binary's own asset tree, so a test asks
// about the bytes that ship rather than about a copy of them on disk.
func embeddedAsset(t *testing.T, name string) string {
	t.Helper()
	b, err := assetsFS.ReadFile("assets/" + name)
	if err != nil {
		t.Fatalf("read assets/%s: %v", name, err)
	}
	return string(b)
}

// themePalette reads one theme block of a stylesheet into the custom properties it
// declares. One block per name is required: a second one would win in the cascade
// while this test reads the first.
func themePalette(t *testing.T, file string, b cssThemeBlock, css string) map[string]string {
	t.Helper()
	found := b.re.FindAllStringSubmatch(css, -1)
	if len(found) != 1 {
		t.Fatalf("assets/%s: %d %s theme blocks, want exactly 1", file, len(found), b.which)
	}
	decls := map[string]string{}
	for _, d := range cssDeclRe.FindAllStringSubmatch(found[0][1], -1) {
		decls[d[1]] = strings.TrimSpace(d[2])
	}
	if len(decls) == 0 {
		t.Fatalf("assets/%s: the %s theme block declares no custom properties", file, b.which)
	}
	return decls
}

func paletteNames(p map[string]string) []string {
	names := make([]string, 0, len(p))
	for name := range p {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// cssRGB is a colour literal in a stylesheet: a hex colour, or an rgba() tint
// composited over the surface it is painted on, which is how a diff token's
// background arrives.
func cssRGB(t *testing.T, value, surface string) [3]float64 {
	t.Helper()
	if hex := strings.TrimPrefix(value, "#"); hex != value {
		if len(hex) == 3 {
			hex = strings.Repeat(hex[0:1], 2) + strings.Repeat(hex[1:2], 2) + strings.Repeat(hex[2:], 2)
		}
		if len(hex) != 6 {
			t.Fatalf("%q is not a colour this test can read", value)
		}
		var c [3]float64
		for i := range c {
			n, err := strconv.ParseUint(hex[2*i:2*i+2], 16, 8)
			if err != nil {
				t.Fatalf("%q is not a colour: %v", value, err)
			}
			c[i] = float64(n)
		}
		return c
	}
	m := cssRGBARe.FindStringSubmatch(value)
	if m == nil {
		t.Fatalf("%q is neither a hex colour nor an rgba() tint", value)
	}
	bg := cssRGB(t, surface, surface)
	a, err := strconv.ParseFloat(m[4], 64)
	if err != nil {
		t.Fatalf("%q carries no alpha: %v", value, err)
	}
	var c [3]float64
	for i := range c {
		fg, err := strconv.ParseFloat(m[i+1], 64)
		if err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		c[i] = fg*a + bg[i]*(1-a)
	}
	return c
}

func contrastRatio(a, b [3]float64) float64 {
	lum := func(c [3]float64) float64 {
		channel := func(v float64) float64 {
			v /= 255
			if v <= 0.03928 {
				return v / 12.92
			}
			return math.Pow((v+0.055)/1.055, 2.4)
		}
		return 0.2126*channel(c[0]) + 0.7152*channel(c[1]) + 0.0722*channel(c[2])
	}
	la, lb := lum(a), lum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// The token palettes stay whole and readable: all three theme blocks declare the
// same colours, a token rule carries a variable rather than a colour of its own,
// and every token colour clears AA contrast against the surface it is drawn on.
func TestChromaPalette(t *testing.T) {
	chromaCSS, docsCSS := embeddedAsset(t, "chroma.css"), embeddedAsset(t, "docs.css")

	tokens, surfaces, rules := map[string]map[string]string{}, map[string]string{}, chromaCSS
	var names []string
	for _, b := range cssThemeBlocks {
		p := themePalette(t, "chroma.css", b, chromaCSS)
		if names != nil && !slices.Equal(names, paletteNames(p)) {
			t.Errorf("the %s theme block declares a different colour list: %v, want %v",
				b.which, paletteNames(p), names)
		}
		names = paletteNames(p)
		tokens[b.which] = p
		surfaces[b.which] = themePalette(t, "docs.css", b, docsCSS)["--code-bg"]
		rules = b.re.ReplaceAllString(rules, "")
	}

	rules = cssCommentRe.ReplaceAllString(rules, "")
	for _, stray := range append(cssHexRe.FindAllString(rules, 1), cssRGBARe.FindAllString(rules, 1)...) {
		t.Errorf("chroma.css colours a token outside its palettes: %s", stray)
	}
	for _, m := range cssVarRe.FindAllStringSubmatch(rules, -1) {
		name := m[1]
		if _, ok := tokens["light"][name]; ok {
			continue
		}
		if !strings.Contains(docsCSS, name+":") {
			t.Errorf("chroma.css uses %s, which no theme block defines", name)
		}
	}

	for _, b := range cssThemeBlocks {
		surface := surfaces[b.which]
		if surface == "" {
			t.Fatalf("docs.css defines no --code-bg for the %s theme", b.which)
		}
		for name, value := range tokens[b.which] {
			if strings.HasSuffix(name, "-bg") {
				continue // a surface, read under the text drawn on it
			}
			bg := surface
			if tint, ok := tokens[b.which][name+"-bg"]; ok {
				bg = tint
			}
			if r := contrastRatio(cssRGB(t, value, surface), cssRGB(t, bg, surface)); r < codeContrastMin {
				t.Errorf("%s theme: %s %s on %s is %.2f:1, under %.1f:1",
					b.which, name, value, bg, r, codeContrastMin)
			}
		}
	}
}

func TestRenderedKey(t *testing.T) {
	for _, tc := range []struct{ urlPath, key string }{
		{"/", "index.html"},
		{"/index.html", "index.html"},
		{"/guide/first.html", "guide/first.html"},
		{"/guide/../first.html", "first.html"},
	} {
		if got := renderedKey(tc.urlPath); got != tc.key {
			t.Errorf("renderedKey(%q) = %q, want %q", tc.urlPath, got, tc.key)
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
