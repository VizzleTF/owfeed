package apk

// Pinned trust anchors for verifying an OpenWrt SDK download.
//
// The chain owfeed relies on is deliberately not self-referential:
//
//	downloads.openwrt.org serves the SDK tarball AND the sha256sums beside it.
//	Whoever can replace one can replace the other, so a checksum from that host
//	proves nothing on its own. What makes it mean something is the ed25519
//	signature over sha256sums, checked with a public key fetched from a
//	DIFFERENT host (github.com/openwrt/keyring) at a pinned commit, whose own
//	content hash is pinned here, in the binary.
//
// The key id embedded in a signature is only an addressing convenience: keyring
// names its files by key id, so the id tells us which file to fetch. It is not
// evidence — an attacker who can substitute the signature can put any id in it.
// Trust comes from the id being in trustedKeys below and the fetched bytes
// hashing to the value recorded there.
//
// When OpenWrt rotates a branch key, verification fails with a key-id mismatch
// rather than a bad signature, and the fix is to add the new id here after
// checking it out of band. That is intended: a rotation and an attack are
// indistinguishable to the verifier, so a human has to look.

// keyringCommit is the openwrt/keyring revision the pins below were taken from.
const keyringCommit = "fbae29d730f81c892f52e0ff00fe867444aeeae6"

// keyringURLTemplate is the raw-content host. Deliberately not
// downloads.openwrt.org: the point of the second fetch is that it is a second
// origin.
const keyringURLTemplate = "https://raw.githubusercontent.com/openwrt/keyring/%s/usign/%s"

// trustedKeys maps a usign key id to the SHA-256 of its key file at keyringCommit.
//
// Recorded 2026-07-27. Comments are the keys' own "untrusted comment" lines,
// which is how OpenWrt labels what each key is for.
var trustedKeys = map[string]string{
	// Signs SNAPSHOT and, as of 25.12, the release target directories.
	"b5043e70f9a75cde": "d7ac10f9ed1b38033855f3d27c9327d558444fca804c685b17d9dcfb0648228f",
	// 24.10 release builds. Kept because the ipk line will need it.
	"d310c6f2833e97f7": "e3624aa9be785362a172595b4919b233268871c4365a9b8da2b42ac41745ad95",
	// 21.02 release builds.
	"2f8b0b98e08306bf": "d102bdd75421c62490b97f520f9db06aadb44ad408b244755d26e96ea5cd3b7f",
	// Jo-Philipp Wich, personal key; signs some artifacts directly.
	"72a57f2191b211e0": "f258219913c4952895770bc753f9c15214edae3b2b330b9aee7060896cd28e4e",
}

// sdkMembers is the complete set of tar members needed to run the SDK's host apk.
//
// The naive extraction is all of staging_dir/host/lib, which is 143 MB. The
// transitive DT_NEEDED closure of .apk.bin is three libraries totalling 2.1 MB,
// so this set is six files and 3.9 MB. See docs/apk-behaviour.md.
//
// bin/apk is the bash wrapper. owfeed does not use it — it invokes the loader
// directly, which drops the bash dependency — but it is 224 bytes and keeping it
// means the cache directory is a faithful, hand-runnable copy of what the SDK
// shipped.
var sdkMembers = []string{
	"staging_dir/host/bin/apk",
	"staging_dir/host/bin/.apk.bin",
	"staging_dir/host/lib/ld-linux-x86-64.so.2",
	"staging_dir/host/lib/libc.so.6",
	"staging_dir/host/lib/libpthread.so.0",
	"staging_dir/host/lib/runas.so",
}

// loaderPath and binPath are where the pieces land inside a cache directory,
// relative to its root.
const (
	loaderPath = "staging_dir/host/lib/ld-linux-x86-64.so.2"
	libDirPath = "staging_dir/host/lib"
	binPath    = "staging_dir/host/bin/.apk.bin"
)

// sdkTarget is the target whose SDK we pull host tools from. Any target's SDK
// ships the same host apk, so this is arbitrary; x86/64 is the smallest and the
// most reliably mirrored.
const sdkTarget = "x86/64"
