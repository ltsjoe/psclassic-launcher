package psc

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The official launcher stores its settings in
// %APPDATA%\PlanetsideClassic\launcher.ini inside the Wine prefix. Reading it lets
// this tool use the same game folder and optional parts the launcher's Settings chose.

// FindPfx accepts either a Heroic/Proton prefix folder (which holds pfx/) or a
// plain Wine prefix (which holds drive_c/), and returns the one with drive_c/.
func FindPfx(p string) (string, error) {
	for _, cand := range []string{p, filepath.Join(p, "pfx")} {
		if fi, err := os.Stat(filepath.Join(cand, "drive_c")); err == nil && fi.IsDir() {
			return cand, nil
		}
	}
	return "", fmt.Errorf("%s is not a Wine prefix (no drive_c found in it or in its pfx/)", p)
}

// FindLauncherINI looks for launcher.ini under every Windows user in the prefix,
// preferring Proton's "steamuser".
func FindLauncherINI(pfx string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(pfx, "drive_c", "users", "*", "AppData", "Roaming", "PlanetsideClassic", "launcher.ini"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fs.ErrNotExist
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return strings.Contains(matches[i], "/steamuser/") && !strings.Contains(matches[j], "/steamuser/")
	})
	return matches[0], nil
}

// ReadINI reads key=value lines. Like the launcher, the first line for a key wins.
func ReadINI(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimRight(sc.Text(), "\r"), "=")
		if !ok {
			continue
		}
		if _, dup := out[k]; !dup {
			out[k] = v
		}
	}
	return out, sc.Err()
}

// WinToLinux turns a Windows path such as C:\Games\Planetside Classic into the
// matching path inside the prefix. Drive letters go through dosdevices/ like Wine
// does, and each part of the path matches an existing entry regardless of case.
func WinToLinux(pfx, win string) (string, error) {
	if len(win) < 3 || win[1] != ':' || (win[2] != '\\' && win[2] != '/') {
		return "", fmt.Errorf("not an absolute Windows path: %q", win)
	}
	letter := strings.ToLower(win[:1])
	root := filepath.Join(pfx, "dosdevices", letter+":")
	if _, err := os.Stat(root); err != nil {
		if letter != "c" {
			return "", fmt.Errorf("drive %s: is not mapped in %s", strings.ToUpper(letter), pfx)
		}
		root = filepath.Join(pfx, "drive_c")
	}
	// dosdevices/c: is a link to ../drive_c; use the real folder, since a
	// path with "c:" in it can't be passed to Windows programs.
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	path := root
	for _, part := range strings.FieldsFunc(win[3:], func(r rune) bool { return r == '\\' || r == '/' }) {
		path = filepath.Join(path, matchCase(path, part))
	}
	return path, nil
}

// matchCase returns the name of the entry in dir that equals name ignoring case,
// or name itself when there is none (the folder will be created as given).
func matchCase(dir, name string) string {
	if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
		return name
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return name
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name(), name) {
			return e.Name()
		}
	}
	return name
}

// PrefixSettings is what the updater takes from the Windows launcher's launcher.ini.
type PrefixSettings struct {
	Path       string
	GameFolder string // Windows path, may be empty
	Groups     []string
	Beta       bool
	All        map[string]string // every key, for importing into the Linux launcher
}

func ReadPrefixSettings(pfx string) (*PrefixSettings, error) {
	path, err := FindLauncherINI(pfx)
	if err != nil {
		return nil, err
	}
	ini, err := ReadINI(path)
	if err != nil {
		return nil, err
	}
	s := &PrefixSettings{Path: path, GameFolder: ini["gamefolder"], Beta: ini["beta"] == "1", All: ini}
	if ini["hd_textures"] == "1" {
		s.Groups = append(s.Groups, "hd-textures")
	}
	if ini["voice_on"] == "1" {
		s.Groups = append(s.Groups, "voice")
	}
	return s, nil
}

// DefaultWinGameFolder is where the launcher installs when nothing is set.
const DefaultWinGameFolder = `C:\Games\Planetside Classic`
