package main

import (
	"fmt"
	"html"
	"html/template"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type node struct {
	Rel      string
	Label    string // sidebar text when the node is a heading, not a page
	Href     string // target when the node carries a fragment
	Children []*node
}

func fileExists(src, rel string) bool {
	_, err := os.Stat(filepath.Join(src, filepath.FromSlash(rel)))
	return err == nil
}

// buildNav walks the root index's `nav: list` children recursively,
// recording for each page its ancestor chain, the DFS order (prev/next) and
// the tree itself.
func (s *Site) buildNav() {
	s.chain = map[string][]string{}
	root, ok := s.Pages["index.md"]
	if !ok {
		return
	}
	visited := map[string]bool{"index.md": true}
	var build func(rel string, chain []string) *node
	build = func(rel string, chain []string) *node {
		if visited[rel] {
			return nil
		}
		visited[rel] = true
		n := &node{Rel: rel}
		s.chain[rel] = chain
		p, ok := s.Pages[rel]
		if !ok {
			return n
		}
		childChain := append(append([]string{}, chain...), rel)
		for _, k := range p.NavChildren {
			if c := build(k, childChain); c != nil {
				n.Children = append(n.Children, c)
			}
		}
		// A page also parents its own top-level sections. They carry no Page, so
		// they stay out of prev/next and out of the chain.
		for _, hd := range p.sectionChildren() {
			n.Children = append(n.Children, &node{
				Rel:   rel + "#" + hd.ID,
				Label: hd.Text,
				Href:  "/" + outPath(rel) + "#" + hd.ID,
			})
		}
		return n
	}
	var roots []*node
	for _, k := range root.NavChildren {
		if c := build(k, nil); c != nil {
			roots = append(roots, c)
		}
	}
	s.roots = roots
	var dfs func(n *node)
	dfs = func(n *node) {
		if p, ok := s.Pages[n.Rel]; ok {
			s.flat = append(s.flat, p)
		}
		for _, c := range n.Children {
			dfs(c)
		}
	}
	for _, r := range roots {
		dfs(r)
	}
	s.prev = map[string]*Page{}
	s.next = map[string]*Page{}
	for i, p := range s.flat {
		if i > 0 {
			s.prev[p.Rel] = s.flat[i-1]
		}
		if i+1 < len(s.flat) {
			s.next[p.Rel] = s.flat[i+1]
		}
	}
}

// renderSidebar shows the root sections; the active section's subtree is
// expanded along the current page's ancestor chain.
func (s *Site) renderSidebar(p *Page) string {
	var b strings.Builder
	chain := map[string]bool{}
	for _, a := range s.chain[p.Rel] {
		chain[a] = true
	}
	var renderNode func(n *node, active string)
	renderNode = func(n *node, active string) {
		title, href := n.Rel, "/"+outPath(n.Rel)
		if n.Label != "" {
			title, href = n.Label, n.Href
		} else if pp, ok := s.Pages[n.Rel]; ok {
			title = pp.Title
		}
		cls := ""
		if n.Rel == active {
			cls = ` class="active"`
		}
		fmt.Fprintf(&b, `<li><a href="%s"%s>%s</a>`, html.EscapeString(href), cls, html.EscapeString(title))
		if (chain[n.Rel] || n.Rel == active) && len(n.Children) > 0 {
			b.WriteString("<ul>")
			for _, c := range n.Children {
				renderNode(c, active)
			}
			b.WriteString("</ul>")
		}
		b.WriteString("</li>")
	}
	b.WriteString(`<ul>`)
	for _, r := range s.roots {
		renderNode(r, p.Rel)
	}
	b.WriteString(`</ul>`)
	return b.String()
}

// navSectionItemRe matches a rendered list item that is nothing but a link to a
// built page, which is what an item of a `nav: list` renders to.
var navSectionItemRe = regexp.MustCompile(`<li><a[^>]*href="([^"]+\.html)"[^>]*>[^<]*</a></li>`)

// navSectionLists lists a child page's top-level sections as a sub-list under the list
// item that links to it. Only items the nav took as children are touched: a sub-list an
// author wrote by hand broke the all-links rule, so that one is prose and stays as
// written. The generated items reuse the item's own href with a heading anchor, so they
// stay file-relative like every other page link.
func (s *Site) navSectionLists(p *Page, body string) string {
	sections := map[string][]heading{}
	for _, rel := range p.NavChildren {
		c := s.Pages[rel]
		if c == nil {
			continue
		}
		secs := c.sectionChildren()
		if p.NavAll[rel] {
			secs = c.everySection()
		}
		if len(secs) > 0 {
			sections[rel] = secs
		}
	}
	if len(sections) == 0 {
		return body
	}
	dir := path.Dir(p.Rel)
	return navSectionItemRe.ReplaceAllStringFunc(body, func(item string) string {
		href := navSectionItemRe.FindStringSubmatch(item)[1]
		secs, ok := sections[srcRel(dir, strings.TrimSuffix(href, ".html")+".md")]
		if !ok {
			return item
		}
		var b strings.Builder
		b.WriteString(strings.TrimSuffix(item, "</li>"))
		b.WriteString(`<ul class="nav-sections">`)
		for _, hd := range secs {
			fmt.Fprintf(&b, `<li><a href="%s#%s">%s</a></li>`, html.EscapeString(href), hd.ID, html.EscapeString(hd.Text))
		}
		b.WriteString("</ul></li>")
		return b.String()
	})
}

func (s *Site) renderCrumbs(p *Page) string {
	var parts []string
	parts = append(parts, fmt.Sprintf(`<a href="/index.html">%s</a>`, html.EscapeString(s.HomeLabel)))
	for _, a := range s.chain[p.Rel] {
		if a == p.Rel {
			continue // index page: it appears as the plain tail below
		}
		title := a
		if pp, ok := s.Pages[a]; ok {
			title = pp.Title
		}
		parts = append(parts, fmt.Sprintf(`<a href="/%s">%s</a>`, html.EscapeString(outPath(a)), html.EscapeString(title)))
	}
	// the current page closes the trail, unlinked
	parts = append(parts, fmt.Sprintf(`<span class="here">%s</span>`, html.EscapeString(p.Title)))
	return strings.Join(parts, `<span class="sep">&gt;</span>`)
}

func (s *Site) renderPrevNext(p *Page) string {
	var b strings.Builder
	if pv, ok := s.prev[p.Rel]; ok && pv != nil {
		fmt.Fprintf(&b, `<a class="prev" href="/%s">← %s</a>`, html.EscapeString(pv.OutPath), html.EscapeString(pv.Title))
	}
	if nx, ok := s.next[p.Rel]; ok && nx != nil {
		fmt.Fprintf(&b, `<a class="next" href="/%s">%s →</a>`, html.EscapeString(nx.OutPath), html.EscapeString(nx.Title))
	}
	return b.String()
}

// pageT wraps a built page in the site template.
// tocRe captures h1/h2/h3 headings with level, id and label (the label stops at
// the heading-anchor link the renderer appends); tocH23 is the outline of a page
// with no sections of its own, which takes h1s alone to be its title.
var tocRe = regexp.MustCompile(`(?s)<h([123]) id="([^"]+)">(.*?)<a class="heading-anchor"`)
var tocH23 = regexp.MustCompile(`(?s)<h([23]) id="([^"]+)">(.*?)<a class="heading-anchor"`)

// renderTOC builds the right-hand "On this page" rail from the page's
// headings; fewer than two headings means no rail. Labels are the
// already-sanitized rendered HTML.
func (s *Site) renderTOC(p *Page) string {
	ms := tocH23.FindAllSubmatch(p.Body, -1)
	if len(p.sectionChildren()) > 0 {
		// The page's own sections belong in the outline, and the heading the title
		// comes from does not, because the page header already shows it.
		title := p.titleHeading()
		all := tocRe.FindAllSubmatch(p.Body, -1)
		kept := all[:0]
		for _, m := range all {
			if string(m[2]) != title {
				kept = append(kept, m)
			}
		}
		ms = kept
	}
	if len(ms) < 2 {
		return ""
	}
	var items []string
	for _, m := range ms {
		cls := ""
		if string(m[1]) == "3" {
			cls = ` class="h3"`
		}
		label := strings.TrimSpace(anchorRe.ReplaceAllString(string(m[3]), ""))
		items = append(items, fmt.Sprintf(`<a%s href="#%s">%s</a>`, cls, html.EscapeString(string(m[2])), label))
	}
	return `<p class="toc-title">On this page</p>` + strings.Join(items, "")
}

// accentCSS renders the config's accent overrides for the theme's three
// selector contexts; empty when no accent key is configured (docs.css
// defaults apply). Values are hex-validated in NewSite, so interpolation
// is safe.
func (s *Site) accentCSS() string {
	if s.Accent == "" && s.AccentDark == "" {
		return ""
	}
	var b strings.Builder
	if s.Accent != "" {
		fmt.Fprintf(&b, ":root, [data-theme=\"light\"] { --accent: %s; }\n", s.Accent)
		fmt.Fprintf(&b, "@media (prefers-color-scheme: dark) { :root:not([data-theme=\"light\"]) { --accent: %s; } }\n", s.Accent)
		fmt.Fprintf(&b, "[data-theme=\"dark\"] { --accent: %s; }\n", s.Accent)
	}
	if s.AccentDark != "" {
		fmt.Fprintf(&b, "@media (prefers-color-scheme: dark) { :root:not([data-theme=\"light\"]) { --accent: %s; } }\n", s.AccentDark)
		fmt.Fprintf(&b, "[data-theme=\"dark\"] { --accent: %s; }\n", s.AccentDark)
	}
	return b.String()
}

func (s *Site) wrapPage(p *Page) ([]byte, error) {
	data := pageData{
		Title:     p.Title,
		SiteTitle: s.SiteTitle,
		Copyright: s.Copyright,
		EditBase:  s.EditBase,
		Logo:      s.Logo,
		Favicon:   s.Favicon,
		Content:   template.HTML(p.Body),
		Sidebar:   template.HTML(s.renderSidebar(p)),
		Crumbs:    template.HTML(s.renderCrumbs(p)),
		TOC:       template.HTML(s.renderTOC(p)),
		AccentCSS: template.CSS(s.accentCSS()),
		Search:    s.Search,
		PrevNext:  template.HTML(s.renderPrevNext(p)),
		Math:      p.Flags.Math,
		Mermaid:   p.Flags.Mermaid,
		Dev:       s.Dev,
		EditPath:  p.Rel,
	}
	var buf strings.Builder
	if err := pageT.Execute(&buf, data); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// WriteOut writes the rendered site plus embedded assets to out, and copies
// the non-Markdown source tree (statics, images, PDFs) verbatim.
func (s *Site) WriteOut(out string) error {
	if err := WriteAssets(out); err != nil {
		return err
	}
	if err := s.writePages(out); err != nil {
		return err
	}
	return copyStatics(s.Src, out)
}

// writePages renders every page into out without touching assets or
// statics; the pagefind pass writes HTML this way before indexing.
func (s *Site) writePages(out string) error {
	for _, p := range s.Pages {
		page, err := s.wrapPage(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(out, filepath.FromSlash(p.OutPath))
		os.MkdirAll(filepath.Dir(dst), 0o755)
		if err := os.WriteFile(dst, page, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func copyStatics(src, out string) error {
	return filepath.Walk(src, func(p string, i os.FileInfo, err error) error {
		if err != nil || i.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(rel, ".md") || strings.HasSuffix(rel, ".py") ||
			strings.Contains(rel, "__pycache__") || strings.HasPrefix(rel, "extensions/") ||
			strings.HasPrefix(rel, "_templates/") {
			return nil
		}
		dst := filepath.Join(out, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(f, in)
		return err
	})
}
