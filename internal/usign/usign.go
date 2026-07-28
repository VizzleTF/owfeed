// Package usign verifies OpenWrt's usign (signify-style ed25519) signatures.
//
// This exists so that verifying an OpenWrt SDK download does not require building
// usign from C first. The reference implementation is github.com/openwrt/usign;
// projects that need it in CI currently clone and `cc *.c` it before they can check
// anything, which puts a C toolchain on the critical path of every consumer.
//
// Wire format, both for keys and signatures, is a two-line text file:
//
//	untrusted comment: <free text>
//	<base64 blob>
//
// The blob is a 2-byte magic, an 8-byte key id, and then the payload:
//
//	public key: "Ed" || keyid[8] || pubkey[32]   = 42 bytes
//	signature:  "Ed" || keyid[8] || sig[64]      = 74 bytes
//
// The key id is not a hash of anything — it is random bytes chosen at keygen — so it
// identifies which key signed, and nothing more. It is NOT a substitute for pinning:
// an attacker who can replace the signature can put any key id in it. Callers must
// decide independently which key ids they trust.
package usign

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// magic prefixes every usign key and signature blob.
var magic = [2]byte{'E', 'd'}

const (
	keyIDLen  = 8
	pubKeyLen = ed25519.PublicKeySize // 32
	sigLen    = ed25519.SignatureSize // 64
)

// KeyID is the 8-byte identifier a usign key carries. Its string form is lowercase
// hex, which is also how github.com/openwrt/keyring names its key files.
type KeyID [keyIDLen]byte

func (k KeyID) String() string { return fmt.Sprintf("%x", k[:]) }

// PublicKey is a parsed usign public key.
type PublicKey struct {
	ID  KeyID
	Key ed25519.PublicKey
}

// Signature is a parsed usign signature.
type Signature struct {
	ID  KeyID
	Sig []byte
}

var (
	ErrFormat   = errors.New("usign: malformed blob")
	ErrBadMagic = errors.New("usign: bad magic (not an Ed blob)")
	ErrKeyID    = errors.New("usign: signature key id does not match public key")
	ErrVerify   = errors.New("usign: signature verification failed")
)

// decode pulls the base64 payload out of a two-line usign file and checks the magic
// and total length. Trailing whitespace and CRLF are tolerated because these files
// travel over HTTP from a mirror; leading whitespace is not, because that would mean
// something reformatted the file.
func decode(blob []byte, wantLen int) (KeyID, []byte, error) {
	var id KeyID
	raw, err := decodeRaw(blob, wantLen)
	if err != nil {
		return id, nil, err
	}
	copy(id[:], raw[2:2+keyIDLen])
	return id, raw[2+keyIDLen:], nil
}

// decodeRaw returns the whole blob. Only public keys and signatures put a key id
// straight after the magic; a secret key lays out its kdf fields there, so reading
// one through decode would take four bytes of salt for a key id.
func decodeRaw(blob []byte, wantLen int) ([]byte, error) {
	var b64 string
	for _, line := range strings.Split(string(blob), "\n") {
		line = strings.TrimRight(line, "\r \t")
		if line == "" || strings.HasPrefix(line, "untrusted comment:") {
			continue
		}
		if b64 != "" {
			return nil, fmt.Errorf("%w: more than one payload line", ErrFormat)
		}
		b64 = line
	}
	if b64 == "" {
		return nil, fmt.Errorf("%w: no payload line", ErrFormat)
	}

	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("%w: base64: %v", ErrFormat, err)
	}

	// Magic before length, deliberately. A blob from some other signing scheme is
	// almost never exactly 42 or 74 bytes long, so checking length first would report
	// it as "got 91 bytes, want 74" — which reads like a truncated download and invites
	// someone to go looking for the missing bytes. "not an Ed blob" says the real thing.
	if len(raw) < len(magic) {
		return nil, fmt.Errorf("%w: %d bytes is too short to be anything", ErrFormat, len(raw))
	}
	if !bytes.Equal(raw[:len(magic)], magic[:]) {
		return nil, fmt.Errorf("%w: got %q", ErrBadMagic, raw[:len(magic)])
	}
	if len(raw) != wantLen {
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrFormat, len(raw), wantLen)
	}

	return raw, nil
}

// ParsePublicKey reads a usign public key file (the format shipped in
// github.com/openwrt/keyring/usign/<keyid>).
func ParsePublicKey(blob []byte) (*PublicKey, error) {
	id, payload, err := decode(blob, 2+keyIDLen+pubKeyLen)
	if err != nil {
		return nil, err
	}
	return &PublicKey{ID: id, Key: ed25519.PublicKey(payload)}, nil
}

// ParseSignature reads a usign detached signature file (e.g. sha256sums.sig).
func ParseSignature(blob []byte) (*Signature, error) {
	id, payload, err := decode(blob, 2+keyIDLen+sigLen)
	if err != nil {
		return nil, err
	}
	return &Signature{ID: id, Sig: payload}, nil
}

// Verify checks sig over message under pub.
//
// The key id is compared first and separately from the cryptography. That is not a
// security boundary — a matching id proves nothing — but it turns the common case of
// "OpenWrt rotated the branch key" into a distinguishable error instead of a bare
// verification failure, which is exactly the confusion the OpenWrt build docs warn
// about: a key rotation and an attack look identical if you only report BAD SIGNATURE.
func Verify(pub *PublicKey, sig *Signature, message []byte) error {
	if pub.ID != sig.ID {
		return fmt.Errorf("%w: signed by %s, have %s", ErrKeyID, sig.ID, pub.ID)
	}
	if !ed25519.Verify(pub.Key, message, sig.Sig) {
		return ErrVerify
	}
	return nil
}

// Secret key layout, from usign's struct seckey (main.c):
//
//	"Ed"        pkalg
//	"BK"        kdfalg — bcrypt, but with kdfrounds 0 the key is stored unprotected
//	uint32      kdfrounds
//	[16]byte    salt
//	[8]byte     checksum, the first 8 bytes of SHA-512 over the 64-byte key
//	[8]byte     fingerprint, which is the key id
//	[64]byte    seed ‖ public key
//
// That last field is the reason this interoperates with the standard library: it
// holds a seed and its public key, which is exactly Go's ed25519.PrivateKey. usign
// clamps the seed before storing it, which the RFC does not, but the seed is hashed
// on use either way, so a key generated here and one generated by usign are read
// identically by both.
const (
	secKeyLen    = 104
	saltOffset   = 8
	checkOffset  = 24
	fprintOffset = 32
	seedOffset   = 40
)

var kdfMagic = [2]byte{'B', 'K'}

// PrivateKey is a usign secret key.
type PrivateKey struct {
	ID  KeyID
	Key ed25519.PrivateKey
}

// Generate creates a new signing key.
func Generate() (*PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	var id KeyID
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	return &PrivateKey{ID: id, Key: priv}, nil
}

// ParsePrivateKey reads a usign secret key.
func ParsePrivateKey(blob []byte) (*PrivateKey, error) {
	raw, err := decodeRaw(blob, secKeyLen)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(raw[2:4], kdfMagic[:]) {
		return nil, fmt.Errorf("%w: kdf algorithm is %q, not BK", ErrFormat, raw[2:4])
	}
	// A non-zero round count means the key is encrypted with a passphrase. Reading
	// it as though it were not would produce a key that signs garbage.
	if rounds := binary.LittleEndian.Uint32(raw[4:8]); rounds != 0 {
		return nil, fmt.Errorf("%w: the key is passphrase-protected (%d kdf rounds)", ErrFormat, rounds)
	}

	key := ed25519.PrivateKey(raw[seedOffset : seedOffset+ed25519.PrivateKeySize])
	sum := sha512.Sum512(key)
	if !bytes.Equal(sum[:8], raw[checkOffset:checkOffset+8]) {
		return nil, fmt.Errorf("%w: the key does not match its own checksum", ErrFormat)
	}

	var id KeyID
	copy(id[:], raw[fprintOffset:fprintOffset+keyIDLen])
	return &PrivateKey{ID: id, Key: key}, nil
}

// MarshalPrivate encodes a secret key in usign's form.
func (k *PrivateKey) MarshalPrivate(comment string) ([]byte, error) {
	raw := make([]byte, secKeyLen)
	copy(raw[0:2], magic[:])
	copy(raw[2:4], kdfMagic[:])
	// kdfrounds stays zero: usign writes unprotected keys, and a passphrase on a key
	// a CI job has to read would be a passphrase stored beside it.
	if _, err := rand.Read(raw[saltOffset : saltOffset+16]); err != nil {
		return nil, err
	}
	sum := sha512.Sum512(k.Key)
	copy(raw[checkOffset:checkOffset+8], sum[:8])
	copy(raw[fprintOffset:fprintOffset+keyIDLen], k.ID[:])
	copy(raw[seedOffset:], k.Key)
	return encode(comment, raw), nil
}

// MarshalPublic encodes the matching public key.
func (k *PrivateKey) MarshalPublic(comment string) []byte {
	raw := make([]byte, 2+keyIDLen+pubKeyLen)
	copy(raw[0:2], magic[:])
	copy(raw[2:2+keyIDLen], k.ID[:])
	copy(raw[2+keyIDLen:], k.Key.Public().(ed25519.PublicKey))
	return encode(comment, raw)
}

// Sign produces a detached signature over message.
func (k *PrivateKey) Sign(message []byte, comment string) []byte {
	raw := make([]byte, 2+keyIDLen+sigLen)
	copy(raw[0:2], magic[:])
	copy(raw[2:2+keyIDLen], k.ID[:])
	copy(raw[2+keyIDLen:], ed25519.Sign(k.Key, message))
	return encode(comment, raw)
}

// encode writes the two-line form usign reads: a comment, then base64.
func encode(comment string, raw []byte) []byte {
	if comment == "" {
		comment = "signed by owfeed"
	}
	return []byte(fmt.Sprintf("untrusted comment: %s\n%s\n", comment, base64.StdEncoding.EncodeToString(raw)))
}
