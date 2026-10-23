package main

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brutasse/petroglyph"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/text"
)

type heading struct {
	Level int
	ID    string
	Text  string
}

type Page struct {
	Rel         string // source-relative path, e.g. runbooks/foo.md
	OutPath     string // site-relative output, e.g. runbooks/foo.html
	Title       string
	Meta        map[string]string
	Headings    []heading
	IDs         map[string]bool // every fragment target in the rendered body: ids and <a name> anchors
	Flags       petroglyph.Flags
	Src         []byte          // preprocessed markdown body (pre-render), for source-level checks
	Raw         []byte          // sanitized body, before the page TOC is inserted
	Body        []byte          // final inner <main> HTML (after finalize)
	TOCDepth    int             // front-matter toc: N (-1 absent, 0 = all levels)
	Includes    map[string]bool // fragment sources inlined into this page
	NavChildren []string        // child pages from all-md-link lists in `nav: list` pages
	// NavAll marks the children whose link ended in `#all`, which says every h1 of
	// that page is a document: the sub-list under the item then includes the heading
	// the page's title came from, not only the ones after it.
	NavAll map[string]bool
}

type Site struct {
	Src        string
	Tags       map[string]bool   // tag names enabled via -tags
	TagDirs    map[string]string // declared tag name → gated source dir; empty unless the tags key declares one
	SiteTitle  string
	HomeLabel  string // opening breadcrumb text
	EditBase   string
	BaseHost   string // host of base-url, for detecting self-referential links
	Copyright  string // "{year}" already substituted; empty = no footer line
	Logo       string // root-absolute URL; empty = brand is the site title text
	Favicon    string // root-absolute URL; empty = no favicon link
	Accent     string // hex override of the light --accent; empty = docs.css default
	AccentDark string // hex override of the dark --accent; empty = docs.css default
	Search     bool   // pagefind bundle built; gates the search UI markup
	Dev        bool

	md     goldmark.Markdown
	policy *bluemonday.Policy // petroglyph DisplayPolicy + configured iframe hosts

	Pages map[string]*Page
	roots []*node             // top-level nav sections
	chain map[string][]string // page rel → ancestor chain (root section first)
	flat  []*Page             // DFS order over the nav tree (prev/next)
	prev  map[string]*Page
	next  map[string]*Page

	fragMu sync.Mutex
	frags  map[string]bool // source rel → declared `fragment: true`
}

// loadConf reads the flat "key: value" site config (same dialect as page
// front matter). The file is the entry point every build runs from, so its
// absence is an error rather than a site of all defaults; malformed contents
// surface as errors too.
func loadConf(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%s not found: write that file to build a site here (it must declare source: <dir>)", path)
	}
	if err != nil {
		return nil, err
	}
	conf, _ := rawFrontMatter("---\n" + string(b) + "\n---\n")
	return conf, nil
}

// sourceDir resolves the source key against the directory holding the config
// file, so a run from anywhere builds the site that file describes. The
// directory is checked here rather than at the first walk so the failure names
// the key and the file that pointed at it.
func sourceDir(confPath string, conf map[string]string) (string, error) {
	v := conf["source"]
	if v == "" {
		return "", fmt.Errorf("config %s: no source key (source: <dir> names the markdown corpus)", confPath)
	}
	src := filepath.FromSlash(v)
	if !filepath.IsAbs(src) {
		src = filepath.Join(filepath.Dir(confPath), src)
	}
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		return "", fmt.Errorf("config source: %s is not a directory (%s in %s)", src, v, confPath)
	}
	return src, nil
}

// NewSite reads the site described by the config file at confPath, which names
// the source directory the pages are read from.
func NewSite(confPath, tagsCSV string) (*Site, error) {
	conf, err := loadConf(confPath)
	if err != nil {
		return nil, err
	}
	src, err := sourceDir(confPath, conf)
	if err != nil {
		return nil, err
	}
	year := time.Now().Format("2006")
	s := &Site{
		Src:       src,
		Tags:      map[string]bool{},
		SiteTitle: "Docs",
		HomeLabel: "Docs",
		md:        petroglyph.New(),
		Pages:     map[string]*Page{},
	}
	for _, key := range []struct {
		conf string
		def  *string
	}{
		{"title", &s.SiteTitle}, {"edit-base", &s.EditBase}, {"copyright", &s.Copyright},
		{"home-label", &s.HomeLabel},
	} {
		if v := conf[key.conf]; v != "" {
			*key.def = strings.ReplaceAll(v, "{year}", year)
		}
	}
	// logo/favicon name a file under the source directory (copyStatics serves
	// it at the same root-relative path); a typo fails the build instead of
	// shipping a broken image.
	for _, key := range []struct {
		conf string
		dst  *string
	}{
		{"logo", &s.Logo}, {"favicon", &s.Favicon},
	} {
		if v := conf[key.conf]; v != "" {
			if _, err := os.Stat(filepath.Join(src, filepath.FromSlash(v))); err != nil {
				return nil, fmt.Errorf("config %s: %s not found under %s", key.conf, v, src)
			}
			*key.dst = "/" + strings.TrimPrefix(v, "/")
		}
	}
	// accent/accent-dark override the theme's accent colours; hex-validated so
	// config values can never inject CSS.
	for _, key := range []struct {
		conf string
		dst  *string
	}{
		{"accent", &s.Accent}, {"accent-dark", &s.AccentDark},
	} {
		if v := conf[key.conf]; v != "" {
			if !hexColorRe.MatchString(v) {
				return nil, fmt.Errorf("config %s: %q is not a hex color like #0969da", key.conf, v)
			}
			*key.dst = v
		}
	}
	// iframe-hosts is the sanitizer's allow-list for embedded frames (docs
	// viewers, presentations). Hosts are bare domains, matched with their
	// subdomains; no key means no iframe keeps its src.
	hosts, err := iframeHosts(conf["iframe-hosts"])
	if err != nil {
		return nil, err
	}
	s.policy = docsPolicy(hosts)
	if v := conf["base-url"]; v != "" {
		h := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(v, "https://"), "http://"), "/")
		s.BaseHost = strings.SplitN(h, "/", 2)[0]
	}
	if v := conf["tags"]; v != "" {
		s.TagDirs = map[string]string{}
		for _, pair := range strings.Split(v, ",") {
			name, dir, ok := strings.Cut(strings.TrimSpace(pair), "=")
			if !ok || name == "" || dir == "" {
				return nil, fmt.Errorf("config tags: expected name=dir pairs, got %q", pair)
			}
			s.TagDirs[name] = dir
		}
	}
	for _, t := range strings.Split(tagsCSV, ",") {
		if t = strings.TrimSpace(t); t == "" {
			continue
		}
		if len(s.TagDirs) == 0 {
			return nil, fmt.Errorf("unknown tag %q: site declares no tags (declare them as tags: name=dir pairs in colophon.conf)", t)
		}
		if _, ok := s.TagDirs[t]; !ok {
			return nil, fmt.Errorf("unknown tag %q (declared in colophon.conf)", t)
		}
		s.Tags[t] = true
	}
	return s, nil
}

// upstreamOwned reports whether rel lives under a tag-declared subtree,
// whose content is synced from upstream repos and not ours to restyle.
func (s *Site) upstreamOwned(rel string) bool {
	for _, dir := range s.TagDirs {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	return false
}

// iframeHosts parses the comma-separated iframe-hosts config value into bare
// domain names. A malformed host fails the build rather than shipping a
// silently loosened sanitizer.
func iframeHosts(v string) ([]string, error) {
	var hosts []string
	for _, h := range strings.Split(v, ",") {
		if h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "."))); h == "" {
			continue
		}
		if !hostnameRe.MatchString(h) {
			return nil, fmt.Errorf("config iframe-hosts: %q is not a domain like example.com", h)
		}
		hosts = append(hosts, h)
	}
	return hosts, nil
}

// docsPolicy is petroglyph's DisplayPolicy plus, for each configured host,
// an iframe src allow-list covering that domain and its subdomains.
func docsPolicy(hosts []string) *bluemonday.Policy {
	p := petroglyph.DisplayPolicy()
	if len(hosts) == 0 {
		return p
	}
	alt := make([]string, len(hosts))
	for i, h := range hosts {
		alt[i] = `([\w-]+\.)*` + regexp.QuoteMeta(h)
	}
	p.AllowElements("iframe")
	p.AllowAttrs("src").Matching(regexp.MustCompile(`(?i)^https://(?:` + strings.Join(alt, "|") + `)/`)).OnElements("iframe")
	p.AllowAttrs("width", "height", "frameborder", "allowfullscreen", "title", "loading", "style").OnElements("iframe")
	return p
}

// metaYes reads a boolean front-matter value.
func metaYes(v string) bool { return v == "true" || v == "yes" }

// isFragment reports whether rel declares `fragment: true`, which keeps a
// source file out of the build: an include comment inlines it into other
// pages, so building it as a page too would publish the same prose at two
// URLs. The verdict is cached per file because the checks ask for it per link,
// and the dev server forgets a file when it changes.
func (s *Site) isFragment(rel string) bool {
	s.fragMu.Lock()
	defer s.fragMu.Unlock()
	if v, ok := s.frags[rel]; ok {
		return v
	}
	v := false
	if b, err := os.ReadFile(filepath.Join(s.Src, filepath.FromSlash(rel))); err == nil {
		meta, _ := rawFrontMatter(string(b))
		v = metaYes(meta["fragment"])
	}
	if s.frags == nil {
		s.frags = map[string]bool{}
	}
	s.frags[rel] = v
	return v
}

// forgetFragment drops a cached verdict and returns the one it replaced, so
// the dev server can see a file cross the fragment boundary either way and
// rebuild the whole site, which is what adding or dropping a page needs.
func (s *Site) forgetFragment(rel string) bool {
	s.fragMu.Lock()
	defer s.fragMu.Unlock()
	was := s.frags[rel]
	delete(s.frags, rel)
	return was
}

func (s *Site) excluded(rel string) bool {
	if strings.HasSuffix(rel, ".md") && s.isFragment(rel) {
		return true // fragments are inlined by include comments, never built standalone
	}
	for name, dir := range s.TagDirs {
		if !s.Tags[name] && (rel == dir || strings.HasPrefix(rel, dir+"/")) {
			return true // gated subtree: only built with -tags <name>
		}
	}
	return false
}

func (s *Site) listSources() ([]string, error) {
	var files []string
	if _, err := os.Stat(s.Src); err != nil {
		return nil, fmt.Errorf("source dir %s: %w", s.Src, err)
	}
	err := filepath.Walk(s.Src, func(p string, i os.FileInfo, err error) error {
		if err != nil || i.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(s.Src, p)
		rel = filepath.ToSlash(rel)
		if s.excluded(rel) {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	return files, err
}

// BuildAll renders every page in parallel (shared goldmark instance), then
// builds the nav model and finalizes all pages (page TOC, template wrap).
func (s *Site) BuildAll() error {
	files, err := s.listSources()
	if err != nil {
		return err
	}
	start := time.Now()
	pages := make([]*Page, len(files))
	var renderErrs []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i, f := range files {
		wg.Add(1)
		go func(i int, rel string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			raw, err := os.ReadFile(filepath.Join(s.Src, filepath.FromSlash(rel)))
			if err != nil {
				mu.Lock()
				renderErrs = append(renderErrs, rel+": "+err.Error())
				mu.Unlock()
				return
			}
			pages[i] = s.renderPage(rel, string(raw))
		}(i, f)
	}
	wg.Wait()
	if len(renderErrs) > 0 {
		return fmt.Errorf("read errors:\n  %s", strings.Join(renderErrs, "\n  "))
	}
	s.Pages = map[string]*Page{}
	for _, p := range pages {
		s.Pages[p.Rel] = p
	}
	s.buildNav()
	for _, p := range s.Pages {
		s.finalize(p)
	}
	fmt.Fprintf(os.Stderr, "rendered %d pages in %v\n", len(pages), time.Since(start).Round(time.Millisecond))
	return nil
}

func outPath(rel string) string {
	return strings.TrimSuffix(rel, ".md") + ".html"
}

func rawFrontMatter(src string) (map[string]string, string) {
	meta := map[string]string{}
	if !strings.HasPrefix(src, "---\n") {
		return meta, src
	}
	lines := strings.Split(src, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if t := strings.TrimRight(lines[i], " \t\r"); t == "---" || t == "..." {
			end = i
			break
		}
	}
	if end < 0 {
		return meta, src
	}
	for _, ln := range lines[1:end] {
		if ln == "" || strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "#") {
			continue
		}
		if k, v, ok := strings.Cut(ln, ":"); ok {
			meta[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return meta, strings.Join(lines[end+1:], "\n")
}

var (
	headingRe = regexp.MustCompile(`(?s)<h([1-6])[^>]*\bid="([^"]+)"[^>]*>(.*?)</h[1-6]>`)
	idRe      = regexp.MustCompile(`id="([^"]+)"`)
	// <a name="x"> is the custom-anchor idiom GitHub documents, and a fragment
	// names it as well as an id, so the check gate counts it as a target.
	anchorNameRe = regexp.MustCompile(`<a\b[^>]*\bname="([^"]+)"`)
	anchorRe     = regexp.MustCompile(`(?s)<a class="heading-anchor"[^>]*>.*?</a>`)
	hexColorRe   = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
	hostnameRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
	// href on <a> tags only (code blocks may contain literal href="x.md" text)
	mdLinkRe = regexp.MustCompile(`(<a [^>]*href=")([^":#][^"]*?)\.md((?:#[^"]*)?")`)
)

func (s *Site) renderPage(rel, raw string) *Page {
	meta, body := rawFrontMatter(raw)
	res := &preResult{includes: map[string]bool{}}
	body = s.preprocess(body, path.Dir(rel), 0, res, meta["nav"] == "list")

	source := []byte(body)
	doc := s.md.Parser().Parse(text.NewReader(source))
	var buf bytes.Buffer
	if err := s.md.Renderer().Render(&buf, source, doc); err != nil {
		fmt.Fprintf(os.Stderr, "render error %s: %v\n", rel, err)
	}
	h := string(s.policy.SanitizeBytes(buf.Bytes()))
	h = petroglyph.AddAlertTitles(h) // trusted markup, post-sanitize (petroglyph's octicons)
	h = strings.ReplaceAll(h, "</table>", "</table></div>")
	h = strings.ReplaceAll(h, "<table", `<div class="table-wrap"><table`)
	h = mdLinkRe.ReplaceAllString(h, `${1}${2}.html${3}`)

	p := &Page{
		Rel:         rel,
		OutPath:     outPath(rel),
		Meta:        meta,
		Includes:    res.includes,
		NavChildren: res.navChildren,
		NavAll:      res.navAll,
		TOCDepth:    -1,
		IDs:         map[string]bool{},
		Flags:       petroglyph.Needs(doc, source),
		Src:         source,
	}
	switch tv := meta["toc"]; tv {
	case "":
	case "true", "yes":
		p.TOCDepth = 0
	case "false", "no":
	default:
		if d, err := strconv.Atoi(tv); err == nil {
			p.TOCDepth = d
		}
	}
	for _, m := range headingRe.FindAllStringSubmatch(h, -1) {
		lv, _ := strconv.Atoi(m[1])
		text := html.UnescapeString(tagStripRe.ReplaceAllString(anchorRe.ReplaceAllString(m[3], ""), ""))
		p.Headings = append(p.Headings, heading{Level: lv, ID: m[2], Text: strings.TrimSpace(text)})
	}
	for _, m := range idRe.FindAllStringSubmatch(h, -1) {
		p.IDs[m[1]] = true
	}
	for _, m := range anchorNameRe.FindAllStringSubmatch(h, -1) {
		p.IDs[m[1]] = true
	}
	p.Title = meta["title"]
	if p.Title == "" && len(p.Headings) > 0 {
		p.Title = p.Headings[0].Text
	}
	if p.Title == "" {
		p.Title = strings.TrimSuffix(path.Base(rel), ".md")
	}
	p.Raw = []byte(h)
	return p
}

// titleHeading is the id of the heading the page takes its title from, or "" when
// the title comes from front matter or from the file name. That heading is not one
// of the page's own sections, and the page header already shows it.
func (p *Page) titleHeading() string {
	if p.Meta["title"] != "" || len(p.Headings) == 0 || p.Headings[0].Level != 1 {
		return ""
	}
	return p.Headings[0].ID
}

// sectionChildren lists the page's top-level sections, which are its children in
// the nav tree: every h1 but the one the title comes from. Heading level is the
// whole rule — a file that glues several documents into one page gives each of
// them a sidebar row and a rail entry, while an h2 subsection stays in the rail
// alone. An h1 repeating the page title is that title written twice, not a
// section. Heading ids are text-derived, so the rows point at anchors the page
// already had.
func (p *Page) sectionChildren() []heading {
	var out []heading
	for _, hd := range p.Headings {
		if hd.Level == 1 && hd.Text != p.Title {
			out = append(out, hd)
		}
	}
	return out
}

// everySection lists the page's top-level sections including the heading its title
// came from, which is what `#all` says about the file: every h1 in it is a document,
// not the name of the file. A heading that only repeats a title the page states for
// itself is that title written twice, so it stays out.
func (p *Page) everySection() []heading {
	var out []heading
	for _, hd := range p.Headings {
		if hd.Level == 1 && (p.Meta["title"] == "" || hd.Text != p.Title) {
			out = append(out, hd)
		}
	}
	return out
}

// navDeclared reports whether a change to a known page can move the nav tree, in
// which case only a full rebuild is right: the page declares children in a list,
// or holds sections that are children of it.
func (s *Site) navDeclared(rel string) bool {
	p, ok := s.Pages[rel]
	if !ok {
		return false
	}
	return p.Meta["nav"] == "list" || len(p.sectionChildren()) > 0
}

// finalize puts the page body in its final form: the sub-sections a nav list
// gains from its children, then the page TOC when `toc:` asks for one.
// wrapPage then puts the body in the site template.
func (s *Site) finalize(p *Page) {
	body := string(p.Raw)
	if len(p.NavChildren) > 0 {
		body = s.navSectionLists(p, body)
	}
	if p.TOCDepth >= 0 {
		body = strings.Replace(body, "</h1>", "</h1>\n"+s.pageTOC(p, p.TOCDepth), 1)
	}
	p.Body = []byte(body)
}

// pageTOC builds a nested list from the page's own headings, up to the depth
// the front matter asked for (0 = all levels).
func (s *Site) pageTOC(p *Page, depth int) string {
	if len(p.Headings) == 0 {
		return ""
	}
	minLevel := p.Headings[0].Level
	maxLevel := 100
	if depth > 0 {
		maxLevel = minLevel + depth - 1
	}
	var b strings.Builder
	b.WriteString(`<nav class="page-toc"><ul>`)
	open := minLevel
	for _, h := range p.Headings {
		if h.Level < minLevel || h.Level > maxLevel {
			continue
		}
		for open < h.Level {
			b.WriteString("<ul>")
			open++
		}
		for open > h.Level {
			b.WriteString("</ul></li>")
			open--
		}
		fmt.Fprintf(&b, `<li><a href="#%s">%s</a>`, html.EscapeString(h.ID), html.EscapeString(h.Text))
	}
	for ; open >= minLevel; open-- {
		b.WriteString("</li></ul>")
	}
	// the loop above closed the outer <ul> the nav opened
	b.WriteString("</nav>")
	return b.String()
}
