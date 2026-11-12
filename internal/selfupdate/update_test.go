package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fakeGitHub serves a release the way GitHub does: the API's releases/latest,
// and the files it links to.
type fakeGitHub struct {
	tag     string
	files   map[string][]byte
	missing bool // releases/latest answers 404
}

func (f *fakeGitHub) server(t *testing.T) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/"+Repo+"/releases/latest" {
			if f.missing {
				http.NotFound(w, r)
				return
			}
			type asset struct {
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
			}
			var as []asset
			for name := range f.files {
				as = append(as, asset{name, srv.URL + "/download/" + name})
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": f.tag, "html_url": srv.URL + "/release", "assets": as})
			return
		}
		if b, ok := f.files[filepath.Base(r.URL.Path)]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sumLine(name string, data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]) + "  " + name + "\n"
}

// release makes a signed release of version v.
func release(t *testing.T, key SecretKey, v, comment string, appimage []byte) map[string][]byte {
	t.Helper()
	sums := []byte(sumLine(AssetName(FlavorAppImage, v), appimage) + sumLine("psclassic-launcher-"+v+"-steamrt4-x86_64.tar.gz", []byte("tar")))
	sig, err := key.Sign(sums, comment)
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{AssetName(FlavorAppImage, v): appimage, sumsName: sums, sigName: sig}
}

func installed(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "psclassic-launcher.AppImage")
	if err := os.WriteFile(p, []byte("old AppImage"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func checkUntouched(t *testing.T, path string) {
	t.Helper()
	if b, _ := os.ReadFile(path); string(b) != "old AppImage" {
		t.Errorf("the installed AppImage changed to %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".psclassic-launcher-update-*")); len(left) > 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func TestUpdate(t *testing.T) {
	key, _ := GenerateKey()
	other, _ := GenerateKey()
	newImage := []byte("new AppImage 0.4.0")
	ctx := context.Background()

	updater := func(srv *httptest.Server, current string) *Updater {
		return &Updater{Client: srv.Client(), UserAgent: "test", Current: current, Key: key.Public(), API: srv.URL, allowHTTP: true}
	}

	t.Run("installs a signed newer release", func(t *testing.T) {
		srv := (&fakeGitHub{tag: "v0.4.0", files: release(t, key, "0.4.0", "psclassic-launcher 0.4.0", newImage)}).server(t)
		u := updater(srv, "0.3.0")
		rel, err := u.Latest(ctx)
		if err != nil || rel == nil || rel.Version != "0.4.0" {
			t.Fatalf("Latest = %+v, %v", rel, err)
		}
		path := installed(t)
		if err := u.Install(ctx, rel, Target{FlavorAppImage, path}); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(path)
		fi, _ := os.Stat(path)
		if string(b) != string(newImage) || fi.Mode().Perm()&0o111 == 0 {
			t.Errorf("after Install: %q, mode %v", b, fi.Mode())
		}
		if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".psclassic-launcher-update-*")); len(left) > 0 {
			t.Errorf("temporary files left behind: %v", left)
		}
	})

	t.Run("nothing newer", func(t *testing.T) {
		srv := (&fakeGitHub{tag: "v0.4.0", files: release(t, key, "0.4.0", "psclassic-launcher 0.4.0", newImage)}).server(t)
		for _, cur := range []string{"0.4.0", "0.5.0"} {
			if rel, err := updater(srv, cur).Latest(ctx); rel != nil || err != nil {
				t.Errorf("current %s: Latest = %+v, %v", cur, rel, err)
			}
		}
	})

	t.Run("no releases yet", func(t *testing.T) {
		srv := (&fakeGitHub{missing: true}).server(t)
		if rel, err := updater(srv, "0.3.0").Latest(ctx); rel != nil || err != nil {
			t.Errorf("Latest = %+v, %v", rel, err)
		}
	})

	refused := []struct {
		name  string
		files func() map[string][]byte
		bad   bool // want ErrBadSignature
	}{
		{"signed by another key", func() map[string][]byte {
			return release(t, other, "0.4.0", "psclassic-launcher 0.4.0", newImage)
		}, true},
		{"an old signed release under a new tag", func() map[string][]byte {
			f := release(t, key, "0.4.0", "psclassic-launcher 0.2.0", newImage)
			return f
		}, true},
		{"SHA256SUMS without the AppImage", func() map[string][]byte {
			f := release(t, key, "0.4.0", "psclassic-launcher 0.4.0", newImage)
			sums := []byte(sumLine("something-else", newImage))
			sig, _ := key.Sign(sums, "psclassic-launcher 0.4.0")
			f[sumsName], f[sigName] = sums, sig
			return f
		}, true},
		{"AppImage changed after signing", func() map[string][]byte {
			f := release(t, key, "0.4.0", "psclassic-launcher 0.4.0", newImage)
			f[AssetName(FlavorAppImage, "0.4.0")] = []byte("evil AppImage")
			return f
		}, false},
		{"no signature file", func() map[string][]byte {
			f := release(t, key, "0.4.0", "psclassic-launcher 0.4.0", newImage)
			delete(f, sigName)
			return f
		}, false},
	}
	for _, tc := range refused {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			srv := (&fakeGitHub{tag: "v0.4.0", files: tc.files()}).server(t)
			u := updater(srv, "0.3.0")
			rel, err := u.Latest(ctx)
			if err != nil || rel == nil {
				t.Fatalf("Latest = %+v, %v", rel, err)
			}
			path := installed(t)
			err = u.Install(ctx, rel, Target{FlavorAppImage, path})
			if err == nil {
				t.Fatal("Install succeeded")
			}
			if tc.bad != errors.Is(err, ErrBadSignature) {
				t.Errorf("error %v: ErrBadSignature = %v, want %v", err, errors.Is(err, ErrBadSignature), tc.bad)
			}
			checkUntouched(t, path)
		})
	}

	t.Run("refuses plain HTTP", func(t *testing.T) {
		srv := (&fakeGitHub{tag: "v0.4.0", files: release(t, key, "0.4.0", "psclassic-launcher 0.4.0", newImage)}).server(t)
		u := updater(srv, "0.3.0")
		rel, _ := u.Latest(ctx)
		u.allowHTTP = false
		path := installed(t)
		if err := u.Install(ctx, rel, Target{FlavorAppImage, path}); err == nil {
			t.Fatal("downloaded over plain HTTP")
		}
		checkUntouched(t, path)
	})
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"0.3.0", "0.2.0", true}, {"0.10.0", "0.9.9", true}, {"1.0.0", "0.99.99", true},
		{"0.2.0", "0.2.0", false}, {"0.2.0", "0.3.0", false},
		{"0.4.0-rc1", "0.3.0", false}, {"0.4", "0.3.0", false}, {"v0.4.0", "0.3.0", false},
	} {
		if got := Newer(tc.a, tc.b); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestAppImagePath(t *testing.T) {
	p := installed(t)
	t.Setenv("APPIMAGE", p)
	if got := AppImagePath(); got != p {
		t.Errorf("AppImagePath = %q, want %q", got, p)
	}
	for _, v := range []string{"", "relative.AppImage", filepath.Dir(p), p + ".missing"} {
		t.Setenv("APPIMAGE", v)
		if got := AppImagePath(); got != "" {
			t.Errorf("APPIMAGE=%q: AppImagePath = %q, want empty", v, got)
		}
	}
}

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func TestUpdateSteamRuntimeBuild(t *testing.T) {
	key, _ := GenerateKey()
	ctx := context.Background()
	top := "psclassic-launcher-0.4.0-steamrt4-x86_64/"
	archive := tarball(t, map[string]string{
		top + "psclassic-launcher": "new launcher",
		top + "psclassic-updater":  "new updater",
		top + "README.md":          "new readme",
		"../evil":                  "outside the folder",
	})
	name := AssetName(FlavorSteamRT, "0.4.0")
	sums := []byte(sumLine(name, archive) + sumLine(AssetName(FlavorAppImage, "0.4.0"), []byte("x")))
	sig, _ := key.Sign(sums, "psclassic-launcher 0.4.0")
	srv := (&fakeGitHub{tag: "v0.4.0", files: map[string][]byte{name: archive, sumsName: sums, sigName: sig}}).server(t)
	u := &Updater{Client: srv.Client(), UserAgent: "test", Current: "0.3.0", Key: key.Public(), API: srv.URL, allowHTTP: true}

	for _, withUpdater := range []bool{true, false} {
		root := t.TempDir()
		dir := filepath.Join(root, "psclassic-launcher-0.3.0-steamrt4-x86_64")
		os.Mkdir(dir, 0o755)
		files := map[string]string{"psclassic-launcher": "old launcher", "README.md": "old readme"}
		if withUpdater {
			files["psclassic-updater"] = "old updater"
		}
		for n, b := range files {
			os.WriteFile(filepath.Join(dir, n), []byte(b), 0o755)
		}
		rel, err := u.Latest(ctx)
		if err != nil || rel == nil {
			t.Fatalf("Latest = %+v, %v", rel, err)
		}
		if err := u.Install(ctx, rel, Target{FlavorSteamRT, filepath.Join(dir, "psclassic-launcher")}); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"psclassic-launcher": "new launcher", "README.md": "old readme"}
		if withUpdater {
			want["psclassic-updater"] = "new updater"
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != len(want) {
			t.Errorf("updater=%v: folder holds %d files, want %d (%v)", withUpdater, len(entries), len(want), entries)
		}
		for n, w := range want {
			if b, _ := os.ReadFile(filepath.Join(dir, n)); string(b) != w {
				t.Errorf("updater=%v: %s = %q, want %q", withUpdater, n, b, w)
			}
		}
		if _, err := os.Stat(filepath.Join(root, "evil")); err == nil {
			t.Error("an archive entry outside the folder was written")
		}
	}
}
