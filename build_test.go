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

// TestBuildClean is the smoke test: every documented construct in
// testdata/site renders, and the link gate stays silent about it.
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
		`/_assets/vendor/tex-svg.js`, // math: true ships MathJax
		`<pre class="mermaid">`,
		`/_assets/vendor/mermaid.min.js`,
		`href="first.html#target-section"`,
		// the diagram viewer: scroll box, zoom, pan, expand
		`mm-view`,
		`'reset zoom'`,
		// reset puts back the inline style mermaid authored, max-width cap included
		`svg.getAttribute('style')`,
	)
	// The zoom read-out and the fit/natural pair of buttons are gone: the chip
	// carries zoom in, zoom out, reset, and expand only.
	wantNot(t, "guide/second.html", second, "mm-zoom")
	wantNot(t, "guide/second.html", second, "natural size")
	// A page without a diagram carries no viewer of its own.
	wantNot(t, "index.html", index, "mm-view")
	// The theme styles what the viewer adds; the shrink-to-fit rule that made a
	// large diagram unreadable must not come back.
	want(t, "_assets/docs.css", readPage(t, out, "_assets/docs.css"),
		".mm-view",
		"overflow: auto; max-height: min(75vh, 42rem)",
		// a diagram takes the width of the pane, not just the text column
		"@container (min-width: 50rem) { .mm-view { width: 100cqi; } }",
		// the control chip floats inside the diagram's frame
		".mm-bar { position: absolute; top: .45rem; right: .6rem",
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

// TestNoDefaultTags pins the default: the tags key is the only source of gated
// subtrees, so a projects/ directory is ordinary content until colophon.conf
// declares it, and its pages are ours to check rather than upstream's.
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

// TestNewSiteRequiresConf pins the gate: colophon.conf is the entry point, and
// it is the file that says where the pages are, so neither a missing file nor a
// file that names no source directory builds anything.
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

// TestNavAnchorRule covers the nav list item that fails silently: a link to a
// section of a page reads like a nav entry to its author, but a `nav: list` page
// takes children only from items that are one plain page link, so the item leaves
// the whole list as prose and the pages beside it lose their breadcrumbs, their
// sidebar row, and their place in prev/next with nothing said about it.
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

func rels(s *Site) []string {
	out := make([]string, 0, len(s.Pages))
	for rel := range s.Pages {
		out = append(out, rel)
	}
	return out
}
