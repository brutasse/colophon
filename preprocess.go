package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// preResult accumulates the artifacts found while preprocessing a body.
type preResult struct {
	includes    map[string]bool
	navChildren []string        // rel pages from nav: list bullet lists (depth 0 only)
	navAll      map[string]bool // of those, the children whose link ended in `#all`
}

// navItem is one item of a nav list: the child page it names, whether its link
// carried the `#all` marker, and the line to emit, which is the source line with the
// marker removed.
type navItem struct {
	rel  string // child page source path
	all  bool   // `#all`: every h1 of the child is a document, title heading included
	line string // the source line, marker stripped
}

var (
	// include syntax with no GFM footprint: an HTML comment GitHub hides
	includeCommentRe = regexp.MustCompile(`^<!--\s*colophon:include\s+(.*?)\s*-->$`)
	// one include option: `literal`, `language=yaml`, `language="yaml"`
	includeArgRe = regexp.MustCompile(`^([a-z][a-z-]*)(?:=("(?:[^"]*)"|[^\s"]+))?`)
	// in `nav: list` pages, a top-level list whose every item is exactly one
	// relative .md link is that page's nav; anything else stays prose. An item may
	// end in `#all` to say every h1 of the target is a document, and preprocess drops
	// the marker from the rendered link.
	navBulletRe  = regexp.MustCompile(`^[-*][ \t]+\[[^\]]+\]\(([^)\s#]+\.md)(#all)?\)[ \t]*$`)
	navNumberRe  = regexp.MustCompile(`^[0-9]+\.[ \t]+\[[^\]]+\]\(([^)\s#]+\.md)(#all)?\)[ \t]*$`)
	navItemishRe = regexp.MustCompile(`^\s*(?:[-*]|\d+\.)[ \t]`)
)

// includeOpts are the options an include comment may carry.
type includeOpts struct {
	language string // implies literal: the fragment ships as a code fence
	literal  bool
}

// srcRel normalizes a link or include destination to a source-relative path.
func srcRel(dir, e string) string {
	e = strings.TrimPrefix(e, "./")
	if path.IsAbs(e) {
		e = strings.TrimPrefix(strings.TrimPrefix(e, "/"), "source/")
	} else {
		e = path.Join(dir, e)
	}
	return e
}

// navRel is srcRel plus the `.md` a nav list leaves off.
func navRel(dir, e string) string {
	e = srcRel(dir, e)
	if !strings.HasSuffix(e, ".md") {
		e += ".md"
	}
	return e
}

// parseIncludeArgs reads the text between `colophon:include` and the comment
// close: a fragment path followed by space-separated options. A quoted value is
// accepted. Anything unparsable is an error rather than a silently dropped
// option, so a typo shows up in the page.
func parseIncludeArgs(dir, args string) (string, includeOpts, error) {
	var opts includeOpts
	rest := strings.TrimSpace(args)
	frag, rest := cutField(rest)
	if frag == "" {
		return "", opts, fmt.Errorf("include comment names no fragment: %q", args)
	}
	rel := srcRel(dir, frag)
	for rest != "" {
		m := includeArgRe.FindStringSubmatch(rest)
		if m == nil {
			return rel, opts, fmt.Errorf("malformed include option in %q", rest)
		}
		key, val := m[1], strings.Trim(m[2], `"`)
		rest = strings.TrimLeft(rest[len(m[0]):], " \t")
		switch key {
		case "language":
			opts.language = val
		case "literal":
			opts.literal = true
		default:
			return rel, opts, fmt.Errorf("unknown include option: %s", key)
		}
	}
	return rel, opts, nil
}

// cutField splits off the first whitespace-delimited field of s and returns it
// with the remainder, leading spaces trimmed.
func cutField(s string) (string, string) {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i], strings.TrimLeft(s[i:], " \t")
	}
	return s, ""
}

// navRun detects an all-md-link top-level list starting at line i (only in
// `nav: list` pages, only at the top level). Returns the index just past the
// run, the child items, and whether it counts as nav. A list that is not
// closed by a blank line is prose (its items still report the run end so the
// caller does not re-test the inner items as a fresh list).
func navRun(lines []string, i int, dir string, depth int, optIn bool, until int) (int, []navItem, bool) {
	if !optIn || depth != 0 || i <= until {
		return 0, nil, false
	}
	if strings.HasPrefix(lines[i], " ") || strings.HasPrefix(lines[i], "\t") {
		return 0, nil, false
	}
	if i > 0 && navItemishRe.MatchString(strings.TrimRight(lines[i-1], "\r")) {
		return 0, nil, false // mid-list restart: the real run began earlier and was rejected
	}
	re := navBulletRe
	if !re.MatchString(lines[i]) {
		re = navNumberRe
	}
	if !re.MatchString(lines[i]) {
		return 0, nil, false
	}
	j, items := i, []navItem{}
	for j < len(lines) {
		m := re.FindStringSubmatch(strings.TrimRight(lines[j], "\r"))
		if m == nil {
			break
		}
		line, marked := lines[j], m[2] != ""
		if marked {
			// `#all` is for colophon, so the page links the target alone
			line = strings.Replace(line, "#all)", ")", 1)
		}
		items = append(items, navItem{rel: navRel(dir, m[1]), all: marked, line: line})
		j++
	}
	if j >= len(lines) || strings.TrimSpace(lines[j]) == "" {
		return j, items, true
	}
	return j, nil, false
}

// preprocess handles the two source conventions GFM has no equivalent for,
// and leaves everything else for the renderer untouched:
//
//	<!-- colophon:include frag.md [options] --> → the fragment, inlined
//	a top-level list of page links in a `nav: list` page → that page's children
//
// It recurses into included fragments (max 5 deep).
func (s *Site) preprocess(body, dir string, depth int, res *preResult, navOptIn bool) string {
	if depth > 5 {
		return body
	}
	lines := strings.Split(body, "\n")
	var out []string
	navScanUntil := -1
	for i := 0; i < len(lines); i++ {
		if m := includeCommentRe.FindStringSubmatch(strings.TrimRight(lines[i], "\r")); m != nil {
			out = append(out, s.includeFragment(dir, m[1], depth, res))
			continue
		}
		k, items, ok := navRun(lines, i, dir, depth, navOptIn, navScanUntil)
		if k > 0 {
			navScanUntil = k - 1 // never re-test this list's inner items
			if ok {
				// A gated subtree stays out of the nav until its tag is passed.
				for _, it := range items {
					if !s.excluded(it.rel) {
						res.navChildren = append(res.navChildren, it.rel)
						if it.all {
							if res.navAll == nil {
								res.navAll = map[string]bool{}
							}
							res.navAll[it.rel] = true
						}
					}
					out = append(out, it.line)
				}
				i = k - 1
				continue
			}
		}
		out = append(out, lines[i])
	}
	return strings.Join(out, "\n")
}

// includeFragment resolves one include comment: the fragment, preprocessed
// against its own directory, or the note that replaces it when it cannot be
// read. args is everything the comment said, so the note can quote it.
func (s *Site) includeFragment(dir, args string, depth int, res *preResult) string {
	frag, opts, err := parseIncludeArgs(dir, args)
	if err == nil {
		var content string
		if content, err = s.readFragment(frag, opts); err == nil {
			res.includes[frag] = true
			return "\n" + s.preprocess(content, path.Dir(frag), depth+1, res, false) + "\n"
		}
	}
	return fmt.Sprintf("\n*include unavailable: %s (%v)*\n", args, err)
}

// readFragment returns a fragment's body, fenced when it should show as code.
// Front matter is stripped from a Markdown fragment only: a `---` line in a
// config file is its own content.
func (s *Site) readFragment(rel string, opts includeOpts) (string, error) {
	b, err := os.ReadFile(filepath.Join(s.Src, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	body := string(b)
	if strings.HasSuffix(rel, ".md") {
		_, body = rawFrontMatter(body)
	}
	if opts.literal || opts.language != "" {
		body = "```" + opts.language + "\n" + strings.TrimSpace(body) + "\n```\n"
	}
	return body, nil
}
