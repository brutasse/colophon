package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// pagefindVersion is the indexer release colophon downloads. Bump it together
// with the pinned sums below; scripts/update_pagefind.sh prints both.
const pagefindVersion = "1.5.2"

// bundleDir is the indexer output subdirectory inside the site; the name is
// what the Pagefind web components load by default.
const bundleDir = "pagefind"

// pagefindDownloadTimeout bounds one download end to end, body included: a
// stalled mirror must degrade to "no search", never to a build that hangs.
const pagefindDownloadTimeout = 3 * time.Minute

// pagefindRelease is the release asset for one platform, and the sha256 of
// its tarball as pinned here. The sum comes from this file rather than the
// release's own .sha256 asset, because a checksum fetched alongside the
// binary it describes validates nothing.
type pagefindRelease struct {
	triple string // release target in the asset name
	sha256 string
}

// pagefindReleases covers every Pagefind release platform; only the host
// entry is ever read, and a platform absent here builds without search.
var pagefindReleases = map[string]pagefindRelease{
	"linux/amd64":   {"x86_64-unknown-linux-musl", "afb824a9e7f64905a934900481cea5be679c03975e527329e0e5e6cc70f5feda"},
	"linux/arm64":   {"aarch64-unknown-linux-musl", "f50ec608bcbf431cebd84e0efa3a5b041ee63df2ad81f138023e0cfd2f509424"},
	"darwin/amd64":  {"x86_64-apple-darwin", "26f51b4ba921897142338fb13b836420696e251fe197b1782ea7981de311156d"},
	"darwin/arm64":  {"aarch64-apple-darwin", "7286f394a349bd37677d44a65a20078a02b1747da0b0814d83403bf86be17abe"},
	"windows/amd64": {"x86_64-pc-windows-msvc", "fab125d5e8e2d3481ffe7d36dec6e101f54a6581cd51cf5e2e09220d4bc78e9c"},
	"windows/arm64": {"aarch64-pc-windows-msvc", "c4d869293a9cd5c14c2dff7e7f568b27a5f4acb1124a2097b638cffc0d82b840"},
	"freebsd/amd64": {"x86_64-unknown-freebsd", "15e9df58db18a81ff211d5fc4a4181e3fc8211b0a20f389533fd18c43f0f2a32"},
}

// indexPagefind runs the platform indexer over an on-disk site directory,
// (re)writing <dir>/pagefind. The site must already contain the built HTML.
func indexPagefind(dir string) error {
	bin, cached, err := pagefindBin()
	if err != nil {
		return err
	}
	run := func() ([]byte, error) {
		cmd := exec.Command(bin, "--site", ".", "--output-subdir", bundleDir)
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}
	// An in-place run reuses the previous bundle and re-indexes only changed
	// pages. Two states make pagefind fail on a site that is otherwise fine: a
	// stale or foreign bundle, and a damaged cache entry. Drop both and retry
	// cold; a second failure belongs to the site or to this platform.
	out, err := run()
	if err == nil {
		return nil
	}
	if rmErr := os.RemoveAll(filepath.Join(dir, bundleDir)); rmErr != nil {
		return rmErr
	}
	if cached {
		os.Remove(bin)
		if bin, _, err = pagefindBin(); err != nil { // refetched: the entry is gone
			return err
		}
	}
	if out, err = run(); err != nil {
		return fmt.Errorf("pagefind: %w: %s", err, firstLine(out))
	}
	return nil
}

// pagefindBin returns the indexer to run on this machine, downloading one into
// the user cache dir the first time it is needed. The bool says whether the
// path came from that cache, which is the only one colophon may throw away.
// COLOPHON_PAGEFIND names a binary to run as is, which is how an air-gapped or
// mirror-only machine keeps search.
func pagefindBin() (string, bool, error) {
	if override := os.Getenv("COLOPHON_PAGEFIND"); override != "" {
		if !isExecutable(override) {
			return "", false, fmt.Errorf("COLOPHON_PAGEFIND=%s is not a runnable file", override)
		}
		return override, false, nil
	}
	rel, ok := pagefindReleases[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", false, fmt.Errorf("search indexer not published for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	dst := filepath.Join(pagefindCacheDir(rel.triple), pagefindExe())
	if isExecutable(dst) {
		return dst, true, nil
	}
	if err := fetchPagefind(rel, dst); err != nil {
		return "", false, err
	}
	return dst, true, nil
}

// fetchPagefind fills the cache entry: download, check the pinned sum, pull
// the binary out of the tarball, then publish it with a rename so that a
// concurrent build never runs a half-written indexer. Every temporary file
// lives in dst's directory, which is what makes that rename atomic.
func fetchPagefind(rel pagefindRelease, dst string) error {
	url := fmt.Sprintf("https://github.com/Pagefind/pagefind/releases/download/v%s/pagefind-v%s-%s.tar.gz",
		pagefindVersion, pagefindVersion, rel.triple)
	fmt.Fprintf(os.Stderr, "colophon: downloading pagefind %s (%s) for the search index\n",
		pagefindVersion, rel.triple)
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tgz, err := os.CreateTemp(dir, "pagefind-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tgz.Name())
	if err := downloadPagefind(url, tgz, rel.sha256); err != nil {
		tgz.Close()
		return err
	}
	if err := tgz.Close(); err != nil {
		return err
	}

	bin, err := os.CreateTemp(dir, "pagefind.new-*")
	if err != nil {
		return err
	}
	defer os.Remove(bin.Name()) // no-op once the rename below has moved it
	if err := extractPagefind(tgz.Name(), bin); err != nil {
		bin.Close()
		return err
	}
	if err := bin.Close(); err != nil {
		return err
	}
	if err := os.Chmod(bin.Name(), 0o755); err != nil {
		return err
	}
	if err := os.Rename(bin.Name(), dst); err != nil {
		if isExecutable(dst) {
			return nil // a concurrent build got there first with a verified copy
		}
		return err
	}
	writePagefindLicense(dir)
	return nil
}

// downloadPagefind copies url into f while hashing it, so a truncated or
// swapped tarball fails the sum check instead of reaching the extractor.
func downloadPagefind(url string, f io.Writer, want string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pagefindDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("pagefind: %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pagefind: %s: %s", url, resp.Status)
	}
	sum := sha256.New()
	if _, err := io.Copy(f, io.TeeReader(resp.Body, sum)); err != nil {
		return fmt.Errorf("pagefind: %s: %w", url, err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != want {
		return fmt.Errorf("pagefind: checksum mismatch for %s: want %s, got %s", filepath.Base(url), want, got)
	}
	return nil
}

// extractPagefind copies the indexer out of the release tarball. The asset
// holds one binary at its top level, pagefind or pagefind.exe.
func extractPagefind(tgz string, dst io.Writer) error {
	f, err := os.Open(tgz)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("pagefind: no indexer binary in %s", filepath.Base(tgz))
		}
		if err != nil {
			return err
		}
		name := filepath.Base(h.Name)
		if h.Typeflag == tar.TypeReg && (name == "pagefind" || name == "pagefind.exe") {
			_, err := io.Copy(dst, tr)
			return err
		}
	}
}

// writePagefindLicense puts Pagefind's licence text next to the cached binary,
// where anyone inspecting the cache would look for it. Best effort: it is
// attribution, not something the index run depends on.
func writePagefindLicense(dir string) {
	b, err := fs.ReadFile(assetsFS, "assets/PAGEFIND-LICENSE.txt")
	if err == nil {
		os.WriteFile(filepath.Join(dir, "LICENSE.txt"), b, 0o644)
	}
}

// pagefindCacheDir holds one directory per release and platform, so bumping
// pagefindVersion or moving between amd64 and arm64 never reuses a binary.
func pagefindCacheDir(triple string) string {
	base, err := os.UserCacheDir()
	if err != nil { // no HOME in the environment: a temp dir is still a cache
		base = filepath.Join(os.TempDir(), "colophon-cache")
	}
	return filepath.Join(base, "colophon", "pagefind-"+pagefindVersion+"-"+triple)
}

func pagefindExe() string {
	if runtime.GOOS == "windows" {
		return "pagefind.exe"
	}
	return "pagefind"
}

// isExecutable is deliberately loose: win32 file modes carry no executable
// bit, so there the file only has to exist.
func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	return runtime.GOOS == "windows" || fi.Mode().Perm()&0o111 != 0
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSuffix(s, "\r")
}
