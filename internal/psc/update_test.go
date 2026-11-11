package psc

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer serves a signed manifest and objects/<hash> like patch.blackarmoredreapers.com.
type fakeServer struct {
	t        *testing.T
	manifest []byte
	sig      string
	objects  map[string][]byte
	mu       sync.Mutex
	gets     map[string]int // object hash -> requests
	ranges   map[string]int // object hash -> requests with a Range header
	fail500  map[string]int // object hash -> how many 500s to send first
	damage   map[string]bool
	priv     *ecdsa.PrivateKey
	beta     []byte // beta/manifest.txt; nil: the server has no beta branch
	betaSig  string
}

func (s *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/manifest.txt":
		w.Write(s.manifest)
	case r.URL.Path == "/manifest.sig":
		fmt.Fprintln(w, s.sig)
	case r.URL.Path == "/beta/manifest.txt" && s.beta != nil:
		w.Write(s.beta)
	case r.URL.Path == "/beta/manifest.sig" && s.beta != nil:
		fmt.Fprintln(w, s.betaSig)
	case strings.HasPrefix(r.URL.Path, "/objects/"):
		hash := strings.TrimPrefix(r.URL.Path, "/objects/")
		data, ok := s.objects[hash]
		if !ok {
			http.NotFound(w, r)
			return
		}
		s.mu.Lock()
		s.gets[hash]++
		if r.Header.Get("Range") != "" {
			s.ranges[hash]++
		}
		if s.fail500[hash] > 0 {
			s.fail500[hash]--
			s.mu.Unlock()
			http.Error(w, "busy", http.StatusInternalServerError)
			return
		}
		damaged := s.damage[hash]
		s.mu.Unlock()
		if damaged {
			data = bytes.Repeat([]byte{'X'}, len(data))
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	default:
		http.NotFound(w, r)
	}
}

func sum(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }

func setup(t *testing.T) (*fakeServer, *Updater, map[string][]byte) {
	t.Helper()
	old := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { retryDelays = old })

	big := make([]byte, 300_000)
	rand.Read(big)
	content := map[string][]byte{
		"planetside.exe":  []byte("game-v1!"),
		"data/big.bin":    big,
		"data/copy1.bin":  []byte("same bytes"),
		"data/copy2.bin":  []byte("same bytes"),
		"fpscap.ini":      []byte("[fpscap]\r\nmax_fps=60\r\n"),
		"patch6/x.fat":    []byte("hd texture"),
		"patch6/base.fat": []byte("sd texture"),
		"hd/base.fat":     []byte("hd override"),
		"voice/v.exe":     []byte("voice chat"),
	}
	var b strings.Builder
	b.WriteString("PSCLASSIC-MANIFEST 1\nversion 7\ncreated now\n")
	line := func(kind, path string) {
		fmt.Fprintf(&b, "%s %s %d %s\n", kind, sum(content[path]), len(content[path]), path)
	}
	line("file", "planetside.exe")
	line("file", "data/big.bin")
	line("file", "data/copy1.bin")
	line("file", "data/copy2.bin")
	line("config", "fpscap.ini")
	line("file", "patch6/base.fat")
	fmt.Fprintf(&b, "optional hd-textures %s %d patch6/base.fat\n", sum(content["hd/base.fat"]), len(content["hd/base.fat"]))
	line("optional hd-textures", "patch6/x.fat")
	line("optional voice", "voice/v.exe")
	b.WriteString("delete old.dll\n")

	priv, b64 := testKey(t)
	pub, err := ParsePublicKey(b64)
	if err != nil {
		t.Fatal(err)
	}
	manifest := []byte(b.String())
	srv := &fakeServer{t: t, manifest: manifest, priv: priv,
		sig:     base64.StdEncoding.EncodeToString(signP1363(t, priv, manifest)),
		objects: map[string][]byte{}, gets: map[string]int{}, ranges: map[string]int{},
		fail500: map[string]int{}, damage: map[string]bool{}}
	for _, c := range content {
		srv.objects[sum(c)] = c
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	u := &Updater{Source: ts.URL, Key: pub, Game: filepath.Join(t.TempDir(), "Planetside Classic"),
		Groups: map[string]bool{"hd-textures": true}, Jobs: 3, Client: ts.Client(), UserAgent: "test",
		Log: t.Logf}
	return srv, u, content
}

func write(t *testing.T, path string, data string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func update(t *testing.T, u *Updater) (Result, error) {
	t.Helper()
	m, err := u.FetchManifest(context.Background())
	if err != nil {
		return Result{}, err
	}
	todo, err := u.Plan(m, true)
	if err != nil {
		return Result{}, err
	}
	return u.Apply(context.Background(), m, todo)
}

func TestFullInstallAndUpdate(t *testing.T) {
	srv, u, content := setup(t)
	g := u.Game
	// What was there before: a player-edited config, a file the list deletes,
	// a voice file whose part is now off, and half of big.bin from an earlier run.
	write(t, filepath.Join(g, "fpscap.ini"), "player's own settings")
	write(t, filepath.Join(g, "old.dll"), "old")
	write(t, filepath.Join(g, "voice", "v.exe"), "voice chat")
	write(t, filepath.Join(g, StateDirName, "download", sum(content["data/big.bin"])+".part"), string(content["data/big.bin"][:1000]))

	res, err := update(t, u)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"planetside.exe", "data/big.bin", "data/copy1.bin", "data/copy2.bin", "patch6/x.fat"} {
		if got := read(t, filepath.Join(g, p)); got != string(content[p]) {
			t.Errorf("%s has the wrong content", p)
		}
	}
	if got := read(t, filepath.Join(g, "patch6", "base.fat")); got != "hd override" {
		t.Errorf("patch6/base.fat = %q, want the hd-textures version", got)
	}
	if got := read(t, filepath.Join(g, "fpscap.ini")); got != "player's own settings" {
		t.Errorf("config file was overwritten: %q", got)
	}
	if exists(filepath.Join(g, "old.dll")) || read(t, filepath.Join(g, StateDirName, "backup", "old.dll")) != "old" {
		t.Error("deleted base file was not moved to backup/")
	}
	if exists(filepath.Join(g, "voice", "v.exe")) || exists(filepath.Join(g, StateDirName, "backup", "voice", "v.exe")) {
		t.Error("file of a switched-off part should be removed without a backup")
	}
	if got := read(t, filepath.Join(g, StateDirName, "version.txt")); got != "7\n" {
		t.Errorf("version.txt = %q", got)
	}
	if srv.gets[sum(content["data/copy1.bin"])] != 1 {
		t.Errorf("shared object fetched %d times, want 1", srv.gets[sum(content["data/copy1.bin"])])
	}
	if srv.ranges[sum(content["data/big.bin"])] != 1 {
		t.Error("partial download was not resumed with a Range request")
	}
	if res.Deleted != 2 || res.Downloaded != 6 {
		t.Errorf("result = %+v", res)
	}
	if entries, _ := os.ReadDir(filepath.Join(g, StateDirName, "download")); len(entries) != 0 {
		t.Errorf("download/ not empty: %v", entries)
	}

	// A second run finds nothing to do, without downloading anything.
	before := len(srv.gets)
	m, _ := u.FetchManifest(context.Background())
	todo, err := u.Plan(m, true)
	if err != nil || len(todo) != 0 {
		t.Fatalf("second plan = %+v, %v", todo, err)
	}
	if len(srv.gets) != before {
		t.Error("second run downloaded objects")
	}

	// A changed game file of the same size is found (new mtime -> re-hashed),
	// replaced, and the old copy kept in backup/.
	exe := filepath.Join(g, "planetside.exe")
	write(t, exe, "tampered")
	os.Chtimes(exe, time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	if _, err := update(t, u); err != nil {
		t.Fatal(err)
	}
	if read(t, exe) != "game-v1!" || read(t, filepath.Join(g, StateDirName, "backup", "planetside.exe")) != "tampered" {
		t.Error("changed file not restored with a backup of the old one")
	}
}

func TestRetriesServerErrors(t *testing.T) {
	srv, u, content := setup(t)
	srv.fail500[sum(content["planetside.exe"])] = 2
	if _, err := update(t, u); err != nil {
		t.Fatal(err)
	}
	if srv.gets[sum(content["planetside.exe"])] != 3 {
		t.Errorf("got %d requests, want 3", srv.gets[sum(content["planetside.exe"])])
	}
}

func TestDamagedDownloadIsRejected(t *testing.T) {
	srv, u, content := setup(t)
	srv.damage[sum(content["data/big.bin"])] = true
	_, err := update(t, u)
	if err == nil || !strings.Contains(err.Error(), "damaged (data/big.bin)") {
		t.Fatalf("err = %v", err)
	}
	if exists(filepath.Join(u.Game, "data", "big.bin")) {
		t.Error("damaged file was installed")
	}
	if exists(filepath.Join(u.Game, StateDirName, "version.txt")) {
		t.Error("version.txt written although the update failed")
	}
	// The other files still went in, and the state remembers them.
	if !exists(filepath.Join(u.Game, "planetside.exe")) {
		t.Error("good files were not installed")
	}
}

func TestBadSignatureChangesNothing(t *testing.T) {
	srv, u, _ := setup(t)
	srv.manifest = append(srv.manifest, []byte("file "+h('f')+" 1 evil.exe\n")...)
	_, err := update(t, u)
	if !errors.Is(err, ErrNotSigned) {
		t.Fatalf("err = %v, want ErrNotSigned", err)
	}
	if dirExists(u.Game) {
		t.Error("game folder was touched")
	}
}
