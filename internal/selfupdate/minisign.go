package selfupdate

// Release signatures use minisign's format (https://jedisct1.github.io/minisign/),
// so anyone can check a release by hand with the minisign tool. Only the
// standard library is needed: signatures are the "Ed" kind (Ed25519 over the
// file itself), which every minisign version verifies.
//
// A .minisig file is four lines:
//
//	untrusted comment: <text>
//	base64("Ed" || key id (8 bytes) || Ed25519 signature of the file)
//	trusted comment: <text>
//	base64(Ed25519 signature of (file signature || trusted comment text))

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

var sigAlg = [2]byte{'E', 'd'}

const (
	untrustedPrefix = "untrusted comment: "
	trustedPrefix   = "trusted comment: "
	secretPrefix    = "psclassic-launcher-signing-key-v1:"
)

// PublicKey is a minisign public key.
type PublicKey struct {
	ID  [8]byte
	Key ed25519.PublicKey
}

// SecretKey is the release signing key. It lives in Bitwarden only.
type SecretKey struct {
	ID  [8]byte
	Key ed25519.PrivateKey
}

// keyIDString is how minisign shows a key id: the 8 bytes as a little-endian
// number, in hex.
func keyIDString(id [8]byte) string {
	return fmt.Sprintf("%016X", binary.LittleEndian.Uint64(id[:]))
}

// IDString is the key id as minisign prints it.
func (k PublicKey) IDString() string { return keyIDString(k.ID) }

// String is the public key file: a comment line and the key line.
func (k PublicKey) String() string {
	raw := append(append(sigAlg[:], k.ID[:]...), k.Key...)
	return untrustedPrefix + "minisign public key " + k.IDString() + "\n" +
		base64.StdEncoding.EncodeToString(raw) + "\n"
}

// ParsePublicKey reads a public key file, or just its key line.
func ParsePublicKey(text string) (PublicKey, error) {
	var k PublicKey
	line := ""
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, untrustedPrefix) {
			continue
		}
		line = l
		break
	}
	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil || len(raw) != 2+8+ed25519.PublicKeySize {
		return k, errors.New("not a minisign public key")
	}
	if raw[0] != sigAlg[0] || raw[1] != sigAlg[1] {
		return k, errors.New("not an Ed25519 minisign public key")
	}
	copy(k.ID[:], raw[2:10])
	k.Key = ed25519.PublicKey(bytes.Clone(raw[10:]))
	return k, nil
}

// Verify checks a .minisig file against the message. It returns the trusted
// comment, which the signature also covers.
func (k PublicKey) Verify(message, minisig []byte) (string, error) {
	lines := strings.Split(strings.ReplaceAll(string(minisig), "\r\n", "\n"), "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[0], untrustedPrefix) ||
		!strings.HasPrefix(lines[2], trustedPrefix) {
		return "", errors.New("not a minisign signature")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(sig) != 2+8+ed25519.SignatureSize {
		return "", errors.New("not a minisign signature")
	}
	if sig[0] != sigAlg[0] || sig[1] != sigAlg[1] {
		return "", errors.New("unsupported signature kind (only Ed is accepted)")
	}
	var id [8]byte
	copy(id[:], sig[2:10])
	if id != k.ID {
		return "", fmt.Errorf("signed with key %s, not the release key %s", keyIDString(id), k.IDString())
	}
	if !ed25519.Verify(k.Key, message, sig[10:]) {
		return "", errors.New("the signature does not match the file")
	}
	trusted := strings.TrimPrefix(lines[2], trustedPrefix)
	global, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3]))
	if err != nil || len(global) != ed25519.SignatureSize {
		return "", errors.New("not a minisign signature")
	}
	if !ed25519.Verify(k.Key, append(bytes.Clone(sig[10:]), trusted...), global) {
		return "", errors.New("the trusted comment does not match its signature")
	}
	return trusted, nil
}

// GenerateKey makes a new signing key.
func GenerateKey() (SecretKey, error) {
	var s SecretKey
	if _, err := rand.Read(s.ID[:]); err != nil {
		return s, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return s, err
	}
	s.Key = priv
	return s, nil
}

// Public is the matching public key.
func (s SecretKey) Public() PublicKey {
	return PublicKey{ID: s.ID, Key: s.Key.Public().(ed25519.PublicKey)}
}

// String is the secret key as one line, the form kept in Bitwarden.
func (s SecretKey) String() string {
	return secretPrefix + base64.StdEncoding.EncodeToString(append(s.ID[:], s.Key.Seed()...))
}

// ParseSecretKey reads the line String writes.
func ParseSecretKey(text string) (SecretKey, error) {
	var s SecretKey
	line := strings.TrimSpace(text)
	if !strings.HasPrefix(line, secretPrefix) {
		return s, errors.New("not a psclassic-launcher signing key")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, secretPrefix))
	if err != nil || len(raw) != 8+ed25519.SeedSize {
		return s, errors.New("damaged signing key")
	}
	copy(s.ID[:], raw[:8])
	s.Key = ed25519.NewKeyFromSeed(raw[8:])
	return s, nil
}

// Sign makes the .minisig file for message. The trusted comment must be one line.
func (s SecretKey) Sign(message []byte, trustedComment string) ([]byte, error) {
	if strings.ContainsAny(trustedComment, "\r\n") {
		return nil, errors.New("the trusted comment must be one line")
	}
	sig := ed25519.Sign(s.Key, message)
	global := ed25519.Sign(s.Key, append(bytes.Clone(sig), trustedComment...))
	raw := append(append(sigAlg[:], s.ID[:]...), sig...)
	var b strings.Builder
	b.WriteString(untrustedPrefix + "signature from psclassic-launcher release key " + keyIDString(s.ID) + "\n")
	b.WriteString(base64.StdEncoding.EncodeToString(raw) + "\n")
	b.WriteString(trustedPrefix + trustedComment + "\n")
	b.WriteString(base64.StdEncoding.EncodeToString(global) + "\n")
	return []byte(b.String()), nil
}
