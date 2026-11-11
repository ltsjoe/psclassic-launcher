// Command psclassic-updater installs and updates Planetside Classic on Linux,
// into a Wine/Proton prefix, without the official launcher. It is the update
// part of psclassic-launcher as a small static program, for Heroic's "before
// launch" script and for machines without a display.
//
// The official launcher (PlanetsideClassic.exe) checks its signed update list with
// ECDSA through Windows CNG. Under Wine that check cannot run: wine-mono leaves
// CngKey and ECDsaCng unimplemented, and Wine's ncrypt.dll cannot import ECDSA
// keys. So the launcher says the list is "not signed" and never enables PLAY.
// This program does the same checks in Go and keeps the launcher's on-disk format.
package main

import (
	"os"

	"github.com/ltsjoe/psclassic-launcher/internal/cli"
)

func main() {
	os.Exit(cli.App{Name: "psclassic-updater"}.Main(os.Args[1:]))
}
