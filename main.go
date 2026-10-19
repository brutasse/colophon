package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	var src, tagsCSV string

	root := &cobra.Command{
		Use:   "colophon",
		Short: "Markdown docs site builder",
		Long: "colophon builds a documentation site from the markdown\n" +
			"corpus under --src. Link contract: links to site pages are written as\n" +
			"file-relative .md paths (plus an optional #fragment) so they navigate\n" +
			"on GitHub; the builder rewrites them to .html at render.",
	}
	root.PersistentFlags().StringVar(&src, "src", "source", "docs source directory")
	root.PersistentFlags().StringVar(&tagsCSV, "tags", "", "comma-separated build tags (declared by the tags key in colophon.conf)")

	build := &cobra.Command{
		Use:   "build",
		Short: "Render the site",
		Long: "build renders every page to --out and then reports link problems as\n" +
			"warnings without failing (exit 0).\n\n" +
			"--check turns the report into a gate and exits 1 on: broken links, missing\n" +
			"anchors, non-portable link forms (.html, root-absolute, extensionless\n" +
			"page links), pages no reader can reach because nothing links to them and no\n" +
			"`nav: list` contains them, a fragment whose `fragment: true` declaration is\n" +
			"missing, pages with math nodes that lack `math: true` front matter.\n\n" +
			"Problems in a tag-gated subtree are always informational: those trees\n" +
			"are copied verbatim from upstream repositories and must be fixed there.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, _ := cmd.Flags().GetString("out")
			check, _ := cmd.Flags().GetBool("check")
			site, err := NewSite(src, tagsCSV, ".")
			if err != nil {
				fail(err)
			}
			if err := site.BuildAll(); err != nil {
				fail(err)
			}
			// The indexer needs HTML on disk, but the bundle's presence
			// gates the search markup inside the HTML: write pages first
			// (search off), index, then write the site for real.
			if err := site.writePages(out); err != nil {
				fail(err)
			}
			if err := indexPagefind(out); err != nil {
				fmt.Fprintln(os.Stderr, "warning:", err, "-- search disabled")
			} else {
				site.Search = true
			}
			if err := site.WriteOut(out); err != nil {
				fail(err)
			}
			if check {
				site.RunCheck()
			} else {
				site.Lint()
			}
			return nil
		},
	}
	build.Flags().String("out", "_build/html", "HTML output directory")
	build.Flags().Bool("check", false, "run the CI gate after building (exit 1 on errors)")

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Dev server: rebuild changed pages, report their problems on each save",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, _ := cmd.Flags().GetString("http")
			site, err := NewSite(src, tagsCSV, ".")
			if err != nil {
				fail(err)
			}
			if err := site.Serve(addr); err != nil {
				fail(err)
			}
			return nil
		},
	}
	serve.Flags().String("http", "127.0.0.1:8000", "listen address")

	root.AddCommand(build, serve)
	if err := root.Execute(); err != nil {
		os.Exit(2) // invocation error; gate failures exit 1 themselves
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "colophon:", err)
	os.Exit(1)
}
