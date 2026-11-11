package psc

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Install is a copy of Planetside Classic already on this machine, set up by
// Heroic, Lutris or the Windows launcher in some prefix. The Linux launcher can
// use its game files in place, so they are not downloaded again; it still runs
// the game in its own prefix.
type Install struct {
	Game   string // the game folder (holds planetside.exe)
	Prefix string // the Wine prefix (the folder with drive_c) holding the Windows launcher's settings, or ""
	From   string // "Heroic", "Lutris" or "a Wine prefix"
}

// FindInstalls looks for the game in Heroic's and Lutris's entries for it, then
// in the usual prefix folders (~/Games/*, Heroic's prefixes, ~/.wine and
// Steam's compatdata). Only folders that hold planetside.exe count.
func FindInstalls() []Install {
	var out []Install
	seen := map[string]bool{}
	add := func(in Install, ok bool) {
		if !ok {
			return
		}
		if r, err := filepath.EvalSymlinks(in.Game); err == nil {
			in.Game = r // e.g. Heroic's pfx -> . link resolved
		}
		if seen[in.Game] {
			return
		}
		seen[in.Game] = true
		out = append(out, in)
	}
	for _, hg := range FindHeroicGames() {
		add(installFromEntry(hg.Executable, hg.Prefix, "Heroic"))
	}
	for _, lg := range findLutrisGames() {
		add(installFromEntry(lg.exe, lg.prefix, "Lutris"))
	}
	home, _ := os.UserHomeDir()
	patterns := []string{
		filepath.Join(home, "Games", "*"),
		filepath.Join(home, "Games", "Heroic", "Prefixes", "*"),
		filepath.Join(home, ".wine"),
	}
	for _, d := range steamDirs() {
		patterns = append(patterns, filepath.Join(d, "steamapps", "compatdata", "*"))
	}
	for _, pat := range patterns {
		m, _ := filepath.Glob(pat)
		for _, p := range m {
			add(installFromPrefix(p, "a Wine prefix"))
		}
	}
	return out
}

// installFromEntry reads a Heroic or Lutris entry: one that runs planetside.exe
// names the game folder; one that runs the Windows launcher points to its prefix.
func installFromEntry(exe, prefix, from string) (Install, bool) {
	pfx, _ := FindPfx(prefix)
	if strings.EqualFold(filepath.Base(exe), GameExe) {
		game := filepath.Dir(exe)
		if !filepath.IsAbs(game) && pfx != "" {
			game = filepath.Join(pfx, game)
		}
		return Install{Game: game, Prefix: pfx, From: from}, isFile(filepath.Join(game, GameExe))
	}
	if pfx == "" {
		return Install{}, false
	}
	in, ok := installFromPrefix(pfx, from)
	return in, ok
}

// installFromPrefix finds the game in a prefix: where the Windows launcher's
// settings say, else its default folder C:\Games\Planetside Classic.
func installFromPrefix(prefix, from string) (Install, bool) {
	pfx, err := FindPfx(prefix)
	if err != nil {
		return Install{}, false
	}
	win := DefaultWinGameFolder
	if ws, err := ReadPrefixSettings(pfx); err == nil && ws.GameFolder != "" {
		win = ws.GameFolder
	}
	game, err := WinToLinux(pfx, win)
	if err != nil || !isFile(filepath.Join(game, GameExe)) {
		return Install{}, false
	}
	return Install{Game: game, Prefix: pfx, From: from}, true
}

type lutrisGame struct{ exe, prefix string }

// findLutrisGames reads Lutris's game files (YAML) for entries that run the game
// or the Windows launcher. Only the two keys needed are read, from the "game:" block.
func findLutrisGames() []lutrisGame {
	home, _ := os.UserHomeDir()
	cfg, _ := os.UserConfigDir()
	dirs := []string{filepath.Join(cfg, "lutris", "games"), filepath.Join(home, ".var/app/net.lutris.Lutris/config/lutris/games")}
	for _, d := range lutrisDataDirs() {
		dirs = append(dirs, filepath.Join(d, "games"))
	}
	var out []lutrisGame
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d, "*.yml"))
		for _, f := range files {
			g, ok := readLutrisGame(f)
			if !ok {
				continue
			}
			base := strings.ToLower(filepath.Base(g.exe))
			if base == "planetside.exe" || base == "planetsideclassic.exe" {
				out = append(out, g)
			}
		}
	}
	return out
}

func readLutrisGame(path string) (lutrisGame, bool) {
	f, err := os.Open(path)
	if err != nil {
		return lutrisGame{}, false
	}
	defer f.Close()
	var g lutrisGame
	inGame := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			inGame = strings.TrimSpace(line) == "game:"
			continue
		}
		if !inGame {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "exe":
			g.exe = v
		case "prefix":
			g.prefix = v
		}
	}
	return g, g.exe != ""
}
