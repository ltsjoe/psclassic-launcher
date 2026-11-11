package psc

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// The saved login lets PLAY log in without the login screen
// (planetside.exe /CC /K:<code> /N:<account>) and lets voice chat sign in.
// The game gives a player their login code when they type !launcher in chat.
//
// On Windows the launcher protects the code with DPAPI for the Windows account.
// Here the code is encrypted with AES-256-GCM, and the key lives in the
// desktop's keyring (KDE Wallet or GNOME Keyring through the Secret Service, or
// the macOS Keychain). launcher.ini only ever holds the encrypted code, which is
// useless without the keyring, and the keyring is locked with the user's login.

// Launcher messages for the login.
var (
	ErrNoChatLog   = errors.New("No chat log found. In the game, type /log on and then !launcher. Then type /log off (or close the game) and click this button again.")
	ErrNoCodeYet   = errors.New("No login code found yet. In the game, type /log on and then !launcher. Then type /log off (or close the game) and click this button again.")
	ErrCodeInLog   = errors.New("The login code in the chat log does not look right. Please type !launcher in the game again.")
	ErrAccountName = errors.New("Please enter your account name (the name you log in with, not a character name).")
	ErrCodeFormat  = errors.New("That login code does not look right. It has 16 letters and numbers, e.g. 7KQ2-MX9D-4HTB-PZ3F.")
)

// codeAlphabet is the alphabet login codes use (Crockford base32: no I, L, O, U).
const codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// CleanCode keeps only the letters and digits of a typed code, upper-cased.
func CleanCode(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}

// LooksValid reports whether a cleaned code has the right shape.
func LooksValid(code string) bool {
	if len(code) != 16 {
		return false
	}
	for _, r := range code {
		if !strings.ContainsRune(codeAlphabet, r) {
			return false
		}
	}
	return true
}

// CheckLogin validates an account name and code the way the Settings window
// does, and returns the cleaned code.
func CheckLogin(user, code string) (string, string, error) {
	user = strings.TrimSpace(user)
	code = CleanCode(code)
	if user == "" || strings.Contains(user, " ") {
		return "", "", ErrAccountName
	}
	if !LooksValid(code) {
		return "", "", ErrCodeFormat
	}
	return user, code, nil
}

var chatLogCode = regexp.MustCompile(`launcher login code:\s*([0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4})\s*\(account:\s*([^)\s]+)\)`)

// LoginFromChatLog finds the newest "launcher login code: XXXX-XXXX-XXXX-XXXX
// (account: name)" line in the game's chat.log. As the launcher does, it then
// removes those lines from the log, so the code does not stay there.
func LoginFromChatLog(game string) (user, code string, err error) {
	path := filepath.Join(game, "chat.log")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", ErrNoChatLog
	}
	if err != nil {
		return "", "", err
	}
	all := chatLogCode.FindAllSubmatch(b, -1)
	if len(all) == 0 {
		return "", "", ErrNoCodeYet
	}
	last := all[len(all)-1]
	code = CleanCode(string(last[1]))
	user = string(last[2])
	if !LooksValid(code) {
		return "", "", ErrCodeInLog
	}
	// Scrub the code from the log. A failure here does not lose the login.
	lines := bytes.Split(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")), []byte("\n"))
	kept := lines[:0]
	for _, l := range lines {
		if !bytes.Contains(l, []byte("launcher login code:")) {
			kept = append(kept, l)
		}
	}
	_ = os.WriteFile(path, bytes.Join(kept, []byte("\r\n")), 0o644)
	return user, code, nil
}

// Keyring stores small secrets for the user (see SystemKeyring).
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	Delete(service, user string) error
}

// ErrSecretNotFound is what a Keyring returns for a missing secret.
var ErrSecretNotFound = errors.New("secret not found in keyring")

const (
	keyringService = "psclassic-launcher"
	keyringUser    = "login-code-key"
	loginAAD       = "Planetside Classic login code" // the launcher's DPAPI entropy string
)

// LoginStore saves the account name and the encrypted login code in the
// launcher settings, with the encryption key in the keyring.
type LoginStore struct {
	Config  *Config
	Keyring Keyring
}

// Save checks and stores a login. Call Config.Save after.
func (s LoginStore) Save(user, code string) error {
	user, code, err := CheckLogin(user, code)
	if err != nil {
		return err
	}
	key, err := s.key(true)
	if err != nil {
		return err
	}
	blob, err := seal(key, []byte(code))
	if err != nil {
		return err
	}
	s.Config.Set("login_user", user)
	s.Config.Set("login_code", blob)
	return nil
}

// Load returns the saved login. No login gives empty strings and no error.
func (s LoginStore) Load() (user, code string, err error) {
	user = s.Config.String("login_user", "")
	blob := s.Config.String("login_code", "")
	if user == "" || blob == "" {
		return "", "", nil
	}
	key, err := s.key(false)
	if err != nil {
		return "", "", err
	}
	plain, err := unseal(key, blob)
	if err != nil {
		return "", "", fmt.Errorf("the saved login code could not be decrypted (was the keyring reset?); please save it again: %w", err)
	}
	return user, string(plain), nil
}

// IsSet reports whether a login is saved and can be read.
func (s LoginStore) IsSet() bool {
	u, c, err := s.Load()
	return err == nil && u != "" && c != ""
}

// Forget removes the saved login (the key stays in the keyring for next time).
// Call Config.Save after.
func (s LoginStore) Forget() {
	s.Config.Set("login_user", "")
	s.Config.Set("login_code", "")
}

// key fetches the encryption key from the keyring, creating it when asked.
func (s LoginStore) key(create bool) ([]byte, error) {
	if s.Keyring == nil {
		return nil, errors.New("no keyring is available to protect the login code")
	}
	enc, err := s.Keyring.Get(keyringService, keyringUser)
	if err == nil {
		k, derr := base64.StdEncoding.DecodeString(enc)
		if derr == nil && len(k) == 32 {
			return k, nil
		}
		if !create {
			return nil, errors.New("the login key in the keyring is damaged")
		}
	} else if !errors.Is(err, ErrSecretNotFound) {
		return nil, fmt.Errorf("could not open the keyring (KDE Wallet, GNOME Keyring or Keychain): %w", err)
	} else if !create {
		return nil, errors.New("the login key is missing from the keyring; please save the login again")
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := s.Keyring.Set(keyringService, keyringUser, base64.StdEncoding.EncodeToString(k)); err != nil {
		return nil, fmt.Errorf("could not store the login key in the keyring: %w", err)
	}
	return k, nil
}

// seal encrypts with AES-256-GCM: base64("PSCL1" || nonce || ciphertext).
func seal(key, plain []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := append([]byte("PSCL1"), nonce...)
	out = gcm.Seal(out, nonce, plain, []byte(loginAAD))
	return base64.StdEncoding.EncodeToString(out), nil
}

func unseal(key []byte, blob string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(blob)
	if err != nil || !bytes.HasPrefix(b, []byte("PSCL1")) {
		return nil, errors.New("not an encrypted login code")
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	b = b[5:]
	if len(b) < gcm.NonceSize() {
		return nil, errors.New("encrypted login code is too short")
	}
	return gcm.Open(nil, b[:gcm.NonceSize()], b[gcm.NonceSize():], []byte(loginAAD))
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
