# owfeed

One binary. Turns a directory into a signed apk feed for **OpenWrt 25.12+**, so your users run three
lines and `apk add` works.

A noarch package across all 35 architectures — built, signed, indexed, laid out — takes ~25 seconds.
The status quo is 35 SDK builds.

---

## I want to publish a feed

```sh
owfeed init --url https://feed.example.org
owfeed keygen -o ~/keys/myfeed.pem      # outside the repo. A published key cannot be revoked.
export OWFEED_SIGN_KEY="$(cat ~/keys/myfeed.pem)"

# edit owfeed.yml: point `files:` at a directory laid out the way it should install

owfeed lock --update                    # derives the architecture list; commit it
owfeed build && owfeed sign && owfeed index
owfeed doctor
```

`out/` is now the feed. Upload it.

## I want to ship a new version

Bump `version:` in `owfeed.yml`, then:

```sh
owfeed build && owfeed sign && owfeed index && owfeed doctor
```

## I want to add a package

Add another entry under `packages:`. Same four commands.

## I want to tell users how to install it

```sh
owfeed install-snippet             # markdown, paste into your README
owfeed install-snippet --format sh # just the commands
```

Do not write your own. `doctor` compares your README against this output, because a feed whose
documented URL 404s is a live bug in a major feed right now.

## I want this in CI

```yaml
- run: owfeed --frozen-lock build && owfeed sign && owfeed index
  env:
    OWFEED_SIGN_KEY: ${{ secrets.OWFEED_SIGN_KEY }}
- run: owfeed publish            # refuses to publish a broken tree. No override flag.
- uses: actions/upload-pages-artifact@v3
  with: { path: out }
- uses: actions/deploy-pages@v4
```

Put `publish` in a separate job with `environment:` so a fork PR cannot reach the key.

## Upstream added an architecture

```sh
owfeed lock --update    # prints the diff; commit it
```

`--frozen-lock` fails the build until you do. What your feed covers should not change without you
seeing it.

## Something broke

```sh
owfeed doctor          # numbered findings, each says why it matters and what to do
```

---

## Things that will bite you

owfeed refuses each of these. Every one has burned a real maintainer.

| | |
|---|---|
| `arch: all` | apk rejects it as uninstallable. Use `noarch`. |
| PKCS#8 key | `openssl genpkey -algorithm EC` writes the wrong PEM. Only SEC1 works. |
| `1.0~beta` | after `~` apk reads a commit hash, so only hex. Use `_beta1`. |
| A feed URL that redirects | apk does not follow 30x ([openwrt#17180](https://github.com/openwrt/openwrt/issues/17180)). |
| No `sysupgrade.conf` line | `/etc/apk/keys/*.pem` do **not** survive sysupgrade. Top cause of post-upgrade `UNTRUSTED signature`. |
| Indexing before signing | signing appends bytes, so the index no longer matches the file. |
| `/etc/config/foo` not in `conffiles:` | sysupgrade replaces the user's settings with your defaults, silently, every upgrade. |
| `.po` files in the payload | LuCI reads compiled `.lmo`. Point `i18n.from:` at them and owfeed compiles them. |
| A README that drifted | already live in a major feed today. |

## Things owfeed will not pretend

- **apk has no revocation.** No CRL, no expiry, no kill signal. owfeed makes rotation cheap; it does
  not claim revocation exists.
- **Your key is a trust anchor for every package name**, not just yours. If you ship one package
  people install occasionally, signed release artifacts are a smaller ask.
- **Attended Sysupgrade will not carry your packages across.** `owut` forwards no custom repositories
  and the ASU server's `repository_allow_list` is empty by default, which denies everything.
- **`build` packages a directory; it does not build one.** Your CSS must already be built. owfeed
  does compile your `.po` catalogues — LuCI reads `.lmo` and ignores `.po`, so that gap is silent
  and worth closing — but it will not run your build for you, and it refuses to package sources.

## Not there yet

SDK builds · GitHub Action and reusable workflow · Cloudflare R2 and rsync · `verify` against a live
URL · `smoke` inside `openwrt/rootfs` · SBOM · key rotation commands.

---

## Docs

- [Examples](docs/examples.md) — a real LuCI theme, end to end.
- [Verified apk behaviour](docs/apk-behaviour.md) — what apk actually does, with reproductions.
- [Design](docs/DESIGN.md) — the full design and the research behind it *(Russian)*.

## License

Apache-2.0.
