package psc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// GameExe and VoiceExe are the programs PLAY starts.
const (
	GameExe  = "planetside.exe"
	VoiceExe = "PlanetsideClassicVoice.exe"
)

// Launcher messages for PLAY.
var (
	ErrAlreadyRunning = errors.New("PlanetSide is already running.")
	ErrNotInstalled   = errors.New("Planetside Classic is not installed in this folder yet.")
	ErrNotWritable    = errors.New("This game folder can't be written to. Please choose another game folder.")
)

// Game is a started game, with voice chat if it is on.
type Game struct {
	cmd     *exec.Cmd
	voice   *exec.Cmd
	mu      sync.Mutex
	logFile *os.File
	done    chan struct{}
	waitErr error
}

// PlayOptions control Play.
type PlayOptions struct {
	Output io.Writer // where Proton/Wine output goes; nil for a log file (see GameLogPath)
	// Warn is told about problems that do not stop the game (it may be called
	// from another goroutine, while voice chat starts).
	Warn func(string)
}

// GameLogPath is where Play writes Proton's and Wine's output by default.
func GameLogPath() string {
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "psclassic-launcher", "game.log")
}

// Play does what the launcher's PLAY button does: it writes the game settings
// into fpscap.ini, starts planetside.exe (logged in when a login is saved) and
// starts voice chat when it is on.
func Play(ctx context.Context, c *Config, login LoginStore, opt PlayOptions) (*Game, error) {
	warn := func(s string) {
		if opt.Warn != nil {
			opt.Warn(s)
		}
	}
	setup, err := SetupFromConfig(c)
	if err != nil {
		return nil, err
	}
	exe := filepath.Join(setup.Game, GameExe)
	if !isFile(exe) {
		return nil, ErrNotInstalled
	}
	if IsRunning(GameExe) {
		return nil, ErrAlreadyRunning
	}
	if err := ApplyPlayerPrefs(c, setup.Game); err != nil {
		warn("Could not save your settings into the game: " + err.Error())
	}
	user, code, lerr := login.Load()
	if lerr != nil {
		warn(lerr.Error())
	}
	args := []string{"/K:StagingTest", "/CC"}
	if user != "" && code != "" {
		args = []string{"/CC", "/K:" + code, "/N:" + user}
	}
	if !FolderWritable(setup.Game) {
		return nil, ErrNotWritable
	}

	out := opt.Output
	var logFile *os.File
	if out == nil {
		p := GameLogPath()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err == nil {
			logFile, _ = os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		}
		if logFile != nil {
			out = logFile
		} else {
			out = io.Discard
		}
	}
	cmd, err := setup.Command(context.WithoutCancel(ctx), exe, args, []string{"WINEDLLOVERRIDES=" + withOverride(GameDLLOverrides)}, true)
	if err != nil {
		closeFile(logFile)
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = out, out
	if setup.GPU != nil {
		set, _ := setup.GPU.Env()
		fmt.Fprintf(out, "psclassic-launcher: running the game on the dedicated GPU, %s (%s; %s)\n",
			setup.GPU.Name, setup.GPU.Slot, strings.Join(set, " "))
	} else if c.Bool(KeyDedicatedGPU, false) {
		_, _, why := DedicatedGPU(FindGPUs())
		warn("The dedicated GPU setting is on, but " + why + ".")
	}
	if err := cmd.Start(); err != nil {
		closeFile(logFile)
		return nil, fmt.Errorf("could not start the game with %s: %w", setup.Runner.Name, err)
	}
	g := &Game{cmd: cmd, logFile: logFile, done: make(chan struct{})}
	go func() {
		g.waitErr = cmd.Wait()
		close(g.done)
	}()

	voice := LoadVoicePrefs(c)
	if voice.On {
		vexe := filepath.Join(setup.Game, "voice", VoiceExe)
		switch {
		case !isFile(vexe):
			warn("Voice chat is on, but its files are not installed yet: click Update.")
		case user == "" || code == "":
			warn("Voice chat needs your saved login (Settings > Account, or 'Save login from game').")
		default:
			if err := os.WriteFile(filepath.Join(setup.Game, "voice", "voice.ini"), []byte(voice.VoiceINI()), 0o644); err != nil {
				warn("Voice chat could not start: " + err.Error())
				break
			}
			// Start voice chat once the game runs, so both share one Wine session
			// and the game's start is not held up by it.
			go func() {
				select {
				case <-g.done:
					return
				default:
				}
				if !WaitForProcess(ctx, GameExe, 3*time.Minute) {
					return
				}
				vcmd, err := setup.Command(context.WithoutCancel(ctx), vexe, []string{"--with-game"},
					[]string{"PSC_VOICE_ACCOUNT=" + user, "PSC_VOICE_CODE=" + code}, false)
				if err == nil {
					vcmd.Stdout, vcmd.Stderr = out, out
					err = vcmd.Start()
				}
				if err != nil {
					warn("Voice chat could not start: " + err.Error())
					return
				}
				g.mu.Lock()
				g.voice = vcmd
				g.mu.Unlock()
				go vcmd.Wait()
			}()
		}
	}
	return g, nil
}

// Done is closed when the runner that started the game exits (with Proton
// that is when the game has closed).
func (g *Game) Done() <-chan struct{} { return g.done }

// Wait waits for the game to end and returns how the runner exited.
func (g *Game) Wait() error {
	<-g.done
	closeFile(g.logFile)
	return g.waitErr
}

// FolderWritable checks that the game folder takes new files, as the launcher
// does before it starts the game or an update.
func FolderWritable(dir string) bool {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	b := make([]byte, 16)
	rand.Read(b)
	p := filepath.Join(dir, ".psclassic-write-test-"+hex.EncodeToString(b))
	if err := os.WriteFile(p, []byte("ok"), 0o644); err != nil {
		return false
	}
	return os.Remove(p) == nil
}

func closeFile(f *os.File) {
	if f != nil {
		f.Close()
	}
}
