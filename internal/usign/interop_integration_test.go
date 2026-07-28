package usign

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The signing side is only useful if the real usign accepts what it writes, and
// reads what it generates. Anything less is a self-consistent format nobody else
// speaks.
//
// The binary is built from git.openwrt.org/project/usign; set OWFEED_USIGN to it.
func TestIntegrationInteropWithUsign(t *testing.T) {
	bin := os.Getenv("OWFEED_USIGN")
	if bin == "" {
		t.Skip("set OWFEED_USIGN to a usign binary to run")
	}
	dir := t.TempDir()
	msg := filepath.Join(dir, "message")
	if err := os.WriteFile(msg, []byte("the bytes a router would install\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	key, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	sec, err := key.MarshalPrivate("owfeed test")
	if err != nil {
		t.Fatal(err)
	}
	pubPath := filepath.Join(dir, "key.pub")
	secPath := filepath.Join(dir, "key.sec")
	sigPath := filepath.Join(dir, "message.sig")
	for path, data := range map[string][]byte{
		pubPath: key.MarshalPublic("owfeed test"),
		secPath: sec,
		sigPath: key.Sign(mustRead(t, msg), "owfeed test"),
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// usign verifies what owfeed signed.
	if out, err := exec.Command(bin, "-V", "-m", msg, "-p", pubPath, "-x", sigPath).CombinedOutput(); err != nil {
		t.Fatalf("usign rejected a signature owfeed produced: %v\n%s", err, out)
	}

	// owfeed verifies what usign signed, with owfeed's own key.
	theirs := filepath.Join(dir, "theirs.sig")
	if out, err := exec.Command(bin, "-S", "-m", msg, "-s", secPath, "-x", theirs).CombinedOutput(); err != nil {
		t.Fatalf("usign could not sign with a key owfeed generated: %v\n%s", err, out)
	}
	pub, err := ParsePublicKey(mustRead(t, pubPath))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ParseSignature(mustRead(t, theirs))
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(pub, sig, mustRead(t, msg)); err != nil {
		t.Fatalf("owfeed rejected a signature usign produced: %v", err)
	}

	// A key owfeed wrote reads back as the same key.
	back, err := ParsePrivateKey(sec)
	if err != nil {
		t.Fatal(err)
	}
	if back.ID != key.ID || string(back.Key) != string(key.Key) {
		t.Error("a secret key does not survive a round trip through its own encoding")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
