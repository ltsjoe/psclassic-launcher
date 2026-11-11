package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/ltsjoe/psclassic-launcher/internal/psc"
)

// TestWindows builds the main and Settings windows without network or keyring.
// With PSC_RENDER_DIR set it also saves pictures of them there, to check the
// layout against the Windows launcher's screenshots in docs/.
func TestWindows(t *testing.T) {
	a := test.NewTempApp(t)
	a.Settings().SetTheme(launcherTheme{})
	dir := t.TempDir()
	cfg, err := psc.LoadConfig(filepath.Join(dir, "launcher.ini"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Set(psc.KeyGameFolder, dir)
	cfg.Set("max_fps", "240")
	cfg.Set("voice_on", "1")

	ui := &launcherUI{app: a, cfg: cfg, offline: true}
	ui.win = a.NewWindow("Planetside Classic")
	ui.win.SetPadded(false)
	ui.win.SetContent(ui.build())
	ui.win.Resize(fyne.NewSize(880, 600))
	ui.showFolder()
	ui.setBusy(false)
	ui.online.SetText("PSC Online: 3   (TR 1 | NC 2 | VS 0)")
	ui.gameVersionText = "Game Version 1.4.0"
	ui.showGameVersion()
	ui.setStatus("Up to date (version 2026.11.4.3). Ready to play!", colText1)
	ui.bar.Set(1, 1)
	save(t, ui.win, "main.png")

	s := newSettingsUI(ui)
	if s.fps.entry.Text != "240" || !s.voiceOn.Checked {
		t.Errorf("settings not loaded: fps %q voice %v", s.fps.entry.Text, s.voiceOn.Checked)
	}
	save(t, s.win, "settings.png")
}

func save(t *testing.T, w fyne.Window, name string) {
	out := os.Getenv("PSC_RENDER_DIR")
	if out == "" {
		return
	}
	f, err := os.Create(filepath.Join(out, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, w.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}
