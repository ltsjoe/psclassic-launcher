// Package selfupdate keeps the release builds of the launcher up to date from
// the project's GitHub releases: the AppImage replaces itself, and the Steam
// Linux Runtime build replaces psclassic-launcher (and psclassic-updater next
// to it) in its folder.
//
// It only installs what the release key signed: each release has SHA256SUMS,
// signed by the release key in SHA256SUMS.minisig. The launcher checks that
// signature with the public key built into it, then checks the downloaded file
// against its line in SHA256SUMS, and only then replaces itself. Taking over
// the GitHub account is not enough to ship an update; the key (kept in
// Bitwarden) is needed too.
package selfupdate

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// Repo is where releases come from.
	Repo = "ltsjoe/psclassic-launcher"
	// DefaultAPI is GitHub's REST API.
	DefaultAPI = "https://api.github.com"
	// KeySelfUpdate is the setting: "0" turns updating off.
	KeySelfUpdate = "self_update"

	sumsName    = "SHA256SUMS"
	sigName     = "SHA256SUMS.minisig"
	maxJSON     = 1 << 20
	maxSums     = 64 << 10
	maxSig      = 4 << 10
	maxDownload = 512 << 20
)

// ErrBadSignature means a release was not signed by the release key. Nothing is installed.
var ErrBadSignature = errors.New("the update is not signed by the psclassic-launcher release key")

// Flavor says which release build this is. The release scripts set it at build
// time (-ldflags -X); builds from source leave it empty and never replace themselves.
var Flavor = ""

// The release builds.
const (
	FlavorAppImage = "appimage" // psclassic-launcher-<v>-x86_64.AppImage
	FlavorSteamRT  = "steamrt4" // psclassic-launcher-<v>-steamrt4-x86_64.tar.gz
)

// AssetName is the release file a build of this flavor updates from.
func AssetName(flavor, version string) string {
	if flavor == FlavorSteamRT {
		return "psclassic-launcher-" + version + "-steamrt4-x86_64.tar.gz"
	}
	return "psclassic-launcher-" + version + "-x86_64.AppImage"
}

// Target is what an update replaces.
type Target struct {
	Flavor string
	Path   string // the AppImage, or the psclassic-launcher program in the Steam Linux Runtime build's folder
}

// CurrentTarget is this program, when it can update itself. Otherwise it says why not.
func CurrentTarget() (Target, string) {
	switch Flavor {
	case FlavorAppImage:
		if p := AppImagePath(); p != "" {
			return Target{Flavor, p}, ""
		}
		return Target{}, "this AppImage build is not running as an AppImage"
	case FlavorSteamRT:
		exe, err := os.Executable()
		if err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err != nil {
			return Target{}, "can't find this program's file: " + err.Error()
		}
		return Target{Flavor, exe}, ""
	}
	return Target{}, "this build is not from a release, so it does not update itself"
}

// AppImagePath is the running AppImage file (the AppImage runtime sets
// $APPIMAGE), or "" when the launcher is not running as an AppImage.
func AppImagePath() string {
	p := os.Getenv("APPIMAGE")
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return p
}

// Updater finds and installs newer releases.
type Updater struct {
	Client    *http.Client
	UserAgent string
	Current   string    // the running version, e.g. "0.3.0"
	Key       PublicKey // the release key (ReleaseKey)
	API       string    // GitHub API base URL; DefaultAPI when empty
	Repo      string    // owner/name; Repo when empty

	allowHTTP bool // tests use a plain-HTTP server
}

// Release is a release newer than the running version.
type Release struct {
	Version string            // "0.3.0"
	Page    string            // the release's web page
	assets  map[string]string // file name -> download URL
}

// Latest returns the newest release when it is newer than Current, else nil.
func (u *Updater) Latest(ctx context.Context) (*Release, error) {
	api, repo := u.API, u.Repo
	if api == "" {
		api = DefaultAPI
	}
	if repo == "" {
		repo = Repo
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(api, "/")+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", u.UserAgent)
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("checking for a launcher update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // no releases yet
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("checking for a launcher update: GitHub said %s", resp.Status)
	}
	var r struct {
		Tag        string `json:"tag_name"`
		Page       string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSON)).Decode(&r); err != nil {
		return nil, fmt.Errorf("checking for a launcher update: %w", err)
	}
	v := strings.TrimPrefix(r.Tag, "v")
	if r.Draft || r.Prerelease || !Newer(v, u.Current) {
		return nil, nil
	}
	rel := &Release{Version: v, Page: r.Page, assets: map[string]string{}}
	for _, a := range r.Assets {
		rel.assets[a.Name] = a.URL
	}
	return rel, nil
}

// Install downloads the release's file for the target, checks it, and replaces
// the target with it. The running program keeps working; the new version starts
// next time.
func (u *Updater) Install(ctx context.Context, rel *Release, t Target) error {
	name := AssetName(t.Flavor, rel.Version)
	for _, n := range []string{sumsName, sigName, name} {
		if rel.assets[n] == "" {
			return fmt.Errorf("release %s has no %s", rel.Version, n)
		}
	}
	sums, err := u.fetch(ctx, rel.assets[sumsName], maxSums)
	if err != nil {
		return err
	}
	sig, err := u.fetch(ctx, rel.assets[sigName], maxSig)
	if err != nil {
		return err
	}
	trusted, err := u.Key.Verify(sums, sig)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadSignature, err)
	}
	// The signed text names the version, so an old signed release can't be
	// passed off as a newer one.
	if f := strings.Fields(trusted); len(f) < 2 || f[0] != "psclassic-launcher" || f[1] != rel.Version {
		return fmt.Errorf("%w: it is signed for %q, not version %s", ErrBadSignature, trusted, rel.Version)
	}
	want, ok := lookupSum(sums, name)
	if !ok {
		return fmt.Errorf("%w: its SHA256SUMS does not list %s", ErrBadSignature, name)
	}

	if _, err := os.Stat(t.Path); err != nil {
		return err
	}
	dir := filepath.Dir(t.Path)
	tmp, err := os.CreateTemp(dir, ".psclassic-launcher-update-*")
	if err != nil {
		return fmt.Errorf("can't update the launcher: %s can't be written to", dir)
	}
	defer os.Remove(tmp.Name()) // gone after a rename; cleans up after a failure
	defer tmp.Close()
	body, err := u.open(ctx, rel.assets[name])
	if err != nil {
		return err
	}
	defer body.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(body, maxDownload+1))
	if err != nil {
		return fmt.Errorf("downloading the launcher update: %w", err)
	}
	if n > maxDownload {
		return errors.New("the launcher update is too big")
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("the downloaded launcher update is damaged (SHA-256 %s, expected %s)", got, want)
	}
	if t.Flavor == FlavorSteamRT {
		return installFromTarball(tmp, rel.Version, t.Path)
	}
	return replaceWith(tmp, t.Path)
}

// replaceWith makes the finished temporary file f (in the same folder) the file
// at path, keeping path's permissions, in one rename.
func replaceWith(f *os.File, path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if err := f.Chmod(fi.Mode().Perm() | 0o111); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("can't replace %s: %w", path, err)
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// installFromTarball takes psclassic-launcher, and psclassic-updater when there
// is one next to it, out of a Steam Linux Runtime release archive (already
// checked) and puts them in place. Nothing else in the folder is touched, and
// only those two exact names are read from the archive.
func installFromTarball(archive *os.File, version, launcher string) error {
	dir := filepath.Dir(launcher)
	top := "psclassic-launcher-" + version + "-steamrt4-x86_64/"
	want := map[string]string{top + "psclassic-launcher": launcher}
	if updater := filepath.Join(dir, "psclassic-updater"); isRegular(updater) {
		want[top+"psclassic-updater"] = updater
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return err
	}
	zr, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("the launcher update is not a .tar.gz: %w", err)
	}
	tr := tar.NewReader(zr)
	ready := map[string]*os.File{} // destination -> extracted temporary file
	defer func() {
		for _, f := range ready {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading the launcher update: %w", err)
		}
		dest, ok := want[hdr.Name]
		if !ok || hdr.Typeflag != tar.TypeReg || ready[dest] != nil {
			continue
		}
		if hdr.Size > maxDownload {
			return errors.New("the launcher update is too big")
		}
		f, err := os.CreateTemp(dir, ".psclassic-launcher-update-*")
		if err != nil {
			return err
		}
		ready[dest] = f
		if _, err := io.Copy(f, io.LimitReader(tr, hdr.Size)); err != nil {
			return fmt.Errorf("unpacking the launcher update: %w", err)
		}
	}
	if ready[launcher] == nil {
		return fmt.Errorf("the update has no %spsclassic-launcher", top)
	}
	// The updater first, so a failure leaves the launcher on the old version.
	order := []string{filepath.Join(dir, "psclassic-updater"), launcher}
	for _, dest := range order {
		f := ready[dest]
		if f == nil {
			continue
		}
		if err := replaceWith(f, dest); err != nil {
			return err
		}
		delete(ready, dest)
	}
	return nil
}

func isRegular(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func (u *Updater) open(ctx context.Context, url string) (io.ReadCloser, error) {
	if !strings.HasPrefix(url, "https://") && !u.allowHTTP {
		return nil, fmt.Errorf("refusing to download the launcher update over plain HTTP: %s", url)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", u.UserAgent)
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading the launcher update: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("downloading the launcher update: %s said %s", req.URL.Host, resp.Status)
	}
	return resp.Body, nil
}

func (u *Updater) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	body, err := u.open(ctx, url)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	b, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("downloading the launcher update: %w", err)
	}
	if int64(len(b)) > limit {
		return nil, errors.New("downloading the launcher update: file too big")
	}
	return b, nil
}

// lookupSum finds a file's hash in sha256sum output ("<hex>  <name>").
func lookupSum(sums []byte, name string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		h, n, ok := strings.Cut(sc.Text(), " ")
		n = strings.TrimPrefix(strings.TrimSpace(n), "*")
		if ok && n == name && len(h) == 64 {
			if _, err := hex.DecodeString(h); err == nil {
				return strings.ToLower(h), true
			}
		}
	}
	return "", false
}

// Newer reports whether version a is newer than b. Both are "X.Y.Z"; anything
// else (a pre-release such as "0.4.0-rc1") is never newer.
func Newer(a, b string) bool {
	pa, okA := parseVersion(a)
	pb, okB := parseVersion(b)
	if !okA || !okB {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
