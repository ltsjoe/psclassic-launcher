package selfupdate

import (
	_ "embed"
	"errors"
)

// releasePub is the release signing key's public half. scripts/make-signing-key.sh
// writes it; the secret half is kept in Bitwarden only.
//
//go:embed release.pub
var releasePub string

// ReleaseKey is the key every update must be signed with.
func ReleaseKey() (PublicKey, error) {
	k, err := ParsePublicKey(releasePub)
	if err != nil {
		return k, errors.New("this build has no release signing key, so it cannot update itself")
	}
	return k, nil
}
