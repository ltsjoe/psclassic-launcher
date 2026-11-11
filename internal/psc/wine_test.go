package psc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDotnetTicks(t *testing.T) {
	if got := dotnetTicks(time.Unix(0, 0)); got != ticksAtUnixEpoch {
		t.Fatalf("epoch = %d", got)
	}
	// new DateTime(2000, 1, 1, 0, 0, 0, DateTimeKind.Utc).Ticks in .NET
	y2k := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := dotnetTicks(y2k); got != 630822816000000000 {
		t.Fatalf("2000-01-01 = %d", got)
	}
	// 100 ns resolution, truncated like Wine: 123456789 ns -> 1234567 ticks
	if got := dotnetTicks(y2k.Add(123456789 * time.Nanosecond)); got != 630822816000000000+1234567 {
		t.Fatalf("sub-second ticks = %d", got)
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := state{"patch6/x.fat": {Hash: h('a'), Size: 5, Ticks: 123}, "a b/c d.txt": {Hash: h('b'), Size: 6, Ticks: 456}}
	if err := saveState(dir, st); err != nil {
		t.Fatal(err)
	}
	got, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["a b/c d.txt"] != st["a b/c d.txt"] || got["patch6/x.fat"] != st["patch6/x.fat"] {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestWinToLinux(t *testing.T) {
	pfx := t.TempDir()
	mustMkdir(t, filepath.Join(pfx, "drive_c", "Games", "Planetside Classic"))
	mustMkdir(t, filepath.Join(pfx, "dosdevices"))
	if err := os.Symlink("../drive_c", filepath.Join(pfx, "dosdevices", "c:")); err != nil {
		t.Fatal(err)
	}
	// The dosdevices/c: link is followed: the result is the real drive_c path.
	realC, err := filepath.EvalSymlinks(filepath.Join(pfx, "drive_c"))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		`C:\Games\Planetside Classic`: filepath.Join(realC, "Games", "Planetside Classic"),
		`c:\games\planetside classic`: filepath.Join(realC, "Games", "Planetside Classic"), // case-insensitive
		`C:\Games\New Folder\`:        filepath.Join(realC, "Games", "New Folder"),
	}
	for in, want := range cases {
		got, err := WinToLinux(pfx, in)
		if err != nil || got != want {
			t.Errorf("WinToLinux(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{`Games\x`, `\\server\share`, `D:\x`} {
		if _, err := WinToLinux(pfx, bad); err == nil {
			t.Errorf("WinToLinux(%q) succeeded", bad)
		}
	}
}

func TestReadLauncherSettings(t *testing.T) {
	pfx := t.TempDir()
	dir := filepath.Join(pfx, "drive_c", "users", "steamuser", "AppData", "Roaming", "PlanetsideClassic")
	mustMkdir(t, dir)
	ini := "gamefolder=C:\\Games\\Planetside Classic\r\nhd_textures=1\r\nvoice_on=0\r\nhd_textures=0\r\n"
	if err := os.WriteFile(filepath.Join(dir, "launcher.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadPrefixSettings(pfx)
	if err != nil {
		t.Fatal(err)
	}
	if s.GameFolder != `C:\Games\Planetside Classic` || len(s.Groups) != 1 || s.Groups[0] != "hd-textures" {
		t.Fatalf("settings = %+v", s)
	}
}

func TestFindPfx(t *testing.T) {
	heroic := t.TempDir()
	mustMkdir(t, filepath.Join(heroic, "pfx", "drive_c"))
	for _, in := range []string{heroic, filepath.Join(heroic, "pfx")} {
		got, err := FindPfx(in)
		if err != nil || got != filepath.Join(heroic, "pfx") {
			t.Errorf("FindPfx(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := FindPfx(t.TempDir()); err == nil {
		t.Error("empty folder accepted as a prefix")
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsSafePath(t *testing.T) {
	pfx := t.TempDir()
	exe := filepath.Join(pfx, "drive_c", "Games", "PSC", "planetside.exe")
	mustMkdir(t, filepath.Dir(exe))
	if err := os.WriteFile(exe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(pfx, "dosdevices"))
	if err := os.Symlink("../drive_c", filepath.Join(pfx, "dosdevices", "c:")); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(exe)
	got, err := windowsSafePath(filepath.Join(pfx, "dosdevices", "c:", "Games", "PSC", "planetside.exe"))
	if err != nil || got != want {
		t.Errorf("through dosdevices/c: = %q, %v; want %q", got, err, want)
	}
	if _, err := windowsSafePath("/home/x/odd:name/planetside.exe"); err == nil {
		t.Error("a folder with a colon was accepted")
	}
}
