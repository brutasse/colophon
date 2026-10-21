package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is stamped into the release binaries by the release workflow with
// -ldflags -X main.version; a build from source reports dev. `colophon version`
// is how an install proves which release it wrote, because the release asset
// names are fixed and carry no version of their own.
var version = "dev"

// The update command's state, read by update.go: the flags of a command defined
// inside main would not reach it, and the environment switches beside these
// (COLOPHON_NO_UPDATE_CHECK and CI) are read there.
var (
	noUpdateCheck   bool
	updateTo        string
	updateCheckOnly bool
)

func main() {
	var tagsCSV string

	root := &cobra.Command{
		Use:     "colophon",
		Short:   "Markdown docs site builder",
		Version: version,
		Long: "colophon builds a documentation site from the markdown corpus named by\n" +
			"the source key in colophon.conf. Link contract: links to site pages are written\n" +
			"as file-relative .md paths (plus an optional #fragment) so they navigate\n" +
			"on GitHub; the builder rewrites them to .html at render.",
	}
	root.PersistentFlags().StringVar(&tagsCSV, "tags", "", "comma-separated build tags (declared by the tags key in colophon.conf)")
	root.PersistentFlags().BoolVar(&noUpdateCheck, "no-update-check", false, "do not ask GitHub whether a newer release exists")

	build := &cobra.Command{
		Use:   "build",
		Short: "Render the site",
		Long: "build renders every page to --out and then reports link problems as\n" +
			"warnings without failing (exit 0).\n\n" +
			"--check turns the report into a gate and exits 1 on: broken links, missing\n" +
			"anchors, non-portable link forms (.html, root-absolute, extensionless\n" +
			"page links), pages no reader can reach because nothing links to them and no\n" +
			"`nav: list` contains them, a fragment whose `fragment: true` declaration is\n" +
			"missing, pages with math nodes that lack `math: true` front matter, and " +
			"`nav: list` items that link to a section of a page instead of the page.\n\n" +
			"Problems in a tag-gated subtree are always informational: those trees\n" +
			"are copied verbatim from upstream repositories and must be fixed there.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, _ := cmd.Flags().GetString("out")
			check, _ := cmd.Flags().GetBool("check")
			site := newSite(cmd, tagsCSV)
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
	configFlag(build)

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Dev server: rebuild changed pages, report their problems on each save",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, _ := cmd.Flags().GetString("http")
			site := newSite(cmd, tagsCSV)
			if err := site.Serve(addr); err != nil {
				fail(err)
			}
			return nil
		},
	}
	serve.Flags().String("http", "127.0.0.1:8000", "listen address")
	configFlag(serve)

	// cobra adds the --version flag on its own but no version command, and
	// scripts/install.sh reads the version back from the binary it installed.
	ver := &cobra.Command{
		Use:   "version",
		Short: "Print the version of this binary",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), version)
		},
	}

	update := &cobra.Command{
		Use:   "update",
		Short: "Replace this binary with a published release",
		Long: "update installs a published release over the running binary. It asks the\n" +
			"GitHub releases API which release is newest, downloads the archive for this\n" +
			"platform, checks it against the sha256 the API publishes for that asset, and\n" +
			"moves the new binary into place. A binary built from source is refused,\n" +
			"because no release owns it.\n\n" +
			"The directory holding this binary must be writable, so an install in\n" +
			"/usr/local/bin normally needs an administrator.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := runUpdate(cmd.OutOrStdout(), updateTo, updateCheckOnly, ""); err != nil {
				fail(err)
			}
			return nil
		},
	}
	update.Flags().BoolVar(&updateCheckOnly, "check", false, "report what is available without installing it")
	update.Flags().StringVar(&updateTo, "to", "", "install this release tag instead of the newest")

	root.AddCommand(build, serve, ver, update)
	// The availability check runs before the work, and only where a human is
	// reading: update and version are about versions already, and help or
	// completion must not touch the network on the way to answering something
	// else.
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		switch cmd.Name() {
		case "update", "version", "help", "completion":
		default:
			notice()
		}
		return nil
	}
	// The bare version, with no surrounding text, so a script can compare it
	// against a release tag.
	root.SetVersionTemplate("{{.Version}}\n")
	if err := root.Execute(); err != nil {
		os.Exit(2) // invocation error; gate failures exit 1 themselves
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "colophon:", err)
	os.Exit(1)
}

// configFlag and newSite belong to the two commands that build a site. The
// config file is their entry point, and its absence is what stops a run in a
// directory that holds no site; version, update, and help read no config, so
// they keep working anywhere and are given no --config to ignore.
func configFlag(cmd *cobra.Command) {
	cmd.Flags().String("config", "colophon.conf", "site config file (its source directory is resolved against this file)")
}

func newSite(cmd *cobra.Command, tagsCSV string) *Site {
	conf, _ := cmd.Flags().GetString("config")
	site, err := NewSite(conf, tagsCSV)
	if err != nil {
		fail(err)
	}
	return site
}
