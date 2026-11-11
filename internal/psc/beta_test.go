package psc

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// addBeta gives the fake server a beta branch: a new planetside.exe and one
// extra file.
func addBeta(t *testing.T, srv *fakeServer) {
	exe, extra := []byte("game-beta"), []byte("beta only")
	release := string(srv.manifest)
	old := fmt.Sprintf("file %s %d planetside.exe\n", sum([]byte("game-v1!")), 8)
	if !strings.Contains(release, old) {
		t.Fatal("test manifest changed")
	}
	beta := strings.Replace(release, old, fmt.Sprintf("file %s %d planetside.exe\nfile %s %d newbeta.dll\n",
		sum(exe), len(exe), sum(extra), len(extra)), 1)
	beta = strings.Replace(beta, "version 7", "version 8-beta", 1)
	srv.beta = []byte(beta)
	srv.betaSig = base64.StdEncoding.EncodeToString(signP1363(t, srv.priv, srv.beta))
	srv.objects[sum(exe)] = exe
	srv.objects[sum(extra)] = extra
}

func TestBetaBranchAndBack(t *testing.T) {
	srv, u, _ := setup(t)
	if _, err := update(t, u); err != nil {
		t.Fatal(err)
	}

	// Beta on, but the server has no beta branch: the release is used.
	u.Beta = true
	m, err := u.FetchManifest(context.Background())
	if err != nil || m.FromBeta {
		t.Fatalf("no beta branch: FromBeta %v, err %v", m != nil && m.FromBeta, err)
	}

	addBeta(t, srv)
	if _, err := update(t, u); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(u.Game, "planetside.exe")); got != "game-beta" {
		t.Errorf("beta planetside.exe = %q", got)
	}
	list := read(t, filepath.Join(u.Game, StateDirName, "beta-files.txt"))
	if list != "newbeta.dll\r\nplanetside.exe\r\n" {
		t.Errorf("beta-files.txt = %q", list)
	}

	// Back to the release: the beta-only file goes, the rest returns to release.
	u.Beta = false
	m, err = u.FetchManifest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	todo, err := u.Plan(m, true)
	if err != nil {
		t.Fatal(err)
	}
	var del bool
	for _, e := range todo {
		if e.Kind == KindDelete && e.Path == "newbeta.dll" && e.Group == betaGroup {
			del = true
		}
	}
	if !del {
		t.Errorf("plan does not remove the beta file: %+v", todo)
	}
	if _, err := u.Apply(context.Background(), m, todo); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(u.Game, "planetside.exe")); got != "game-v1!" {
		t.Errorf("release planetside.exe = %q", got)
	}
	if _, err := os.Stat(filepath.Join(u.Game, "newbeta.dll")); !os.IsNotExist(err) {
		t.Errorf("newbeta.dll still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(u.Game, StateDirName, "beta-files.txt")); !os.IsNotExist(err) {
		t.Errorf("beta-files.txt still there: %v", err)
	}
}
