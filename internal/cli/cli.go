// Package cli is the terminal side of psclassic-launcher and psclassic-updater.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/ltsjoe/psclassic-launcher/internal/psc"
	"github.com/ltsjoe/psclassic-launcher/internal/selfupdate"
)

// Version is the launcher's version.
var Version = "0.3.0"

// DefaultSource is the Planetside Classic update server.
const DefaultSource = "https://patch.blackarmoredreapers.com"

// UserAgent is sent with every request.
func UserAgent() string {
	osName := runtime.GOOS
	switch osName {
	case "linux":
		osName = "Linux"
	case "darwin":
		osName = "macOS"
	}
	return fmt.Sprintf("psclassic-launcher/%s (%s)", Version, osName)
}

// HTTPClient is the client for the update server.
func HTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}}
}

// SelfUpdater checks the project's GitHub releases for a newer launcher.
func SelfUpdater(key selfupdate.PublicKey) *selfupdate.Updater {
	return &selfupdate.Updater{Client: HTTPClient(), UserAgent: UserAgent(), Current: Version, Key: key}
}

// App is one of the two programs.
type App struct {
	Name string                                    // "psclassic-launcher" or "psclassic-updater"
	GUI  func(cfg *psc.Config, notes []string) int // opens the window; nil in psclassic-updater
	Keys func() psc.Keyring                        // the keyring for the saved login
}

// Main runs a command line. With no command, psclassic-launcher opens its window
// (or shows help when there is no display) and psclassic-updater updates.
func (a App) Main(args []string) int {
	configPath := ""
	if len(args) >= 2 && (args[0] == "-config" || args[0] == "--config") {
		configPath, args = args[1], args[2:]
	}
	if a.GUI == nil {
		// psclassic-updater: every argument is an update flag (Heroic runs it with none).
		if len(args) > 0 && (args[0] == "check" || args[0] == "update") {
			return a.update(args[1:], args[0] == "check", configPath)
		}
		return a.update(args, false, configPath)
	}
	if len(args) == 0 {
		if hasDisplay() {
			return a.gui(configPath)
		}
		a.help()
		return 0
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "gui":
		return a.gui(configPath)
	case "update":
		return a.update(rest, false, configPath)
	case "check":
		return a.update(rest, true, configPath)
	case "play":
		return a.play(rest, configPath)
	case "login":
		return a.login(rest, configPath)
	case "logout":
		return a.logout(configPath)
	case "settings", "set":
		return a.settings(rest, configPath)
	case "runners":
		return a.runners()
	case "gpus":
		return a.gpus(configPath)
	case "self-update":
		return a.selfUpdate(rest)
	case "status", "news":
		return a.status(configPath)
	case "version", "-version", "--version":
		fmt.Println(a.Name, Version)
		return 0
	case "help", "-h", "-help", "--help":
		a.help()
		return 0
	}
	fmt.Fprintf(os.Stderr, "%s: unknown command %q\n\n", a.Name, cmd)
	a.help()
	return 2
}

func (a App) help() {
	fmt.Printf(`%[1]s %[2]s: Planetside Classic launcher for Linux (unofficial)

Usage: %[1]s [command] [flags]

With no command it opens the launcher window.

Commands:
  play       update if needed, then start the game (and voice chat when it's on)
               -no-update  start without checking for updates
               -yes        install updates without asking
               -detach     don't wait for the game to close
               -v          show Proton/Wine output here instead of the log file
  check      show what an update would do; changes nothing
  update     install or update the game
               -v          list every file; -jobs N parallel downloads (default 2)
               -groups hd-textures,voice|none  and  -branch release|beta  override Settings
  login      save your account name and login code (asks for them)
               -from-game  take them from the game's chat.log (type /log on, !launcher, /log off in game)
               -show       show the saved account name
  logout     forget the saved login
  settings   show the settings
  settings KEY=VALUE ...   change settings, e.g.  settings fps=240 hd=on squad_key=V
  runners    list the Proton and Wine builds found on this machine
  gpus       list the graphics cards, and which one the dedicated_gpu setting uses
  self-update  update this launcher from the project's signed GitHub releases
               (the AppImage and the Steam Linux Runtime build; the window does it by itself)
               -check      only say whether there is a newer version
  status     news, game version and players online
  gui        open the launcher window
  version    print the version

Global flag (before the command): -config FILE  use another settings file
Settings file: %[3]s
Game output log: %[4]s
`, a.Name, Version, defaultConfigPathOr("~/.config/psclassic-launcher/launcher.ini"), psc.GameLogPath())
}

func defaultConfigPathOr(def string) string {
	if p, err := psc.DefaultConfigPath(); err == nil {
		return p
	}
	return def
}

func hasDisplay() bool {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return true
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

func (a App) gui(configPath string) int {
	cfg, notes, err := LoadConfig(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, a.Name+":", err)
		return 1
	}
	return a.GUI(cfg, notes)
}

// LoadConfig opens the settings file and, when it is new, fills it in with
// psc.AutoSetup and saves it. It returns AutoSetup's notes.
func LoadConfig(path string) (*psc.Config, []string, error) {
	if path == "" {
		var err error
		if path, err = psc.DefaultConfigPath(); err != nil {
			return nil, nil, err
		}
	}
	cfg, err := psc.LoadConfig(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var notes []string
	if !cfg.Exists() || cfg.String(psc.KeyGameFolder, "") == "" || cfg.String(psc.KeyRunnerPath, "") == "" {
		notes = psc.AutoSetup(cfg)
		if err := cfg.Save(); err != nil {
			return nil, nil, fmt.Errorf("saving %s: %w", path, err)
		}
	}
	return cfg, notes, nil
}

func (a App) loginStore(cfg *psc.Config) psc.LoginStore {
	var k psc.Keyring
	if a.Keys != nil {
		k = a.Keys()
	}
	return psc.LoginStore{Config: cfg, Keyring: k}
}

func fail(name string, err error) int {
	fmt.Fprintln(os.Stderr, name+":", err)
	return 1
}

// interrupted gives a context that ends on Ctrl+C or SIGTERM.
func interrupted() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// ask asks a yes/no question on the terminal; def is the answer for Enter.
func ask(question string, def bool) bool {
	hint := " [y/N] "
	if def {
		hint = " [Y/n] "
	}
	fmt.Print(question + hint)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Println()
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "yes", "j", "ja":
		return true
	}
	return false
}

// target is the folder an update works on.
type target struct {
	game   string
	groups map[string]bool
	beta   bool
	cfg    *psc.Config // the Linux launcher's settings, when they were used
}

type updateOpts struct {
	prefix, game, groups, branch string
}

// resolveTarget works out the game folder, optional parts and branch:
// flags first, then the Linux launcher's settings, then the Windows launcher's
// launcher.ini in the prefix. Run by Heroic (HEROIC_GAME_PREFIX set, no flags),
// the Linux launcher's settings are used only when they are for that prefix.
func resolveTarget(o updateOpts, configPath string, logf func(string, ...any)) (target, error) {
	var t target
	useConfig := false
	if o.prefix == "" && o.game == "" {
		if hp := os.Getenv("HEROIC_GAME_PREFIX"); hp != "" {
			logf("Running for Heroic (%s)", os.Getenv("HEROIC_GAME_TITLE"))
			o.prefix = hp
			if cfg, err := existingConfig(configPath); err == nil && cfg != nil && samePrefix(cfg.String(psc.KeyPrefix, ""), hp) {
				useConfig = true
				t.cfg = cfg
			}
		} else {
			useConfig = true
		}
	}
	if useConfig {
		cfg := t.cfg
		if cfg == nil {
			var notes []string
			var err error
			cfg, notes, err = LoadConfig(configPath)
			if err != nil {
				return t, err
			}
			for _, n := range notes {
				logf("%s", n)
			}
			t.cfg = cfg
		}
		logf("Settings: %s", cfg.Path())
		t.game = cfg.String(psc.KeyGameFolder, "")
		if t.game == "" {
			return t, errors.New("no game folder is set (settings game_folder=...)")
		}
		t.groups = psc.Groups(cfg)
		t.beta = cfg.Bool("beta", false)
		logf("Game folder: %s", t.game)
	} else {
		var ws *psc.PrefixSettings
		var pfx string
		if o.prefix != "" {
			var err error
			if pfx, err = psc.FindPfx(o.prefix); err != nil {
				return t, err
			}
			ws, err = psc.ReadPrefixSettings(pfx)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return t, fmt.Errorf("reading launcher.ini: %w", err)
			}
			if ws != nil {
				logf("Windows launcher settings: %s", ws.Path)
			} else {
				logf("No launcher.ini in %s; using the launcher's defaults.", pfx)
			}
		}
		switch {
		case o.game != "":
			abs, err := filepath.Abs(o.game)
			if err != nil {
				return t, err
			}
			t.game = abs
		case pfx != "":
			win := psc.DefaultWinGameFolder
			if ws != nil && ws.GameFolder != "" {
				win = ws.GameFolder
			}
			g, err := psc.WinToLinux(pfx, win)
			if err != nil {
				return t, err
			}
			t.game = g
			logf("Game folder: %s (%s)", win, g)
		}
		t.groups = map[string]bool{}
		if ws != nil {
			for _, g := range ws.Groups {
				t.groups[g] = true
			}
			t.beta = ws.Beta
		}
	}
	if o.groups != "" {
		t.groups = map[string]bool{}
		for _, g := range strings.Split(o.groups, ",") {
			if g = strings.TrimSpace(g); g != "" && g != "none" {
				t.groups[g] = true
			}
		}
	}
	switch o.branch {
	case "":
	case "beta":
		t.beta = true
	case "release":
		t.beta = false
	default:
		return t, fmt.Errorf("-branch must be release or beta, not %q", o.branch)
	}
	names := make([]string, 0, len(t.groups))
	for g := range t.groups {
		names = append(names, g)
	}
	sort.Strings(names)
	if len(names) == 0 {
		names = []string{"none"}
	}
	branch := "release"
	if t.beta {
		branch = "beta (when the server has one)"
	}
	logf("Optional parts: %s; branch: %s", strings.Join(names, ", "), branch)
	return t, nil
}

func existingConfig(path string) (*psc.Config, error) {
	if path == "" {
		var err error
		if path, err = psc.DefaultConfigPath(); err != nil {
			return nil, err
		}
	}
	cfg, err := psc.LoadConfig(path)
	if err != nil || !cfg.Exists() {
		return nil, err
	}
	return cfg, nil
}

func samePrefix(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	pa, err1 := psc.FindPfx(a)
	pb, err2 := psc.FindPfx(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	ra, _ := filepath.EvalSymlinks(pa)
	rb, _ := filepath.EvalSymlinks(pb)
	return ra == rb
}

// lockGame stops two copies from updating one folder at once.
func lockGame(game string) (func(), error) {
	dir := filepath.Join(game, psc.StateDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "linux-updater.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another update of this game folder is already running")
	}
	return func() { f.Close() }, nil
}

// LockGame is lockGame for the window.
func LockGame(game string) (func(), error) { return lockGame(game) }
