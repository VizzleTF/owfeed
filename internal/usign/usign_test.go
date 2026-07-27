package usign

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The testdata is the real thing, not a fixture we minted: OpenWrt's own sha256sums
// for the 25.12.5 x86/64 target, its detached signature, and the public key that
// signs that branch, fetched from github.com/openwrt/keyring. If this test passes we
// have verified a live OpenWrt release artifact without linking any C.
func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return b
}

func TestVerifyRealOpenWrtRelease(t *testing.T) {
	pub, err := ParsePublicKey(read(t, "b5043e70f9a75cde.pub"))
	if err != nil {
		t.Fatalf("ParsePublicKey: %v", err)
	}
	if got, want := pub.ID.String(), "b5043e70f9a75cde"; got != want {
		t.Errorf("key id = %s, want %s", got, want)
	}

	sig, err := ParseSignature(read(t, "sha256sums.sig"))
	if err != nil {
		t.Fatalf("ParseSignature: %v", err)
	}
	if got, want := sig.ID.String(), "b5043e70f9a75cde"; got != want {
		t.Errorf("sig key id = %s, want %s", got, want)
	}

	if err := Verify(pub, sig, read(t, "sha256sums")); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// A single flipped byte in the signed message must fail. Without this the test above
// would also pass against a verifier that always returns nil.
func TestVerifyRejectsTamperedMessage(t *testing.T) {
	pub, _ := ParsePublicKey(read(t, "b5043e70f9a75cde.pub"))
	sig, _ := ParseSignature(read(t, "sha256sums.sig"))

	msg := read(t, "sha256sums")
	msg[0] ^= 0x01

	if err := Verify(pub, sig, msg); !errors.Is(err, ErrVerify) {
		t.Fatalf("Verify(tampered) = %v, want ErrVerify", err)
	}
}

// A signature made by a different key must be reported as a key-id mismatch, not as a
// generic verification failure — an OpenWrt branch key rotation looks exactly like an
// attack unless the error says which key signed.
func TestVerifyReportsKeyIDMismatch(t *testing.T) {
	pub, _ := ParsePublicKey(read(t, "b5043e70f9a75cde.pub"))
	sig, _ := ParseSignature(read(t, "sha256sums.sig"))
	sig.ID[0] ^= 0xff

	err := Verify(pub, sig, read(t, "sha256sums"))
	if !errors.Is(err, ErrKeyID) {
		t.Fatalf("Verify(other key) = %v, want ErrKeyID", err)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	good := read(t, "sha256sums.sig")

	tests := []struct {
		name string
		blob []byte
		want error
	}{
		{"empty", nil, ErrFormat},
		{"comment only", []byte("untrusted comment: nothing follows\n"), ErrFormat},
		{"not base64", []byte("untrusted comment: x\n!!!not base64!!!\n"), ErrFormat},
		{"truncated", []byte("untrusted comment: x\nRWS1BD5w+adc3g==\n"), ErrFormat},
		// A usign public key is 42 bytes and a signature is 74; feeding one where the
		// other is expected must be a length error, never a silent short read.
		{"public key as signature", read(t, "b5043e70f9a75cde.pub"), ErrFormat},
		{"two payload lines", append(append([]byte{}, good...), good[len(good)-90:]...), ErrFormat},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseSignature(tc.blob); !errors.Is(err, tc.want) {
				t.Fatalf("ParseSignature = %v, want %v", err, tc.want)
			}
		})
	}
}

// The magic check must fire before anything else looks at the payload, so that a blob
// from some other signing scheme is rejected as "not usign" rather than as a length
// mismatch that invites a workaround.
func TestParseRejectsBadMagic(t *testing.T) {
	// Built rather than pasted: a hand-written base64 literal silently drifts by a byte
	// and then the test passes for the wrong reason (it did, once).
	mk := func(n int) []byte {
		raw := make([]byte, n)
		copy(raw, "Xx")
		return []byte("untrusted comment: x\n" + base64.StdEncoding.EncodeToString(raw) + "\n")
	}

	// Right length, wrong magic: magic is what should be reported.
	if _, err := ParseSignature(mk(2 + keyIDLen + sigLen)); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("ParseSignature(right length) = %v, want ErrBadMagic", err)
	}
	// Wrong length AND wrong magic: still magic, because "not usign" is the useful
	// answer and "got 91 bytes, want 74" is a red herring.
	if _, err := ParseSignature(mk(91)); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("ParseSignature(wrong length) = %v, want ErrBadMagic", err)
	}
}

func TestKeyIDString(t *testing.T) {
	var id KeyID
	copy(id[:], []byte{0xb5, 0x04, 0x3e, 0x70, 0xf9, 0xa7, 0x5c, 0xde})
	if got, want := id.String(), "b5043e70f9a75cde"; got != want {
		t.Errorf("KeyID.String() = %q, want %q", got, want)
	}
}

// CRLF and trailing whitespace survive a trip through a mirror; a leading space does
// not happen by accident and would mean something rewrote the file.
func TestParseToleratesCRLF(t *testing.T) {
	blob := bytes.ReplaceAll(read(t, "sha256sums.sig"), []byte("\n"), []byte("\r\n"))
	if _, err := ParseSignature(blob); err != nil {
		t.Fatalf("ParseSignature(CRLF) = %v, want nil", err)
	}
}
