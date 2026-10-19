package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// pagefindTarball builds a release-shaped tarball holding one file, so the
// extractor can be tested without a network or a 5 MB fixture.
func pagefindTarball(t *testing.T, name, body string) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatalf("tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	if _, err := w.Write(raw.Bytes()); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return gz.Bytes()
}

func writeTarballFile(t *testing.T, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pagefind.tar.gz")
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatalf("write tarball: %v", err)
	}
	return p
}

func TestExtractPagefind(t *testing.T) {
	for _, name := range []string{"pagefind", "pagefind.exe"} {
		tgz := writeTarballFile(t, pagefindTarball(t, name, "#!/bin/sh\necho indexed\n"))
		var got bytes.Buffer
		if err := extractPagefind(tgz, &got); err != nil {
			t.Fatalf("extractPagefind(%s): %v", name, err)
		}
		if want := "#!/bin/sh\necho indexed\n"; got.String() != want {
			t.Errorf("extracted %q, want %q", got.String(), want)
		}
	}
}

func TestExtractPagefindNoBinary(t *testing.T) {
	tgz := writeTarballFile(t, pagefindTarball(t, "README.md", "docs only\n"))
	err := extractPagefind(tgz, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no pagefind") {
		t.Errorf("extractPagefind on an archive without a binary: %v, want a missing-binary error", err)
	}
}

func TestDownloadPagefindChecksum(t *testing.T) {
	content := pagefindTarball(t, "pagefind", "payload")
	sum := sha256.Sum256(content)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	if err := downloadVerified(srv.URL, &buf, hex.EncodeToString(sum[:]), "pagefind"); err != nil {
		t.Fatalf("downloadVerified with the pinned sum: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), content) {
		t.Errorf("downloaded %d bytes, want %d", buf.Len(), len(content))
	}

	buf.Reset()
	err := downloadVerified(srv.URL, &buf, strings.Repeat("0", 64), "pagefind")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("downloadVerified with a wrong sum: %v, want a mismatch error", err)
	}
}

func TestDownloadPagefindStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	err := downloadVerified(srv.URL, io.Discard, strings.Repeat("0", 64), "pagefind")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("downloadVerified on 404: %v, want the status in the error", err)
	}
}

// The pinned sums are the only thing standing between a release download and
// an arbitrary binary being executed, so they are guarded here as data.
func TestPagefindPinnedSums(t *testing.T) {
	for platform, rel := range pagefindReleases {
		if rel.triple == "" {
			t.Errorf("%s: empty release triple", platform)
		}
		if len(rel.sha256) != 64 {
			t.Errorf("%s: sha256 %q is not 64 hex characters", platform, rel.sha256)
		}
		for _, c := range rel.sha256 {
			if !strings.ContainsRune("0123456789abcdef", c) {
				t.Errorf("%s: sha256 %q is not hex", platform, rel.sha256)
				break
			}
		}
	}
	for _, platform := range []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"} {
		if _, ok := pagefindReleases[platform]; !ok {
			t.Errorf("%s has no pinned release, so search would be off there", platform)
		}
	}
}

func TestPagefindBinOverride(t *testing.T) {
	// Only the override branches: resolving the real path would download, and
	// go test must stay offline.
	t.Setenv("COLOPHON_PAGEFIND", filepath.Join(t.TempDir(), "missing"))
	if _, _, err := pagefindBin(); err == nil || !strings.Contains(err.Error(), "COLOPHON_PAGEFIND") {
		t.Errorf("pagefindBin with a dangling override: %v, want it named in the error", err)
	}

	bin := filepath.Join(t.TempDir(), "pagefind")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("COLOPHON_PAGEFIND", bin)
	got, cached, err := pagefindBin()
	if err != nil {
		t.Fatalf("pagefindBin with a runnable override: %v", err)
	}
	if got != bin {
		t.Errorf("pagefindBin returned %q, want the override %q", got, bin)
	}
	if cached {
		t.Error("an override is not a cache entry, so colophon must not delete it")
	}
}

// The image build reads the release table through scripts/pagefind_pin.sh, so
// the two readers of pagefind.go must agree; a table format change that breaks
// the script's parsing fails here rather than in the image pipeline.
func TestPagefindPinScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripts/pagefind_pin.sh is a POSIX shell script")
	}
	for platform, rel := range pagefindReleases {
		for field, want := range map[string]string{
			"triple":  rel.triple,
			"sha256":  rel.sha256,
			"version": pagefindVersion,
		} {
			out, err := exec.Command("scripts/pagefind_pin.sh", platform, field).Output()
			if err != nil {
				t.Fatalf("pagefind_pin.sh %s %s: %v", platform, field, err)
			}
			if got := strings.TrimSpace(string(out)); got != want {
				t.Errorf("pagefind_pin.sh %s %s = %q, want %q", platform, field, got, want)
			}
		}
		out, err := exec.Command("scripts/pagefind_pin.sh", platform, "dir", "/cache").Output()
		if err != nil {
			t.Fatalf("pagefind_pin.sh %s dir: %v", platform, err)
		}
		want := filepath.Join("/cache", "colophon", "pagefind-"+pagefindVersion+"-"+rel.triple)
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("pagefind_pin.sh %s dir = %q, want %q", platform, got, want)
		}
	}

	if out, err := exec.Command("scripts/pagefind_pin.sh", "plan9/amd64", "triple").Output(); err == nil {
		t.Errorf("pagefind_pin.sh for an unpinned platform printed %q, want a failure", out)
	}
}

// The image warms the indexer cache through scripts/pagefind_pin.sh, and colophon
// reads it back through os.UserCacheDir, whose root is platform specific:
// $XDG_CACHE_HOME on Linux, which is what the image sets, and $HOME/Library/Caches
// on macOS, where XDG_CACHE_HOME is ignored. The two paths are computed apart, so
// the layout below the root is pinned here against the script the image ran.
func TestPagefindCacheDirLayout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripts/pagefind_pin.sh is a POSIX shell script")
	}
	// Each platform finds its root inside this one directory, so the test asserts
	// the layout rather than whatever cache the machine running it happens to have.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, "caches"))
	root, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("no user cache dir on %s: %v", runtime.GOOS, err)
	}
	for platform, rel := range pagefindReleases {
		out, err := exec.Command("scripts/pagefind_pin.sh", platform, "dir", root).Output()
		if err != nil {
			t.Fatalf("pagefind_pin.sh %s dir: %v", platform, err)
		}
		if got, want := pagefindCacheDir(rel.triple), strings.TrimSpace(string(out)); got != want {
			t.Errorf("pagefindCacheDir(%s) = %q, want the path the image warms, %q", rel.triple, got, want)
		}
	}
}
