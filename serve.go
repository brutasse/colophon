package main

import (
	"bytes"
	"context"
	"fmt"
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
	rebuildPage := func(rel string) error {
		old, ok := s.Pages[rel]
		raw, err := os.ReadFile(filepath.Join(s.Src, filepath.FromSlash(rel)))
		if err != nil {
			if ok {
				delete(s.Pages, rel)
				mu.Lock()
				delete(rendered, "/"+old.OutPath)
				mu.Unlock()
			}
			return nil
		}
		p := s.renderPage(rel, string(raw))
		s.Pages[rel] = p
		s.finalize(p) // sets p.Body; wrapPage renders from it
		b, err := s.wrapPage(p)
		if err != nil {
			return err
		}
		mu.Lock()
		rendered["/"+p.OutPath] = b
		mu.Unlock()
		return nil
	}
	s.Search = true // optimistic; the first index result re-wraps without it on failure
	if err := rebuild(); err != nil {
		return err
	}

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
				fmt.Printf("search indexed in %v\n", time.Since(start).Round(time.Millisecond))
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
	mux.Handle("/pagefind/", http.FileServer(http.Dir(siteDir)))
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
		upath := renderedKey(r.URL.Path)
		if strings.HasPrefix(upath, "_assets/") {
			b, err := assetsFS.ReadFile("assets/" + strings.TrimPrefix(upath, "_assets/"))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			serveBytes(w, upath, b)
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
				// An asset that no page renders and no include comment inlines
				// changes no markup, so only pages and fragments reach `pending`.
				if !strings.HasSuffix(rel, ".md") && s.includeReferrer(filepath.ToSlash(rel)) == nil {
					continue
				}
				pending[filepath.ToSlash(rel)] = true
				debounce.Reset(120 * time.Millisecond)
			case <-debounce.C:
				changed := pending
				pending = map[string]bool{}
				// Resolved before the rebuild, so a page this save deletes still
				// reaches the tab that is showing it.
				targets := s.changedPaths(changed)
				start := time.Now()
				full := false
				for rel := range changed {
					_, known := s.Pages[rel]
					// A file that changed its `fragment:` verdict changes which
					// files are pages at all, so only a full rebuild is right.
					if was := s.forgetFragment(rel); was != s.isFragment(rel) {
						full = true
						break
					}
					if strings.HasSuffix(rel, "index.md") || s.includeReferrer(rel) != nil ||
						s.navDeclared(rel) || (!known && !s.excluded(rel)) {
						full = true
						break
					}
				}
				if full {
					if err := s.BuildAll(); err != nil {
						fmt.Fprintln(os.Stderr, "rebuild:", err)
						continue
					}
					if err := rebuild(); err != nil {
						fmt.Fprintln(os.Stderr, "wrap:", err)
					}
					// A new page takes this branch, and it is the page whose
					// reachability the author most wants told.
					s.checkChanged(changed)
				} else {
					for rel := range changed {
						if s.excluded(rel) {
							continue
						}
						if err := rebuildPage(rel); err != nil {
							fmt.Fprintln(os.Stderr, "rebuild:", err)
						}
					}
					for rel := range changed { // pages including a fragment
						for _, ref := range s.includeReferrerAll(rel) {
							rebuildPage(ref)
						}
					}
					s.checkChanged(changed)
				}
				what := "all"
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
				fmt.Printf("rebuilt %s in %v\n", what, time.Since(start).Round(time.Millisecond))
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

func serveBytes(w http.ResponseWriter, upath string, b []byte) {
	switch {
	case strings.HasSuffix(upath, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(upath, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	}
	w.Write(b)
}

// checkChanged runs the link check on just-rebuilt pages without killing the server
// on a failure, and recomputes reachability because it depends on every page.
func (s *Site) checkChanged(changed map[string]bool) {
	byOut := map[string]*Page{}
	for _, p := range s.Pages {
		byOut[p.OutPath] = p
	}
	reasons := s.orphanReasons(byOut)
	for rel := range changed {
		p, ok := s.Pages[rel]
		if !ok {
			continue
		}
		if msg, ok := reasons[rel]; ok {
			fmt.Fprintln(os.Stderr, "check:", rel+": "+msg)
		}
		errs, hints := s.pageProblems(p, byOut)
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "check:", e)
		}
		for _, h := range hints {
			fmt.Fprintln(os.Stderr, "hint:", h)
		}
	}
}

// renderedKey maps a URL path to its key in the rendered-page map, which the
// dev server's request handler and the reload channel's ?path= both work in.
func renderedKey(urlPath string) string {
	k := strings.TrimPrefix(path.Clean(urlPath), "/")
	if k == "" || k == "." {
		k = "index.html"
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
