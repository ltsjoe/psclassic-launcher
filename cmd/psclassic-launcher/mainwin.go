package main

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ltsjoe/psclassic-launcher/internal/cli"
	"github.com/ltsjoe/psclassic-launcher/internal/psc"
	"github.com/ltsjoe/psclassic-launcher/internal/selfupdate"
)

// launcherUI is the main window. Its fields are only touched on Fyne's UI
// goroutine; background work reports back with fyne.Do.
type launcherUI struct {
	app  fyne.App
	win  fyne.Window
	cfg  *psc.Config
	keys psc.Keyring

	launcherVer *canvas.Text
	gameVersion *widget.Label
	online      *widget.Label
	news        *widget.Label
	folder      *widget.Label
	status      *widget.Label
	bar         *progressBar
	settingsBtn *flatButton
	updateBtn   *flatButton
	loginBtn    *flatButton
	folderBtn   *flatButton
	playBtn     *flatButton

	busy            bool
	todo            []psc.Entry
	todoKnown       bool
	manifest        *psc.Manifest
	usingBeta       bool
	gameVersionText string
	cancel          context.CancelFunc
	settingsWin     fyne.Window
	playing         bool
	offline         bool // set by tests: no network, no keyring
}

func newLauncherUI(a fyne.App, cfg *psc.Config, keys psc.Keyring) *launcherUI {
	ui := &launcherUI{app: a, cfg: cfg, keys: keys}
	ui.win = a.NewWindow("Planetside Classic")
	ui.win.SetPadded(false)
	ui.win.SetFixedSize(true)
	ui.win.SetIcon(appIcon)
	ui.win.SetContent(ui.build())
	ui.win.Resize(fyne.NewSize(880, 600))
	ui.win.CenterOnScreen()
	ui.win.SetCloseIntercept(ui.onClose)
	ui.showFolder()
	ui.setBusy(false)
	return ui
}

func (ui *launcherUI) build() fyne.CanvasObject {
	var objs []fyne.CanvasObject
	objs = append(objs, header()...)

	ver := newText("Launcher v"+cli.Version+" (Linux)", colText2, 11, false)
	ui.launcherVer = ver
	objs = append(objs, place(ver, 8, 4, ver.MinSize().Width, 16))
	ui.gameVersion = widget.NewLabel("")
	ui.gameVersion.Alignment = fyne.TextAlignTrailing
	ui.gameVersion.Importance = widget.LowImportance
	ui.gameVersion.SizeName = theme.SizeNameCaptionText
	objs = append(objs, place(ui.gameVersion, 560, -6, 312, 28))

	ui.online = widget.NewLabel("")
	ui.online.Alignment = fyne.TextAlignCenter
	ui.online.Importance = widget.SuccessImportance
	ui.online.TextStyle.Bold = true
	ui.online.SizeName = theme.SizeNameSubHeadingText
	objs = append(objs, place(ui.online, 20, 256, 840, 30))

	newsBg := canvas.NewRectangle(colPanel)
	ui.news = widget.NewLabel("Welcome to Planetside Classic!")
	ui.news.Wrapping = fyne.TextWrapWord
	scroll := newScroll(ui.news)
	objs = append(objs, place(newsBg, 20, 292, 840, 150), place(scroll, 20, 292, 840, 150))

	ui.folder = widget.NewLabel("")
	ui.folder.Importance = widget.LowImportance
	ui.folder.Truncation = fyne.TextTruncateEllipsis
	objs = append(objs, place(ui.folder, 16, 446, 728, 30))
	ui.folderBtn = newFlatButton("Change...", false, ui.chooseFolder)
	objs = append(objs, place(ui.folderBtn, 760, 449, 100, 26))

	ui.status = widget.NewLabel("")
	ui.status.Truncation = fyne.TextTruncateEllipsis
	objs = append(objs, place(ui.status, 16, 476, 848, 30))
	ui.bar = newProgressBar()
	objs = append(objs, place(ui.bar, 20, 506, 840, 12))

	ui.settingsBtn = newFlatButton("Settings", false, ui.openSettings)
	ui.updateBtn = newFlatButton("Check for updates", false, ui.onUpdateButton)
	ui.loginBtn = newFlatButton("Save login from game", false, ui.saveLoginFromGame)
	ui.playBtn = newFlatButton("PLAY", true, ui.onPlay)
	ui.playBtn.textSize, ui.playBtn.bold = 21, true
	objs = append(objs,
		place(ui.settingsBtn, 20, 536, 120, 44),
		place(ui.updateBtn, 150, 536, 280, 44),
		place(ui.loginBtn, 440, 536, 190, 44),
		place(ui.playBtn, 640, 530, 220, 54))
	return newBox(nil, objs...)
}

// header is the logo: logo.png from the settings folder when there is one
// (the Planetside Classic artwork is not part of this program), else a title.
func header() []fyne.CanvasObject {
	if dir, err := psc.ConfigDir(); err == nil {
		p := filepath.Join(dir, "logo.png")
		if _, err := os.Stat(p); err == nil {
			img := canvas.NewImageFromFile(p)
			img.FillMode = canvas.ImageFillContain
			return []fyne.CanvasObject{place(img, 20, 8, 840, 250)}
		}
	}
	glow := canvas.NewRadialGradient(color.NRGBA{70, 150, 230, 150}, color.NRGBA{10, 14, 22, 0})
	ring := canvas.NewCircle(color.Transparent)
	ring.StrokeColor, ring.StrokeWidth = color.NRGBA{232, 150, 60, 140}, 2
	title := newText("PLANETSIDE", colText1, 74, true)
	title.Alignment = fyne.TextAlignCenter
	sub := newText("CLASSIC", colAccent, 38, true)
	sub.Alignment = fyne.TextAlignCenter
	return []fyne.CanvasObject{
		place(glow, 290, 10, 300, 240),
		place(ring, 330, 30, 220, 200),
		place(title, 20, 58, 840, 90),
		place(sub, 20, 150, 840, 50),
	}
}

func newScroll(o fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVScroll(o)
}

// --- state -------------------------------------------------------------------

func (ui *launcherUI) game() string { return ui.cfg.String(psc.KeyGameFolder, "") }

func (ui *launcherUI) setStatus(s string, c color.Color) {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", "; ")
	ui.status.SetText(s)
	switch c {
	case colBad:
		ui.status.Importance = widget.DangerImportance
	case colAccent:
		ui.status.Importance = widget.WarningImportance
	case colText2:
		ui.status.Importance = widget.LowImportance
	default:
		ui.status.Importance = widget.MediumImportance
	}
	ui.status.Refresh()
}

func (ui *launcherUI) showFolder() {
	ui.folder.SetText("Game folder: " + ui.game())
}

func (ui *launcherUI) showGameVersion() {
	t := ui.gameVersionText
	ui.gameVersion.Importance = widget.LowImportance
	if ui.usingBeta {
		if t != "" {
			t += "  -  "
		}
		t += "BETA"
		ui.gameVersion.Importance = widget.WarningImportance
	}
	ui.gameVersion.SetText(t)
	ui.gameVersion.Refresh()
}

func (ui *launcherUI) canPlay() bool {
	return !ui.busy && !ui.playing && ui.todoKnown && len(ui.todo) == 0 && fileExists(filepath.Join(ui.game(), psc.GameExe))
}

// setBusy dims the buttons while work runs, like the launcher's Busy.
func (ui *launcherUI) setBusy(b bool) {
	ui.busy = b
	for _, btn := range []*flatButton{ui.updateBtn, ui.folderBtn, ui.settingsBtn, ui.loginBtn} {
		btn.SetDim(b)
	}
	ui.playBtn.SetActive(ui.canPlay())
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// --- start-up, news, checking and updating -----------------------------------

// start runs what the launcher does when its window opens.
func (ui *launcherUI) start(notes []string) {
	if len(notes) > 0 {
		ui.setStatus(notes[len(notes)-1], colText2)
	}
	ui.loadNews()
	ui.startCheck()
	ui.startSelfUpdate()
}

// startSelfUpdate looks for a newer launcher in the background and, for the
// release builds, installs it (only when it is signed by the release key). The
// new version runs the next time the launcher starts; the label at the top left
// says so.
func (ui *launcherUI) startSelfUpdate() {
	if ui.offline || !ui.cfg.Bool(selfupdate.KeySelfUpdate, true) {
		return
	}
	target, why := selfupdate.CurrentTarget()
	if why != "" {
		return
	}
	key, err := selfupdate.ReleaseKey()
	if err != nil {
		return
	}
	u := cli.SelfUpdater(key)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		rel, err := u.Latest(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "launcher update:", err)
			return
		}
		if rel == nil {
			return
		}
		fyne.Do(func() { ui.setLauncherNote("downloading v"+rel.Version+"...", colText2) })
		err = u.Install(ctx, rel, target)
		fyne.Do(func() {
			if err != nil {
				fmt.Fprintln(os.Stderr, "launcher update:", err)
				msg := "update to v" + rel.Version + " failed"
				if errors.Is(err, selfupdate.ErrBadSignature) {
					msg = "update to v" + rel.Version + " refused: not signed by the release key"
				}
				ui.setLauncherNote(msg, colBad)
				return
			}
			ui.setLauncherNote("updated to v"+rel.Version+", restart to use it", colAccent)
		})
	}()
}

// setLauncherNote adds a note after the launcher's version at the top left.
func (ui *launcherUI) setLauncherNote(note string, c color.Color) {
	ui.launcherVer.Text = "Launcher v" + cli.Version + " (Linux): " + note
	ui.launcherVer.Color = c
	ui.launcherVer.Resize(ui.launcherVer.MinSize())
	ui.launcherVer.Refresh()
}

func (ui *launcherUI) loadNews() {
	if ui.offline {
		return
	}
	beta := ui.cfg.Bool("beta", false)
	go func() {
		info := psc.FetchServerInfo(context.Background(), cli.HTTPClient(), cli.DefaultSource, cli.UserAgent(), beta)
		fyne.Do(func() {
			if info.News != "" {
				ui.news.SetText(info.News)
			}
			if info.GameVersion != "" {
				ui.gameVersionText = "Game Version " + info.GameVersion
				ui.showGameVersion()
			}
			if info.Online != "" {
				ui.online.SetText(info.Online)
			}
		})
	}()
}

func (ui *launcherUI) newUpdater() *psc.Updater {
	key, _ := psc.ParsePublicKey(psc.DefaultPublicKey)
	return &psc.Updater{
		Source:    cli.DefaultSource,
		Key:       key,
		Game:      ui.game(),
		Groups:    psc.Groups(ui.cfg),
		Beta:      ui.cfg.Bool("beta", false),
		Jobs:      2,
		Client:    cli.HTTPClient(),
		UserAgent: cli.UserAgent(),
		Log: func(format string, args ...any) {
			msg := fmt.Sprintf(format, args...)
			if strings.HasPrefix(msg, "Connection problem") {
				fyne.Do(func() { ui.setStatus(msg, colAccent) })
			}
		},
		Progress: func(p psc.Progress) {
			fyne.Do(func() {
				ui.setStatus(p.Message, colText1)
				ui.bar.Set(p.Done, p.Total)
			})
		},
		ProgressEvery: 150 * time.Millisecond,
	}
}

func (ui *launcherUI) onUpdateButton() {
	if ui.busy {
		return
	}
	if ui.todoKnown && len(ui.todo) > 0 {
		ui.startUpdate()
	} else {
		ui.startCheck()
	}
}

func (ui *launcherUI) startCheck() {
	if ui.offline {
		return
	}
	game := ui.game()
	if game == "" {
		ui.setStatus("No game folder is chosen yet: click 'Change...'.", colBad)
		return
	}
	ui.todo, ui.todoKnown = nil, false
	ui.setBusy(true)
	ctx, cancel := context.WithCancel(context.Background())
	ui.cancel = cancel
	u := ui.newUpdater()
	go func() {
		var m *psc.Manifest
		var todo []psc.Entry
		unlock, err := cli.LockGame(game)
		if err == nil {
			m, err = u.FetchManifest(ctx)
			if err == nil {
				todo, err = u.Plan(m, true)
			}
			unlock()
		}
		fyne.Do(func() {
			ui.cancel = nil
			if err != nil {
				ui.setStatus("Could not check for updates: "+errText(err), colBad)
				ui.setBusy(false)
				return
			}
			ui.manifest, ui.todo, ui.todoKnown, ui.usingBeta = m, todo, true, m.FromBeta
			ui.showGameVersion()
			beta := ""
			if m.FromBeta {
				beta = "BETA "
			}
			var files int
			var size int64
			for _, e := range todo {
				if e.Kind != psc.KindDelete {
					files++
					size += e.Size
				}
			}
			switch {
			case len(todo) == 0:
				ui.setStatus("Up to date ("+beta+"version "+m.Version+"). Ready to play!", colText1)
				ui.bar.Set(1, 1)
				ui.updateBtn.SetText("Check for updates")
			case !fileExists(filepath.Join(game, psc.GameExe)):
				ui.setStatus("Planetside Classic is not installed in this folder yet ("+psc.Mb(size)+" to download).", colAccent)
				ui.bar.Set(0, 1)
				ui.updateBtn.SetText("Install")
			default:
				what := "An update"
				if m.FromBeta {
					what = "A BETA update"
				}
				ui.setStatus(fmt.Sprintf("%s is available: %d file(s), %s.", what, len(todo), psc.Mb(size)), colAccent)
				ui.bar.Set(0, 1)
				ui.updateBtn.SetText("Update")
			}
			ui.setBusy(false)
		})
	}()
}

func (ui *launcherUI) startUpdate() {
	game := ui.game()
	if psc.IsRunning(psc.GameExe) {
		ui.setStatus("Please close PlanetSide before updating.", colBad)
		return
	}
	if !psc.FolderWritable(game) {
		ui.setStatus("This game folder can't be written to. Please click 'Change...' and choose another folder.", colBad)
		return
	}
	ui.setBusy(true)
	ctx, cancel := context.WithCancel(context.Background())
	ui.cancel = cancel
	u := ui.newUpdater()
	m, todo := ui.manifest, ui.todo
	go func() {
		var res psc.Result
		unlock, err := cli.LockGame(game)
		if err == nil {
			res, err = u.Apply(ctx, m, todo)
			unlock()
		}
		fyne.Do(func() {
			ui.cancel = nil
			if err != nil {
				ui.setStatus("Update stopped: "+strings.TrimPrefix(errText(err), "update stopped: "), colBad)
				ui.todoKnown = false
				ui.updateBtn.SetText("Check for updates")
				ui.setBusy(false)
				return
			}
			ui.setStatus(fmt.Sprintf("Done: %d file(s) installed (%s). Ready to play!", res.Downloaded, psc.Mb(res.DownloadedBytes)), colText1)
			ui.bar.Set(1, 1)
			ui.todo, ui.todoKnown = nil, true
			ui.updateBtn.SetText("Check for updates")
			ui.setBusy(false)
		})
	}()
}

func errText(err error) string {
	if errors.Is(err, context.Canceled) {
		return "stopped"
	}
	return err.Error()
}

// --- buttons -------------------------------------------------------------------

func (ui *launcherUI) chooseFolder() {
	if ui.busy {
		return
	}
	d := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil || uri == nil {
			return
		}
		ui.cfg.Set(psc.KeyGameFolder, uri.Path())
		if err := ui.cfg.Save(); err != nil {
			ui.setStatus("Could not save the settings: "+err.Error(), colBad)
			return
		}
		ui.showFolder()
		ui.startCheck()
	}, ui.win)
	start := ui.game()
	for start != "" && start != "/" {
		if fi, err := os.Stat(start); err == nil && fi.IsDir() {
			if l, err := storage.ListerForURI(storage.NewFileURI(start)); err == nil {
				d.SetLocation(l)
			}
			break
		}
		start = filepath.Dir(start)
	}
	d.SetConfirmText("Choose")
	d.Resize(fyne.NewSize(820, 520))
	d.Show()
}

func (ui *launcherUI) loginStore() psc.LoginStore {
	return psc.LoginStore{Config: ui.cfg, Keyring: ui.keys}
}

func (ui *launcherUI) saveLoginFromGame() {
	if ui.busy {
		return
	}
	game := ui.game()
	store := ui.loginStore()
	go func() {
		msg, col := "", color.Color(colText1)
		user, code, err := psc.LoginFromChatLog(game)
		switch {
		case errors.Is(err, psc.ErrNoChatLog), errors.Is(err, psc.ErrNoCodeYet), errors.Is(err, psc.ErrCodeInLog):
			msg, col = err.Error(), colAccent
		case err != nil:
			msg, col = "Could not read the chat log: "+err.Error(), colBad
		default:
			if err := store.Save(user, code); err != nil {
				msg, col = "Could not save the login: "+err.Error(), colBad
			} else if err := ui.cfg.Save(); err != nil {
				msg, col = "Could not save the settings: "+err.Error(), colBad
			} else {
				msg = "Login saved for account " + user + ". PLAY now logs you in automatically."
			}
		}
		fyne.Do(func() { ui.setStatus(msg, col) })
	}()
}

func (ui *launcherUI) onPlay() {
	if !ui.canPlay() {
		return
	}
	ui.playing = true
	ui.setBusy(false)
	ui.setStatus("Starting PlanetSide...", colText1)
	var mu sync.Mutex
	var warnings []string
	started := false
	warn := func(s string) {
		mu.Lock()
		warnings = append(warnings, s)
		late := started
		mu.Unlock()
		if late {
			fyne.Do(func() { ui.setStatus(s, colAccent) })
		}
	}
	go func() {
		g, err := psc.Play(context.Background(), ui.cfg, ui.loginStore(), psc.PlayOptions{Warn: warn})
		mu.Lock()
		started = true
		early := append([]string(nil), warnings...)
		mu.Unlock()
		fyne.Do(func() {
			if err != nil {
				ui.playing = false
				col := colBad
				if errors.Is(err, psc.ErrAlreadyRunning) {
					col = colAccent
				}
				ui.setStatus(err.Error(), col)
				ui.setBusy(false)
				return
			}
			setup, _ := psc.SetupFromConfig(ui.cfg)
			if len(early) > 0 {
				// Like the launcher: a voice chat problem stays on screen.
				ui.setStatus(early[len(early)-1], colAccent)
			} else {
				ui.setStatus("PlanetSide is running ("+setup.Runner.Name+").", colText1)
				ui.win.Hide()
			}
		})
		if err != nil {
			return
		}
		g.Wait()
		fyne.Do(func() {
			ui.playing = false
			mu.Lock()
			last := ""
			if len(warnings) > len(early) {
				last = warnings[len(warnings)-1]
			}
			mu.Unlock()
			if last != "" {
				ui.setStatus(last, colAccent)
			} else {
				ui.setStatus("PlanetSide has closed. Game output: "+psc.GameLogPath(), colText2)
			}
			ui.setBusy(false)
			ui.win.Show()
			ui.win.RequestFocus()
		})
	}()
}

func (ui *launcherUI) onClose() {
	if !ui.busy || ui.cancel == nil {
		ui.app.Quit()
		return
	}
	dialog.ShowConfirm("Planetside Classic", "An update is running. Stop it and close?", func(yes bool) {
		if yes {
			ui.cancel()
			ui.app.Quit()
		}
	}, ui.win)
}

func (ui *launcherUI) openSettings() {
	if ui.busy {
		return
	}
	if ui.settingsWin != nil {
		ui.settingsWin.RequestFocus()
		return
	}
	before := struct{ hd, voice, beta bool }{ui.cfg.Bool("hd_textures", false), ui.cfg.Bool("voice_on", false), ui.cfg.Bool("beta", false)}
	sw := newSettingsUI(ui)
	ui.settingsWin = sw.win
	sw.win.SetOnClosed(func() {
		ui.settingsWin = nil
		if !sw.saved {
			return
		}
		ui.showFolder()
		after := struct{ hd, voice, beta bool }{ui.cfg.Bool("hd_textures", false), ui.cfg.Bool("voice_on", false), ui.cfg.Bool("beta", false)}
		if after.beta != before.beta {
			ui.loadNews()
		}
		if after != before || sw.setupChanged {
			ui.startCheck()
		}
	})
	sw.win.Show()
}
