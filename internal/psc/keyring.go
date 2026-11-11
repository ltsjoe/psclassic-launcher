package psc

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// SystemKeyring is the desktop's keyring: the Secret Service on Linux (KDE
// Wallet, GNOME Keyring, KeePassXC) and the Keychain on macOS.
type SystemKeyring struct{}

func (SystemKeyring) Get(service, user string) (string, error) {
	v, err := keyring.Get(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrSecretNotFound
	}
	return v, err
}

func (SystemKeyring) Set(service, user, secret string) error {
	return keyring.Set(service, user, secret)
}

func (SystemKeyring) Delete(service, user string) error {
	err := keyring.Delete(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
