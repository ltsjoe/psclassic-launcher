package psc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

// testKey makes a P-256 key and its base64 ECS1 blob, like the launcher's.
func testKey(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := priv.PublicKey.Bytes() // 0x04 || X || Y
	if err != nil {
		t.Fatal(err)
	}
	blob := append([]byte("ECS1\x20\x00\x00\x00"), pt[1:]...)
	return priv, base64.StdEncoding.EncodeToString(blob)
}

// signP1363 signs data the way ECDsaCng.SignData does: SHA-256, raw r||s.
func signP1363(t *testing.T, priv *ecdsa.PrivateKey, data []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(data)
	r, s, err := ecdsa.Sign(rand.Reader, priv, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return sig
}

func TestVerifySignature(t *testing.T) {
	priv, b64 := testKey(t)
	pub, err := ParsePublicKey(b64)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("PSCLASSIC-MANIFEST 1\nversion 1\n")
	sig := signP1363(t, priv, data)
	if !VerifySignature(pub, data, sig) {
		t.Fatal("good signature rejected")
	}
	if VerifySignature(pub, append(data, 'x'), sig) {
		t.Fatal("signature accepted for changed data")
	}
	sig[10] ^= 1
	if VerifySignature(pub, data, sig) {
		t.Fatal("damaged signature accepted")
	}
	if VerifySignature(pub, data, sig[:63]) {
		t.Fatal("short signature accepted")
	}
}

func TestDefaultPublicKeyParses(t *testing.T) {
	if _, err := ParsePublicKey(DefaultPublicKey); err != nil {
		t.Fatal(err)
	}
}

func TestParsePublicKeyRejects(t *testing.T) {
	for _, s := range []string{"", "not base64!", base64.StdEncoding.EncodeToString([]byte("RSA1\x20\x00\x00\x00")),
		// right header, but the point is not on the curve
		base64.StdEncoding.EncodeToString(append([]byte("ECS1\x20\x00\x00\x00"), make([]byte, 64)...))} {
		if _, err := ParsePublicKey(s); err == nil {
			t.Errorf("ParsePublicKey(%q) succeeded", s)
		}
	}
}
