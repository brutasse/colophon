package main

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
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
		fmt.Sprintf(`<img src=%q alt="Colophon test site">`, s.Logo),
		fmt.Sprintf(`<link rel="icon" href=%q>`, s.Favicon),
		`--accent: #0b7285;`,
		`<a href="guide/index.html">Guide</a>`, // sidebar, home-relative like every chrome URL
		`href="guide/first.html"`,              // .md link rewritten to .html
		`© 2024-`,                              // {year} substituted in copyright
		`<a href="https://example.com/edit/main/testdata/site/source/index.md">Edit this page</a>`,
	)
	wantNot(t, "index.html", index, "pagefind-modal", `href="guide/first.md"`)
	// The header names the logo by its bytes, so a replaced file arrives without the
	// reader's cache ever having held the name it replaces. The copy at the
	// configured path stays for anything that links the file directly.
	for _, u := range []string{s.Logo, s.Favicon} {
		if !strings.HasPrefix(u, "_assets/logo.") || !exists(out, u) {
			t.Errorf("config image published at %q, want a written _assets/logo.<digest>.svg", u)
		}
	}
	// The gated subtree is listed in the home nav but is not built without its
	// tag, so it must stay out of the sidebar too. The home body keeps its own
	// link to it; the gate is what the navigation shows.
	wantNot(t, "index.html sidebar", sliceBetween(t, "index.html", index, `<nav class="sidebar"`, "</nav>"),
		`<a href="gated/index.html"`)

	guide := readPage(t, out, "guide/index.html")
	want(t, "guide/index.html", guide,
		`<title>Guide · Colophon test site</title>`, // title from the h1
		`<nav class="page-toc">`,                    // toc: true
		`<nav class="toc" aria-label="On this page">`,
		`<a href="../guide/first.html">`, // nav: list children, one level up like every chrome URL
		`<a href="../guide/second.html">`,
		`<span class="here">Guide</span>`, // breadcrumbs end on the page itself
		`<a class="next" href="../guide/first.html">`,
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
		`<a class="prev" href="../guide/index.html">`,
	)
	wantNot(t, "guide/first.html", first,
		"colophon:include", // every include comment was consumed, fenced fragment included
	)

	// A page parents its own top-level sections, so each glued document gets a
	// sidebar row and a rail entry pointing at the anchor the page already had.
	composite := readPage(t, out, "guide/composite.html")
	want(t, "guide/composite.html", composite,
		`<a href="../guide/composite.html#second-glued-document">Second glued document</a>`, // sidebar
		`<a href="#third-glued-document">Third glued document</a>`,                          // rail
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

	// Assets are extracted from the binary at the address their bytes name, statics
	// copied verbatim.
	for _, f := range writtenAssets(t) {
		if !exists(out, f) {
			t.Errorf("%s was not written", f)
		}
	}
	if exists(out, "_assets/docs.css") {
		t.Error("_assets/docs.css was written under a name no page can cache forever")
	}
	if !exists(out, "_static/logo.svg") {
		t.Error("_static/logo.svg was not copied")
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
		`<a href="../index.html">Home</a>`, // home-label
		`<a href="../gated/index.html">Gated section</a>`,
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
		"link with no text [](one.md)",
	} {
		if !strings.Contains(joined, wantMsg) {
			t.Errorf("problems missing %q, got:\n%s", wantMsg, joined)
		}
	}
	// orphan.md is the unreachable page and inlined.md is the fragment the
	// source forgot to declare. one.md and two.md are unlisted too, but the home
	// page links them from prose, which keeps them reachable and out of the
	// report. The last home-page bullet is the link with no text.
	if len(errs) != 8 {
		t.Errorf("got %d problems, want 8:\n%s", len(errs), joined)
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

// A link with no text renders an empty spot where the reader should have found one,
// so it is reported wherever it was written. A gated subtree keeps the problem
// informational rather than silent: nobody reads an empty link, and a file owned
// upstream is still not this site's to rewrite.
func TestEmptyLinkTextRule(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	pages := map[string]string{
		"index.md": "# Home\n\nSee [the page](page.md) and the [gated notes](gated/index.md).\n",
		"page.md": "# Page\n\nAn empty link, which is the report: [](page.md#second-thing).\n\n" +
			"Quiet here: an image asks for no alt text, so ![](_static/pic.png) stays as it is, " +
			"[a link with text](page.md#second-thing) is the form wanted, `[](page.md#second-thing)` " +
			"is code the reader reads rather than a link to follow, and " +
			"`<a name=...>` is the custom anchor an author writes on purpose:\n\n" +
			"<a name=\"somewhere\"></a>\n\n```\n[](page.md#second-thing)\n```\n\n## Second thing\n\nProse.\n",
		"gated/index.md":  "# Gated notes\n\nUpstream wrote [](../page.md).\n",
		"_static/pic.png": "not really a png, and nothing reads it",
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
	conf := "source: source\ntags: gated=gated\n"
	if err := os.WriteFile(filepath.Join(dir, "colophon.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSite(filepath.Join(dir, "colophon.conf"), "gated")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BuildAll(); err != nil {
		t.Fatal(err)
	}
	errs, info, _ := s.collectLinkProblems()
	if len(errs) != 1 || len(info) != 1 {
		t.Fatalf("got errs=%v info=%v", errs, info)
	}
	if !strings.Contains(errs[0], "page.md: link with no text [](page.md#second-thing)") {
		t.Errorf("the empty link in prose was not reported:\n%v", errs)
	}
	if !strings.Contains(info[0], "gated/index.md: link with no text [](../page.md)") {
		t.Errorf("the empty link in the gated subtree is not the one informational problem:\n%v", info)
	}
}

// A file the corpus does not hold is a hole in the page no rule looked at: the image
// renders nothing and the download opens nothing, and only the reader finds out. So
// every link to a file colophon cannot serve is a report, including one whose path
// leads above the source directory, which resolves on the author's disk and reaches
// no reader because copyStatics copies the corpus and nothing beside it. A gated
// subtree's missing file stays informational, as for every other rule.
func TestMissingFileRule(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	pages := map[string]string{
		"index.md": "# Home\n\nSee [the page](page.md) and the [gated notes](gated/index.md).\n",
		"page.md": "# Page\n\n" +
			"A missing image is the report: ![nope](_static/nope.png).\n\n" +
			"So is a missing download, and a query on it is no reason to look away: [the bundle](_static/bundle.zip?v=2).\n\n" +
			"And so is markup written by hand: <img src=\"_static/raw-nope.png\">\n\n" +
			"One leading out of the corpus is dead however it resolves on disk: ![out](../outside.png)\n\n" +
			"Quiet, in order: ![here](_static/ok.png?v=2) and [the tools](_static/tools.tar.gz) are in the " +
			"corpus, and so is ![the spaced one](<_static/a b.png>), which the page asks for by its encoded " +
			"name. ![another host](https://example.com/absent.png) is not ours to check, " +
			"[the stylesheet](/_assets/docs.css) is a file colophon writes itself, " +
			"[write](mailto:docs@example.com) names no file, and `![written](_static/code-nope.png)` is " +
			"markup a reader reads rather than an image to load:\n\n" +
			"```html\n<img src=\"_static/fence-nope.png\">\n```\n",
		"gated/index.md":       "# Gated notes\n\nUpstream asks for what is not there: ![gone](../_static/gated-nope.png).\n",
		"_static/ok.png":       "not really a png, and nothing reads it",
		"_static/a b.png":      "the same, with a space in its name",
		"_static/tools.tar.gz": "not really a tarball",
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
	if err := os.WriteFile(filepath.Join(dir, "colophon.conf"), []byte("source: source\ntags: gated=gated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSite(filepath.Join(dir, "colophon.conf"), "gated")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BuildAll(); err != nil {
		t.Fatal(err)
	}
	errs, info, _ := s.collectLinkProblems()
	if len(errs) != 4 || len(info) != 1 {
		t.Fatalf("got errs=%v info=%v", errs, info)
	}
	for _, want := range []string{
		"page.md: missing file _static/nope.png",
		"page.md: missing file _static/bundle.zip?v=2",
		"page.md: missing file _static/raw-nope.png",
		"page.md: file above the source directory ../outside.png",
	} {
		found := false
		for _, e := range errs {
			if strings.Contains(e, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q was not reported:\n%v", want, errs)
		}
	}
	if !strings.Contains(info[0], "gated/index.md: missing file ../_static/gated-nope.png") {
		t.Errorf("the gated subtree's missing file is not the one informational problem:\n%v", info)
	}
	for _, silent := range []string{"code-nope.png", "fence-nope.png", "example.com", "_assets/docs.css", "mailto:", "tools.tar.gz"} {
		for _, e := range append(errs, info...) {
			if strings.Contains(e, silent) {
				t.Errorf("%q was reported, but the corpus or another host answers for it:\n%s", silent, e)
			}
		}
	}
}

// The dev server re-wraps the whole site for what a save can move in a second page's
// HTML: the title the sidebar and the breadcrumbs show, the children a `nav: list`
// declares, the top-level sections that become rows under the page, and the `#all`
// marker deciding which of a child's headings are documents. Everything else a save
// changes reaches that page's own bytes alone, so naming the difference is what lets
// a save of a 1300-page corpus rebuild one page.
func TestNavMovedNamesWhatRebuildsEverything(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	const (
		indexSrc = "---\nnav: list\n---\n\n# Home\n\n- [Page](page.md)\n"
		pageSrc  = "---\ntitle: The page\n---\n\n# The page\n\nRead me.\n\n## Detail\n\nMore.\n"
		plainSrc = "# Plain\n\nProse.\n"
	)
	for rel, body := range map[string]string{"index.md": indexSrc, "page.md": pageSrc, "plain.md": plainSrc, "dup.md": "# Plain\n\nSame heading text as plain.md, so the two pages compete for a slug.\n"} {
		p := filepath.Join(src, rel)
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
	for _, tc := range []struct {
		what, rel, body, want string
	}{
		{"the same bytes again", "page.md", pageSrc, ""},
		{"only its prose", "page.md", strings.Replace(pageSrc, "Read me.", "Read me now, all of it.", 1), ""},
		{"a heading of its own rail", "page.md", strings.Replace(pageSrc, "## Detail", "## Detail, renamed", 1), ""},
		{"its front-matter title", "page.md", strings.Replace(pageSrc, "title: The page", "title: The page, retitled", 1), "a page title moved"},
		{"the heading another page's title came from", "plain.md", strings.Replace(plainSrc, "# Plain", "# Plain, retitled", 1), "a page title moved"},
		{"a new top-level section", "page.md", pageSrc + "\n# Another document\n\nText.\n", "the page sections moved"},
		{"the section a front-matter title hides", "page.md", strings.Replace(pageSrc, "# The page", "# The page, rewritten", 1), "the page sections moved"},
		{"a child added to its list", "index.md", indexSrc + "- [New page](new.md)\n", "a nav list moved"},
		{"the #all marker", "index.md", strings.Replace(indexSrc, "](page.md)", "](page.md#all)", 1), "a #all marker moved"},
	} {
		if why := navMoved(s.Pages[tc.rel], s.renderPage(tc.rel, tc.body)); why != tc.want {
			t.Errorf("saving %s as %q: navMoved() = %q, want %q (%s)", tc.rel, tc.body, why, tc.want, tc.what)
		}
	}
	if why := navMoved(nil, s.renderPage("new.md", "# New\n")); why == "" {
		t.Error("a page joining the build moves nothing, but every sidebar could gain it")
	}
	if why := navMoved(s.Pages["page.md"], nil); why == "" {
		t.Error("a page leaving the build moves nothing, but its row is in every sidebar showing it")
	}
	// The narrow rule is only safe if rendering a page twice gives the same page
	// back. BuildAll renders every page through one shared goldmark instance and in
	// parallel, so heading ids carrying state across documents would make the page
	// differ from its own bytes and every save of it a nav move.
	for rel, p := range s.Pages {
		raw, err := os.ReadFile(filepath.Join(s.Src, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if why := navMoved(p, s.renderPage(rel, string(raw))); why != "" {
			t.Errorf("saving %s with its own bytes: navMoved() = %q, want the save to move nothing", rel, why)
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

// assetRefRe matches an asset URL a page points at: src="../_assets/site.js" from a
// nested page, src="_assets/site.js" from the root — a chrome URL and its page prefix.
var assetRefRe = regexp.MustCompile(`(?:src|href)="(?:\.\./)*(_assets/[^"]+)"`)

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

// searchURL is the one script a page names that colophon does not address: pagefind
// writes its own bundle, under its own names. Like every chrome URL a page gets it with
// its own Prefix in front.
const searchURL = "pagefind/pagefind-component-ui.js"

// assetURLt resolves one of the binary's assets to the URL a page's tag names it by,
// through the lookup the template calls. Editing a stylesheet moves the address, so the
// tests follow the assets rather than pinning a digest that moves with them.
func assetURLt(t *testing.T, name string) string {
	t.Helper()
	u, err := assetURL(name)
	if err != nil {
		t.Fatalf("assetURL(%q): %v", name, err)
	}
	return u
}

// writtenAssets are the site paths the embedded assets are expected at in the output
// tree.
func writtenAssets(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, name := range []string{"docs.css", "chroma.css", "site.js", "toc.js",
		"mermaid-viewer.js", "vendor/mermaid.min.js", "vendor/tex-svg.js"} {
		out = append(out, strings.TrimPrefix(assetURLt(t, name), "/"))
	}
	return out
}

// wantScripts are the scripts a page earns its tags on, from the same values wrapPage
// hands the template. Deriving them from the site rather than from the page's own bytes
// is what makes a tag the template dropped a failure: a page can be missing the spy it
// needs as quietly as it can carry one it has no rail for.
func wantScripts(t *testing.T, s *Site, p *Page, page string) []string {
	var want []string
	if s.Search {
		want = append(want, p.Prefix+searchURL)
	}
	if p.Flags.Math {
		want = append(want, p.Prefix+assetURLt(t, "vendor/tex-svg.js"))
	}
	if p.Flags.Mermaid {
		want = append(want, p.Prefix+assetURLt(t, "vendor/mermaid.min.js"), p.Prefix+assetURLt(t, "mermaid-viewer.js"))
	}
	want = append(want, p.Prefix+assetURLt(t, "site.js")) // the theme control and the mobile nav, one file for the whole site
	// The rail is toc.js's whole subject — its first line gives up without one — so the
	// tag follows the <nav> and no flag decides either of them.
	if strings.Contains(page, `<nav class="toc"`) {
		want = append(want, p.Prefix+assetURLt(t, "toc.js"))
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
		want := wantScripts(t, s, p, page)
		wantSrcs := slices.Clone(want)
		slices.Sort(wantSrcs)
		if gotSrcs := sortedSrcs(got); !slices.Equal(gotSrcs, wantSrcs) {
			t.Errorf("%s loads %v, want %v", rel, gotSrcs, wantSrcs)
		}

		// The one dependency among the scripts, and the reason the vendor file needs its
		// own tag rather than an onload handler: only deferred tags have a document order to
		// run in, and the viewer calls mermaid.initialize.
		if p.Flags.Mermaid {
			vendor := indexSrc(got, p.Prefix+assetURLt(t, "vendor/mermaid.min.js"))
			viewer := indexSrc(got, p.Prefix+assetURLt(t, "mermaid-viewer.js"))
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
		if tex := strings.Index(page, p.Prefix+assetURLt(t, "vendor/tex-svg.js")); p.Flags.Math && tex >= 0 && strings.Index(page, "window.MathJax") > tex {
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
		// The bundle styles :root directly, so docs.css — which maps the bundle's
		// --pf-* names onto the site's palette — must load after its link in the
		// head, or the pagefind modal keeps its light palette in a dark theme.
		page := string(b)
		linkAt := strings.Index(page, "pagefind/pagefind-component-ui.css")
		docsAt := strings.Index(page, strings.TrimPrefix(assetURLt(t, "docs.css"), "/"))
		if linkAt < 0 || docsAt < 0 {
			t.Fatal("the page loads no search stylesheet or no docs.css")
		}
		if linkAt > docsAt {
			t.Error("the search stylesheet loads after docs.css, so its :root palette wins against the site's")
		}
		if !strings.Contains(embeddedAsset(t, "docs.css"), "--pf-background: var(--bg)") ||
			!strings.Contains(embeddedAsset(t, "docs.css"), "--pf-mark: var(--fg)") {
			t.Error("docs.css maps no search UI colours onto the site's palette")
		}
	})
}

// Goes through WriteAssets rather than checkASCII alone: the vendored scripts hold
// literal glyphs and have to stay outside the rule.
func TestWriteAssetsRefusesNonASCIIScripts(t *testing.T) {
	out := t.TempDir()
	if err := WriteAssets(out); err != nil {
		t.Fatalf("WriteAssets: %v", err)
	}
	for _, name := range []string{"site.js", "mermaid-viewer.js", "vendor/mermaid.min.js"} {
		if !exists(out, strings.TrimPrefix(assetURLt(t, name), "/")) {
			t.Errorf("%s was not written at its addressed name", name)
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

// A content-addressed name is the whole cache-busting story: bytes that did not change
// must keep the address pages already link, and bytes that did must not keep it, or a
// reader's cache serves the file the build replaced. The suffix has to survive the
// rewrite, because a host maps a file to its media type from it.
func TestContentDigestAddressesBytes(t *testing.T) {
	first := contentDigest([]byte("body { color: red }\n"))
	if len(first) != 12 {
		t.Errorf("digest %q is not 12 hex characters", first)
	}
	if again := contentDigest([]byte("body { color: red }\n")); again != first {
		t.Errorf("the same bytes addressed twice: %q, then %q", first, again)
	}
	if moved := contentDigest([]byte("body { color: blue }\n")); moved == first {
		t.Errorf("different bytes share the address %q", moved)
	}
	for _, c := range []struct{ name, want string }{
		{"docs.css", "docs." + first + ".css"},
		{"vendor/mermaid.min.js", "vendor/mermaid.min." + first + ".js"},
	} {
		if got := digestPath(c.name, first); got != c.want {
			t.Errorf("digestPath(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// The config's logo, logo-dark and favicon are the site's own files, and colophon names one in
// every page header, so they are addressed like the binary's assets: a replaced file
// arrives at a URL no cache has ever held. A file deleted mid-session must not leave the
// site publishing an address with nothing behind it.
func TestHashedStaticsMoveWithTheirBytes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	logo := filepath.Join(src, "logo.svg")
	write := func(b []byte) {
		t.Helper()
		if err := os.WriteFile(logo, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "index.md"), []byte("# Home\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "logo-dark.svg"), []byte("<svg><rect/></svg>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write([]byte("<svg></svg>\n"))
	conf := filepath.Join(dir, "colophon.conf")
	if err := os.WriteFile(conf, []byte("source: source\nlogo: logo.svg\nlogo-dark: logo-dark.svg\nfavicon: logo.svg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSite(conf, "")
	if err != nil {
		t.Fatalf("NewSite: %v", err)
	}
	// One file named twice is one address, and the address still says what it holds.
	if s.Logo != s.Favicon || !strings.HasPrefix(s.Logo, assetDir+"/logo.") || !strings.HasSuffix(s.Logo, ".svg") {
		t.Fatalf("logo %q, favicon %q: want one content-addressed %s/logo.<digest>.svg", s.Logo, s.Favicon, assetDir)
	}
	first := s.Logo
	a, ok := s.requestAsset(first)
	if !ok {
		t.Fatalf("%s resolves to nothing", first)
	}
	if string(a.b) != "<svg></svg>\n" {
		t.Errorf("%s resolves to the wrong bytes", first)
	}
	// logo-dark is its own address with its own bytes, and the brand gains the
	// class the dark theme blocks need to swap one img for the other.
	if s.LogoDark == s.Logo || !strings.HasPrefix(s.LogoDark, assetDir+"/logo-dark.") {
		t.Errorf("logo-dark %q, want its own %s/logo-dark.<digest>.svg", s.LogoDark, assetDir)
	}
	if a, ok := s.requestAsset(s.LogoDark); !ok || string(a.b) != "<svg><rect/></svg>\n" {
		t.Errorf("%s does not resolve to its bytes", s.LogoDark)
	}
	if !s.isHashedStatic("logo-dark.svg") {
		t.Error("logo-dark is no hashed static, so saving it rebuilds nothing")
	}
	if err := s.BuildAll(); err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	page, err := s.wrapPage(s.Pages["index.md"])
	if err != nil {
		t.Fatalf("wrapPage: %v", err)
	}
	if !strings.Contains(string(page), `class="brand brand-themed"`) ||
		!strings.Contains(string(page), `<img class="brand-dark" src="`+s.LogoDark) {
		t.Error("the brand does not name the dark logo the theme blocks swap to")
	}
	out := t.TempDir()
	if err := s.writeStatics(out); err != nil {
		t.Fatalf("writeStatics: %v", err)
	}
	if !exists(out, first) {
		t.Errorf("%s was not written", first)
	}

	// A save moves the address, and the one the open pages hold stops resolving: serving
	// new bytes behind the old name is the staleness the address exists to prevent, and a
	// page still holding the old name is one reload away from asking for the new one.
	write([]byte("<svg><circle/></svg>\n"))
	if err := s.hashedStatics(); err != nil {
		t.Fatalf("hashedStatics: %v", err)
	}
	second := s.Logo
	if second == first {
		t.Fatal("a changed logo kept its address")
	}
	if _, ok := s.requestAsset(first); ok {
		t.Errorf("%s still resolves, so new bytes could be served under the name of the old ones", first)
	}
	if !s.isHashedStatic("logo.svg") {
		t.Error("the config logo is no hashed static, so saving it rebuilds nothing")
	}
	if s.isHashedStatic("index.md") {
		t.Error("a page is a hashed static")
	}

	// A deletion is reported and changes nothing, which leaves every page naming an
	// address that still has a file behind it.
	if err := os.Remove(logo); err != nil {
		t.Fatal(err)
	}
	if err := s.hashedStatics(); err == nil {
		t.Error("a deleted logo raised no error")
	}
	if strings.TrimPrefix(s.Logo, "/") != second {
		t.Errorf("a deleted logo republished %q, want the site to stay on %s", s.Logo, second)
	}
}

// An addressed asset is worth caching for a year, because nothing can change behind its
// name. Everything a rebuild can move under an unchanged name is not cacheable at all.
func TestAssetHeaders(t *testing.T) {
	s := &Site{}
	upath := strings.TrimPrefix(assetURLt(t, "docs.css"), "/")
	a, ok := s.requestAsset(upath)
	if !ok {
		t.Fatalf("%s resolves to nothing", upath)
	}
	rec := httptest.NewRecorder()
	serveAsset(rec, upath, a)
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("%s served with Cache-Control %q, want it immutable", upath, got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Errorf("%s served as %q", upath, got)
	}
	if want := embeddedAsset(t, "docs.css"); rec.Body.String() != want {
		t.Errorf("%s served %d bytes, want the %d the binary holds", upath, rec.Body.Len(), len(want))
	}
	// A name with no digest in it addresses no bytes, so nothing can be served for it.
	if _, ok := s.requestAsset(assetDir + "/docs.css"); ok {
		t.Error("an unaddressed name resolved")
	}

	// pagefind names its own bundle, and those names do not address their contents, so
	// one run's files must not be kept for the next.
	rec = httptest.NewRecorder()
	noStore(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", "/pagefind/pagefind.js", nil))
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("/pagefind/ served with Cache-Control %q, want no-store", got)
	}

	// An SVG carries no magic bytes a sniffer could name, so its type has to come
	// from the extension: served as sniffed text/plain, a config logo is an image
	// no browser will render, and the brand shows only its alt text.
	rec = httptest.NewRecorder()
	serveAsset(rec, assetDir+"/logo.deadbeefdeadbeef.svg", assetFile{b: []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>")})
	if got := rec.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Errorf("an svg asset served as %q, want image/svg+xml", got)
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
