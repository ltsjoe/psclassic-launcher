package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignVerify(t *testing.T) {
	s, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("abc  psclassic-launcher-0.3.0-x86_64.AppImage\n")
	sig, err := s.Sign(msg, "psclassic-launcher 0.3.0")
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ParsePublicKey(s.Public().String())
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := pub.Verify(msg, sig)
	if err != nil || trusted != "psclassic-launcher 0.3.0" {
		t.Fatalf("Verify = %q, %v", trusted, err)
	}

	// The secret key survives its round trip through text (Bitwarden).
	s2, err := ParseSecretKey(s.String() + "\n")
	if err != nil || s2.ID != s.ID || !s2.Key.Equal(s.Key) {
		t.Fatalf("ParseSecretKey: %v", err)
	}

	if _, err := pub.Verify(append([]byte("x"), msg...), sig); err == nil {
		t.Error("a changed file passed")
	}
	lines := strings.Split(string(sig), "\n")
	lines[2] = "trusted comment: psclassic-launcher 9.9.9"
	if _, err := pub.Verify(msg, []byte(strings.Join(lines, "\n"))); err == nil {
		t.Error("a changed trusted comment passed")
	}
	other, _ := GenerateKey()
	if _, err := other.Public().Verify(msg, sig); err == nil {
		t.Error("another key passed")
	}
	same := other.Public()
	same.ID = s.ID // right id, wrong key
	if _, err := same.Verify(msg, sig); err == nil {
		t.Error("a key with the right id but the wrong key passed")
	}
	if _, err := s.Sign(msg, "two\nlines"); err == nil {
		t.Error("a two-line trusted comment was signed")
	}
	if _, err := ParsePublicKey("untrusted comment: no release key yet\n"); err == nil {
		t.Error("the placeholder parsed as a key")
	}
}

// TestWriteForMinisign writes a key, a file and its signature to
// $PSC_MINISIGN_OUT, to check them with the real minisign tool:
//
//	minisign -Vm msg.txt -p test.pub
func TestWriteForMinisign(t *testing.T) {
	dir := os.Getenv("PSC_MINISIGN_OUT")
	if dir == "" {
		t.Skip("PSC_MINISIGN_OUT not set")
	}
	s, _ := GenerateKey()
	msg := []byte("hello from psclassic-launcher\n")
	sig, _ := s.Sign(msg, "psclassic-launcher test")
	for name, data := range map[string][]byte{
		"test.pub": []byte(s.Public().String()), "msg.txt": msg, "msg.txt.minisig": sig,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
