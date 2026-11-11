package psc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBestRunner(t *testing.T) {
	r := func(kind RunnerKind, name string) Runner { return Runner{Kind: kind, Name: name, Path: "/x/" + name} }
	cases := []struct {
		want string
		rs   []Runner
	}{
		{"GE-Proton11-11", []Runner{r(RunnerProton, "Proton 11.0"), r(RunnerProton, "GE-Proton9-20"), r(RunnerProton, "GE-Proton11-11"), r(RunnerWine, "wine-ge-8-26")}},
		{"Proton-GE Latest", []Runner{r(RunnerProton, "GE-Proton11-11"), r(RunnerProton, "Proton-GE RTSP Latest"), r(RunnerProton, "Proton-GE Latest")}},
		{"Proton 10.0", []Runner{r(RunnerProton, "Proton 3.16"), r(RunnerProton, "Proton 10.0"), r(RunnerProton, "Proton-CachyOS Latest"), r(RunnerProton, "Proton 9.0")}},
		{"Proton-CachyOS Latest", []Runner{r(RunnerWine, "System Wine"), r(RunnerProton, "Proton-CachyOS Latest")}},
		{"System Wine", []Runner{r(RunnerWine, "System Wine")}},
	}
	for _, c := range cases {
		got, ok := BestRunner(c.rs)
		if !ok || got.Name != c.want {
			t.Errorf("BestRunner = %q, want %q", got.Name, c.want)
		}
	}
	// Among equals the first found wins (Steam is searched before Heroic).
	a, b := r(RunnerProton, "GE-Proton11-11"), r(RunnerProton, "GE-Proton11-11")
	a.From, b.From = "Steam", "Heroic"
	if got, _ := BestRunner([]Runner{a, b}); got.From != "Steam" {
		t.Errorf("tie went to %s", got.From)
	}
	if _, ok := BestRunner(nil); ok {
		t.Error("BestRunner(nil) found one")
	}
}

// fakeHome makes an empty home folder and points HOME and the XDG folders at it.
func fakeHome(t *testing.T) string {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("PATH", t.TempDir()) // no system wine
	return home
}

// fakePrefix makes a prefix with the game in C:\<folder> and, if winGame is set,
// a Windows launcher launcher.ini that points there.
func fakePrefix(t *testing.T, prefix, folder, winGame string) string {
	game := filepath.Join(prefix, "drive_c", folder)
	write(t, filepath.Join(game, GameExe), "exe")
	if winGame != "" {
		write(t, filepath.Join(prefix, "drive_c", "users", "steamuser", "AppData", "Roaming", "PlanetsideClassic", "launcher.ini"),
			"gamefolder="+winGame+"\r\nmax_fps=144\r\nlogin_code=secret\r\n")
	}
	return game
}

func TestAutoSetupNewMachine(t *testing.T) {
	home := fakeHome(t)
	write(t, filepath.Join(home, ".local/share/Steam/compatibilitytools.d/GE-Proton11-11/proton"), "#!/bin/sh")
	write(t, filepath.Join(home, ".config/heroic/tools/proton/Proton-CachyOS Latest/proton"), "#!/bin/sh")
	c, _ := LoadConfig(filepath.Join(home, "launcher.ini"))
	AutoSetup(c)
	if got := c.String(KeyRunnerPath, ""); got != filepath.Join(home, ".local/share/Steam/compatibilitytools.d/GE-Proton11-11/proton") {
		t.Errorf("runner = %s", got)
	}
	if got := c.String(KeyPrefix, ""); got != filepath.Join(home, ".local/share/psclassic-launcher/prefix") {
		t.Errorf("prefix = %s", got)
	}
	if got := c.String(KeyGameFolder, ""); got != filepath.Join(home, "Games", "Planetside Classic") {
		t.Errorf("game = %s", got)
	}
}

func TestAutoSetupUsesHeroicGameNotItsPrefix(t *testing.T) {
	home := fakeHome(t)
	write(t, filepath.Join(home, ".local/share/Steam/compatibilitytools.d/GE-Proton11-11/proton"), "#!/bin/sh")
	heroicPrefix := filepath.Join(home, "Games", "Heroic", "Prefixes", "PSC")
	game := fakePrefix(t, heroicPrefix, "Games/PSC Classic", `C:\Games\PSC Classic`)
	write(t, filepath.Join(home, ".config/heroic/sideload_apps/library.json"),
		`{"games":[{"app_name":"abc","title":"PlanetSide Classic","install":{"executable":"`+filepath.Join(game, GameExe)+`"}}]}`)
	write(t, filepath.Join(home, ".config/heroic/GamesConfig/abc.json"),
		`{"abc":{"winePrefix":"`+heroicPrefix+`","wineVersion":{"bin":"/nowhere/proton","name":"Proton","type":"proton"}}}`)

	ins := FindInstalls()
	if len(ins) != 1 || ins[0].From != "Heroic" {
		t.Fatalf("FindInstalls = %+v", ins)
	}
	c, _ := LoadConfig(filepath.Join(home, "launcher.ini"))
	AutoSetup(c)
	realGame, _ := filepath.EvalSymlinks(game)
	if got := c.String(KeyGameFolder, ""); got != realGame {
		t.Errorf("game = %s, want %s", got, realGame)
	}
	if got := c.String(KeyPrefix, ""); got == heroicPrefix || filepath.Dir(got) != filepath.Join(home, ".local/share/psclassic-launcher") {
		t.Errorf("prefix = %s: should be the launcher's own", got)
	}
	if got := c.String("max_fps", ""); got != "144" {
		t.Errorf("max_fps not taken over: %q", got)
	}
	if _, ok := c.Get("login_code"); ok {
		t.Error("the DPAPI login code was copied")
	}
}

func TestFindInstallsLutrisAndScan(t *testing.T) {
	home := fakeHome(t)
	// Lutris entry running the Windows launcher in its prefix.
	lutrisPrefix := filepath.Join(home, "Games", "planetside-classic")
	fakePrefix(t, lutrisPrefix, `Games/Planetside Classic`, "")
	write(t, filepath.Join(home, ".local/share/lutris/games/planetside-classic-123.yml"),
		"game:\n  exe: "+filepath.Join(lutrisPrefix, "drive_c/PlanetSide/PlanetsideClassic.exe")+"\n  prefix: '"+lutrisPrefix+"'\nsystem: {}\nwine:\n  version: lutris-GE\n")
	// A prefix nobody lists, found by the scan.
	other := filepath.Join(home, ".wine")
	fakePrefix(t, other, "Games/Planetside Classic", "")

	ins := FindInstalls()
	if len(ins) != 2 {
		t.Fatalf("FindInstalls = %+v", ins)
	}
	if ins[0].From != "Lutris" || ins[1].From != "a Wine prefix" {
		t.Errorf("order/source: %+v", ins)
	}
	if _, err := os.Stat(filepath.Join(ins[1].Game, GameExe)); err != nil {
		t.Error(err)
	}
}
