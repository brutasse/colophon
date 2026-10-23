package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// updateStub serves the two documents an update reads: a release and the asset
// it names. The release points at the server, which proves the updater follows
// browser_download_url instead of building a URL from the tag: that is what
// makes COLOPHON_RELEASES_API a working mirror.
type updateStub struct {
	tag      string
	archive  []byte
	sum      string // digest field, in the API's "sha256:<hex>" form
	etag     string
	requests []string
	url      string
}

func newUpdateStub(t *testing.T, tag string, archive []byte) *updateStub {
	t.Helper()
	sum := sha256.Sum256(archive)
	s := &updateStub{tag: tag, archive: archive, sum: "sha256:" + hex.EncodeToString(sum[:]), etag: `W/"stub"`}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	s.url = srv.URL
	t.Setenv("COLOPHON_RELEASES_API", srv.URL+"/api/releases")
	return s
}

func (s *updateStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.requests = append(s.requests, fmt.Sprintf("%s if-none-match=%q auth=%t",
		r.URL.Path, r.Header.Get("If-None-Match"), r.Header.Get("Authorization") != ""))
	switch {
	case r.URL.Path == "/api/releases/latest" || strings.HasPrefix(r.URL.Path, "/api/releases/tags/"):
		if r.Header.Get("If-None-Match") == s.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if s.tag == "" {
			http.NotFound(w, r) // the state before the first release is published
			return
		}
		all := []string{
			"colophon-linux-amd64.tar.gz", "colophon-linux-arm64.tar.gz",
			"colophon-darwin-amd64.tar.gz", "colophon-darwin-arm64.tar.gz",
			"colophon-windows-amd64.zip",
		}
		rel := release{Tag: s.tag}
		for _, name := range all {
			rel.Assets = append(rel.Assets, asset{Name: name, URL: s.url + "/asset", Digest: s.sum})
		}
		w.Header().Set("ETag", s.etag)
		if err := json.NewEncoder(w).Encode(rel); err != nil {
			panic(err)
		}
	case r.URL.Path == "/asset":
		w.Write(s.archive)
	case r.URL.Path == "/api/releases/ratelimited":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"API rate limit exceeded for 1.2.3.4."}`)
	default:
		http.NotFound(w, r)
	}
}

// cacheHome points the user cache dir at a temp dir on both the Linux and the
// macOS layout, so the update check never writes into a developer's real cache.
func cacheHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	return home
}

func TestSemverVersion(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"1.2.3", "v1.2.3", true},
		{"v1.2.3", "v1.2.3", true},
		{"1.2", "v1.2.0", true}, // a tag that omits the patch still orders
		{"1", "v1.0.0", true},   // as does one that omits both
		{"1.6.0-rc.1", "v1.6.0-rc.1", true},
		{"dev", "", false}, // a build from source has no release to compare with
		{"", "", false},
		{"latest", "", false},
	} {
		got, ok := semverVersion(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("semverVersion(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestIsNewer(t *testing.T) {
	for _, tc := range []struct{ have, want string }{
		{"v1.9.0", "v1.10.0"}, // numeric, not lexicographic
		{"v1.6.0-rc.1", "v1.6.0"},
		{"v1.5.9", "v1.6.0"},
	} {
		if !isNewer(tc.want, tc.have) {
			t.Errorf("isNewer(%q, %q) = false, want true", tc.want, tc.have)
		}
		if isNewer(tc.have, tc.want) {
			t.Errorf("isNewer(%q, %q) = true, want false", tc.have, tc.want)
		}
	}
	if isNewer("v1.2.3", "v1.2.3") {
		t.Error("a release is never newer than itself")
	}
}

func TestReleaseAsset(t *testing.T) {
	rel := &release{Tag: "v1.2.3", Assets: []asset{
		{Name: "checksums.txt"},
		{Name: "colophon-darwin-arm64.tar.gz", URL: "u", Digest: "sha256:" + strings.Repeat("a", 64)},
		{Name: "colophon-windows-amd64.zip"},
	}}
	a, err := releaseAsset(rel, "darwin", "arm64")
	if err != nil || a.URL != "u" {
		t.Errorf("releaseAsset(darwin/arm64) = %+v, %v", a, err)
	}
	if _, err := releaseAsset(rel, "darwin", "amd64"); err == nil ||
		!strings.Contains(err.Error(), "colophon-darwin-amd64.tar.gz") {
		t.Errorf("releaseAsset for an absent platform: %v, want the asset name in the error", err)
	}
	if a, err := releaseAsset(rel, "windows", "amd64"); err != nil || !strings.HasSuffix(a.Name, ".zip") {
		t.Errorf("releaseAsset(windows/amd64) = %+v, %v; want the zip", a, err)
	}
}

func TestDigestHex(t *testing.T) {
	hex64 := strings.Repeat("a", 64)
	for _, in := range []string{"sha256:" + hex64, hex64} {
		got, err := digestHex(in)
		if err != nil || got != hex64 {
			t.Errorf("digestHex(%q) = %q, %v; want the bare sum", in, got, err)
		}
	}
	for _, in := range []string{"", "md5:" + hex64, "sha256:short"} {
		if _, err := digestHex(in); err == nil {
			t.Errorf("digestHex(%q) accepted a digest it cannot verify", in)
		}
	}
}

func TestFetchRelease(t *testing.T) {
	archive := pagefindTarball(t, "colophon", "new binary")
	s := newUpdateStub(t, "v0.2.0", archive)

	ctx := t.Context()
	rel, etag, err := fetchRelease(ctx, releasesURL("latest"), "")
	if err != nil {
		t.Fatalf("fetchRelease: %v", err)
	}
	if rel.Tag != "v0.2.0" || len(rel.Assets) == 0 {
		t.Errorf("fetchRelease = %+v", rel)
	}
	if etag != s.etag {
		t.Errorf("etag %q, want %q", etag, s.etag)
	}

	// A token is used when the environment has one, which is how a shared build
	// host escapes the anonymous quota.
	t.Setenv("GITHUB_TOKEN", "tok")
	if _, _, err := fetchRelease(ctx, releasesURL("latest"), ""); err != nil {
		t.Fatalf("fetchRelease with a token: %v", err)
	}
	if !strings.Contains(s.requests[len(s.requests)-1], "auth=true") {
		t.Errorf("no Authorization header sent: %s", s.requests[len(s.requests)-1])
	}

	if _, _, err := fetchRelease(ctx, releasesURL("latest"), s.etag); !errors.Is(err, errNotModified) {
		t.Errorf("conditional request: %v, want errNotModified", err)
	}
	if _, _, err := fetchRelease(ctx, releasesURL("tags/v9.9.9"), ""); err != nil {
		t.Errorf("fetchRelease by tag: %v", err)
	}

	s.tag = "" // the repository has no release yet
	if _, _, err := fetchRelease(ctx, releasesURL("latest"), ""); !errors.Is(err, errNoRelease) {
		t.Errorf("fetchRelease with no release: %v, want errNoRelease", err)
	}

	rateLimited := newUpdateStub(t, "v0.2.0", archive)
	if _, _, err := fetchRelease(ctx, rateLimited.url+"/api/releases/ratelimited", ""); err == nil ||
		!strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("fetchRelease when rate limited: %v, want the API's reason", err)
	}
}

func TestCheckForUpdateAsksOnceADay(t *testing.T) {
	cacheHome(t)
	archive := pagefindTarball(t, "colophon", "new binary")
	s := newUpdateStub(t, "v0.2.0", archive)
	restore := stampVersion(t, "0.1.0")
	defer restore()

	var out bytes.Buffer
	checkForUpdate(&out)
	if got := out.String(); !strings.Contains(got, "release 0.2.0 is available") || !strings.Contains(got, "colophon update") {
		t.Errorf("notice = %q, want the newer release and the command", got)
	}
	if len(s.requests) != 1 {
		t.Fatalf("requests after the first check: %d, want 1", len(s.requests))
	}

	// A second command inside the interval is answered from the cache file.
	out.Reset()
	checkForUpdate(&out)
	if len(s.requests) != 1 {
		t.Errorf("requests after a cached check: %d, want 1", len(s.requests))
	}
	if !strings.Contains(out.String(), "release 0.2.0") {
		t.Errorf("cached notice = %q, want the same line", out.String())
	}

	// Once the interval passes, the stored ETag comes back on the request, which
	// is what turns the next answer into a 304.
	expireUpdateCheck(t)
	out.Reset()
	checkForUpdate(&out)
	if len(s.requests) != 2 {
		t.Fatalf("requests after the interval: %d, want 2", len(s.requests))
	}
	if !strings.Contains(s.requests[1], `if-none-match="W/\"stub\""`) {
		t.Errorf("expired check sent no ETag: %s", s.requests[1])
	}
	if !strings.Contains(out.String(), "release 0.2.0") {
		t.Errorf("notice after a 304 = %q, want the cached line", out.String())
	}
}

func TestCheckForUpdateSilence(t *testing.T) {
	archive := pagefindTarball(t, "colophon", "new binary")

	t.Run("this release is the newest", func(t *testing.T) {
		cacheHome(t)
		s := newUpdateStub(t, "v0.1.0", archive)
		defer stampVersion(t, "0.1.0")()
		var out bytes.Buffer
		checkForUpdate(&out)
		if out.Len() != 0 {
			t.Errorf("notice for the newest release = %q, want nothing", out.String())
		}
		if len(s.requests) != 1 {
			t.Errorf("requests = %d, want 1", len(s.requests))
		}
	})

	t.Run("a source build asks nothing", func(t *testing.T) {
		cacheHome(t)
		s := newUpdateStub(t, "v0.2.0", archive)
		var out bytes.Buffer
		checkForUpdate(&out) // version is dev unless a test restamps it
		if out.Len() != 0 || len(s.requests) != 0 {
			t.Errorf("dev build: out=%q requests=%d, want nothing and no request", out.String(), len(s.requests))
		}
	})

	t.Run("an unreachable API stays quiet", func(t *testing.T) {
		cacheHome(t)
		t.Setenv("COLOPHON_RELEASES_API", "http://127.0.0.1:1/releases")
		defer stampVersion(t, "0.1.0")()
		var out bytes.Buffer
		checkForUpdate(&out)
		if out.Len() != 0 {
			t.Errorf("notice with no network = %q, want nothing", out.String())
		}
	})
}

func TestRunUpdateInstallsItself(t *testing.T) {
	cacheHome(t)
	archive := pagefindTarball(t, "colophon", "the new binary")
	s := newUpdateStub(t, "v0.2.0", archive)
	defer stampVersion(t, "0.1.0")()

	self := writeSelf(t, "the old binary", 0o750)

	var out bytes.Buffer
	if err := runUpdate(&out, "", false, self); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	got, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "the new binary" {
		t.Errorf("self after update = %q, want the released binary", got)
	}
	if fi, _ := os.Stat(self); fi.Mode().Perm() != 0o750 {
		t.Errorf("mode after update = %v, want the mode of the binary it replaced", fi.Mode().Perm())
	}
	if msg := out.String(); !strings.Contains(msg, "0.2.0 installed at") {
		t.Errorf("update report = %q", msg)
	}

	// Nothing left to do: the next command is the binary that was just
	// installed, so it reports the version it installed.
	version = "0.2.0"
	out.Reset()
	if err := runUpdate(&out, "", false, self); err != nil {
		t.Fatalf("second runUpdate: %v", err)
	}
	if !strings.Contains(out.String(), "is the newest release") {
		t.Errorf("second update report = %q", out.String())
	}

	// An explicit tag moves backwards, which is how a user rolls back.
	out.Reset()
	if err := runUpdate(&out, "0.1.0", false, self); err != nil {
		t.Fatalf("runUpdate to an older tag: %v", err)
	}
	if len(s.requests) < 3 {
		t.Errorf("requests = %d, want the tag endpoint asked as well", len(s.requests))
	}
}

func TestRunUpdateRefusesToTouchAnything(t *testing.T) {
	archive := pagefindTarball(t, "colophon", "the new binary")

	t.Run("a checksum that does not match", func(t *testing.T) {
		cacheHome(t)
		s := newUpdateStub(t, "v0.2.0", archive)
		s.sum = "sha256:" + strings.Repeat("0", 64)
		defer stampVersion(t, "0.1.0")()
		self := writeSelf(t, "the old binary", 0o755)

		err := runUpdate(&bytes.Buffer{}, "", false, self)
		if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("runUpdate with a wrong digest: %v, want a mismatch", err)
		}
		got, _ := os.ReadFile(self)
		if string(got) != "the old binary" {
			t.Errorf("self after a refused update = %q, want the bytes it started with", got)
		}
	})

	t.Run("an asset with no published digest", func(t *testing.T) {
		cacheHome(t)
		s := newUpdateStub(t, "v0.2.0", archive)
		s.sum = ""
		defer stampVersion(t, "0.1.0")()
		self := writeSelf(t, "the old binary", 0o755)

		if err := runUpdate(&bytes.Buffer{}, "", false, self); err == nil ||
			!strings.Contains(err.Error(), "digest") {
			t.Fatalf("runUpdate with no digest: %v, want a refusal to install what it cannot verify", err)
		}
		got, _ := os.ReadFile(self)
		if string(got) != "the old binary" {
			t.Errorf("self after an unverified release = %q, want it untouched", got)
		}
	})

	t.Run("a directory nobody may write", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root may write anywhere, so the refusal cannot be shown")
		}
		cacheHome(t)
		newUpdateStub(t, "v0.2.0", archive)
		defer stampVersion(t, "0.1.0")()
		dir := filepath.Join(t.TempDir(), "locked")
		self := filepath.Join(dir, "colophon")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(self, []byte("the old binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o500); err != nil { // read and execute only from here on
			t.Fatal(err)
		}
		defer os.Chmod(dir, 0o700)
		if err := runUpdate(&bytes.Buffer{}, "", false, self); err == nil ||
			!strings.Contains(err.Error(), dir) {
			t.Fatalf("runUpdate into an unwritable directory: %v, want the directory named", err)
		}
	})

	t.Run("a binary built from source", func(t *testing.T) {
		cacheHome(t)
		newUpdateStub(t, "v0.2.0", archive)
		self := writeSelf(t, "the old binary", 0o755)
		err := runUpdate(&bytes.Buffer{}, "", false, self)
		if err == nil || !strings.Contains(err.Error(), "scripts/install.sh") {
			t.Fatalf("runUpdate on a dev build: %v, want the install fallback", err)
		}
	})

	t.Run("a repository with no release yet", func(t *testing.T) {
		cacheHome(t)
		newUpdateStub(t, "", nil)
		defer stampVersion(t, "0.1.0")()
		self := writeSelf(t, "the old binary", 0o755)
		err := runUpdate(&bytes.Buffer{}, "", false, self)
		if err == nil || !strings.Contains(err.Error(), "no published release") {
			t.Fatalf("runUpdate before the first release: %v, want that answer", err)
		}
		got, _ := os.ReadFile(self)
		if string(got) != "the old binary" {
			t.Errorf("self after a 404 = %q, want it untouched", got)
		}
		// Asking for one tag says which tag was missing.
		if err := runUpdate(&bytes.Buffer{}, "9.9.9", false, self); err == nil ||
			!strings.Contains(err.Error(), "9.9.9") {
			t.Errorf("runUpdate --to a missing tag: %v, want the tag named", err)
		}
	})

	t.Run("--check installs nothing", func(t *testing.T) {
		cacheHome(t)
		newUpdateStub(t, "v0.2.0", archive)
		defer stampVersion(t, "0.1.0")()
		self := writeSelf(t, "the old binary", 0o755)
		var out bytes.Buffer
		if err := runUpdate(&out, "", true, self); err != nil {
			t.Fatalf("runUpdate --check: %v", err)
		}
		if !strings.Contains(out.String(), "0.2.0 is available") {
			t.Errorf("--check report = %q", out.String())
		}
		got, _ := os.ReadFile(self)
		if string(got) != "the old binary" {
			t.Errorf("self after --check = %q, want it untouched", got)
		}
	})
}

// The Windows asset is the one release archive that is not a tarball.
func TestInstallReleaseFromZip(t *testing.T) {
	var raw bytes.Buffer
	zw := zip.NewWriter(&raw)
	for _, name := range []string{"colophon.exe", "LICENSE.txt"} {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("binary:" + name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	body := raw.Bytes()
	sum := sha256.Sum256(body)

	dir := t.TempDir()
	self := filepath.Join(dir, "colophon.exe")
	if err := os.WriteFile(self, []byte("the old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := asset{
		Name:   "colophon-windows-amd64.zip",
		URL:    serveOnce(t, body),
		Digest: "sha256:" + hex.EncodeToString(sum[:]),
	}
	if err := installRelease(&bytes.Buffer{}, a, self); err != nil {
		t.Fatalf("installRelease from a zip: %v", err)
	}
	got, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary:colophon.exe" {
		t.Errorf("installed %q, want the archive's binary", got)
	}
}

// stampVersion makes the running test binary look like a released one, and puts
// the value back afterwards.
func stampVersion(t *testing.T, v string) func() {
	t.Helper()
	have := version
	version = v
	return func() { version = have }
}

// writeSelf stands in for the running binary at a known path with a known mode.
func writeSelf(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "colophon")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func serveOnce(t *testing.T, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/asset"
}

// expireUpdateCheck rewinds the cache file's clock, which is how a test reaches
// the once-a-day boundary without waiting a day.
func expireUpdateCheck(t *testing.T) {
	t.Helper()
	c := readUpdateCheck()
	if c.Latest == "" {
		t.Fatal("no cached update check to expire")
	}
	c.Checked = time.Now().Add(-2 * updateCheckInterval)
	writeUpdateCheck(c)
}
