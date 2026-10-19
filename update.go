package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Self-update reads the GitHub releases API rather than a page or a feed: the
// response names the asset for this platform, points at it, and carries the
// sha256 GitHub computed for the stored bytes, so one request answers "what is
// newest" and "what may I trust" together.
const (
	releasesAPI = "https://api.github.com/repos/brutasse/colophon/releases"

	// noticeTimeout bounds the courtesy check that runs before a build. A
	// network that will not answer must cost seconds, not the build.
	noticeTimeout = 2 * time.Second
	// updateAPITimeout bounds the same request when the user asked for it, and
	// downloadVerified bounds the archive itself.
	updateAPITimeout = 15 * time.Second
	// updateCheckInterval is how long a cached answer is believed, which is what
	// keeps an unauthenticated API quota of 60 requests an hour from mattering.
	updateCheckInterval = 24 * time.Hour
	// releaseResponseLimit bounds a response body read into memory.
	releaseResponseLimit = 4 << 20
)

var (
	errNoRelease   = errors.New("no published release found")
	errNotModified = errors.New("not modified")
)

// release is the part of a GitHub release response colophon reads.
type release struct {
	Tag        string  `json:"tag_name"`
	Assets     []asset `json:"assets"`
	Prerelease bool    `json:"prerelease"`
}

type asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

// updateCheck is the cached answer, kept so a build pays for at most one
// request a day and so the API's own ETag can make even that a no-op.
type updateCheck struct {
	Checked time.Time `json:"checked"`
	ETag    string    `json:"etag,omitempty"`
	Latest  string    `json:"latest,omitempty"`
}

// releasesURL builds an API URL for "latest" or "tags/v1.2.3".
// COLOPHON_RELEASES_API names a mirror or a GitHub Enterprise endpoint serving
// the same responses; asset URLs come from the response, never from here, so a
// mirror needs nothing else.
func releasesURL(suffix string) string {
	base := os.Getenv("COLOPHON_RELEASES_API")
	if base == "" {
		base = releasesAPI
	}
	return strings.TrimSuffix(base, "/") + "/" + suffix
}

// githubToken raises the request from the 60-per-hour anonymous quota, which a
// shared build host burns through quickly. It is read, never requested: a user
// who has the CLI configured already has one in the environment.
func githubToken() string {
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if tok := os.Getenv(name); tok != "" {
			return tok
		}
	}
	return ""
}

// fetchRelease asks for one release. The returned etag is the one to send back
// on the next request; etag may be empty to ask unconditionally.
func fetchRelease(ctx context.Context, url, etag string) (*release, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if tok := githubToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", url, err)
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, releaseResponseLimit)
	switch resp.StatusCode {
	case http.StatusOK:
		var rel release
		if err := json.NewDecoder(body).Decode(&rel); err != nil {
			return nil, "", fmt.Errorf("%s: %w", url, err)
		}
		return &rel, resp.Header.Get("ETag"), nil
	case http.StatusNotModified:
		return nil, etag, errNotModified
	case http.StatusNotFound:
		return nil, "", errNoRelease
	case http.StatusForbidden, http.StatusTooManyRequests:
		// The body says which it was, and the difference matters: a rate limit
		// is lifted by a token, anything else is not.
		b, _ := io.ReadAll(body)
		return nil, "", fmt.Errorf("%s: %s; %s", url, resp.Status, firstLine(b))
	default:
		return nil, "", fmt.Errorf("%s: %s", url, resp.Status)
	}
}

// releaseAsset picks the archive for one platform. The name is the fixed one
// the release workflow publishes, with no version in it: that name is a
// contract both scripts/install.sh and this reader depend on.
func releaseAsset(rel *release, goos, goarch string) (asset, error) {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	name := fmt.Sprintf("colophon-%s-%s.%s", goos, goarch, ext)
	for _, a := range rel.Assets {
		if a.Name == name {
			return a, nil
		}
	}
	return asset{}, fmt.Errorf("release %s has no %s asset", rel.Tag, name)
}

// digestHex reads the sha256 out of an asset's digest field, which GitHub
// writes as "sha256:<hex>". Without a digest there is nothing to check the
// download against, so the update stops rather than installing what it cannot
// verify.
func digestHex(digest string) (string, error) {
	sum := digest
	if algo, hex, ok := strings.Cut(digest, ":"); ok {
		if algo != "sha256" {
			return "", fmt.Errorf("release asset digest %q is not sha256", digest)
		}
		sum = hex
	}
	if len(sum) != 64 {
		return "", fmt.Errorf("release asset digest %q is not a sha256", digest)
	}
	return sum, nil
}

// semverVersion turns a version string into one semver can order. The stamp
// strips the leading v that semver requires, and a build from source reports
// dev, which is not a version at all: ok is false there, which is what stops a
// source build from updating itself.
func semverVersion(v string) (string, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return "", false
	}
	num, pre, _ := strings.Cut(v, "-")
	switch strings.Count(num, ".") {
	case 0:
		num += ".0"
		fallthrough
	case 1:
		num += ".0"
	}
	full := "v" + num
	if pre != "" {
		full += "-" + pre
	}
	if !semver.IsValid(full) {
		return "", false
	}
	return full, true
}

// isNewer reports whether want supersedes have, prerelease included: v1.6.0 is
// newer than v1.6.0-rc.1, and v1.10.0 is newer than v1.9.0.
func isNewer(want, have string) bool {
	return semver.Compare(want, have) > 0
}

// announce reports a newer release in the terms a user acts on.
func announce(w io.Writer, have, latest string) {
	want, ok := semverVersion(latest)
	if !ok || !isNewer(want, have) {
		return
	}
	fmt.Fprintf(w, "colophon: release %s is available (this is %s); run colophon update\n", want[1:], have[1:])
}

// notice prints the availability line when a check is wanted: not for a source
// build, not in CI, not into a pipe, and not when the user or the container
// image has asked for quiet. It never fails a command.
func notice() {
	if noUpdateCheck || os.Getenv("COLOPHON_NO_UPDATE_CHECK") != "" || os.Getenv("CI") != "" {
		return
	}
	if fi, err := os.Stderr.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return // stderr is a pipe or a file: nobody is reading a prompt there
	}
	checkForUpdate(os.Stderr)
}

// checkForUpdate prints one line when a newer release exists, having asked the
// API at most once in the last updateCheckInterval. Every failure is silence:
// this is a courtesy, not a step of the build, in the same way that a missing
// indexer disables search instead of failing the site.
func checkForUpdate(w io.Writer) {
	have, ok := semverVersion(version)
	if !ok {
		return
	}
	c := readUpdateCheck()
	if c.Latest != "" && time.Since(c.Checked) < updateCheckInterval {
		announce(w, have, c.Latest)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), noticeTimeout)
	defer cancel()
	rel, etag, err := fetchRelease(ctx, releasesURL("latest"), c.ETag)
	switch {
	case errors.Is(err, errNotModified):
		// The cached tag is still the newest, so buy another interval without
		// receiving it again.
	case err != nil:
		return
	default:
		c.ETag, c.Latest = etag, rel.Tag
	}
	c.Checked = time.Now()
	writeUpdateCheck(c)
	announce(w, have, c.Latest)
}

// runUpdate installs a release over the running binary. checkOnly stops after
// the report, and toTag names a release to move to, which is also how a user
// goes back to an older one. self names the file to replace, and is empty for
// the running binary; tests name a file of their own.
func runUpdate(out io.Writer, toTag string, checkOnly bool, self string) error {
	have, ok := semverVersion(version)
	if !ok {
		return fmt.Errorf("this binary reports version %q, which no release published: rebuild it, or install one with scripts/install.sh", version)
	}
	suffix := "latest"
	if toTag != "" {
		tag, ok := semverVersion(toTag)
		if !ok {
			return fmt.Errorf("%q is not a release tag", toTag)
		}
		suffix = "tags/" + tag
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateAPITimeout)
	defer cancel()
	rel, _, err := fetchRelease(ctx, releasesURL(suffix), "")
	if errors.Is(err, errNoRelease) {
		if toTag == "" {
			return fmt.Errorf("%w for this repository; the first release publishes the binaries", err)
		}
		return fmt.Errorf("%w for tag %s", err, toTag)
	}
	if err != nil {
		return err
	}
	want, ok := semverVersion(rel.Tag)
	if !ok {
		return fmt.Errorf("release tag %q is not a version", rel.Tag)
	}
	if toTag == "" && !isNewer(want, have) {
		fmt.Fprintf(out, "colophon %s is the newest release\n", have[1:])
		return nil
	}
	if checkOnly {
		fmt.Fprintf(out, "colophon %s is available (this is %s); run colophon update to install it\n", want[1:], have[1:])
		return nil
	}
	a, err := releaseAsset(rel, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if self == "" {
		if self, err = selfPath(); err != nil {
			return err
		}
	}
	if err := installRelease(out, a, self); err != nil {
		return err
	}
	fmt.Fprintf(out, "colophon %s installed at %s\n", want[1:], self)
	return nil
}

// selfPath is the file an update replaces: the running binary with symlinks
// resolved, so an install reached through a link updates the file the link
// points at rather than growing a second one beside it.
func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// installRelease downloads one release asset and moves its binary onto path.
// Every temporary file lives beside path, so the final move is a rename within
// one filesystem, and a failure at any step leaves the running binary alone.
func installRelease(out io.Writer, a asset, path string) error {
	want, err := digestHex(a.Digest)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	old, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	// The pattern keeps the asset's own suffix after the random digits, which is
	// how extractBinary below is told a tarball from a zip.
	suffix := ".tar.gz"
	if strings.HasSuffix(a.Name, ".zip") {
		suffix = ".zip"
	}
	dl, err := os.CreateTemp(dir, "colophon.update-*"+suffix)
	if err != nil {
		return fmt.Errorf("cannot write into %s: %w", dir, err)
	}
	defer os.Remove(dl.Name())
	fmt.Fprintf(os.Stderr, "colophon: downloading %s\n", a.Name)
	if err := downloadVerified(a.URL, dl, want, "colophon"); err != nil {
		dl.Close()
		return err
	}
	if err := dl.Close(); err != nil {
		return err
	}

	fresh, err := os.CreateTemp(dir, "colophon.new-*")
	if err != nil {
		return err
	}
	defer os.Remove(fresh.Name()) // no-op once the rename below has moved it
	if err := extractBinary(dl.Name(), []string{"colophon", "colophon.exe"}, fresh); err != nil {
		fresh.Close()
		return err
	}
	if err := fresh.Close(); err != nil {
		return err
	}
	// The mode of the binary being replaced, not of the archive: an install that
	// was group-writable or read-only on purpose stays that way.
	if err := os.Chmod(fresh.Name(), old.Mode().Perm()); err != nil {
		return err
	}
	return replaceFile(fresh.Name(), path)
}

// replaceFile moves src onto dst. A running file may be renamed but not
// overwritten on Windows, so there the old binary steps aside first and stays
// behind as path.old, which is also where a rollback would find it.
func replaceFile(src, dst string) error {
	if runtime.GOOS == "windows" {
		previous := dst + ".old"
		os.Remove(previous) // whatever the update before this one left
		if err := os.Rename(dst, previous); err != nil {
			return fmt.Errorf("cannot move %s aside: %w", dst, err)
		}
		if err := os.Rename(src, dst); err != nil {
			os.Rename(previous, dst)
			return fmt.Errorf("cannot install %s: %w", dst, err)
		}
		return nil
	}
	return os.Rename(src, dst)
}

func updateCheckPath() string {
	return filepath.Join(cacheRoot(), "update-check.json")
}

func readUpdateCheck() updateCheck {
	var c updateCheck
	b, err := os.ReadFile(updateCheckPath())
	if err != nil {
		return updateCheck{}
	}
	if json.Unmarshal(b, &c) != nil {
		return updateCheck{} // a cache file from an older version is no answer
	}
	return c
}

// writeUpdateCheck is best effort: a cache that cannot be written only costs
// the next command one extra request.
func writeUpdateCheck(c updateCheck) {
	dir := filepath.Dir(updateCheckPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	if b, err := json.Marshal(c); err == nil {
		os.WriteFile(updateCheckPath(), b, 0o644)
	}
}
