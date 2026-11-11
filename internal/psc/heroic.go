package psc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// HeroicGame is a Heroic sideload entry that runs Planetside Classic.
type HeroicGame struct {
	AppName    string
	Title      string
	Executable string // Linux path of planetside.exe or PlanetsideClassic.exe
	Prefix     string // Heroic's winePrefix (holds pfx/ for Proton)
	Runner     Runner // zero if Heroic did not say
}

// FindHeroicGames lists Heroic sideload entries whose executable is the game
// or the Windows launcher. Entries that run planetside.exe come first.
func FindHeroicGames() []HeroicGame {
	var games, launchers []HeroicGame
	for _, dir := range HeroicConfigDirs() {
		b, err := os.ReadFile(filepath.Join(dir, "sideload_apps", "library.json"))
		if err != nil {
			continue
		}
		var lib struct {
			Games []struct {
				AppName string `json:"app_name"`
				Title   string `json:"title"`
				Install struct {
					Executable string `json:"executable"`
				} `json:"install"`
			} `json:"games"`
		}
		if json.Unmarshal(b, &lib) != nil {
			continue
		}
		for _, g := range lib.Games {
			base := strings.ToLower(filepath.Base(g.Install.Executable))
			if base != "planetside.exe" && base != "planetsideclassic.exe" {
				continue
			}
			hg := HeroicGame{AppName: g.AppName, Title: g.Title, Executable: g.Install.Executable}
			readHeroicGameConfig(dir, &hg)
			if base == "planetside.exe" {
				games = append(games, hg)
			} else {
				launchers = append(launchers, hg)
			}
		}
	}
	return append(games, launchers...)
}

func readHeroicGameConfig(dir string, hg *HeroicGame) {
	b, err := os.ReadFile(filepath.Join(dir, "GamesConfig", hg.AppName+".json"))
	if err != nil {
		return
	}
	var cfg map[string]json.RawMessage
	if json.Unmarshal(b, &cfg) != nil {
		return
	}
	var gc struct {
		WinePrefix  string `json:"winePrefix"`
		WineVersion struct {
			Bin  string `json:"bin"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"wineVersion"`
	}
	if json.Unmarshal(cfg[hg.AppName], &gc) != nil {
		return
	}
	hg.Prefix = gc.WinePrefix
	if gc.WineVersion.Bin != "" {
		if r, err := RunnerFromPath(gc.WineVersion.Bin); err == nil {
			if gc.WineVersion.Name != "" {
				r.Name = gc.WineVersion.Name
			}
			r.From = "Heroic"
			hg.Runner = r
		}
	}
}
