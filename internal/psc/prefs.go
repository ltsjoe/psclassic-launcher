package psc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// FpsCapINI is the game's fpscap.ini, the settings file of the Classic team's
// client add-on (dinput8.dll). The launcher writes its Settings into it before
// the game starts.
type FpsCapINI struct{ Path string }

// GameFpsCap is the fpscap.ini in a game folder.
func GameFpsCap(game string) FpsCapINI { return FpsCapINI{Path: filepath.Join(game, "fpscap.ini")} }

// Exists reports whether the game has the file (the launcher only writes into
// an fpscap.ini that the update installed).
func (f FpsCapINI) Exists() bool {
	fi, err := os.Stat(f.Path)
	return err == nil && fi.Mode().IsRegular()
}

func (f FpsCapINI) readLines() ([]string, error) {
	b, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, err
	}
	s := strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\r", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

// Read returns key in [section], or def. Like the launcher, lines are trimmed and
// the first match wins.
func (f FpsCapINI) Read(section, key, def string) string {
	lines, err := f.readLines()
	if err != nil {
		return def
	}
	cur := ""
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			cur = t[1 : len(t)-1]
			continue
		}
		if cur == section && strings.HasPrefix(t, key+"=") {
			return strings.TrimSpace(t[len(key)+1:])
		}
	}
	return def
}

// Write sets key=value in [section], the way the launcher's FpsCapIni.Write
// does: it replaces the first line for the key in that section, or adds the key
// at the end of the section, or adds the section at the end of the file. Lines
// are written with CRLF, as .NET's File.WriteAllLines does on Windows.
func (f FpsCapINI) Write(section, key, value string) error {
	lines, err := f.readLines()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	cur := ""
	insertAt := -1
	found := false
	for i := 0; i < len(lines) && !found; i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			cur = t[1 : len(t)-1]
			if cur == section {
				insertAt = i + 1
			}
			continue
		}
		if cur == section {
			insertAt = i + 1
			if strings.HasPrefix(t, key+"=") {
				lines[i] = key + "=" + value
				found = true
			}
		}
	}
	if !found {
		if insertAt < 0 {
			if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
				lines = append(lines, "")
			}
			lines = append(lines, "["+section+"]")
			insertAt = len(lines)
		}
		lines = append(lines, "")
		copy(lines[insertAt+1:], lines[insertAt:])
		lines[insertAt] = key + "=" + value
	}
	return writeFileAtomic(f.Path, []byte(strings.Join(lines, "\r\n")+"\r\n"))
}

// PlayerPrefs are the game settings in the launcher's Settings window.
type PlayerPrefs struct {
	MaxFPS           int    // 0 = no limit, at most 2000
	IndoorWeatherFix bool   // no rain, snow or sand inside buildings
	DarkNights       bool   // Planetside Classic's darker nights
	EmpireDarklight  bool   // Darklight Vision in the empire's colour
	HDTextures       bool   // the optional "hd-textures" part
	Flashlight       bool   // the add-on's flashlight
	FlashlightKey    string // launcher key name: "F9" style via VK, e.g. "VK120"
	Beta             bool   // use the beta branch
}

// LoadPlayerPrefs reads the launcher settings, falling back to what the game's
// fpscap.ini says, as the launcher does.
func LoadPlayerPrefs(c *Config, game string) PlayerPrefs {
	ini := GameFpsCap(game)
	flag := func(key, section, iniKey string) bool {
		if v, ok := c.Get(key); ok {
			return v != "0"
		}
		return ini.Read(section, iniKey, "1") != "0"
	}
	p := PlayerPrefs{
		IndoorWeatherFix: flag("indoor_particles_fix", "weather", "indoor_particles_fix"),
		DarkNights:       flag("dark_nights", "lighting", "dark_nights"),
		EmpireDarklight:  flag("empire_darklight", "lighting", "empire_darklight"),
		Flashlight:       flag("flashlight", "flashlight", "enabled"),
		HDTextures:       c.Bool("hd_textures", false),
		Beta:             c.Bool("beta", false),
	}
	fps, ok := c.Get("max_fps")
	if !ok {
		fps = ini.Read("fpscap", "max_fps", "500")
	}
	if n, err := strconv.Atoi(strings.TrimSpace(fps)); err == nil {
		p.MaxFPS = max(0, min(2000, n))
	} else {
		p.MaxFPS = 500
	}
	if k, ok := c.Get("flashlight_key"); ok && k != "" {
		p.FlashlightKey = k
	} else if n, err := strconv.Atoi(ini.Read("flashlight", "key", "120")); err == nil {
		p.FlashlightKey = FromKeyNumber(n)
	} else {
		p.FlashlightKey = "VK120"
	}
	return p
}

// Save stores the prefs in the launcher settings (call Config.Save after).
func (p PlayerPrefs) Save(c *Config) {
	c.SetBool("beta", p.Beta)
	c.SetBool("flashlight", p.Flashlight)
	c.Set("flashlight_key", p.FlashlightKey)
	c.Set("max_fps", strconv.Itoa(max(0, min(2000, p.MaxFPS))))
	c.SetBool("indoor_particles_fix", p.IndoorWeatherFix)
	c.SetBool("dark_nights", p.DarkNights)
	c.SetBool("empire_darklight", p.EmpireDarklight)
	c.SetBool("hd_textures", p.HDTextures)
}

// ApplyPlayerPrefs writes the launcher settings into the game's fpscap.ini, as
// PLAY and Save in Settings do. Settings that were never set are left alone, and
// nothing is written when the game has no fpscap.ini yet.
func ApplyPlayerPrefs(c *Config, game string) error {
	ini := GameFpsCap(game)
	if !ini.Exists() {
		return nil
	}
	for _, w := range []struct{ key, section, iniKey string }{
		{"max_fps", "fpscap", "max_fps"},
		{"indoor_particles_fix", "weather", "indoor_particles_fix"},
		{"dark_nights", "lighting", "dark_nights"},
		{"empire_darklight", "lighting", "empire_darklight"},
		{"flashlight", "flashlight", "enabled"},
	} {
		if v, ok := c.Get(w.key); ok {
			if err := ini.Write(w.section, w.iniKey, v); err != nil {
				return err
			}
		}
	}
	if k, ok := c.Get("flashlight_key"); ok && k != "" {
		if err := ini.Write("flashlight", "key", strconv.Itoa(KeyNumber(k))); err != nil {
			return err
		}
	}
	return nil
}

// Groups gives the optional parts switched on in the settings.
func Groups(c *Config) map[string]bool {
	g := map[string]bool{}
	if c.Bool("hd_textures", false) {
		g["hd-textures"] = true
	}
	if c.Bool("voice_on", false) {
		g["voice"] = true
	}
	return g
}

// Keys are stored the launcher's way: "Mouse3", "Mouse4", "Mouse5", "None", a
// single letter or digit ("Y"), or "VK" plus a Windows virtual-key number ("VK120").

// KeyNumber turns a stored key into the Windows virtual-key number the add-on
// reads (the mouse buttons are VK_MBUTTON, VK_XBUTTON1 and VK_XBUTTON2).
func KeyNumber(key string) int {
	switch key {
	case "Mouse3":
		return 4
	case "Mouse4":
		return 5
	case "Mouse5":
		return 6
	}
	if r := []rune(key); len(r) == 1 && (unicode.IsLetter(r[0]) || unicode.IsDigit(r[0])) {
		return int(unicode.ToUpper(r[0]))
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(key, "VK")); strings.HasPrefix(key, "VK") && err == nil {
		return n
	}
	return 0
}

// FromKeyNumber is the reverse of KeyNumber.
func FromKeyNumber(n int) string {
	switch {
	case n == 4:
		return "Mouse3"
	case n == 5:
		return "Mouse4"
	case n == 6:
		return "Mouse5"
	case n <= 0:
		return "None"
	case n >= 'A' && n <= 'Z', n >= '0' && n <= '9':
		return string(rune(n))
	}
	return "VK" + strconv.Itoa(n)
}

// VoicePrefs are the voice chat settings.
type VoicePrefs struct {
	On        bool
	Keys      [3]string // squad, allies within 50 m, everyone within 50 m
	Mic       string    // device name, "" for the default device
	Speakers  string
	MicVolume int // percent, 0-300
	OutVolume int
	Relay     string
}

// VoiceKeyNames are the settings keys for the three push-to-talk keys.
var VoiceKeyNames = [3]string{"voice_squad_key", "voice_ally_key", "voice_nearby_key"}

// VoiceKeyLabels name the three push-to-talk keys as the launcher does.
var VoiceKeyLabels = [3]string{"Squad (any distance):", "Allies within 50 m:", "Everyone within 50 m:"}

var voiceKeyDefaults = [3]string{"Y", "Mouse4", "Mouse5"}

// DefaultVoiceRelay is the voice server the launcher uses.
const DefaultVoiceRelay = "play.blackarmoredreapers.com:47800"

// LoadVoicePrefs reads the voice chat settings.
func LoadVoicePrefs(c *Config) VoicePrefs {
	v := VoicePrefs{
		On:        c.Bool("voice_on", false),
		Mic:       c.String("voice_mic", ""),
		Speakers:  c.String("voice_speakers", ""),
		MicVolume: c.Int("voice_mic_volume", 100, 0, 300),
		OutVolume: c.Int("voice_out_volume", 100, 0, 300),
		Relay:     c.String("voice_relay", ""),
	}
	for i, k := range VoiceKeyNames {
		v.Keys[i] = c.String(k, "")
		if v.Keys[i] == "" {
			v.Keys[i] = voiceKeyDefaults[i]
		}
	}
	if v.Relay == "" {
		v.Relay = DefaultVoiceRelay
	}
	return v
}

// Save stores the voice settings (call Config.Save after). The relay is not a
// Settings field, so it is left as it is.
func (v VoicePrefs) Save(c *Config) {
	c.SetBool("voice_on", v.On)
	for i, k := range VoiceKeyNames {
		c.Set(k, v.Keys[i])
	}
	c.Set("voice_mic", v.Mic)
	c.Set("voice_speakers", v.Speakers)
	c.Set("voice_mic_volume", strconv.Itoa(max(0, min(300, v.MicVolume))))
	c.Set("voice_out_volume", strconv.Itoa(max(0, min(300, v.OutVolume))))
}

// VoiceINI is the voice\voice.ini the launcher writes each time the game starts.
// The account and code are left empty in the file; they go to the voice program
// in its environment (PSC_VOICE_ACCOUNT, PSC_VOICE_CODE).
func (v VoicePrefs) VoiceINI() string {
	pct := func(n int) string { return strconv.FormatFloat(float64(n)/100, 'f', -1, 64) }
	var b strings.Builder
	b.WriteString("[voice]\r\n# written by the launcher each time the game starts: change these in the launcher's Settings\r\n")
	b.WriteString("account=\r\ncode=\r\n")
	b.WriteString("relay=" + v.Relay + "\r\n")
	b.WriteString("squad_key=" + v.Keys[0] + "\r\n")
	b.WriteString("ally_key=" + v.Keys[1] + "\r\n")
	b.WriteString("nearby_key=" + v.Keys[2] + "\r\n")
	b.WriteString("loopback=0\r\n")
	b.WriteString("mic_volume=" + pct(v.MicVolume) + "\r\n")
	b.WriteString("output_volume=" + pct(v.OutVolume) + "\r\n")
	b.WriteString("mic_device=" + v.Mic + "\r\n")
	b.WriteString("speaker_device=" + v.Speakers + "\r\n")
	return b.String()
}
