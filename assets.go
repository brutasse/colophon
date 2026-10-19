package main

import (
	"embed"
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
		dst := filepath.Join(out, "_assets", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
}
