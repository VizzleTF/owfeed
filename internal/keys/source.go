package keys

import (
	"crypto/ecdsa"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Source is a `signing.key` value: "env:VARNAME" or "file:PATH".
type Source string

// Load reads and parses the private key a Source names.
//
// root is the directory a relative file: path resolves against.
func (s Source) Load(root string) (*ecdsa.PrivateKey, error) {
	scheme, arg, ok := strings.Cut(string(s), ":")
	if !ok || arg == "" {
		return nil, fmt.Errorf("signing key %q names no source; use env:VARNAME or file:PATH", s)
	}

	switch scheme {
	case "env":
		pem := os.Getenv(arg)
		if pem == "" {
			return nil, fmt.Errorf("$%s is empty; owfeed reads the signing key from it because "+
				"signing.key is set to %q", arg, s)
		}
		return LoadPrivate([]byte(pem))
	case "file":
		path := arg
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return LoadPrivate(b)
	default:
		return nil, fmt.Errorf("signing key source %q is not supported; use env: or file:", scheme)
	}
}

// StageDir writes a private key into its own directory, readable only by the owner,
// and returns that directory.
//
// The key never goes into the tree being published or built. That tree is one
// `git add -A` or one artifact upload away from being distributed, which is exactly
// the footgun in openwrt/gh-action-sdk: its PRIVATE_KEY input writes the secret into
// the build tree that callers routinely upload.
//
// The caller is responsible for removing the directory; owfeed does so at the end of
// every command that stages a key.
func StageDir(parent, name string, key *ecdsa.PrivateKey) (string, error) {
	dir, err := os.MkdirTemp(parent, ".owfeed-key-*")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	pem, err := MarshalPrivate(key)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, name), pem, 0o600); err != nil {
		return "", err
	}
	return dir, nil
}

// StagePublic writes a public key into its own directory for apk to trust, and
// returns that directory.
//
// It must not be the same directory as the private key: apk reads every file in a
// trust directory as a public key.
func StagePublic(parent, name string, pub *ecdsa.PublicKey) (string, error) {
	dir, err := os.MkdirTemp(parent, ".owfeed-trust-*")
	if err != nil {
		return "", err
	}
	pem, err := MarshalPublic(pub)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, name), pem, 0o644); err != nil {
		return "", err
	}
	return dir, nil
}
