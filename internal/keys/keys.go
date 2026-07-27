// Package keys generates and loads the EC keys that sign an OpenWrt apk feed.
//
// OpenWrt's own build system creates its signing key with
//
//	openssl ecparam -name prime256v1 -genkey -noout -out private-key.pem
//	openssl ec -in private-key.pem -pubout -out public-key.pem
//
// and apk verifies against exactly that. Two details in there are load-bearing and both
// have burned maintainers in the wild:
//
//   - `ecparam -genkey` emits the SEC1 form ("BEGIN EC PRIVATE KEY"). The obvious modern
//     alternative, `openssl genpkey -algorithm EC`, emits PKCS#8 ("BEGIN PRIVATE KEY"),
//     and apk will not load it. We only ever write SEC1, and we reject PKCS#8 on read
//     with an error that names the fix.
//   - The curve must be prime256v1 (NIST P-256). Nothing else is accepted.
package keys

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// pemTypePrivateSEC1 is what `openssl ecparam -genkey` writes, and the only private
	// key form apk loads.
	pemTypePrivateSEC1 = "EC PRIVATE KEY"
	// pemTypePrivatePKCS8 is what `openssl genpkey -algorithm EC` writes. Recognised
	// solely so the error can say what went wrong.
	pemTypePrivatePKCS8 = "PRIVATE KEY"
	pemTypePublic       = "PUBLIC KEY"
)

var (
	ErrPKCS8     = errors.New("keys: private key is in PKCS#8 form, apk requires SEC1")
	ErrCurve     = errors.New("keys: not a prime256v1 (P-256) key")
	ErrPEM       = errors.New("keys: not a PEM file")
	ErrPEMType   = errors.New("keys: unexpected PEM block type")
	ErrNotECDSA  = errors.New("keys: not an ECDSA key")
	ErrKeyExists = errors.New("keys: refusing to overwrite an existing key")
)

// Identity is how apk names a key: the first 16 bytes of SHA-512 over the public key.
//
// "Over the public key" is ambiguous and the ambiguity matters. apk hashes the output of
// OpenSSL's i2d_PublicKey, which for an EC key is the *uncompressed point* (0x04‖X‖Y,
// 65 bytes for P-256) — not the SubjectPublicKeyInfo structure that i2d_PUBKEY and
// `openssl ec -pubout -outform DER` produce. Hashing the SPKI instead yields a
// completely different, silently wrong identity.
//
// Verified against apk-tools 3.0.5: for a test key, apk recorded 364b7138efaa7f7f862f62fd04099d96
// in the index signature block, which is SHA-512(point)[:16]; SHA-512(SPKI)[:16] was
// a406a2ccc40af6dbf8e44f7081f716d7.
type Identity [16]byte

func (id Identity) String() string { return fmt.Sprintf("%x", id[:]) }

// IdentityOf computes the apk key identity for a public key.
func IdentityOf(pub *ecdsa.PublicKey) (Identity, error) {
	var id Identity
	if pub.Curve != elliptic.P256() {
		return id, ErrCurve
	}
	// elliptic.Marshal is deprecated in favour of ecdh, but it is the only stdlib call
	// that produces exactly the 0x04‖X‖Y encoding apk hashes, and the encoding is frozen
	// by apk's on-disk format rather than by us.
	point := elliptic.Marshal(pub.Curve, pub.X, pub.Y) //nolint:staticcheck // SEC1 point is the wire format apk hashes
	sum := sha512.Sum512(point)
	copy(id[:], sum[:len(id)])
	return id, nil
}

// Generate creates a new prime256v1 key.
func Generate() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// MarshalPrivate encodes a key in the SEC1 PEM form apk accepts.
func MarshalPrivate(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemTypePrivateSEC1, Bytes: der}), nil
}

// MarshalPublic encodes the public half as SPKI PEM, matching `openssl ec -pubout`.
// This is the file users install into /etc/apk/keys.
func MarshalPublic(pub *ecdsa.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemTypePublic, Bytes: der}), nil
}

// LoadPrivate parses a private key and rejects the forms apk cannot use.
//
// The PKCS#8 case is called out by name because the failure it causes downstream is
// opaque: apk reports "cryptographic key format not recognized" and exits 99, which does
// not suggest that the key needs regenerating with a different openssl subcommand.
func LoadPrivate(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, ErrPEM
	}
	switch block.Type {
	case pemTypePrivateSEC1:
	case pemTypePrivatePKCS8:
		return nil, ErrPKCS8
	default:
		return nil, fmt.Errorf("%w: %q", ErrPEMType, block.Type)
	}

	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	if key.Curve != elliptic.P256() {
		return nil, ErrCurve
	}
	return key, nil
}

// LoadPublic parses an SPKI PEM public key.
func LoadPublic(pemBytes []byte) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, ErrPEM
	}
	if block.Type != pemTypePublic {
		return nil, fmt.Errorf("%w: %q", ErrPEMType, block.Type)
	}
	any, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	pub, ok := any.(*ecdsa.PublicKey)
	if !ok {
		return nil, ErrNotECDSA
	}
	if pub.Curve != elliptic.P256() {
		return nil, ErrCurve
	}
	return pub, nil
}

// WritePrivate writes a private key 0600, creating parent directories as needed.
// It refuses to clobber an existing file: a signing key that gets silently replaced
// makes every already-published index unverifiable by subscribers.
func WritePrivate(path string, key *ecdsa.PrivateKey) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%w: %s", ErrKeyExists, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	pemBytes, err := MarshalPrivate(key)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, pemBytes, 0o600)
}
