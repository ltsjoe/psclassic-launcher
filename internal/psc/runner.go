package psc

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RunnerKind says how a runner starts Windows programs.
type RunnerKind string

const (
	RunnerProton RunnerKind = "proton" // a Proton build: "proton run <exe>" with STEAM_COMPAT_* variables
	RunnerWine   RunnerKind = "wine"   // a plain Wine build: "wine <exe>" with WINEPREFIX
)

// Runner is a Proton or Wine build.
type Runner struct {
	Kind RunnerKind
	Path string // the proton script or the wine binary
	Name string // e.g. "Proton-CachyOS Latest"
	From string // where it was found: "Heroic", "Steam", "system"
}

func (r Runner) String() string {
	if r.From != "" {
		return fmt.Sprintf("%s (%s, %s)", r.Name, r.Kind, r.From)
	}
	return fmt.Sprintf("%s (%s)", r.Name, r.Kind)
}

// RunnerFromPath makes a Runner from a proton script or a wine binary.
func RunnerFromPath(path string) (Runner, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return Runner{}, err
	}
	if fi.IsDir() {
		for _, cand := range []string{"proton", "bin/wine", "bin/wine64", "files/bin/wine"} {
			if p := filepath.Join(path, cand); isFile(p) {
				return RunnerFromPath(p)
			}
		}
		return Runner{}, fmt.Errorf("%s holds no proton script or wine binary", path)
	}
	base := filepath.Base(path)
	switch {
	case base == "proton":
		return Runner{Kind: RunnerProton, Path: path, Name: filepath.Base(filepath.Dir(path))}, nil
	case strings.HasPrefix(base, "wine"):
		name := filepath.Base(filepath.Dir(filepath.Dir(path))) // <name>/bin/wine
		if filepath.Dir(path) == "/usr/bin" || filepath.Dir(path) == "/usr/local/bin" {
			name = "System Wine"
		}
		return Runner{Kind: RunnerWine, Path: path, Name: name}, nil
	}
	return Runner{}, fmt.Errorf("%s is neither a proton script nor a wine binary", path)
}

// HeroicConfigDirs are where Heroic keeps its settings (Flatpak first).
func HeroicConfigDirs() []string {
	home, _ := os.UserHomeDir()
	cfg, _ := os.UserConfigDir()
	return []string{
		filepath.Join(home, ".var/app/com.heroicgameslauncher.hgl/config/heroic"),
		filepath.Join(cfg, "heroic"),
	}
}

func steamDirs() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(home, ".steam/root"),
		filepath.Join(home, ".steam/steam"),
		filepath.Join(home, ".local/share/Steam"),
		filepath.Join(home, ".var/app/com.valvesoftware.Steam/.local/share/Steam"),
		filepath.Join(home, ".var/app/com.valvesoftware.Steam/data/Steam"),
	}
}

// lutrisDataDirs are where Lutris keeps its runners and games (native, then Flatpak).
func lutrisDataDirs() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(xdgDataHome(), "lutris"),
		filepath.Join(home, ".var/app/net.lutris.Lutris/data/lutris"),
	}
}

func xdgDataHome() string {
	if d := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}

// BestRunner picks the runner a new setup uses: the newest GE-Proton (made for
// running games outside Steam), else the newest of Valve's numbered Proton
// releases, else another Proton build, else Wine. Among equals the one found
// first wins, so Steam's copy beats Lutris's and Heroic's.
func BestRunner(rs []Runner) (Runner, bool) {
	if len(rs) == 0 {
		return Runner{}, false
	}
	best := 0
	for i := 1; i < len(rs); i++ {
		if runnerBetter(rs[i], rs[best]) {
			best = i
		}
	}
	return rs[best], true
}

var valveProton = regexp.MustCompile(`^Proton \d+(\.\d+)*$`)

func runnerTier(r Runner) int {
	n := strings.ToLower(r.Name)
	switch {
	case r.Kind != RunnerProton:
		return 0
	case strings.Contains(n, "ge-proton") || strings.Contains(n, "proton-ge"):
		return 3
	case valveProton.MatchString(r.Name):
		return 2
	}
	return 1
}

// runnerVersion reads the numbers in a build's name ("GE-Proton11-11" -> 11, 11).
// A "Latest" build counts as newer than any number.
func runnerVersion(r Runner) []int {
	if strings.Contains(strings.ToLower(r.Name), "latest") {
		return []int{1 << 30}
	}
	var v []int
	for _, f := range regexp.MustCompile(`\d+`).FindAllString(r.Name, -1) {
		n, _ := strconv.Atoi(f)
		v = append(v, n)
	}
	return v
}

func runnerBetter(a, b Runner) bool {
	if ta, tb := runnerTier(a), runnerTier(b); ta != tb {
		return ta > tb
	}
	if c := slices.Compare(runnerVersion(a), runnerVersion(b)); c != 0 {
		return c > 0
	}
	return len(a.Name) < len(b.Name) // "Proton-GE Latest" over "Proton-GE RTSP Latest"
}

// FindRunners lists the Proton and Wine builds on this machine: Steam's
// (compatibilitytools.d, where ProtonPlus and ProtonUp-Qt put GE-Proton, and
// Valve's own Proton), Lutris's, Heroic's (each native or Flatpak), and Wine on
// the PATH. The launcher only borrows these files; it runs them in its own prefix.
func FindRunners() []Runner {
	var out []Runner
	seen := map[string]bool{}
	add := func(path, from string) {
		real, err := filepath.EvalSymlinks(path)
		if err != nil || seen[real] {
			return
		}
		r, err := RunnerFromPath(path)
		if err != nil || !nativeArch(r) {
			return
		}
		seen[real] = true
		r.From = from
		out = append(out, r)
	}
	glob := func(pattern, from string) {
		m, _ := filepath.Glob(pattern)
		sort.Strings(m)
		for _, p := range m {
			add(p, from)
		}
	}
	for _, d := range steamDirs() {
		glob(filepath.Join(d, "compatibilitytools.d/*/proton"), "Steam")
		glob(filepath.Join(d, "steamapps/common/Proton*/proton"), "Steam")
	}
	for _, d := range lutrisDataDirs() {
		glob(filepath.Join(d, "runners/proton/*/proton"), "Lutris")
		glob(filepath.Join(d, "runners/wine/*/bin/wine"), "Lutris")
	}
	for _, d := range HeroicConfigDirs() {
		glob(filepath.Join(d, "tools/proton/*/proton"), "Heroic")
		glob(filepath.Join(d, "tools/wine/*/bin/wine"), "Heroic")
	}
	if runtime.GOOS == "darwin" {
		glob("/Applications/*.app/Contents/Resources/wine/bin/wine*", "app")
	}
	for _, name := range []string{"wine", "wine64"} {
		if p, err := exec.LookPath(name); err == nil {
			add(p, "system")
		}
	}
	return out
}

// nativeArch is false for a build made for another CPU (Steam installs
// "Proton 11.0 (ARM64)" on x86 PCs too). A build whose wine binary can't be
// read is kept.
func nativeArch(r Runner) bool {
	bin := r.Path
	if r.Kind == RunnerProton {
		files := filepath.Join(filepath.Dir(r.Path), "files")
		bin = filepath.Join(files, "bin", "wine")
		if !isFile(bin) && isDir(filepath.Join(files, "bin-arm64")) {
			bin = filepath.Join(files, "bin-arm64", "wine") // Valve's ARM64 builds
		}
	}
	f, err := elf.Open(bin)
	if err != nil {
		return true
	}
	defer f.Close()
	switch runtime.GOARCH {
	case "amd64", "386":
		return f.Machine == elf.EM_X86_64 || f.Machine == elf.EM_386
	case "arm64":
		return f.Machine == elf.EM_AARCH64
	}
	return true
}

// Setup is how this machine runs the game.
type Setup struct {
	Runner Runner
	Prefix string // Proton: the folder that holds pfx/ (STEAM_COMPAT_DATA_PATH); Wine: WINEPREFIX
	Game   string // game folder on disk
}

// SetupFromConfig reads the runner, prefix and game folder from the settings.
func SetupFromConfig(c *Config) (Setup, error) {
	var s Setup
	rp := c.String(KeyRunnerPath, "")
	if rp == "" {
		return s, errors.New("no Proton or Wine is chosen yet (Settings, or: psclassic-launcher settings runner_path=...)")
	}
	r, err := RunnerFromPath(rp)
	if err != nil {
		return s, fmt.Errorf("the chosen runner %s: %w", rp, err)
	}
	if k := c.String(KeyRunner, ""); k != "" {
		r.Kind = RunnerKind(k)
	}
	s.Runner = r
	s.Prefix = c.String(KeyPrefix, "")
	s.Game = c.String(KeyGameFolder, "")
	if s.Prefix == "" {
		return s, errors.New("no prefix is chosen yet")
	}
	if s.Game == "" {
		return s, errors.New("no game folder is chosen yet")
	}
	return s, nil
}

// WinePrefix is the folder that holds drive_c (it may not exist yet).
func (s Setup) WinePrefix() string {
	if pfx, err := FindPfx(s.Prefix); err == nil {
		return pfx
	}
	if s.Runner.Kind == RunnerProton {
		return filepath.Join(s.Prefix, "pfx")
	}
	return s.Prefix
}

// compatDataPath is STEAM_COMPAT_DATA_PATH: the folder that holds pfx/.
func (s Setup) compatDataPath() string {
	if filepath.Base(s.Prefix) == "pfx" && !isDir(filepath.Join(s.Prefix, "pfx")) {
		return filepath.Dir(s.Prefix)
	}
	return s.Prefix
}

// windowsSafePath resolves symlinks in a program's path, so a folder reached
// through a prefix's dosdevices/c: link becomes its real drive_c path. Wine
// turns the path into a Windows one, and a "c:" in the middle of it breaks the
// game: PSCCX.ocx then looks for itself at Z:\Games\... and the game stops with
// "Runtime error 217".
func windowsSafePath(p string) (string, error) {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	for _, part := range strings.Split(p, string(filepath.Separator)) {
		if strings.ContainsAny(part, `:\*?"<>|`) {
			return "", fmt.Errorf("the game can't run from %s: Windows programs can't use a folder name with %q in it; please move the game or choose another folder", p, part)
		}
	}
	return p, nil
}

// Command builds the command that runs a Windows program (a path on disk) in
// the prefix. extraEnv entries are KEY=value. With wait set, Proton first waits
// for anything else in the prefix to finish ("waitforexitandrun"), as Steam and
// Heroic do for a game.
func (s Setup) Command(ctx context.Context, exe string, args []string, extraEnv []string, wait bool) (*exec.Cmd, error) {
	exe, err := windowsSafePath(exe)
	if err != nil {
		return nil, err
	}
	var cmd *exec.Cmd
	env := os.Environ()
	switch s.Runner.Kind {
	case RunnerProton:
		verb := "run"
		if wait {
			verb = "waitforexitandrun"
		}
		data := s.compatDataPath()
		if err := os.MkdirAll(data, 0o755); err != nil {
			return nil, err
		}
		cmd = exec.CommandContext(ctx, s.Runner.Path, append([]string{verb, exe}, args...)...)
		env = setEnv(env, "STEAM_COMPAT_DATA_PATH", data)
		env = setEnv(env, "STEAM_COMPAT_CLIENT_INSTALL_PATH", steamClientPath())
		env = setEnv(env, "STEAM_COMPAT_INSTALL_PATH", s.Game)
		if os.Getenv("SteamAppId") == "" {
			env = setEnv(env, "SteamAppId", "0")
			env = setEnv(env, "SteamGameId", "0")
		}
	case RunnerWine:
		if err := os.MkdirAll(s.Prefix, 0o755); err != nil {
			return nil, err
		}
		cmd = exec.CommandContext(ctx, s.Runner.Path, append([]string{exe}, args...)...)
		env = setEnv(env, "WINEPREFIX", s.Prefix)
	default:
		return nil, fmt.Errorf("unknown runner kind %q", s.Runner.Kind)
	}
	for _, kv := range extraEnv {
		k, v, _ := strings.Cut(kv, "=")
		env = setEnv(env, k, v)
	}
	cmd.Env = env
	cmd.Dir = filepath.Dir(exe)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	return cmd, nil
}

// GameDLLOverrides makes Wine load the Classic team's client add-on
// (dinput8.dll in the game folder). Without it Wine uses its own dinput8 and the
// add-on's fixes (FPS cap, flashlight, dark nights, ...) never run.
const GameDLLOverrides = "dinput8=n,b"

// withOverride adds an override to the user's WINEDLLOVERRIDES, if any.
func withOverride(override string) string {
	if cur := os.Getenv("WINEDLLOVERRIDES"); cur != "" {
		return cur + ";" + override
	}
	return override
}

func steamClientPath() string {
	for _, d := range steamDirs() {
		if isDir(d) {
			return d
		}
	}
	return ""
}

func setEnv(env []string, key, value string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return append(out, key+"="+value)
}

// IsRunning reports whether a Windows program with this file name (ignoring
// case) is running for this user, under any Wine.
func IsRunning(exeName string) bool {
	return len(findProcesses(exeName)) > 0
}

// WaitForProcess polls until a program with this name runs, or the time is up.
func WaitForProcess(ctx context.Context, exeName string, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if IsRunning(exeName) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
	return false
}

// findProcesses scans /proc (Linux) or ps (elsewhere) for command lines whose
// program is exeName. Wine shows Windows programs with their Windows path
// (C:\Games\...\planetside.exe), so both kinds of separator count.
func findProcesses(exeName string) []int {
	want := strings.ToLower(exeName)
	if runtime.GOOS != "linux" {
		return psProcesses(want)
	}
	match := func(arg0 string) bool {
		a := strings.ToLower(arg0)
		if i := strings.LastIndexAny(a, `/\`); i >= 0 {
			a = a[i+1:]
		}
		return a == want
	}
	var pids []int
	{
		dirs, _ := os.ReadDir("/proc")
		uid := os.Getuid()
		for _, d := range dirs {
			var pid int
			if _, err := fmt.Sscanf(d.Name(), "%d", &pid); err != nil {
				continue
			}
			if fi, err := os.Stat("/proc/" + d.Name()); err != nil || !ownedBy(fi, uid) {
				continue
			}
			b, err := os.ReadFile("/proc/" + d.Name() + "/cmdline")
			if err != nil || len(b) == 0 {
				continue
			}
			arg0, _, _ := strings.Cut(string(b), "\x00")
			if match(arg0) {
				pids = append(pids, pid)
			}
		}
		return pids
	}
}

func psProcesses(want string) []int {
	var pids []int
	out, err := exec.Command("ps", "-x", "-o", "pid=,command=").Output()
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		pidStr, cmdline, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(pidStr, "%d", &pid); err != nil {
			continue
		}
		// ps joins the arguments with spaces, so look for the name ending a path.
		l := strings.ToLower(cmdline)
		if i := strings.Index(l, want); i >= 0 && (i == 0 || strings.ContainsAny(l[i-1:i], `/\`)) &&
			(i+len(want) == len(l) || l[i+len(want)] == ' ') {
			pids = append(pids, pid)
		}
	}
	return pids
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
