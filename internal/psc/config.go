package psc

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Config is the Linux launcher's settings file. It uses the Windows launcher's
// launcher.ini format and key names (max_fps, hd_textures, voice_on, ...), so
// settings can be imported from it and read the same way, plus a few keys of its
// own for Linux:
//
//	game_folder  the game folder on disk
//	prefix       the Proton prefix (holds pfx/) or Wine prefix (holds drive_c/)
//	runner       "proton" or "wine"
//	runner_path  the proton script or the wine binary
//
// Like the Windows launcher, Get uses the first line for a key and Set replaces
// every line for it with one line at the end.
type Config struct {
	mu    sync.Mutex
	path  string
	lines []string
}

// Linux launcher keys.
const (
	KeyGameFolder = "game_folder"
	KeyPrefix     = "prefix"
	KeyRunner     = "runner"
	KeyRunnerPath = "runner_path"
)

// ConfigDir is where the Linux launcher keeps its settings:
// ~/.config/psclassic-launcher on Linux, ~/Library/Application Support/psclassic-launcher on macOS.
func ConfigDir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "psclassic-launcher"), nil
}

// DefaultConfigPath is ConfigDir()/launcher.ini.
func DefaultConfigPath() (string, error) {
	d, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "launcher.ini"), nil
}

// LoadConfig reads a settings file. A missing file gives an empty Config that
// Save will create.
func LoadConfig(path string) (*Config, error) {
	c := &Config{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if l != "" {
			c.lines = append(c.lines, l)
		}
	}
	return c, nil
}

// Path is the file the Config was loaded from.
func (c *Config) Path() string { return c.path }

// Exists reports whether the settings file is on disk yet.
func (c *Config) Exists() bool {
	_, err := os.Stat(c.path)
	return err == nil
}

// Get returns the value of the first line for key.
func (c *Config) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range c.lines {
		if strings.HasPrefix(l, key+"=") {
			return l[len(key)+1:], true
		}
	}
	return "", false
}

// String returns the value for key, or def when it is not set.
func (c *Config) String(key, def string) string {
	if v, ok := c.Get(key); ok {
		return v
	}
	return def
}

// Bool is true when key is "1"; def when it is not set.
func (c *Config) Bool(key string, def bool) bool {
	if v, ok := c.Get(key); ok {
		return v == "1"
	}
	return def
}

// Int parses key, clamped to [lo, hi]; def when it is not set or not a number.
func (c *Config) Int(key string, def, lo, hi int) int {
	v, ok := c.Get(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return max(lo, min(hi, n))
}

// Set replaces every line for key with key=value at the end.
func (c *Config) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.lines[:0]
	for _, l := range c.lines {
		if !strings.HasPrefix(l, key+"=") {
			kept = append(kept, l)
		}
	}
	c.lines = append(kept, key+"="+value)
}

// SetBool stores b as "1" or "0".
func (c *Config) SetBool(key string, b bool) {
	c.Set(key, boolStr(b))
}

// Keys lists the keys that are set, in file order.
func (c *Config) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	seen := map[string]bool{}
	for _, l := range c.lines {
		if k, _, ok := strings.Cut(l, "="); ok && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// Save writes the file atomically, readable only by the user (it holds the
// encrypted login code).
func (c *Config) Save() error {
	c.mu.Lock()
	data := strings.Join(c.lines, "\n") + "\n"
	c.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(data), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
