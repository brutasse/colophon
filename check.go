package main

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	hrefRe    = regexp.MustCompile(`href="([^"]*)"`)
	dashRunRe = regexp.MustCompile(`-{2,}`)
	// source-level scans for the link-portability contract
	mdDestRe  = regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	refDefRe  = regexp.MustCompile(`(?m)^\s{0,3}\[[^\]]+\]:\s*(\S+)`)
	rawAttrRe = regexp.MustCompile(`(?:href|src)="([^"]+)"`)
	urlBareRe = regexp.MustCompile(`(https?://[^\s)<>"']+)`)
	fenceRe   = regexp.MustCompile(`^\s*(` + "`" + `{3,}|~{3,})`)
	// a list item that is exactly one link to a section of a page, which reads to
	// its author like a nav entry and to colophon like prose
	navAnchorItemRe = regexp.MustCompile(`(?m)^[ \t]*[-*][ \t]+\[[^\]]*\]\([^)\s]+\.md#([^)\s]+)\)[ \t]*$`)
)

// linkStyle reports one source href that renders fine but is not
// GitHub-navigable. The contract: file-relative .md (+ optional #fragment);
// the builder owns the .md -> .html rewrite at render.
func (s *Site) linkStyle(p *Page, byOut map[string]*Page, href string) string {
	if strings.Contains(href, "://") || strings.HasPrefix(href, "//") {
		return s.selfLinkStyle(p, byOut, href)
	}
	if href == "" || href == "#" || strings.HasPrefix(href, "#") ||
		strings.HasPrefix(href, "//") || strings.Contains(href, "://") ||
		strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "tel:") {
		return ""
	}
	dest := href
	if i := strings.IndexByte(dest, '#'); i >= 0 {
		dest = dest[:i]
	}
	if dest == "" {
		return ""
	}
	abs := strings.HasPrefix(dest, "/")
	cand := strings.TrimPrefix(dest, "/")
	if !abs {
		cand = path.Join(path.Dir(p.Rel), dest)
	}
	cand = path.Clean(cand)
	if strings.HasPrefix(cand, "..") {
		return ""
	}
	switch {
	case strings.HasSuffix(dest, ".md"):
		if abs {
			return "root-absolute page link; use a file-relative .md path"
		}
		return "" // compliant; resolution is covered by the render-time check
	case strings.HasSuffix(dest, ".html"):
		if _, ok := byOut[cand]; ok {
			return "link to a built page uses .html; use .md"
		}
		return "" // a .html matching no page is already a broken-link error
	default:
		why := ""
		if _, ok := byOut[cand+".html"]; ok {
			why = "extensionless page link; add .md"
		} else if _, ok := byOut[path.Join(cand, "index.html")]; ok {
			why = "extensionless page link; add .md"
		} else if abs && fileExists(s.Src, cand) {
			return "root-absolute asset link; use a file-relative path"
		} else {
			return ""
		}
		if abs {
			return "root-absolute page link; use a file-relative .md path"
		}
		return why
	}
}

// selfLinkStyle flags absolute links pointing at this very site (host match
// against base-url): they bypass link checking, break local/preview builds
// and GitHub navigation. Suggests the relative .md replacement when the
// target resolves to a page.
func (s *Site) selfLinkStyle(p *Page, byOut map[string]*Page, href string) string {
	if s.BaseHost == "" {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil || !strings.EqualFold(u.Host, s.BaseHost) {
		return ""
	}
	if sug := s.siteLinkSuggestion(p, byOut, u); sug != "" {
		return "link to this site; use a relative .md path (" + sug + ")"
	}
	return "link to this site; use a relative .md path"
}

func (s *Site) siteLinkSuggestion(p *Page, byOut map[string]*Page, u *url.URL) string {
	cand := strings.TrimPrefix(u.Path, "/")
	if cand == "" {
		cand = "index.html"
	}
	var srcRel string
	switch {
	case strings.HasSuffix(cand, ".html"):
		base := strings.TrimSuffix(cand, ".html")
		if pp, ok := byOut[base+".html"]; ok {
			srcRel = pp.Rel
		} else if pp, ok := byOut[path.Join(base, "index.html")]; ok {
			srcRel = pp.Rel
		} else if fileExists(s.Src, base+".md") {
			srcRel = base + ".md"
		}
	default:
		switch {
		case fileExists(s.Src, cand+".md"):
			srcRel = cand + ".md"
		case fileExists(s.Src, path.Join(cand, "index.md")):
			srcRel = path.Join(cand, "index.md")
		}
	}
	// A fragment is inlined, never built: linking to it as a page is the bug, so
	// never suggest it.
	if srcRel == "" || s.isFragment(srcRel) {
		return ""
	}
	rel, err := filepath.Rel(path.Dir(p.Rel), srcRel)
	if err != nil {
		return ""
	}
	if !strings.HasPrefix(rel, ".") {
		rel = "./" + rel
	}
	if u.Fragment != "" {
		rel += "#" + u.Fragment
	}
	return rel
}

// styleProblems applies linkStyle to every href in a page's pre-render
// markdown. Fenced blocks are skipped. Tag-gated subtrees are upstream-owned
// and exempt.
func (s *Site) styleProblems(p *Page, byOut map[string]*Page) (errs []string) {
	if s.upstreamOwned(p.Rel) {
		return nil
	}
	spans := fenceSpans(p.Src)
	var covered [][2]int
	scan := func(re *regexp.Regexp, record bool) {
		for _, m := range re.FindAllSubmatchIndex(p.Src, -1) {
			if inSpan(spans, m[0]) || (record == false && inSpan(covered, m[0])) {
				continue
			}
			if record {
				covered = append(covered, [2]int{m[2], m[3]})
			}
			if why := s.linkStyle(p, byOut, string(p.Src[m[2]:m[3]])); why != "" {
				errs = append(errs, fmt.Sprintf("%s: non-portable link %s: %s", p.Rel, string(p.Src[m[2]:m[3]]), why))
			}
		}
	}
	scan(mdDestRe, true)
	scan(refDefRe, true)
	scan(rawAttrRe, true)
	scan(urlBareRe, false)
	// A `nav: list` page gives children to a top-level list only while every item of
	// it is one plain [text](page.md) link, so an item linking to a section of a page
	// costs that whole list its children, and the pages beside it lose their place in
	// the navigation with nothing to report. `#all` is the exception an author reaches
	// for on purpose: it names that page's sections as the list under its item.
	if p.Meta["nav"] == "list" {
		for _, m := range navAnchorItemRe.FindAllSubmatchIndex(p.Src, -1) {
			if inSpan(spans, m[0]) || string(p.Src[m[2]:m[3]]) == "all" {
				continue // `#all` is the marker a section listing asks for
			}
			errs = append(errs, fmt.Sprintf("%s: nav list item %s links to a section, not a page: children come only from items that are one plain [text](page.md) link, or one ending in #all to list the page's sections under it",
				p.Rel, strings.TrimSpace(string(p.Src[m[0]:m[1]]))))
		}
	}
	return errs
}

func fenceSpans(raw []byte) [][2]int {
	var spans [][2]int
	open := -1
	off := 0
	for _, line := range bytes.Split(raw, []byte("\n")) {
		end := off + len(line) + 1
		if fenceRe.Match(line) {
			if open < 0 {
				open = off
			} else {
				spans = append(spans, [2]int{open, end})
				open = -1
			}
		}
		off = end
	}
	if open >= 0 {
		spans = append(spans, [2]int{open, len(raw)})
	}
	return spans
}

func inSpan(spans [][2]int, pos int) bool {
	for _, s := range spans {
		if pos >= s[0] && pos < s[1] {
			return true
		}
	}
	return false
}

// pageProblems checks every internal link on a wrapped page. It returns
// hard errors plus "rewrite hints": for a missing anchor whose fragment
// matches a real one once hyphen runs are collapsed on both sides, the
// fragment to write instead. petroglyph builds ids the way GitHub does, so a
// fragment carried over from the old collapsing slug differs from the target
// only by those runs.
func (s *Site) pageProblems(p *Page, byOut map[string]*Page) (errs, hints []string) {
	errs = append(errs, s.styleProblems(p, byOut)...)
	page, err := s.wrapPage(p)
	if err != nil {
		return append(errs, p.Rel+": wrap: "+err.Error()), nil
	}
	for _, m := range hrefRe.FindAllStringSubmatch(string(page), -1) {
		href := m[1]
		var target *Page
		var frag string
		if strings.HasPrefix(href, "#") {
			if href == "#" {
				continue
			}
			target, frag = p, urlDecode(href[1:])
		} else {
			t, f, ok := s.resolveHref(p.Rel, href, byOut)
			if !ok {
				continue // external / asset / non-page link
			}
			if t == nil {
				errs = append(errs, fmt.Sprintf("%s: broken link %s", p.Rel, href))
				continue
			}
			target, frag = t, f
		}
		if frag == "" || target.IDs[frag] {
			continue
		}
		if cand := dashRunRe.ReplaceAllString(frag, "-"); target.IDs[cand] {
			hints = append(hints, fmt.Sprintf("%s: %s -> %s", p.Rel, href, cand))
		} else if cand := dashCollapseHint(target, frag); cand != "" {
			hints = append(hints, fmt.Sprintf("%s: %s -> %s", p.Rel, href, cand))
		}
		errs = append(errs, fmt.Sprintf("%s: missing anchor %s", p.Rel, href))
	}
	return errs, hints
}

// dashCollapseHint names the fragment on target that a collapsed fragment was
// meant to reach: "#foo-bar-baz" against the id "foo-bar--baz". It stays quiet
// when more than one id would match, because then there is no single fragment
// to suggest.
func dashCollapseHint(target *Page, frag string) string {
	want := dashRunRe.ReplaceAllString(frag, "-")
	found := ""
	for id := range target.IDs {
		if id == frag || dashRunRe.ReplaceAllString(id, "-") != want {
			continue
		}
		if found != "" && found != id {
			return ""
		}
		found = id
	}
	return found
}

// inNav reports whether rel is in the nav tree built from the root index's
// `nav: list` chain. The sidebar, the breadcrumbs, and prev/next all derive
// from that tree, so a page outside it has none of the three. Root sections
// are stored with a nil chain, so only presence in the map counts.
func (s *Site) inNav(rel string) bool {
	_, ok := s.chain[rel]
	return ok
}

// orphanReasons maps every built page no reader can reach to the reason to
// report it. A page is reachable when it is in the nav tree or when another
// page's own body links it; the body is scanned as p.Raw, since a wrapped page
// carries the sidebar, which would make every nav page look linked. A page
// whose whole section is unlisted is reported once, on the section index whose
// nav entry is missing, because that index's own list links everything below it.
func (s *Site) orphanReasons(byOut map[string]*Page) map[string]string {
	if _, ok := s.Pages["index.md"]; !ok {
		return map[string]string{"index.md": "no root index.md: the navigation has no root, so no page can be in it"}
	}
	linked := map[string]bool{}
	for _, p := range s.Pages {
		for _, m := range hrefRe.FindAllStringSubmatch(string(p.Raw), -1) {
			if t, _, ok := s.resolveHref(p.Rel, m[1], byOut); ok && t != nil && t.Rel != p.Rel {
				linked[t.Rel] = true
			}
		}
	}
	unreachable := map[string]bool{}
	for rel, p := range s.Pages {
		switch {
		case rel == "index.md", linked[rel], s.inNav(rel):
		// A fragment is inlined into other pages and never built, so declaring
		// one already implies `orphan: true`.
		case metaYes(p.Meta["orphan"]), metaYes(p.Meta["fragment"]):
		default:
			unreachable[rel] = true
		}
	}
	reasons := map[string]string{}
	for rel := range unreachable {
		// A file an include comment already inlines is a fragment the source
		// forgot to declare, and adding a nav entry is not the fix: the prose
		// would reach readers twice over. The lowest referrer keeps the report
		// stable when several pages inline the same fragment.
		if refs := s.includeReferrerAll(rel); len(refs) > 0 {
			sort.Strings(refs)
			reasons[rel] = "undeclared fragment: an include comment in " + refs[0] +
				" inlines this file, so declare `fragment: true` to keep it out of the build"
			continue
		}
		msg := "orphan page: no nav: list contains it and no page links to it, so it stays out of the sidebar and prev/next"
		if idx := path.Join(path.Dir(rel), "index.md"); idx != "index.md" && unreachable[idx] {
			msg += " (" + idx + " is not in the navigation either: start there)"
		}
		reasons[rel] = msg
	}
	return reasons
}

// orphanProblems renders orphanReasons as check problems. Pages of an
// upstream-owned subtree are informational here as for every other rule.
func (s *Site) orphanProblems(byOut map[string]*Page) (errs, info []string) {
	reasons := s.orphanReasons(byOut)
	rels := make([]string, 0, len(reasons))
	for rel := range reasons {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		msg := rel + ": " + reasons[rel]
		if s.upstreamOwned(rel) {
			info = append(info, msg)
		} else {
			errs = append(errs, msg)
		}
	}
	return errs, info
}

// collectLinkProblems wraps every page and verifies its links: pages exist and
// fragments resolve, plus the source-level link-portability rules, plus whether
// every built page is reachable at all. Problems on tag-gated subtrees are
// split into info: those trees are copied verbatim from upstream repos, so
// their errors are reported but never gate.
func (s *Site) collectLinkProblems() (errs, info, hints []string) {
	byOut := map[string]*Page{}
	for _, p := range s.Pages {
		byOut[p.OutPath] = p
	}
	rels := make([]string, 0, len(s.Pages))
	for r := range s.Pages {
		rels = append(rels, r)
	}
	sort.Strings(rels)
	for _, r := range rels {
		e, h := s.pageProblems(s.Pages[r], byOut)
		if s.upstreamOwned(r) {
			info = append(info, e...)
		} else {
			errs = append(errs, e...)
		}
		hints = append(hints, h...)
	}
	// Reachability is a property of the whole link graph, so it is assessed once
	// all pages and nav entries are known.
	oe, oi := s.orphanProblems(byOut)
	return append(errs, oe...), append(info, oi...), hints
}

// Lint surfaces nav and link problems after a build without failing it: a build
// should stay usable; build --check is the gate.
func (s *Site) Lint() {
	errs, info, _ := s.collectLinkProblems()
	all := append(append([]string{}, errs...), info...)
	for _, e := range all {
		fmt.Fprintln(os.Stderr, "warning:", e)
	}
	if len(all) > 0 {
		fmt.Fprintf(os.Stderr, "nav and link problems: %d (run colophon build --check for the full report)\n", len(all))
	}
}

// RunCheck is the CI gate invoked by build --check: broken or non-portable
// links, missing anchors, and pages no reader can reach. Only repo-native
// pages gate; problems in tag-gated subtrees are printed as informational.
// Exit 1 on any gating problem.
func (s *Site) RunCheck() {
	errs, info, hints := s.collectLinkProblems()

	fmt.Printf("check: %d pages, %d errors, %d informational (%s)\n",
		len(s.Pages), len(errs), len(info), s.gatedDirs())
	for _, e := range errs {
		fmt.Println(" ", e)
	}
	if len(info) > 0 {
		fmt.Printf("  informational (%s, fix upstream):\n", s.gatedDirs())
		for _, e := range info {
			fmt.Println(" ", e)
		}
	}
	if len(hints) > 0 {
		sort.Strings(hints)
		fmt.Println("  dash-collapse rewrites (old fragment -> new slug):")
		seen := map[string]bool{}
		for _, h := range hints {
			if !seen[h] {
				seen[h] = true
				fmt.Println(" ", h)
			}
		}
	}
	if len(errs) > 0 {
		os.Exit(1)
	}
}

func urlDecode(s string) string {
	if d, err := url.PathUnescape(s); err == nil {
		return d
	}
	return s
}

// gatedDirs names the tag-declared subtrees, for check output that must say
// which tree a problem belongs to.
func (s *Site) gatedDirs() string {
	if len(s.TagDirs) == 0 {
		return "no gated subtrees"
	}
	dirs := make([]string, 0, len(s.TagDirs))
	for _, d := range s.TagDirs {
		dirs = append(dirs, d+"/")
	}
	sort.Strings(dirs)
	return strings.Join(dirs, ",")
}

// resolveHref classifies an href. ok=false means "not a site-internal page
// reference" (external URL, asset, bare anchor is checked separately).
func (s *Site) resolveHref(fromRel, href string, byOut map[string]*Page) (*Page, string, bool) {
	if href == "" || strings.HasPrefix(href, "#") {
		return nil, "", false
	}
	u, err := url.Parse(href)
	if err != nil || u.Scheme != "" || strings.HasPrefix(href, "//") {
		return nil, "", false // external, non-http scheme, or unparseable
	}
	target, frag := u.Path, u.Fragment
	if strings.HasPrefix(target, "/_assets/") || target == "/_assets" || target == "/sse" {
		return nil, "", false
	}
	var candidate string
	if strings.HasPrefix(target, "/") {
		candidate = strings.TrimPrefix(target, "/")
	} else {
		candidate = path.Join(path.Dir(fromRel), target)
	}
	if candidate == "" {
		candidate = "index.html"
	}
	candidate = path.Clean(candidate)
	if p, ok := byOut[candidate]; ok {
		return p, frag, true
	}
	// extensionless page and dir/index conventions
	if !strings.HasSuffix(candidate, ".html") && !strings.HasSuffix(candidate, ".md") {
		if p, ok := byOut[candidate+".html"]; ok {
			return p, frag, true
		}
		if p, ok := byOut[path.Join(candidate, "index.html")]; ok {
			return p, frag, true
		}
		if !strings.Contains(candidate, ".") {
			// a bare path with no extension and no index: a page that must exist
			return nil, frag, true
		}
	}
	// .md survived rewriting (should not happen) or an unknown dotted file
	if strings.HasSuffix(candidate, ".md") || !strings.Contains(candidate, ".") {
		return nil, frag, true
	}
	if strings.HasSuffix(candidate, ".html") {
		// An internal page reference that no built page matches. If the source
		// .md exists, the page is tag-gated out of this build, not broken.
		base := strings.TrimSuffix(candidate, ".html")
		if fileExists(s.Src, base+".md") && !s.isFragment(base+".md") {
			return nil, "", false
		}
		return nil, frag, true
	}
	// non-page file targets (_static, pdfs...) — existence not checked here
	return nil, "", false
}
