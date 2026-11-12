package main

import (
	"errors"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/ltsjoe/psclassic-launcher/internal/psc"
	"github.com/ltsjoe/psclassic-launcher/internal/selfupdate"
)

const defaultDevice = "Windows default"

// settingsUI is the Settings window: the Windows launcher's fields in the same
// places, plus a Linux section for Proton/Wine and the prefix.
type settingsUI struct {
	main *launcherUI
	win  fyne.Window

	fps                                  *spinner
	weather, nights, darklight, hd, beta *widget.Check
	user, code                           *widget.Entry
	voiceOn, lightOn                     *widget.Check
	keys                                 [3]*keyPicker
	lightKey                             *keyPicker
	mic, speakers                        *widget.Select
	micVol, outVol                       *spinner
	runner                               *widget.Select
	runnerPaths                          map[string]string // option label -> path
	prefix                               *widget.Entry
	dgpu, selfUpdate                     *widget.Check
	saved, setupChanged                  bool
}

func newSettingsUI(m *launcherUI) *settingsUI {
	s := &settingsUI{main: m}
	s.win = m.app.NewWindow("Settings")
	s.win.SetPadded(false)
	s.win.SetFixedSize(true)
	s.win.SetIcon(appIcon)
	s.win.SetContent(s.build())
	s.win.Resize(fyne.NewSize(900, 724))
	s.win.CenterOnScreen()
	s.loadLogin()
	return s
}

func groupNote(m *psc.Manifest, group string) string {
	if m == nil {
		return ""
	}
	if n := m.GroupSize(group); n > 0 {
		return " (" + psc.Mb(n) + " download)"
	}
	return ""
}

func (s *settingsUI) build() fyne.CanvasObject {
	m := s.main
	cfg := m.cfg
	pp := psc.LoadPlayerPrefs(cfg, m.game())
	vp := psc.LoadVoicePrefs(cfg)
	c := func() fyne.Canvas { return s.win.Canvas() }
	check := func(text string, on bool) *widget.Check {
		ch := widget.NewCheck(text, nil)
		ch.SetChecked(on)
		return ch
	}
	var objs []fyne.CanvasObject
	add := func(o ...fyne.CanvasObject) { objs = append(objs, o...) }

	// Left column: game, account, beta.
	add(label("Frame rate limit (FPS, 0 = no limit):", 20, 22))
	s.fps = newSpinner(pp.MaxFPS, 0, 2000, 10)
	add(place(s.fps.box, 300, 16, 120, 28))
	add(place(note("FPS settings over 500 will cause issues with passengers in flying vehicles.", widget.WarningImportance), 14, 44, 430, 50))
	s.weather = check("No rain, snow or sand inside buildings", pp.IndoorWeatherFix)
	s.nights = check("Dark nights (untick for the original game's nights)", pp.DarkNights)
	s.darklight = check("Darklight in your empire's colour (untick for the original green)", pp.EmpireDarklight)
	s.hd = check("HD Player and Vehicle Pack"+groupNote(m.manifest, "hd-textures"), pp.HDTextures)
	add(place(s.weather, 14, 98, 440, 30), place(s.nights, 14, 128, 440, 30),
		place(s.darklight, 14, 158, 450, 30), place(s.hd, 14, 186, 440, 30))
	add(place(note("Changes take effect the next time the game starts.", widget.LowImportance), 14, 214, 430, 30))

	add(heading("Account (log in automatically)", 20, 246))
	add(place(note("In the game type /log on, then !launcher, then /log off (or close the game), and click 'Get from game'. Or type the account name and code yourself. Leave both empty for the normal login screen.", widget.LowImportance), 14, 264, 436, 66))
	add(label("Account name:", 20, 334))
	s.user = widget.NewEntry()
	add(place(s.user, 150, 328, 290, 28))
	add(label("Login code:", 20, 368))
	s.code = widget.NewPasswordEntry()
	s.code.SetPlaceHolder("XXXX-XXXX-XXXX-XXXX")
	add(place(s.code, 150, 362, 290, 28))
	add(place(newFlatButton("Get from game", false, s.getFromGame), 20, 400, 140, 30))

	s.beta = check("Beta branch (test builds, may be broken)", pp.Beta)
	add(place(s.beta, 14, 446, 440, 30))
	add(place(note("Help test new client changes. Untick it and press Update to go back to the normal release.", widget.LowImportance), 14, 470, 436, 50))

	// Right column: voice chat, flashlight.
	add(heading("Voice chat", 480, 20))
	s.voiceOn = check("Voice chat on"+groupNote(m.manifest, "voice"), vp.On)
	add(place(s.voiceOn, 474, 40, 400, 30))
	add(place(note("Hold a key to talk. Uses your saved login (left). Starts and stops with the game.", widget.LowImportance), 474, 66, 412, 50))
	for i := range 3 {
		y := float32(118 + 36*i)
		add(label(psc.VoiceKeyLabels[i], 480, y))
		s.keys[i] = newKeyPicker(vp.Keys[i], c)
		add(place(s.keys[i], 650, y-5, 230, 28))
	}
	mics, speakers := psc.AudioDevices()
	s.mic = deviceSelect(mics, vp.Mic)
	s.speakers = deviceSelect(speakers, vp.Speakers)
	add(label("Microphone:", 480, 234), place(s.mic, 650, 228, 230, 28))
	add(label("Speakers:", 480, 270), place(s.speakers, 650, 264, 230, 28))
	s.micVol = newSpinner(vp.MicVolume, 0, 300, 5)
	s.outVol = newSpinner(vp.OutVolume, 0, 300, 5)
	add(label("Microphone volume %:", 480, 307), place(s.micVol.box, 650, 302, 80, 28))
	add(label("Voice volume %:", 480, 343), place(s.outVol.box, 650, 338, 80, 28))

	add(heading("Flashlight", 480, 390))
	s.lightOn = check("Flashlight on (only you see the light)", pp.Flashlight)
	add(place(s.lightOn, 474, 410, 400, 30))
	add(label("Switch on/off:", 480, 451))
	s.lightKey = newKeyPicker(pp.FlashlightKey, c)
	add(place(s.lightKey, 650, 446, 230, 28))

	// Linux: how the game runs.
	line := canvas.NewRectangle(colBorder)
	add(place(line, 20, 528, 860, 1))
	add(heading("Linux: Proton or Wine", 20, 540))
	add(label("Runs with:", 20, 572))
	s.runnerPaths = map[string]string{}
	var opts []string
	cur := m.cfg.String(psc.KeyRunnerPath, "")
	selected := ""
	for _, r := range psc.FindRunners() {
		l := r.String()
		opts = append(opts, l)
		s.runnerPaths[l] = r.Path
		if r.Path == cur {
			selected = l
		}
	}
	if cur != "" && selected == "" {
		selected = "Other: " + cur
		opts = append(opts, selected)
		s.runnerPaths[selected] = cur
	}
	s.runner = widget.NewSelect(opts, nil)
	s.runner.PlaceHolder = "(no Proton or Wine found: install GE-Proton with ProtonPlus, or Wine)"
	if selected != "" {
		s.runner.SetSelected(selected)
	}
	add(place(s.runner, 150, 566, 600, 28))
	add(label("Prefix:", 20, 608))
	s.prefix = widget.NewEntry()
	s.prefix.SetText(m.cfg.String(psc.KeyPrefix, ""))
	add(place(s.prefix, 150, 602, 600, 28))
	add(place(newFlatButton("Change...", false, s.choosePrefix), 760, 602, 120, 28))

	// Laptops with two GPUs: run the game on the dedicated one.
	gpu, gpuOK, why := psc.DedicatedGPU(psc.FindGPUs())
	gpuText := "Use the dedicated GPU"
	if gpuOK {
		gpuText += ": " + shorten(gpu.Name, 44)
	} else {
		gpuText += " (" + shorten(why, 44) + ")"
	}
	s.dgpu = check(gpuText, cfg.Bool(psc.KeyDedicatedGPU, false))
	if !gpuOK {
		s.dgpu.Disable()
	}
	add(place(s.dgpu, 14, 636, 470, 30))
	s.selfUpdate = check("Update the launcher by itself", cfg.Bool(selfupdate.KeySelfUpdate, true))
	if _, why := selfupdate.CurrentTarget(); why != "" {
		s.selfUpdate.SetText("Update the launcher by itself (release builds only)")
		s.selfUpdate.Disable()
	}
	add(place(s.selfUpdate, 494, 636, 386, 30))

	// Save and Cancel sit at the bottom right, under the Linux rows.
	save := newFlatButton("Save", true, s.save)
	save.active = true
	add(place(save, 680, 676, 100, 34))
	add(place(newFlatButton("Cancel", false, func() { s.win.Close() }), 790, 676, 90, 34))
	return newBox(nil, objs...)
}

// shorten cuts s to at most n characters, ending in "…" when cut.
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func deviceSelect(names []string, cur string) *widget.Select {
	opts := append([]string{defaultDevice}, names...)
	if cur != "" && !contains(names, cur) {
		opts = append(opts, cur)
	}
	sel := widget.NewSelect(opts, nil)
	if cur == "" {
		sel.SetSelected(defaultDevice)
	} else {
		sel.SetSelected(cur)
	}
	return sel
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func device(sel *widget.Select) string {
	if sel.Selected == defaultDevice {
		return ""
	}
	return sel.Selected
}

// loadLogin fills in the saved login (reading the keyring can take a moment,
// or ask to unlock it).
func (s *settingsUI) loadLogin() {
	if s.main.offline {
		return
	}
	store := s.main.loginStore()
	go func() {
		user, code, err := store.Load()
		fyne.Do(func() {
			if user != "" {
				s.user.SetText(user)
			}
			if code != "" {
				s.code.SetText(code)
			}
			if err != nil {
				dialog.ShowInformation("Settings", err.Error(), s.win)
			}
		})
	}()
}

func (s *settingsUI) getFromGame() {
	game := s.main.game()
	go func() {
		user, code, err := psc.LoginFromChatLog(game)
		fyne.Do(func() {
			if err != nil {
				msg := err.Error()
				if !errors.Is(err, psc.ErrNoChatLog) && !errors.Is(err, psc.ErrNoCodeYet) && !errors.Is(err, psc.ErrCodeInLog) {
					msg = "Could not read the chat log: " + msg
				}
				dialog.ShowInformation("Settings", msg, s.win)
				return
			}
			s.user.SetText(user)
			s.code.SetText(code)
		})
	}()
}

func (s *settingsUI) choosePrefix() {
	d := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
		if err == nil && uri != nil {
			s.prefix.SetText(uri.Path())
		}
	}, s.win)
	if p := s.prefix.Text; p != "" {
		if l, err := storage.ListerForURI(storage.NewFileURI(p)); err == nil {
			d.SetLocation(l)
		}
	}
	d.SetConfirmText("Choose")
	d.Resize(fyne.NewSize(820, 520))
	d.Show()
}

func (s *settingsUI) info(msg string) { dialog.ShowInformation("Settings", msg, s.win) }

// save checks the login like the launcher's Save, stores everything, writes the
// game settings into fpscap.ini and closes the window.
func (s *settingsUI) save() {
	m := s.main
	cfg := m.cfg
	user := strings.TrimSpace(s.user.Text)
	code := psc.CleanCode(s.code.Text)
	if user != "" || code != "" {
		if user == "" || strings.Contains(user, " ") {
			s.info(psc.ErrAccountName.Error())
			return
		}
		if !psc.LooksValid(code) {
			s.info(psc.ErrCodeFormat.Error())
			return
		}
	}
	fps, ok := s.fps.Value()
	if !ok {
		s.info("Please enter a frame rate limit from 0 to 2000.")
		return
	}
	micVol, _ := s.micVol.Value()
	outVol, _ := s.outVol.Value()

	psc.PlayerPrefs{
		MaxFPS: fps, IndoorWeatherFix: s.weather.Checked, DarkNights: s.nights.Checked,
		EmpireDarklight: s.darklight.Checked, HDTextures: s.hd.Checked,
		Flashlight: s.lightOn.Checked, FlashlightKey: s.lightKey.Key(), Beta: s.beta.Checked,
	}.Save(cfg)
	vp := psc.LoadVoicePrefs(cfg)
	vp.On = s.voiceOn.Checked
	for i := range 3 {
		vp.Keys[i] = s.keys[i].Key()
	}
	vp.Mic, vp.Speakers = device(s.mic), device(s.speakers)
	vp.MicVolume, vp.OutVolume = micVol, outVol
	vp.Save(cfg)

	if p, ok := s.runnerPaths[s.runner.Selected]; ok && p != cfg.String(psc.KeyRunnerPath, "") {
		if r, err := psc.RunnerFromPath(p); err == nil {
			cfg.Set(psc.KeyRunner, string(r.Kind))
			cfg.Set(psc.KeyRunnerPath, r.Path)
			s.setupChanged = true
		}
	}
	if p := strings.TrimSpace(s.prefix.Text); p != cfg.String(psc.KeyPrefix, "") {
		cfg.Set(psc.KeyPrefix, p)
		s.setupChanged = true
	}
	if !s.dgpu.Disabled() {
		cfg.SetBool(psc.KeyDedicatedGPU, s.dgpu.Checked)
	}
	if !s.selfUpdate.Disabled() {
		cfg.SetBool(selfupdate.KeySelfUpdate, s.selfUpdate.Checked)
	}

	// The keyring can be slow or ask to be unlocked, so the rest runs off the
	// UI goroutine (Config has its own lock).
	store := m.loginStore()
	voiceOn := s.voiceOn.Checked
	loginSet := user != ""
	game := m.game()
	go func() {
		var err error
		if !loginSet {
			store.Forget()
		} else {
			err = store.Save(user, code)
		}
		if err == nil {
			err = cfg.Save()
		}
		var applyErr error
		if err == nil {
			applyErr = psc.ApplyPlayerPrefs(cfg, game)
		}
		fyne.Do(func() {
			if err != nil {
				s.info("Could not save the settings: " + err.Error())
				return
			}
			if applyErr != nil {
				m.setStatus("Could not save your settings into the game: "+applyErr.Error(), colBad)
			}
			s.saved = true
			if voiceOn && !loginSet {
				d := dialog.NewInformation("Settings", "Voice chat uses your saved login: please also fill in the account name and login code.", s.win)
				d.SetOnClosed(func() { s.win.Close() })
				d.Show()
				return
			}
			s.win.Close()
		})
	}()
}
