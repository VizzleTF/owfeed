# Verified apk-tools behaviour

Facts established by running the real thing, not by reading documentation. Every claim
here has a reproduction below it. Anything not reproduced is not in this file.

**Environment:** apk-tools 3.0.5 extracted from `openwrt-sdk-25.12.5-x86-64_gcc-14.3.0_musl.Linux-x86_64.tar.zst`,
run under `linux/amd64`. Date: 2026-07-27.

Throughout, `$APK` is the loader invocation from
[Extracting a host apk](#extracting-a-host-apk-from-the-sdk):

```sh
APK="/opt/apk/lib/ld-linux-x86-64.so.2 --library-path /opt/apk/lib /opt/apk/bin/.apk.bin"
```

---

## `mkndx` accepts repeated `--sign-key`

**This unblocks key rotation.** An index can carry several signatures, so during a
rotation window both the old and the new key can sign the same `packages.adb` and
subscribers with either key installed keep working. No two-index dance, no URL flip.

```sh
$APK mkndx --allow-untrusted --sign-key a.pem --sign-key b.pem --output two.adb ./h.apk
$APK adbdump two.adb | grep -c '^# sig'      # => 2
```

Dump excerpt:

```
# sig v00 h04 364b7138efaa7f7f862f62fd04099d96...: UNTRUSTED signature
# sig v00 h04 a4e7527837b5ee992d0619a1c3c27aff...: UNTRUSTED signature
```

(`UNTRUSTED` here only means neither key is in this container's trust directory; it is
the expected output when dumping a third-party index without installing its key.)

## `adbsign` reports failure on stdout and still exits 0

**Treat `adbsign`'s exit status as carrying no information.** It printed an error, did
nothing, and returned success:

```sh
$APK mkndx --allow-untrusted --sign-key a.pem --output x.adb ./h.apk
$APK adbsign --sign-key b.pem x.adb
#   ERROR: x.adb: UNTRUSTED signature
#   exit=0
#   md5 before=1499aca8 after=1499aca8  -> UNCHANGED
```

A pipeline written as `adbsign … || die` ships a silently unsigned or half-signed index.
Verify by re-reading the artifact and counting signature blocks, never by `$?`.

The cause is that `adbsign` reads the existing index through the normal trust path, so
signing an index whose own signatures are not trusted locally requires the flag:

```sh
$APK adbsign --allow-untrusted --sign-key b.pem y.adb
#   exit=0
#   md5 before=440c1a68 after=8c464210  -> CHANGED
#   signature blocks: 2
```

## An unsigned package is "untrusted", so signing one needs `--allow-untrusted`

There is no bootstrap from unsigned. Neither `adbsign` nor `mkndx` will touch a
package that carries no signature at all, and the diagnostic says `UNTRUSTED
signature` for a file that has none:

```sh
$APK mkpkg --info name:h --info version:1.0-r1 --info arch:noarch --files root --output h.apk
$APK adbsign --sign-key a.pem h.apk
#   ERROR: h.apk: UNTRUSTED signature
#   exit=0, signature count still 0

$APK mkndx --sign-key a.pem --output i.adb ./h.apk
#   ERROR: ./h.apk: UNTRUSTED signature
#   ERROR: 1 errors, not creating index
#   exit=99
```

This is why `--allow-untrusted` cannot be designed away entirely. It belongs on the
signing step and nowhere else, because that is the one place the input is a file
produced seconds earlier by the same run.

## `--keys-dir` is honoured only when the path is absolute

**A relative `--keys-dir` loads nothing and says nothing.** The failure looks
identical to a signature that genuinely does not verify:

```sh
$APK --keys-dir pub       mkndx --sign-key a.pem -o i.adb ./h.apk   # ERROR: UNTRUSTED signature, exit=99
$APK --keys-dir /work/pub mkndx --sign-key a.pem -o i.adb ./h.apk   # Index has 1 packages
```

With the absolute form, building an index is a real verification of every package
signature it ingests — which is what makes `adbsign`'s useless exit status
survivable: if signing silently did nothing, indexing fails.

## `adbsign` appends signatures rather than replacing them

Signing twice with different keys leaves both blocks in place, on packages as well
as on indexes:

```sh
$APK --allow-untrusted adbsign --sign-key a.pem h.apk
$APK --allow-untrusted adbsign --sign-key b.pem h.apk
$APK adbdump h.apk | grep -c '^# sig'    # => 2
```

## The index records each package's `file-size`, so sign before indexing

```
packages: # 1 items
  - name: h
    version: 1.0-r1
    hashes: 6cd348e1587b108cda4a141d8e35815ddb42cfdf3f11aff5b0bd9cf5b762b6f6
    arch: noarch
    installed-size: 2
    file-size: 265
```

Signing appends bytes to the `.apk`, so a package signed after it was indexed no
longer matches its own index entry. The order is not a preference.

The index carries no file *names*: apk derives the download name from the package
name and version, so the files have to sit flat beside `packages.adb` under exactly
`<name>-<version>.apk`.

## `mkndx` does fail loudly on an unusable key

Unlike `adbsign`, this one is trustworthy — exit 99, and no output file is left behind:

```sh
echo garbage > bad.pem
$APK mkndx --allow-untrusted --sign-key bad.pem --output z.adb ./h.apk
#   ERROR: Failed to load signing key: bad.pem: cryptographic key format not recognized
#   exit=99
#   z.adb not created
```

## Index magic is `ADBd`

Default compression is deflate, which is the only thing OpenWrt's on-device apk can
read (it is built `-Dzstd=disabled`):

```sh
head -c 4 one.adb | od -c | head -1
#   0000000   A   D   B   d
```

## `installed-size` and `file-size` are computed, not supplied

They appear in the index without being passed to `--info` (and `--info` rejects them):

```
    installed-size: 3
    file-size: 239
```

## `mkpkg` records unknown file owners as `nobody`, not as root

**This is the most consequential finding here, because it is silent.** `mkpkg` stores
each file's owner *by name*, resolving the numeric uid through the passwd file under
apk's root. An id with no entry there does not fall back to root:

```c
/* src/io.c, apk_id_cache_resolve_user */
if (ci) return APK_BLOB_PTR_LEN(ci->name, ci->len);
if (uid == 0) return APK_BLOB_STRLIT("root");
return APK_BLOB_STRLIT("nobody");
```

So a package built by an ordinary user records every file as `nobody:nobody`, and the
router chowns them that way on install. Nothing warns, at build time or at install time.

```sh
chown -R 1001:1001 root/
$APK mkpkg --info name:d --info version:1.0-r1 --info arch:noarch --files root --output plain.apk
$APK adbdump plain.apk | grep -E 'user|group' | sort -u
#   group: nobody
#   user: nobody
```

OpenWrt does not hit this because `package-pack.mk` runs `mkpkg` under `$(FAKEROOT)`.
`runas.so`, which the SDK's apk wrapper sets as `LD_PRELOAD`, is **not** a fakeroot: its
only exported behaviour is rewriting `argv[0]` from `RUNAS_ARG0`.

The fix needs no extra tooling. Point apk's `--root` at a directory whose `etc/passwd`
calls the building uid `root`:

```sh
mkdir -p idroot/etc
printf 'root:x:1001:1001:root:/root:/bin/sh\n' > idroot/etc/passwd
printf 'root:x:1001:\n'                        > idroot/etc/group
$APK --root idroot mkpkg --info name:d --info version:1.0-r1 --info arch:noarch \
     --files root --output rooted.apk
$APK adbdump rooted.apk | grep -E 'user|group' | sort -u
#   group: root
#   user: root
```

`--files` and `--output` stay relative to the working directory; `--root` affects only
id resolution here, and apk writes nothing into that directory.

## `mkpkg` records extended attributes by default

`--xattrs` defaults to on, which on macOS ships the build host's residue to routers:

```
  - name: etc
    acl:
      mode: 0755
      user: root
      group: root
      xattrs: # 1 items
        - com.apple.provenance=01020013ed3d264d9d6efa
```

`com.apple.provenance` is stamped by macOS on downloaded files. Besides being
meaningless on OpenWrt, it makes the same inputs produce different package bytes on
macOS and on Linux. `--no-xattrs` removes them.

## `mkpkg` does not stamp a build time

Nothing in the output varies between two runs over identical inputs — there is no
`build-time` field unless one is passed. The only source of non-determinism left is the
payload's mtimes, which `mkpkg` does record, so normalising those is enough to make a
build reproducible.

---

## Extracting a host apk from the SDK

The SDK ships `staging_dir/host/bin/apk` as a **bash** wrapper around a hidden real
binary, running it under a bundled glibc loader:

```sh
$ cat staging_dir/host/bin/apk
#!/usr/bin/env bash
dir="$(dirname "$0")"
export RUNAS_ARG0="$0"
export LD_PRELOAD="${LD_PRELOAD:+$LD_PRELOAD:}$dir/../lib/runas.so"
exec "$dir/../lib/ld-linux-x86-64.so.2" --library-path "$dir/../lib/" "$dir/.apk.bin" "$@"

$ file staging_dir/host/bin/.apk.bin
ELF 64-bit LSB pie executable, x86-64, dynamically linked,
interpreter /lib64/ld-linux-x86-64.so.2, stripped
```

### You do not need all of `staging_dir/host/lib`

That directory is **143 MB**. The transitive `DT_NEEDED` closure of `.apk.bin` is three
libraries totalling 2.1 MB:

```
ld-linux-x86-64.so.2   173 KB
libc.so.6             1856 KB
libpthread.so.0        115 KB
```

So the complete extraction set is six files, **3.9 MB**:

```
staging_dir/host/bin/apk                    (wrapper, 224 B)
staging_dir/host/bin/.apk.bin               (1.9 MB)
staging_dir/host/lib/ld-linux-x86-64.so.2
staging_dir/host/lib/libc.so.6
staging_dir/host/lib/libpthread.so.0
staging_dir/host/lib/runas.so               (14 KB, LD_PRELOAD from the wrapper)
```

Extracted straight from the network without ever storing the 285 MB tarball:

```sh
curl -fsS "$BASE/$SDK" | zstd -dc | tar -x -C ext --strip-components=1 \
  '*/staging_dir/host/bin/apk'   '*/staging_dir/host/bin/.apk.bin' \
  '*/staging_dir/host/lib'
```

### Neither bash nor glibc is required to run it

Invoking the loader directly bypasses the wrapper, so `bash` is not needed. Because the
loader and libc are bundled, the host libc is irrelevant — it runs unmodified on a
musl-only Alpine image:

```sh
$ docker run --rm --platform linux/amd64 -v "$PWD/min":/opt/apk:ro alpine:3 \
    /opt/apk/lib/ld-linux-x86-64.so.2 --library-path /opt/apk/lib \
    /opt/apk/bin/.apk.bin --version
apk-tools 3.0.5, compiled for x86_64.
```

`LD_PRELOAD=runas.so` is also not required for `mkndx`/`mkpkg`/`adbdump` to run, and it
is not a fakeroot — its only exported symbols are `getenv`/`unsetenv` and the string
`RUNAS_ARG0`, i.e. it rewrites `argv[0]` and nothing else. Ownership is handled the way
described above, under `--root`. It is kept in the extraction set anyway, at 14 KB, so
the SDK's own wrapper script still works if anyone invokes it.

### macOS needs a container

`.apk.bin` is a Linux x86-64 ELF. On darwin there is nothing to run it with, so the
SDK-extraction path requires Docker there. It works under Docker Desktop's amd64
emulation on Apple silicon (slowly). This is the concrete form of the design's open
question about whether owfeed can be container-free on macOS: for the SDK route, no.

---

## Signature verification of the SDK itself

`sha256sums.sig` is a usign (signify-style ed25519) detached signature. The key id is in
the signature, and `github.com/openwrt/keyring/usign/` names its key files by that same
id, so the key is addressable directly from the signature:

```sh
$ base64 -d <<< "$(tail -1 sha256sums.sig)" | xxd | head -1
# magic "Ed", then 8-byte key id b5043e70f9a75cde, then a 64-byte signature
```

```
$ sha256sum b5043e70f9a75cde
d7ac10f9ed1b38033855f3d27c9327d558444fca804c685b17d9dcfb0648228f
```

Reading the key id out of the signature is a convenience for *locating* the key, never
for deciding whether to trust it — an attacker who can replace the signature can put any
key id in it. The trusted set of key ids and their hashes has to be pinned independently.

`internal/usign` verifies this natively, so no C toolchain is on the path; the test in
that package verifies a real 25.12.5 release artifact.
