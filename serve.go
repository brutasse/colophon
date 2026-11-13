package main

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/fsnotify/fsnotify"
)

// Serve runs the dev server: pages rebuilt in memory per change, statics
// served straight from source/, WebSocket-triggered browser reload.
//
// The reload channel is one WebSocket per open document, not a stream: a stream holds
// one of the six HTTP/1.1 connections a browser allows per origin for the whole life
// of the page, so six open docs starve a seventh navigation, and a reload in an
// existing tab deadlocks behind its own stream.
func (s *Site) Serve(addr string) error {
	s.Dev = true
	if err := s.BuildAll(); err != nil {
		return err
	}

	var mu sync.Mutex
	rendered := map[string][]byte{} // "/runbooks/foo.html" -> page
	rebuild := func() error {
		next := make(map[string][]byte, len(s.Pages))
		for _, p := range s.Pages {
			b, err := s.wrapPage(p)
			if err != nil {
				return err
			}
			next["/"+p.OutPath] = b
		}
		mu.Lock() // momentary: serving handlers never wait on wrapping
		rendered = next
		mu.Unlock()
		return nil
	}
	// loadPage reads and renders one source file, or returns no page for one that is
	// gone. A file that exists and will not read is an error to report, not a page to
	// drop.
	loadPage := func(rel string) (*Page, error) {
		raw, err := os.ReadFile(filepath.Join(s.Src, filepath.FromSlash(rel)))
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return s.renderPage(rel, string(raw)), nil
	}
	// publish puts a rendered page where the handler and the next wrap read it.
	publish := func(p *Page) error {
		s.Pages[p.Rel] = p
		s.finalize(p)           // sets p.Body; wrapPage renders from it
		s.updateLinksFor(p.Rel) // the body moved, so the page's outgoing links did
		b, err := s.wrapPage(p)
		if err != nil {
			return err
		}
		mu.Lock()
		rendered["/"+p.OutPath] = b
		mu.Unlock()
		return nil
	}
	rebuildPage := func(rel string) error {
		p, err := loadPage(rel)
		if err != nil {
			return err
		}
		if p == nil {
			if old, ok := s.Pages[rel]; ok {
				delete(s.Pages, rel)
				s.dropPageLinks(rel)
				mu.Lock()
				delete(rendered, "/"+old.OutPath)
				mu.Unlock()
			}
			return nil
		}
		return publish(p)
	}
	s.Search = true // optimistic; the first index result re-wraps without it on failure
	if err := rebuild(); err != nil {
		return err
	}
	// The first render reports the whole gate, so the author sees every standing
	// problem, not only the ones a later save introduces. The link index backs
	// the incremental check each save then runs.
	s.buildLinkIndex()
	errs, _, hints := s.collectLinkProblems()
	reportProblems(errs, hints)

	// Search: Pagefind only indexes HTML on disk, so the in-memory pages are
	// mirrored to a temp site dir; the bundle is served from there. Indexing
	// runs asynchronously so neither startup nor saves wait on it: pages are
	// served and reload instantly while search catches up a run behind. The
	// indexer reads only byte snapshots; success/failure is reported to the
	// watcher goroutine, which is the sole mutator of s.Search/s.Pages.
	siteDir, err := os.MkdirTemp("", "colophon-serve")
	if err != nil {
		return err
	}
	defer os.RemoveAll(siteDir)
	reindex := make(chan struct{}, 1) // cap 1: bursts coalesce into one run
	requestReindex := func() {
		select {
		case reindex <- struct{}{}:
		default:
		}
	}
	indexed := make(chan bool, 1)
	go func() {
		written := map[string][]byte{}
		for range reindex {
			mu.Lock()
			snap := make(map[string][]byte, len(rendered))
			for k, v := range rendered {
				snap[k] = v
			}
			mu.Unlock()
			start := time.Now()
			var err error
			for urlPath, b := range snap {
				if old, ok := written[urlPath]; ok && bytes.Equal(old, b) {
					continue // only changed pages hit the disk
				}
				f := filepath.Join(siteDir, filepath.FromSlash(strings.TrimPrefix(urlPath, "/")))
				if err = os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
					break
				}
				if err = os.WriteFile(f, b, 0o644); err != nil {
					break
				}
				written[urlPath] = b
			}
			for urlPath := range written {
				if err != nil {
					break
				}
				if _, ok := snap[urlPath]; ok {
					continue
				}
				if err = os.Remove(filepath.Join(siteDir, filepath.FromSlash(strings.TrimPrefix(urlPath, "/")))); err != nil && !os.IsNotExist(err) {
					break
				}
				delete(written, urlPath)
			}
			if err == nil {
				err = indexPagefind(siteDir)
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "search reindex:", err, "-- search disabled")
			} else {
				fmt.Println(paint(os.Stdout, ansiGray,
					fmt.Sprintf("search indexed in %v", time.Since(start).Round(time.Millisecond))))
			}
			indexed <- err == nil
		}
	}()
	setSearch := func(on bool) { // watcher goroutine only: wrapPage reads s.Search
		if s.Search == on {
			return
		}
		s.Search = on
		if err := rebuild(); err != nil {
			fmt.Fprintln(os.Stderr, "wrap:", err)
		}
	}

	// One entry per open document, keyed by its channel and carrying the output
	// path the tab shows, so a save pings only the tabs that can change.
	clients := map[chan string]string{}
	var cmu sync.Mutex
	// broadcast pings every open tab when paths is nil, else only the tabs
	// showing one of these pages.
	broadcast := func(paths map[string]bool) {
		cmu.Lock()
		defer cmu.Unlock()
		for ch, shown := range clients {
			if paths != nil && !paths[shown] {
				continue
			}
			select {
			case ch <- "reload":
			default:
			}
		}
	}

	mux := http.NewServeMux()
	mux.Handle("/pagefind/", noStore(http.FileServer(http.Dir(siteDir))))
	mux.HandleFunc("/opensearch.xml", func(w http.ResponseWriter, r *http.Request) {
		// Generated, not served from source: the template URL names the base-url
		// the config holds, and a search the site stops answering must 404, not
		// register.
		if !s.SearchEngine() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/opensearchdescription+xml; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(s.openSearchXML())
	})
	mux.HandleFunc("/reload", func(w http.ResponseWriter, r *http.Request) {
		// Same-origin requests only: Accept compares the Origin against r.Host,
		// which is the whole access-control story for a loopback dev server.
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "")
		// Which document this tab is showing, so a save can reach it. A client
		// that says nothing keeps no key and so reloads only on a full rebuild.
		key := ""
		if p := r.URL.Query().Get("path"); p != "" {
			key = renderedKey(p)
		}
		ch := make(chan string, 4)
		cmu.Lock()
		clients[ch] = key
		cmu.Unlock()
		defer func() { cmu.Lock(); delete(clients, ch); cmu.Unlock() }()

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		// No message is ever expected from a tab, but reading is what notices the
		// tab went away, and the library needs the reads to run its control frames.
		gone := make(chan struct{})
		go func() {
			defer close(gone)
			for {
				if _, _, err := ws.Read(ctx); err != nil {
					return
				}
			}
		}()
		// A tab whose peer went half-open (sleep, network change) would otherwise
		// stay registered for the life of the server; a missed pong ends this one.
		// The page reconnects a second later on its own.
		ping := time.NewTicker(20 * time.Second)
		defer ping.Stop()
		for {
			select {
			case msg := <-ch:
				if err := ws.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
					return
				}
			case <-ping.C:
				pctx, cancelPing := context.WithTimeout(ctx, 10*time.Second)
				// Ping waits for the pong, and any expired context closes the
				// connection, so this is the whole keepalive.
				err := ws.Ping(pctx)
				cancelPing()
				if err != nil {
					return
				}
			case <-gone:
				return
			}
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Nothing a rebuild can move is worth caching here: a page keeps its URL
		// while its bytes change, and a source static keeps its name whatever it
		// holds. /_assets/ is the exception, addressed by its bytes and cached for a
		// year, which is what lets a reload pick up a new colophon on its own.
		w.Header().Set("Cache-Control", "no-store")
		upath := renderedKey(r.URL.Path)
		if strings.HasPrefix(upath, assetDir+"/") {
			a, ok := s.requestAsset(upath)
			if !ok {
				http.NotFound(w, r)
				return
			}
			serveAsset(w, upath, a)
			return
		}
		mu.Lock()
		page, ok := rendered["/"+upath]
		mu.Unlock()
		if ok {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(page)
			return
		}
		// fall through to source statics (images, PDFs, _static/…)
		f, err := os.Open(filepath.Join(s.Src, filepath.FromSlash(upath)))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if st.IsDir() {
			f2, err := os.Open(filepath.Join(s.Src, filepath.FromSlash(upath), "index.html"))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer f2.Close()
			http.ServeContent(w, r, "index.html", st.ModTime(), f2)
			return
		}
		http.ServeContent(w, r, upath, st.ModTime(), f)
	})

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	if err := filepath.Walk(s.Src, func(p string, i os.FileInfo, err error) error {
		if err == nil && i.IsDir() && !strings.Contains(p, "__pycache__") {
			watcher.Add(p)
		}
		return nil
	}); err != nil {
		return err
	}

	go func() {
		debounce := time.NewTimer(time.Hour)
		debounce.Stop()
		pending := map[string]bool{}
		for {
			select {
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
					continue
				}
				rel, err := filepath.Rel(s.Src, ev.Name)
				if err != nil {
					continue
				}
				rel = filepath.ToSlash(rel)
				// A save that changes no markup is dropped: no page renders it, no
				// include comment inlines it, no page header names it.
				if !strings.HasSuffix(rel, ".md") && s.includeReferrer(rel) == nil && !s.isHashedStatic(rel) {
					continue
				}
				pending[rel] = true
				debounce.Reset(120 * time.Millisecond)
			case <-debounce.C:
				changed := pending
				pending = map[string]bool{}
				// Resolved before the rebuild, so a page this save deletes still
				// reaches the tab that is showing it.
				targets := s.changedPaths(changed)
				start := time.Now()
				full := false
				why := "" // what the save moved that a second page's HTML is built from
				// Pages this decision rendered on its way to that answer, kept to
				// publish as they are when nothing but their own bytes changed.
				loaded := map[string]*Page{}
				for rel := range changed {
					// A file that changed its `fragment:` verdict changes which
					// files are pages at all, so only a full rebuild is right.
					if was := s.forgetFragment(rel); was != s.isFragment(rel) {
						full, why = true, "a `fragment:` declaration moved"
						break
					}
					// A logo or favicon is named in every page header, at a URL derived
					// from its bytes, so a save re-digests it and re-wraps the site.
					if s.isHashedStatic(rel) {
						if err := s.hashedStatics(); err != nil {
							fmt.Fprintln(os.Stderr, "rebuild:", err)
						}
						full, why = true, "the logo or favicon moved"
						break
					}
					// An inlined fragment's headings become the host page's, and a host
					// page parents its sections in the nav.
					if s.includeReferrer(rel) != nil {
						full, why = true, "an inlined fragment moved"
						break
					}
					if s.excluded(rel) {
						continue // gated out of this build, so there is nothing to publish
					}
					next, err := loadPage(rel)
					if err != nil {
						fmt.Fprintln(os.Stderr, "rebuild:", err)
						continue
					}
					if why = navMoved(s.Pages[rel], next); why != "" {
						full = true
						break
					}
					loaded[rel] = next
				}
				warned := 0 // warnings this save reported, to color the rebuilt line
				if full {
					// The pages this rebuild moves, for the check that follows:
					// the saved pages, the hosts of the saved inlined fragments,
					// whose body they write into, and the pages joining the build.
					oldPages := map[string]bool{}
					for rel := range s.Pages {
						oldPages[rel] = true
					}
					if err := s.BuildAll(); err != nil {
						fmt.Fprintln(os.Stderr, "rebuild:", err)
						continue
					}
					checked := map[string]bool{}
					for rel := range changed {
						if _, ok := s.Pages[rel]; ok {
							checked[rel] = true
						}
						for _, host := range s.includeReferrerAll(rel) {
							checked[host] = true
						}
					}
					for rel := range s.Pages {
						if !oldPages[rel] {
							checked[rel] = true
						}
					}
					// A page the rebuild dropped leaves its targeters with broken
					// links; report them from the index the rebuild just replaced.
					warned += reportProblems(leftProblems(s.buildLinkIndex(), checked), nil)
					if err := rebuild(); err != nil {
						fmt.Fprintln(os.Stderr, "wrap:", err)
					}
					warned += reportProblems(s.checkChanged(checked))
				} else {
					for _, p := range loaded {
						if err := publish(p); err != nil {
							fmt.Fprintln(os.Stderr, "rebuild:", err)
						}
					}
					for rel := range changed { // pages including a fragment
						for _, ref := range s.includeReferrerAll(rel) {
							rebuildPage(ref)
						}
					}
					warned += reportProblems(s.checkChanged(changed))
				}
				what := "all"
				if why != "" {
					what += " (" + why + ")"
				}
				if !full {
					names := make([]string, 0, len(changed))
					for r := range changed {
						names = append(names, r)
					}
					sort.Strings(names)
					what = strings.Join(names, ", ")
					if len(names) > 3 {
						what = fmt.Sprintf("%s (+%d more)", strings.Join(names[:3], ", "), len(names)-3)
					}
				}
				code := ansiGreen
				if warned > 0 {
					code = ansiYellow
				}
				fmt.Println(paint(os.Stdout, code,
					fmt.Sprintf("rebuilt %s in %v", what, time.Since(start).Round(time.Millisecond))))
				// Reload now; search catches up when the index run lands.
				if full {
					broadcast(nil) // the nav moved on every page, and every page can link a new one
				} else {
					broadcast(targets)
				}
				requestReindex()
			case ok := <-indexed:
				setSearch(ok)
			case err, ok := <-watcher.Errors:
				if ok {
					fmt.Fprintln(os.Stderr, "watch:", err)
				}
			}
		}
	}()

	fmt.Printf("colophon dev server on http://%s (watching %s/)\n", addr, s.Src)
	requestReindex() // first index runs behind the listener, never in front of it
	return http.ListenAndServe(addr, mux)
}

// serveAsset answers a request for a content-addressed asset. The address holds
// the bytes' digest, so the file behind a URL never changes and a year of
// "immutable" costs nothing: a page that wants new bytes asks for a new URL.
func serveAsset(w http.ResponseWriter, upath string, a assetFile) {
	switch path.Ext(upath) {
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	default:
		// A name the built-in table knows gets its type stated; without this a
		// config logo's SVG bytes would be sniffed to text/plain, which is an
		// image no browser will render. Unknown extensions keep sniffing.
		if ct := mime.TypeByExtension(path.Ext(upath)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(a.b)
}

// noStore keeps a response out of the cache, for a dev resource whose name does
// not address its contents: pagefind's bundle keeps its file names between index
// runs, so a previous run's files would otherwise be served to this one.
func noStore(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

// pageLink is one author link from a page to another, recorded in the dev
// server's 2-way link index. from and to are the source and target pages'
// source-relative paths, frag is the fragment the link names ("" for none), and
// href is the link as written, for reporting.
type pageLink struct {
	from string
	to   string
	frag string
	href string
}

// pageLinks lists the page links in p's rendered body: the links to other pages,
// with the fragment each names. Assets and external URLs carry no page target,
// and the body is scanned unwrapped, since the chrome the wrap adds links pages
// by construction and would make every nav page look linked.
func (s *Site) pageLinks(p *Page, byOut map[string]*Page) []pageLink {
	var out []pageLink
	for _, m := range hrefRe.FindAllStringSubmatch(string(p.Raw), -1) {
		t, frag, ok := s.resolveHref(p.Rel, m[1], byOut)
		if !ok || t == nil {
			continue
		}
		out = append(out, pageLink{from: p.Rel, to: t.Rel, frag: frag, href: m[1]})
	}
	return out
}

// buildLinkIndex (re)builds the 2-way link index from the current pages and
// returns the links the old index held to a page no longer in the build, which a
// full rebuild just orphaned.
func (s *Site) buildLinkIndex() []pageLink {
	var dropped []pageLink
	for _, ls := range s.links {
		for _, l := range ls {
			if _, fromOK := s.Pages[l.from]; !fromOK {
				continue // the source is gone too; nothing left to report
			}
			if _, toOK := s.Pages[l.to]; !toOK {
				dropped = append(dropped, l)
			}
		}
	}
	s.links = map[string][]pageLink{}
	s.revLinks = map[string][]pageLink{}
	byOut := map[string]*Page{}
	for _, p := range s.Pages {
		byOut[p.OutPath] = p
	}
	for rel, p := range s.Pages {
		links := s.pageLinks(p, byOut)
		s.links[rel] = links
		for _, l := range links {
			s.revLinks[l.to] = append(s.revLinks[l.to], l)
		}
	}
	return dropped
}

// updateLinksFor re-indexes rel's outgoing page links: its old links are pulled
// from the pages they pointed at, and its new ones, read from the just
// re-rendered body, are filed under their targets. The caller has already put the
// new page in s.Pages.
func (s *Site) updateLinksFor(rel string) {
	for _, l := range s.links[rel] {
		s.revLinks[l.to] = removeLink(s.revLinks[l.to], l)
	}
	delete(s.links, rel)
	p := s.Pages[rel]
	if p == nil {
		return
	}
	byOut := map[string]*Page{}
	for _, q := range s.Pages {
		byOut[q.OutPath] = q
	}
	newLinks := s.pageLinks(p, byOut)
	s.links[rel] = newLinks
	for _, l := range newLinks {
		s.revLinks[l.to] = append(s.revLinks[l.to], l)
	}
}

// dropPageLinks forgets rel from the link index, for a page leaving the build:
// its outgoing links are pulled from the pages they pointed at, and the links
// that pointed at it are dropped, since their target is gone.
func (s *Site) dropPageLinks(rel string) {
	for _, l := range s.links[rel] {
		s.revLinks[l.to] = removeLink(s.revLinks[l.to], l)
	}
	delete(s.links, rel)
	delete(s.revLinks, rel)
}

// removeLink drops the first entry equal to want from links.
func removeLink(links []pageLink, want pageLink) []pageLink {
	out := make([]pageLink, 0, len(links))
	for _, l := range links {
		if l != want {
			out = append(out, l)
		}
	}
	return out
}

// ANSI colors for the dev server's terminal output. A colored line always ends
// in reset, so its color never leaks into the next one.
const (
	ansiReset  = "\x1b[0m"
	ansiGray   = "\x1b[90m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
)

// colorable reports whether a stream should carry ANSI color: it is a terminal
// (a character device, the same test notice() uses) and the user has not set
// NO_COLOR. Piped and redirected output stays plain.
func colorable(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// paint wraps s in an ANSI color when colorable(f) holds, leaving it bare
// otherwise.
func paint(f *os.File, code, s string) string {
	if !colorable(f) {
		return s
	}
	return code + s + ansiReset
}

// reportProblems prints a render's gate errors and rewrite hints on the
// terminal, the way the dev server surfaces what a build --check would flag.
// It returns how many warnings it printed, so the caller can color the
// rebuilt line that follows.
func reportProblems(errs, hints []string) int {
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, paint(os.Stderr, ansiYellow, "warning: "+e))
	}
	for _, h := range hints {
		fmt.Fprintln(os.Stderr, paint(os.Stderr, ansiYellow, "hint: "+h))
	}
	return len(errs)
}

// leftProblems names the broken links a full rebuild orphaned: the links the
// dropped pages' targeters kept to them. It skips the changed pages, whose own
// links checkChanged's pageProblems already covers.
func leftProblems(dropped []pageLink, changed map[string]bool) []string {
	seen := map[string]bool{}
	var errs []string
	for _, l := range dropped {
		if changed[l.from] {
			continue
		}
		e := fmt.Sprintf("%s: broken link %s", l.from, l.href)
		if seen[e] {
			continue
		}
		seen[e] = true
		errs = append(errs, e)
	}
	return errs
}

// checkChanged computes the problems a render just introduced, without walking
// the site: the gate errors of the re-rendered pages, and the broken fragment
// targets the pages that point at them, read from the 2-way link index.
// Problems in pages this render did not touch are build --check's to report.
func (s *Site) checkChanged(changed map[string]bool) (errs, hints []string) {
	byOut := map[string]*Page{}
	for _, p := range s.Pages {
		byOut[p.OutPath] = p
	}
	seenErr := map[string]bool{}
	seenHint := map[string]bool{}
	addErr := func(msg string) {
		if !seenErr[msg] {
			seenErr[msg] = true
			errs = append(errs, msg)
		}
	}
	addHint := func(msg string) {
		if !seenHint[msg] {
			seenHint[msg] = true
			hints = append(hints, msg)
		}
	}
	rels := make([]string, 0, len(changed))
	for rel := range changed {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		p, ok := s.Pages[rel]
		if !ok {
			continue
		}
		e, h := s.pageProblems(p, byOut)
		for _, x := range e {
			addErr(x)
		}
		for _, x := range h {
			addHint(x)
		}
		// the pages that point at this one now link its new ids, and a heading
		// this save moved leaves them a missing anchor behind
		for _, l := range s.revLinks[rel] {
			if l.frag == "" || p.IDs[l.frag] {
				continue
			}
			addErr(fmt.Sprintf("%s: missing anchor %s", l.from, l.href))
			if cand := closestSlug(p.IDs, l.frag); cand != "" {
				addHint(fmt.Sprintf("%s: %s -> %s", l.from, l.href, cand))
			}
		}
	}
	return errs, hints
}

// renderedKey maps a URL path to its key in the rendered-page map, which the
// dev server's request handler and the reload channel's ?path= both work in.
func renderedKey(urlPath string) string {
	k := strings.TrimPrefix(path.Clean(urlPath), "/")
	if k == "" || k == "." {
		return "index.html"
	}
	if strings.HasSuffix(urlPath, "/") {
		// A directory URL addresses its index, the way every static host the
		// site may land on reads one. Clean has already collapsed any .., so
		// the joined key is still just a map lookup: miss means 404.
		k = path.Join(k, "index.html")
	}
	return k
}

// changedPaths maps saved sources to the pages whose rendered bytes a save can
// move, which is the saved page itself: a fragment, nav or new-file change takes
// the full rebuild instead, and that reloads every tab.
func (s *Site) changedPaths(changed map[string]bool) map[string]bool {
	out := map[string]bool{}
	for rel := range changed {
		if p, ok := s.Pages[rel]; ok {
			out[p.OutPath] = true
		}
	}
	return out
}

// includeReferrer returns a page that inlines rel (fragments drive a full
// rebuild since the tree/titles they contain may change).
func (s *Site) includeReferrer(rel string) *Page {
	for _, p := range s.Pages {
		if p.Includes[rel] {
			return p
		}
	}
	return nil
}

func (s *Site) includeReferrerAll(rel string) []string {
	var out []string
	for _, p := range s.Pages {
		if p.Includes[rel] {
			out = append(out, p.Rel)
		}
	}
	return out
}
