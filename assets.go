package main

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed tmpl/page.tmpl
var pageTmpl string

//go:embed assets
var assetsFS embed.FS

var pageT = template.Must(template.New("page").Parse(pageTmpl))

type pageData struct {
	Title     string
	SiteTitle string
	Copyright string
	EditBase  string
	Logo      string
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

// WriteAssets extracts the embedded assets (css, vendored mermaid/MathJax)
// into the output tree under /_assets/.
func WriteAssets(out string) error {
	return fs.WalkDir(assetsFS, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, "assets/")
		if rel == "LICENSES" || strings.HasSuffix(rel, "-LICENSE.txt") {
			return nil
		}
		b, err := assetsFS.ReadFile(p)
		if err != nil {
			return err
		}
		// Upstream's minified files hold literal glyphs and are not ours to rewrite.
		if strings.HasSuffix(rel, ".js") && !strings.HasPrefix(rel, "vendor/") {
			if err := checkASCII(rel, b); err != nil {
				return err
			}
		}
		dst := filepath.Join(out, "_assets", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
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
