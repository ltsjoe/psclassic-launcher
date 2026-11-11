package cli

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ltsjoe/psclassic-launcher/internal/psc"
	"golang.org/x/term"
)

// update runs "check" or "update" (and psclassic-updater).
func (a App) update(args []string, check bool, configPath string) int {
	fs := flag.NewFlagSet(a.Name+" update", flag.ContinueOnError)
	var o updateOpts
	fs.StringVar(&o.prefix, "prefix", "", "Wine/Proton prefix that holds the Windows launcher's settings (instead of this launcher's settings)")
	fs.StringVar(&o.game, "game", "", "game folder on disk (instead of the settings)")
	fs.StringVar(&o.groups, "groups", "", "optional parts, comma-separated: hd-textures, voice, or none (instead of the settings)")
	fs.StringVar(&o.branch, "branch", "", "release or beta (instead of the settings)")
	checkFlag := fs.Bool("check", check, "only show what would change; write nothing")
	jobs := fs.Int("jobs", 2, "parallel downloads")
	source := fs.String("source", DefaultSource, "update server")
	pubkey := fs.String("pubkey", psc.DefaultPublicKey, "manifest signing key (base64 ECS1 blob, as in the launcher)")
	verbose := fs.Bool("v", false, "list every file checked, installed or removed")
	showVer := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVer {
		fmt.Println(a.Name, Version)
		return 0
	}
	if fs.NArg() > 0 {
		return fail(a.Name, fmt.Errorf("unexpected argument %q (see -h)", fs.Arg(0)))
	}
	con := newConsole(os.Stdout)
	t, err := resolveTarget(o, configPath, con.Logf)
	if err != nil {
		return fail(a.Name, err)
	}
	key, err := psc.ParsePublicKey(*pubkey)
	if err != nil {
		return fail(a.Name, err)
	}
	ctx, stop := interrupted()
	defer stop()
	u := a.newUpdater(t, key, *source, *jobs, *verbose, con)
	_, code := a.runUpdate(ctx, u, con, *checkFlag, nil)
	return code
}

func (a App) newUpdater(t target, key *ecdsa.PublicKey, source string, jobs int, verbose bool, con *console) *psc.Updater {
	if key == nil {
		key, _ = psc.ParsePublicKey(psc.DefaultPublicKey)
	}
	return &psc.Updater{
		Key:           key,
		Source:        strings.TrimRight(source, "/\\"),
		Game:          t.game,
		Groups:        t.groups,
		Beta:          t.beta,
		Jobs:          jobs,
		Client:        HTTPClient(),
		UserAgent:     UserAgent(),
		Log:           con.Logf,
		Verbose:       verbose,
		Progress:      con.progress,
		ProgressEvery: con.interval(),
	}
}

// runUpdate checks and, unless checkOnly, installs. confirm (if set) is asked
// before a download starts; returning false stops there. It returns whether the
// game is up to date afterwards and an exit code.
func (a App) runUpdate(ctx context.Context, u *psc.Updater, con *console, checkOnly bool, confirm func(files int, size int64) bool) (bool, int) {
	if !checkOnly {
		unlock, err := lockGame(u.Game)
		if err != nil {
			return false, fail(a.Name, err)
		}
		defer unlock()
	}
	con.Logf("Checking for updates...")
	m, err := u.FetchManifest(ctx)
	if err != nil {
		con.Status("")
		return false, fail(a.Name, err)
	}
	branch := ""
	if m.FromBeta {
		branch = "BETA "
	}
	con.Logf("Update list: %sversion %s (created %s), signature OK", branch, m.Version, m.Created)
	todo, err := u.Plan(m, !checkOnly)
	con.Status("")
	if err != nil {
		return false, fail(a.Name, err)
	}
	var nFiles, nDelete int
	var size int64
	for _, e := range todo {
		if e.Kind == psc.KindDelete {
			nDelete++
			if checkOnly {
				con.Logf("  would remove %s", e.Path)
			}
			continue
		}
		nFiles++
		size += e.Size
		if checkOnly && (u.Verbose || len(todo) <= 20) {
			con.Logf("  would install %s (%s)", e.Path, humanBytes(e.Size))
		}
	}
	if len(todo) == 0 {
		if !checkOnly && psc.InstalledVersion(u.Game) != m.Version {
			if err := psc.WriteVersion(u.Game, m.Version); err != nil {
				return false, fail(a.Name, err)
			}
		}
		con.Logf("Up to date (%sversion %s). Ready to play!", branch, m.Version)
		return true, 0
	}
	if inst := psc.InstalledVersion(u.Game); inst == "" {
		con.Logf("Planetside Classic is not installed in this folder yet.")
	} else {
		con.Logf("Installed version: %s", inst)
	}
	con.Logf("To do: %d file(s) to download (%s), %d to remove.", nFiles, humanBytes(size), nDelete)
	if checkOnly {
		return false, 0
	}
	if confirm != nil && !confirm(nFiles, size) {
		return false, 1
	}
	start := time.Now()
	res, err := u.Apply(ctx, m, todo)
	con.Status("")
	if err != nil {
		return false, fail(a.Name, err)
	}
	con.Logf("Done: %d file(s) installed (%s), %d removed, in %s. Up to date (%sversion %s).",
		res.Downloaded, humanBytes(res.DownloadedBytes), res.Deleted, time.Since(start).Round(time.Second), branch, m.Version)
	return true, 0
}

func (a App) play(args []string, configPath string) int {
	fs := flag.NewFlagSet(a.Name+" play", flag.ContinueOnError)
	noUpdate := fs.Bool("no-update", false, "start without checking for updates")
	yes := fs.Bool("yes", false, "install updates without asking")
	detach := fs.Bool("detach", false, "don't wait for the game to close")
	verbose := fs.Bool("v", false, "show Proton/Wine output here instead of the log file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	con := newConsole(os.Stdout)
	cfg, notes, err := LoadConfig(configPath)
	if err != nil {
		return fail(a.Name, err)
	}
	for _, n := range notes {
		con.Logf("%s", n)
	}
	ctx, stop := interrupted()
	defer stop()
	if !*noUpdate {
		t := target{game: cfg.String(psc.KeyGameFolder, ""), groups: psc.Groups(cfg), beta: cfg.Bool("beta", false), cfg: cfg}
		u := a.newUpdater(t, nil, DefaultSource, 2, false, con)
		confirm := func(files int, size int64) bool {
			if *yes || !isTerminal(os.Stdin) {
				return true
			}
			if ask(fmt.Sprintf("Download %d file(s) (%s) now?", files, humanBytes(size)), true) {
				return true
			}
			con.Logf("Not updated. The game needs the update before it can be played (or use: play -no-update).")
			return false
		}
		if ok, code := a.runUpdate(ctx, u, con, false, confirm); !ok {
			return max(code, 1)
		}
	}
	opt := psc.PlayOptions{Warn: func(s string) { con.Logf("%s", s) }}
	if *verbose {
		opt.Output = os.Stderr
	}
	g, err := psc.Play(ctx, cfg, a.loginStore(cfg), opt)
	if err != nil {
		return fail(a.Name, err)
	}
	setup, _ := psc.SetupFromConfig(cfg)
	con.Logf("Starting PlanetSide with %s...", setup.Runner.Name)
	if !*verbose {
		con.Logf("Proton/Wine output: %s", psc.GameLogPath())
	}
	if *detach {
		return 0
	}
	con.Logf("Waiting for the game to close (Ctrl+C stops waiting; the game keeps running).")
	select {
	case <-g.Done():
		g.Wait()
		con.Logf("PlanetSide has closed.")
	case <-ctx.Done():
		con.Logf("Stopped waiting.")
	}
	return 0
}

func (a App) login(args []string, configPath string) int {
	fs := flag.NewFlagSet(a.Name+" login", flag.ContinueOnError)
	fromGame := fs.Bool("from-game", false, "take the login from the game's chat.log")
	show := fs.Bool("show", false, "show the saved account name")
	userFlag := fs.String("user", "", "account name (the code is then read from the terminal or stdin)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, _, err := LoadConfig(configPath)
	if err != nil {
		return fail(a.Name, err)
	}
	store := a.loginStore(cfg)
	if *show {
		user, code, err := store.Load()
		switch {
		case err != nil:
			return fail(a.Name, err)
		case user == "" || code == "":
			fmt.Println("No login saved: the game shows its normal login screen.")
		default:
			fmt.Printf("Saved login for account %s (code encrypted, key in the keyring).\n", user)
		}
		return 0
	}
	var user, code string
	if *fromGame {
		user, code, err = psc.LoginFromChatLog(cfg.String(psc.KeyGameFolder, ""))
		if err != nil {
			return fail(a.Name, err)
		}
	} else {
		user = *userFlag
		in := bufio.NewReader(os.Stdin)
		tty := isTerminal(os.Stdin)
		if user == "" {
			if tty {
				fmt.Println("In the game, type !launcher in chat to see your login code.")
				fmt.Print("Account name (the name you log in with, not a character name): ")
			}
			line, _ := in.ReadString('\n')
			user = strings.TrimSpace(line)
		}
		if tty {
			fmt.Print("Login code (hidden): ")
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return fail(a.Name, err)
			}
			code = string(b)
		} else {
			line, _ := in.ReadString('\n')
			code = strings.TrimSpace(line)
		}
	}
	if err := store.Save(user, code); err != nil {
		return fail(a.Name, err)
	}
	if err := cfg.Save(); err != nil {
		return fail(a.Name, err)
	}
	fmt.Printf("Login saved for account %s. PLAY now logs you in automatically.\n", strings.TrimSpace(user))
	return 0
}

func (a App) logout(configPath string) int {
	cfg, _, err := LoadConfig(configPath)
	if err != nil {
		return fail(a.Name, err)
	}
	a.loginStore(cfg).Forget()
	if err := cfg.Save(); err != nil {
		return fail(a.Name, err)
	}
	fmt.Println("Login forgotten: the game shows its normal login screen.")
	return 0
}

func (a App) runners() int {
	rs := psc.FindRunners()
	if len(rs) == 0 {
		fmt.Println("No Proton or Wine found. Install one (e.g. with ProtonPlus or Heroic), or set runner_path yourself.")
		return 1
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, r := range rs {
		fmt.Fprintf(w, "%s\t%s\n", r.String(), r.Path)
	}
	w.Flush()
	fmt.Println("\nPick one with: settings runner_path=<path>")
	return 0
}

func (a App) status(configPath string) int {
	cfg, _, err := LoadConfig(configPath)
	if err != nil {
		return fail(a.Name, err)
	}
	ctx, stop := interrupted()
	defer stop()
	info := psc.FetchServerInfo(ctx, HTTPClient(), DefaultSource, UserAgent(), cfg.Bool("beta", false))
	if info.Online != "" {
		fmt.Println(info.Online)
	}
	if info.GameVersion != "" {
		fmt.Println("Game Version", info.GameVersion)
	}
	if v := psc.InstalledVersion(cfg.String(psc.KeyGameFolder, "")); v != "" {
		fmt.Println("Installed update:", v)
	}
	if info.News != "" {
		fmt.Println()
		fmt.Println(info.News)
	}
	if info.Online == "" && info.News == "" {
		return fail(a.Name, errors.New("could not reach the Planetside Classic servers"))
	}
	return 0
}

// setting describes one key for "settings".
type setting struct {
	key, alias, kind, label string
}

var settingList = []setting{
	{psc.KeyGameFolder, "", "path", "Game folder"},
	{psc.KeyPrefix, "", "path", "Wine/Proton prefix"},
	{psc.KeyRunnerPath, "runner", "runner", "Proton or Wine (see: runners)"},
	{"max_fps", "fps", "fps", "Frame rate limit (FPS, 0 = no limit; over 500 causes issues with passengers in flying vehicles)"},
	{"indoor_particles_fix", "weather", "bool", "No rain, snow or sand inside buildings"},
	{"dark_nights", "nights", "bool", "Dark nights (off for the original game's nights)"},
	{"empire_darklight", "darklight", "bool", "Darklight in your empire's colour (off for the original green)"},
	{"hd_textures", "hd", "bool", "HD Player and Vehicle Pack (optional download)"},
	{"beta", "", "bool", "Beta branch (test builds, may be broken)"},
	{"flashlight", "", "bool", "Flashlight on (only you see the light)"},
	{"flashlight_key", "", "key", "Flashlight switch on/off"},
	{"voice_on", "voice", "bool", "Voice chat on (optional download; uses your saved login)"},
	{"voice_squad_key", "squad_key", "key", "Voice: squad (any distance)"},
	{"voice_ally_key", "ally_key", "key", "Voice: allies within 50 m"},
	{"voice_nearby_key", "nearby_key", "key", "Voice: everyone within 50 m"},
	{"voice_mic", "mic", "text", "Microphone (empty = Windows default)"},
	{"voice_speakers", "speakers", "text", "Speakers (empty = Windows default)"},
	{"voice_mic_volume", "mic_volume", "volume", "Microphone volume %"},
	{"voice_out_volume", "voice_volume", "volume", "Voice volume %"},
}

func (a App) settings(args []string, configPath string) int {
	cfg, notes, err := LoadConfig(configPath)
	if err != nil {
		return fail(a.Name, err)
	}
	for _, n := range notes {
		fmt.Println(n)
	}
	if len(args) == 0 {
		a.showSettings(cfg)
		return 0
	}
	changedGame, changedParts := false, false
	for _, arg := range args {
		k, v, ok := strings.Cut(arg, "=")
		if !ok {
			return fail(a.Name, fmt.Errorf("%q: use KEY=VALUE (see: settings)", arg))
		}
		s, ok := findSetting(strings.TrimSpace(k))
		if !ok {
			return fail(a.Name, fmt.Errorf("unknown setting %q (see: settings)", k))
		}
		val, err := parseSetting(s, strings.TrimSpace(v))
		if err != nil {
			return fail(a.Name, fmt.Errorf("%s: %w", s.key, err))
		}
		if s.key == psc.KeyRunnerPath {
			r, _ := psc.RunnerFromPath(val)
			cfg.Set(psc.KeyRunner, string(r.Kind))
		}
		cfg.Set(s.key, val)
		switch s.key {
		case "hd_textures", "voice_on", "beta":
			changedParts = true
		case psc.KeyGameFolder, psc.KeyPrefix, psc.KeyRunnerPath:
		default:
			changedGame = true
		}
	}
	if err := cfg.Save(); err != nil {
		return fail(a.Name, err)
	}
	if changedGame {
		if err := psc.ApplyPlayerPrefs(cfg, cfg.String(psc.KeyGameFolder, "")); err != nil {
			fmt.Fprintln(os.Stderr, "Could not save your settings into the game:", err)
		}
	}
	fmt.Println("Saved. Changes take effect the next time the game starts.")
	if changedParts {
		fmt.Printf("Run '%s update' to download or remove what changed.\n", a.Name)
	}
	return 0
}

func findSetting(k string) (setting, bool) {
	for _, s := range settingList {
		if strings.EqualFold(k, s.key) || (s.alias != "" && strings.EqualFold(k, s.alias)) {
			return s, true
		}
	}
	return setting{}, false
}

func parseSetting(s setting, v string) (string, error) {
	switch s.kind {
	case "bool":
		switch strings.ToLower(v) {
		case "1", "on", "yes", "true", "y":
			return "1", nil
		case "0", "off", "no", "false", "n":
			return "0", nil
		}
		return "", fmt.Errorf("use on or off, not %q", v)
	case "fps", "volume":
		n, err := strconv.Atoi(v)
		hi := 2000
		if s.kind == "volume" {
			hi = 300
		}
		if err != nil || n < 0 || n > hi {
			return "", fmt.Errorf("use a number from 0 to %d", hi)
		}
		return strconv.Itoa(n), nil
	case "key":
		return psc.ParseKey(v)
	case "path":
		if v == "" {
			return "", errors.New("give a folder")
		}
		return absPath(v)
	case "runner":
		p, err := absPath(v)
		if err != nil {
			return "", err
		}
		r, err := psc.RunnerFromPath(p)
		if err != nil {
			return "", err
		}
		return r.Path, nil
	}
	return v, nil
}

func absPath(p string) (string, error) {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = home + p[1:]
	}
	if !strings.HasPrefix(p, "/") {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		p = wd + "/" + p
	}
	return p, nil
}

func (a App) showSettings(cfg *psc.Config) {
	game := cfg.String(psc.KeyGameFolder, "")
	pp := psc.LoadPlayerPrefs(cfg, game)
	vp := psc.LoadVoicePrefs(cfg)
	onOff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	val := map[string]string{
		psc.KeyGameFolder:      game,
		psc.KeyPrefix:          cfg.String(psc.KeyPrefix, ""),
		psc.KeyRunnerPath:      cfg.String(psc.KeyRunnerPath, ""),
		"max_fps":              strconv.Itoa(pp.MaxFPS),
		"indoor_particles_fix": onOff(pp.IndoorWeatherFix),
		"dark_nights":          onOff(pp.DarkNights),
		"empire_darklight":     onOff(pp.EmpireDarklight),
		"hd_textures":          onOff(pp.HDTextures),
		"beta":                 onOff(pp.Beta),
		"flashlight":           onOff(pp.Flashlight),
		"flashlight_key":       psc.KeyDisplay(pp.FlashlightKey),
		"voice_on":             onOff(vp.On),
		"voice_squad_key":      psc.KeyDisplay(vp.Keys[0]),
		"voice_ally_key":       psc.KeyDisplay(vp.Keys[1]),
		"voice_nearby_key":     psc.KeyDisplay(vp.Keys[2]),
		"voice_mic":            vp.Mic,
		"voice_speakers":       vp.Speakers,
		"voice_mic_volume":     strconv.Itoa(vp.MicVolume),
		"voice_out_volume":     strconv.Itoa(vp.OutVolume),
	}
	fmt.Println("Settings file:", cfg.Path())
	fmt.Println()
	for _, s := range settingList {
		name := s.key
		if s.alias != "" {
			name = s.alias
		}
		v := val[s.key]
		if v == "" {
			v = "(not set)"
			if s.kind == "text" {
				v = "(default)"
			}
		}
		if len(v) > 28 { // paths: the value on a line of its own
			fmt.Printf("  %-14s %s\n  %-14s %s\n", name, s.label, "", v)
			continue
		}
		fmt.Printf("  %-14s %-28s %s\n", name, v, s.label)
	}
	fmt.Println()
	user := cfg.String("login_user", "")
	if user != "" && cfg.String("login_code", "") != "" {
		fmt.Printf("  Login: account %s, code saved (encrypted; see: login -show)\n", user)
	} else {
		fmt.Println("  Login: none saved (see: login)")
	}
	fmt.Printf("\nChange with: %s settings KEY=VALUE ...  (names in the first column)\n", a.Name)
}
