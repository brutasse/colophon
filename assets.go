package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed tmpl/page.tmpl
var pageTmpl string

//go:embed assets
var assetsFS embed.FS

// assetDir is the site directory every content-addressed file is served from,
// whether it is embedded in the binary or named by the site config.
const assetDir = "_assets"

// pageT asks for each asset by name, so a page carries the URL that names the
// bytes it loads rather than a name a host can serve stale under.
var pageT = template.Must(template.New("page").Funcs(template.FuncMap{
	"asset": assetURL,
}).Parse(pageTmpl))

// assetFile is one file a site can load from /_assets/, addressed by its
// contents. A page's URL and the bytes behind it come from the same read, so they
// cannot disagree — which is what lets the dev server and any host cache the file
// forever.
type assetFile struct {
	path string // site path without a leading slash: "_assets/docs.9f3c1a2b.css"
	b    []byte
}

// contentDigest is the address part of a content-addressed name: 12 hex of
// sha256, more than enough for the handful of files a site ships.
func contentDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// digestPath puts the digest in front of the extension so the name still reads
// as the file it is -- "vendor/mermaid.min.js" becomes
// "vendor/mermaid.min.9f3c1a2b.js" -- and a host still reaches the media type it
// maps from the suffix.
func digestPath(name, digest string) string {
	dir, base := path.Split(name)
	ext := path.Ext(base)
	return dir + strings.TrimSuffix(base, ext) + "." + digest + ext
}

// assetIndex holds assets for lookup by the name a template asks for, and by the
// path they are served at, which is what a request carries. A site keeps its own
// index for the files its config names, because the dev server replaces that one
// when such a file is saved while the binary's own assets never move.
type assetIndex struct {
	byName map[string]assetFile
	byPath map[string]assetFile
}

// publish files bytes under the address derived from them and returns the asset,
// so a caller keeps the URL without deriving it a second time. Two names can
// reach one path when they hold the same bytes; that is one file, not a clash.
func (ix *assetIndex) publish(name string, b []byte) assetFile {
	if ix.byPath == nil {
		ix.byPath = map[string]assetFile{}
	}
	a := assetFile{path: assetDir + "/" + digestPath(name, contentDigest(b)), b: b}
	ix.byPath[a.path] = a
	return a
}

// pathOf resolves a served path, with or without its leading slash.
func (ix *assetIndex) pathOf(upath string) (assetFile, bool) {
	if ix == nil {
		return assetFile{}, false
	}
	a, ok := ix.byPath[strings.TrimPrefix(upath, "/")]
	return a, ok
}

// embeddedAssets indexes the binary's own assets, under the names a template
// asks for as well as the paths they are served at. The binary is fixed, so one
// walk covers every run of it.
var embeddedAssets = sync.OnceValue(func() *assetIndex {
	ix := &assetIndex{byName: map[string]assetFile{}}
	err := fs.WalkDir(assetsFS, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, "assets/")
		// Licences travel with the source and the release, never to a browser.
		if rel == "LICENSES" || strings.HasSuffix(rel, "-LICENSE.txt") {
			return nil
		}
		b, err := assetsFS.ReadFile(p)
		if err != nil {
			return err
		}
		ix.byName[rel] = ix.publish(rel, b)
		return nil
	})
	if err != nil {
		panic("embedded assets: " + err.Error()) // a file `go build` compiled in is present at run time
	}
	return ix
})

// assetURL resolves one of the binary's own assets to its content-addressed URL,
// site-root-relative; a page prepends its own Prefix to reach it.
// A name with no asset behind it is a bug in tmpl/page.tmpl, and it fails the
// build rather than shipping a URL that 404s on every page.
func assetURL(name string) (string, error) {
	a, ok := embeddedAssets().byName[name]
	if !ok {
		return "", fmt.Errorf("no embedded asset %q", name)
	}
	return a.path, nil
}

// requestAsset resolves a /_assets/ request: the binary's own files first, then
// this site's config-named ones. The addresses are the ones the pages were
// wrapped with, so a miss is a page from an earlier build or a link nobody
// maintains, never an asset asking under a name it has since changed.
func (s *Site) requestAsset(upath string) (assetFile, bool) {
	if a, ok := embeddedAssets().pathOf(upath); ok {
		return a, true
	}
	s.staticMu.Lock()
	defer s.staticMu.Unlock()
	return s.statics.pathOf(upath)
}

// isHashedStatic reports whether rel is one of the config files published at a
// content-addressed URL, which the dev server re-digests when it is saved.
func (s *Site) isHashedStatic(rel string) bool {
	return rel != "" && (rel == s.logoSrc || rel == s.logoDarkSrc || rel == s.faviconSrc)
}

// hashedStatics reads the config's logo:, logo-dark: and favicon: files and publishes each at
// a URL naming its bytes, which is what the header tags colophon writes point at.
// copyStatics still copies each file to its own path, so a page that links the
// logo directly keeps working. Nothing is replaced until every file reads, so a
// save that deletes one leaves the site serving what it served before, with the
// failure reported.
func (s *Site) hashedStatics() error {
	rels := []string{s.logoSrc, s.logoDarkSrc, s.faviconSrc}
	urls := make([]string, len(rels))
	ix := &assetIndex{}
	for i, rel := range rels {
		if rel == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.Src, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		// The address carries the file name a reader recognizes, not the directory
		// the site kept it in.
		urls[i] = ix.publish(path.Base(rel), b).path
	}
	s.staticMu.Lock()
	s.statics = ix
	s.staticMu.Unlock()
	s.Logo, s.LogoDark, s.Favicon = urls[0], urls[1], urls[2]
	return nil
}

// writeStatics puts the config-named assets into out/_assets/ beside the
// embedded ones.
func (s *Site) writeStatics(out string) error {
	s.staticMu.Lock()
	defer s.staticMu.Unlock()
	if s.statics == nil {
		return nil
	}
	for _, a := range s.statics.byPath {
		if err := writeAsset(out, a); err != nil {
			return err
		}
	}
	return nil
}

type pageData struct {
	Title     string
	SiteTitle string
	Prefix    string // the page's path back to the site root, prepended to every chrome URL
	Copyright string
	EditBase  string
	Logo      string
	LogoDark  string
	Favicon   string
	Content   template.HTML
	Sidebar   template.HTML
	Crumbs    template.HTML
	TOC       template.HTML
	AccentCSS template.CSS
	Search    bool
	PrevNext  template.HTML
	Math      bool
	Mermaid   bool
	Dev       bool
	EditPath  string
}

// WriteAssets extracts the embedded assets (css, vendored mermaid/MathJax) into
// the output tree under /_assets/, each under the content-addressed name the
// pages name.
func WriteAssets(out string) error {
	for name, a := range embeddedAssets().byName {
		// Upstream's minified files hold literal glyphs and are not ours to rewrite.
		if strings.HasSuffix(name, ".js") && !strings.HasPrefix(name, "vendor/") {
			if err := checkASCII(name, a.b); err != nil {
				return err
			}
		}
		if err := writeAsset(out, a); err != nil {
			return err
		}
	}
	return nil
}

func writeAsset(out string, a assetFile) error {
	dst := filepath.Join(out, filepath.FromSlash(a.path))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, a.b, 0o644)
}

// checkASCII rejects a non-ASCII byte in an extracted script: a host that serves a
// .js without a charset has its bytes read as latin-1, so a literal glyph arrives as
// mojibake with nothing failing. The scripts keep their glyphs as \u escapes.
func checkASCII(name string, b []byte) error {
	line, col := 1, 0
	for _, c := range b {
		col++
		if c == '\n' {
			line++
			col = 0
			continue
		}
		if c > 0x7f {
			return fmt.Errorf("assets/%s:%d:%d: non-ASCII byte 0x%02x: write the glyph as a \\u escape, or serve the file with a charset",
				name, line, col, c)
		}
	}
	return nil
}
