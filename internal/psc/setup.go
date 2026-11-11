package psc

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// importKeys are the Windows launcher settings worth taking over. The login is
// not among them: its code is protected with Windows DPAPI.
var importKeys = []string{
	"max_fps", "indoor_particles_fix", "dark_nights", "empire_darklight", "hd_textures", "beta",
	"flashlight", "flashlight_key",
	"voice_on", "voice_squad_key", "voice_ally_key", "voice_nearby_key",
	"voice_mic", "voice_speakers", "voice_mic_volume", "voice_out_volume", "voice_relay",
}

// DataDir is where the launcher keeps its own data, such as its prefix:
// ~/.local/share/psclassic-launcher on Linux ($XDG_DATA_HOME), the settings
// folder on macOS.
func DataDir() (string, error) {
	if runtime.GOOS == "darwin" {
		return ConfigDir()
	}
	return filepath.Join(xdgDataHome(), "psclassic-launcher"), nil
}

// DefaultPrefix is the launcher's own prefix, DataDir()/prefix. Proton or Wine
// fills it in the first time the game starts.
func DefaultPrefix() (string, error) {
	d, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "prefix"), nil
}

// DefaultGameFolder is where a new install goes: ~/Games/Planetside Classic.
// It is outside the prefix, so a broken prefix can be deleted and made again
// without downloading the game again.
func DefaultGameFolder() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Games", "Planetside Classic"), nil
}

// AutoSetup fills in what is missing from a new settings file and returns notes
// on what it did. Settings that are already there are never changed.
//
//   - Runner: the best Proton or Wine build found (see BestRunner), from Steam,
//     Lutris, Heroic or the system.
//   - Prefix: the launcher's own (DefaultPrefix). It never runs the game in
//     Heroic's or Lutris's prefix, so it works without them.
//   - Game folder: a copy of the game that Heroic, Lutris or the Windows
//     launcher already installed (see FindInstalls), used in place; else
//     DefaultGameFolder, to install into.
//   - Game settings: taken over from the Windows launcher's launcher.ini in that
//     copy's prefix.
func AutoSetup(c *Config) []string {
	var notes []string
	if c.String(KeyRunnerPath, "") == "" {
		if r, ok := BestRunner(FindRunners()); ok {
			c.Set(KeyRunner, string(r.Kind))
			c.Set(KeyRunnerPath, r.Path)
			notes = append(notes, "Runs the game with "+r.String())
		} else {
			notes = append(notes, "No Proton or Wine found. Install GE-Proton (e.g. with ProtonPlus) or Wine, then choose it in Settings.")
		}
	}
	if c.String(KeyPrefix, "") == "" {
		if p, err := DefaultPrefix(); err == nil {
			c.Set(KeyPrefix, p)
			notes = append(notes, "Own prefix: "+p+" (set up the first time the game starts)")
		}
	}
	installs := FindInstalls()
	if c.String(KeyGameFolder, "") == "" {
		if len(installs) > 0 {
			in := installs[0]
			c.Set(KeyGameFolder, in.Game)
			notes = append(notes, fmt.Sprintf("Found the game (from %s) at %s: using those files", in.From, in.Game))
		} else if g, err := DefaultGameFolder(); err == nil {
			c.Set(KeyGameFolder, g)
			notes = append(notes, "Game folder: "+g)
		}
	}
	game := c.String(KeyGameFolder, "")
	for _, in := range installs {
		if in.Prefix == "" || !sameDir(in.Game, game) {
			continue
		}
		ws, err := ReadPrefixSettings(in.Prefix)
		if err != nil {
			break
		}
		n := 0
		for _, k := range importKeys {
			if _, have := c.Get(k); have {
				continue
			}
			if v, ok := ws.All[k]; ok {
				c.Set(k, v)
				n++
			}
		}
		if n > 0 {
			notes = append(notes, fmt.Sprintf("Took over %d setting(s) from the Windows launcher (%s)", n, ws.Path))
		}
		break
	}
	return notes
}

func sameDir(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
