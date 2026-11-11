package psc

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigFirstWinsSetReplaces(t *testing.T) {
	p := filepath.Join(t.TempDir(), "launcher.ini")
	if err := os.WriteFile(p, []byte("max_fps=120\r\nbeta=1\r\nmax_fps=60\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Int("max_fps", 500, 0, 2000); got != 120 {
		t.Errorf("first line should win: %d", got)
	}
	c.Set("max_fps", "240")
	c.SetBool("dark_nights", false)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); got != "beta=1\nmax_fps=240\ndark_nights=0\n" {
		t.Errorf("saved %q", got)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
}

func TestFpsCapWrite(t *testing.T) {
	f := FpsCapINI{Path: filepath.Join(t.TempDir(), "fpscap.ini")}
	write(t, f.Path, "[fpscap]\r\nmax_fps=60\r\n[weather]\r\nindoor_particles_fix=0\r\n")
	for _, w := range [][3]string{
		{"fpscap", "max_fps", "240"},           // replaced in place
		{"weather", "rain", "1"},               // added at the end of its section
		{"flashlight", "key", "120"},           // new section, after a blank line
		{"fpscap", "frame_limiter", "precise"}, // added to the first section
	} {
		if err := f.Write(w[0], w[1], w[2]); err != nil {
			t.Fatal(err)
		}
	}
	want := "[fpscap]\r\nmax_fps=240\r\nframe_limiter=precise\r\n[weather]\r\nindoor_particles_fix=0\r\nrain=1\r\n\r\n[flashlight]\r\nkey=120\r\n"
	if got := read(t, f.Path); got != want {
		t.Errorf("fpscap.ini =\n%q\nwant\n%q", got, want)
	}
	if got := f.Read("flashlight", "key", ""); got != "120" {
		t.Errorf("Read = %q", got)
	}
}

func TestKeys(t *testing.T) {
	for key, n := range map[string]int{"Mouse3": 4, "Mouse4": 5, "Mouse5": 6, "Y": 'Y', "7": '7', "VK120": 120, "None": 0} {
		if got := KeyNumber(key); got != n {
			t.Errorf("KeyNumber(%q) = %d, want %d", key, got, n)
		}
		if got := FromKeyNumber(n); got != key {
			t.Errorf("FromKeyNumber(%d) = %q, want %q", n, got, key)
		}
	}
	for in, want := range map[string]string{"f9": "VK120", "y": "Y", "Mouse button 4 (side)": "Mouse4", "(none)": "None", "vk120": "VK120", "Space": "VK32"} {
		if got, err := ParseKey(in); err != nil || got != want {
			t.Errorf("ParseKey(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseKey("NotAKey"); err == nil {
		t.Error("ParseKey accepted nonsense")
	}
	if got := KeyDisplay("VK120"); got != "F9" {
		t.Errorf("KeyDisplay(VK120) = %q", got)
	}
}

// memKeyring is a Keyring in memory.
type memKeyring map[string]string

func (m memKeyring) Get(s, u string) (string, error) {
	v, ok := m[s+"/"+u]
	if !ok {
		return "", ErrSecretNotFound
	}
	return v, nil
}
func (m memKeyring) Set(s, u, v string) error { m[s+"/"+u] = v; return nil }
func (m memKeyring) Delete(s, u string) error { delete(m, s+"/"+u); return nil }

func TestLoginStore(t *testing.T) {
	c, _ := LoadConfig(filepath.Join(t.TempDir(), "launcher.ini"))
	keys := memKeyring{}
	st := LoginStore{Config: c, Keyring: keys}
	if err := st.Save("joe", "7kq2-mx9d-4htb-pz3f"); err != nil {
		t.Fatal(err)
	}
	blob := c.String("login_code", "")
	if raw, err := base64.StdEncoding.DecodeString(blob); err != nil || !strings.HasPrefix(string(raw), "PSCL1") || strings.Contains(string(raw), "7KQ2") {
		t.Errorf("stored code is not sealed: %q", blob)
	}
	user, code, err := st.Load()
	if err != nil || user != "joe" || code != "7KQ2MX9D4HTBPZ3F" {
		t.Errorf("Load = %q %q %v", user, code, err)
	}

	// Another keyring (e.g. a reset wallet) can't open it.
	other := LoginStore{Config: c, Keyring: memKeyring{}}
	if _, _, err := other.Load(); err == nil {
		t.Error("loaded without the key")
	}
	// A changed blob fails the GCM check.
	c.Set("login_code", blob[:len(blob)-4]+"AAAA")
	if _, _, err := st.Load(); err == nil {
		t.Error("loaded a damaged code")
	}

	if err := st.Save("two words", "7KQ2MX9D4HTBPZ3F"); !errors.Is(err, ErrAccountName) {
		t.Errorf("account with a space: %v", err)
	}
	if err := st.Save("joe", "7KQ2-MX9D-4HTB-PZ3I"); !errors.Is(err, ErrCodeFormat) { // I is not in the alphabet
		t.Errorf("bad code: %v", err)
	}
	st.Forget()
	if st.IsSet() {
		t.Error("still set after Forget")
	}
}

func TestLoginFromChatLog(t *testing.T) {
	game := t.TempDir()
	if _, _, err := LoginFromChatLog(game); !errors.Is(err, ErrNoChatLog) {
		t.Errorf("no log: %v", err)
	}
	log := filepath.Join(game, "chat.log")
	write(t, log, "[12:00] hello\r\n")
	if _, _, err := LoginFromChatLog(game); !errors.Is(err, ErrNoCodeYet) {
		t.Errorf("no code: %v", err)
	}
	write(t, log, "[12:00] hello\r\n"+
		"[12:01] launcher login code: AAAA-BBBB-CCCC-DDDD (account: old)\r\n"+
		"[12:02] bye\r\n"+
		"[12:03] launcher login code: 7KQ2-MX9D-4HTB-PZ3F (account: joe)\r\n")
	user, code, err := LoginFromChatLog(game)
	if err != nil || user != "joe" || code != "7KQ2MX9D4HTBPZ3F" {
		t.Errorf("= %q %q %v", user, code, err)
	}
	if got := read(t, log); strings.Contains(got, "login code") || !strings.Contains(got, "bye") {
		t.Errorf("log not scrubbed right: %q", got)
	}
}

func TestOnlineText(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	fresh := `{"time":1799999940000,"total":3,"TR":1,"NC":2,"VS":0}`
	if got := onlineText([]byte(fresh), now); got != "PSC Online: 3   (TR 1 | NC 2 | VS 0)" {
		t.Errorf("fresh = %q", got)
	}
	stale := `{"time":1799999000000,"total":3,"TR":1,"NC":2,"VS":0}`
	if got := onlineText([]byte(stale), now); got != "Server offline" {
		t.Errorf("stale = %q", got)
	}
	if got := Mb(860 << 20); got != "860.0 MB" {
		t.Errorf("Mb = %q", got)
	}
	if got := Mb(4_692_251_771); got != "4.37 GB" {
		t.Errorf("Mb = %q", got)
	}
}
