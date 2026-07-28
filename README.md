# owfeed

Build, sign and publish an **apk package feed for OpenWrt 25.12+** — so your users run three lines
and `apk add` just works.

> **Status: design stage. There is no code yet.**
> This repository currently contains a research-backed design document ([`docs/DESIGN.md`](docs/DESIGN.md)).
> Several claims in it are explicitly marked unverified and need prototyping before implementation.
> Do not depend on anything here yet.

## The problem

OpenWrt 25.12 replaced opkg with apk (APKv3). The index is now a binary `packages.adb`, signing moved
from usign/ed25519 to EC prime256v1, and `ipkg-make-index.sh` — the script third-party maintainers
used for a decade — no longer applies. There is no documentation for running your own feed.

The result, as of mid-2026:

- `ERROR: <file>.ipk: v2 package format error` appears in issue trackers of a dozen unrelated projects.
- `ERROR: <file>.apk: UNTRUSTED signature` appears in as many more, plus the LuCI "Upload Package"
  flow ([luci#8482](https://github.com/openwrt/luci/issues/8482), open) which cannot pass
  `--allow-untrusted` at all.
- Maintainers answer "won't convert", "build it yourself", "stay on 24.10", or nothing.
- The same question — *how do I host an apk feed?* — was asked on the forum in
  [Sep 2025](https://forum.openwrt.org/t/creating-an-apk-openwrt-repository/240519) and again in
  [Jul 2026](https://forum.openwrt.org/t/custom-feeds-package-repos-how-to-create-server/252104).
  [openwrt#16946](https://github.com/openwrt/openwrt/issues/16946), which asked for a replacement
  indexing tool, was closed without one.

Every feed that does exist is a hand-rolled GitHub Actions workflow wrapping `openwrt/gh-action-sdk`
plus a copy-pasted deploy step, with a hardcoded 36-entry architecture matrix.

## What owfeed does

A single Go binary — identical behaviour locally and in CI. Each stage takes a directory in and
leaves a directory behind, with no hidden state between them, so any of them runs on its own.

```
owfeed init             scaffold owfeed.yml and .gitignore
owfeed keygen           EC prime256v1 SEC1 keypair, correct by construction
owfeed lock             derive the architecture matrix; never hardcode it
owfeed build            SDK-less `apk mkpkg` from a staged rootfs
owfeed sign             sign every .apk, not just the index
owfeed index            fan out and build a signed packages.adb + index.json + sha256sums
owfeed doctor           numbered checks that catch the traps below before your users do
owfeed publish          gate the tree on those checks; refuses to publish a broken one
owfeed install-snippet  the instructions your subscribers follow, from one source
```

One noarch package across all 35 architectures — built, signed, indexed and laid out for
publication — takes about 25 seconds on a laptop, against 35 SDK builds today. The result installs on
`openwrt/rootfs:x86-64-25.12.4` with `apk add`, no `--allow-untrusted`.

Still to come: SDK builds, a GitHub Action and reusable workflow, Cloudflare R2 and rsync targets,
`verify` against a live URL, `smoke` inside `openwrt/rootfs`, and an SBOM.

### Things it refuses to let you get wrong

Each of these has burned a real maintainer, and each is a `doctor` gate:

| | |
|---|---|
| `arch: all` | rejected by apk — must be `noarch` |
| `-C zstd` | OpenWrt builds apk with zstd disabled; the index dies on-device |
| PKCS#8 key | `openssl genpkey -algorithm EC` produces the wrong PEM form; only SEC1 works |
| `~` in a version | only hex digits may follow it; `-r<n>` must be last |
| A feed URL that redirects | apk does not follow 30x with the stock `uclient-fetch` ([openwrt#17180](https://github.com/openwrt/openwrt/issues/17180)) |
| A missing `sysupgrade.conf` line | `/etc/apk/keys/*.pem` do **not** survive sysupgrade — the top cause of post-upgrade "UNTRUSTED signature" reports |
| A dependency that provides `wget` | swaps the user's fetcher and breaks their `apk update` entirely ([openwrt#24270](https://github.com/openwrt/openwrt/issues/24270)) |
| A README that drifted from the real URL | already live in a major feed today |
| A payload staged from a source tree | `.po` files instead of compiled `.lmo` catalogues means the package installs cleanly with no translations at all |

### Things it will tell you honestly

- **Attended Sysupgrade will not preserve your packages.** `owut` forwards no custom repositories,
  and the ASU server's `repository_allow_list` defaults to empty — which denies everything.
- **apk has no revocation.** No CRL, no expiry, no kill signal. owfeed makes rotation cheap enough to
  actually do; it does not pretend revocation exists.
- **A key in `/etc/apk/keys` is a trust anchor for everything.** If you ship one package that people
  install occasionally, loose signed artifacts may be the smaller ask. owfeed says so during `init`.
- **The SDK-less path packages a staged rootfs; it does not build one.** `apk mkpkg` turns a directory
  into a package in about a second, which is the whole point — but the directory has to be what you
  want installed. For a LuCI package that means the CSS is already built and the `.po` catalogues are
  already compiled to `.lmo`. owfeed will not compile them for you: the catalogue's basename is a
  packaging decision rather than a derivable one, and getting it wrong causes a file conflict with the
  `luci-i18n-*` package an older router still owns, which breaks the very upgrade it was meant to
  deliver. It does refuse to package the sources, so the mistake is loud rather than silent.

## Design document

[`docs/DESIGN.md`](docs/DESIGN.md) — the full design: verified invariants with sources, config schema,
CLI surface, pipeline architecture, key rotation, milestones, and the open risks that must be
prototyped first.

*Currently written in Russian; an English version will land before v0.1.*

## License

Apache-2.0.
