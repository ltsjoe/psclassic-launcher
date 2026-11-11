// Command psclassic-launcher is an unofficial Linux and macOS launcher for
// Planetside Classic: the Windows launcher's window, settings and PLAY button,
// running the game with Proton or Wine. With arguments it works in a terminal
// instead (see "psclassic-launcher help").
package main

import (
	_ "embed"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"github.com/ltsjoe/psclassic-launcher/internal/cli"
	"github.com/ltsjoe/psclassic-launcher/internal/psc"
)

//go:embed assets/icon.svg
var iconSVG []byte

// appIcon is the program's own icon (not the game's artwork).
var appIcon = fyne.NewStaticResource("psclassic-launcher.svg", iconSVG)

const appID = "io.github.ltsjoe.psclassic-launcher"

func main() {
	os.Exit(cli.App{
		Name: "psclassic-launcher",
		GUI:  runGUI,
		Keys: func() psc.Keyring { return psc.SystemKeyring{} },
	}.Main(os.Args[1:]))
}

func runGUI(cfg *psc.Config, notes []string) int {
	a := app.NewWithID(appID)
	a.SetIcon(appIcon)
	a.Settings().SetTheme(launcherTheme{})
	ui := newLauncherUI(a, cfg, psc.SystemKeyring{})
	ui.start(notes)
	ui.win.ShowAndRun()
	return 0
}
