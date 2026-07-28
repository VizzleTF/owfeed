# What is built in owfeed, and what is not

*[ECOSYSTEM.md](ECOSYSTEM.md) says where the boundaries between owlab, owfeed and
owfeed-packages run and why. This file says how much of owfeed's side of that
actually exists, as of 2026-07-28.*

This file used to cover all three repositories, and it went stale twice — both
times on a fact belonging to another repository, never on one of its own. Nothing
in owfeed's CI touches owlab, so nothing here could notice when owlab shipped a
feature this file called unimplemented. Each repository now keeps its own:

- **owlab** — [`docs/STATUS.md`](https://github.com/VizzleTF/owlab/blob/main/docs/STATUS.md)
- **owfeed-packages** — [`STATUS.md`](https://github.com/VizzleTF/owfeed-packages/blob/main/STATUS.md)

The boundaries are meant to outlive any particular week. A status file is a
snapshot and will go stale; keeping it beside the code that changes it is what
makes going stale a diff somebody has to write rather than something that happens
quietly.

## Working, and verified rather than assumed

Everything here was checked by running it — against live routers, the published
feed, or released binaries — not by reading the code.

| | Evidence |
|---|---|
| `owlab build` → `owfeed release` compose | Built luci-theme-footstrap through the SDK, produced a signed 7-field manifest, verified the signature back |
| `dist/<arch>/` agreed by three implementations | owlab's `archDirs`, owfeed's `buildIPK`, the feed's `fetch.sh` — apk to `noarch/`, ipk to `all/` |
| Install from a signed feed by name | `owlab test --feed` on 25.12: package installs, LuCI page renders. With the key removed the router reports it as not existing at all |
| Third-party intake | Run against a real release: signature verifies, and the six-field manifest is correctly refused as the wrong format |
| Schema published and generated | `owfeed.org/schema/v1.json` is byte-identical to the checked-in copy; the drift test fails when a field is added and the annotation guard fails when a key is renamed |
| `owfeed releases` against owlab | Both answer 25.12→25.12.5/apk and 24.10→24.10.8/ipk today; the nightly job ran green on a runner, and reports a disagreement when either answer is perturbed |
| `feed.yml` in `dry-run` mode | owfeed-packages ran it end to end: build with no key, throwaway keys, sign, index, check-tree, doctor, smoke on both lines, a tree digest, publish job skipped |

## Built but not yet exercised in anger

- **`owfeed release --sign-also`** works and is wired into luci-theme-footstrap's
  release job, but that job only runs on a tag and no tag has been cut since. The
  first real proof arrives with the next theme release, where the verify step will
  demand `install.sh.sig` and fail loudly if it is missing.
- **The intake funnel** answers correctly when run by hand. No third party has used
  it yet, so the first genuine request is still the first genuine test.
- **`feed.yml`'s `published` and `page-url` outputs.** `digest` is confirmed; the
  other two only ever have a value on the publish path, which nothing calls yet.
- **Reading the signing key from the environment** in `feed.yml`'s publish job.
  GitHub's documentation is explicit that a called job declaring `environment:`
  uses that environment's secret rather than a passed-in one, and the job now
  reads `secrets.OWFEED_SIGN_KEY` with the `sign-key` input as fallback — so an
  existing caller is unaffected either way. What one live run still has to settle
  is *whose* environment resolves, the caller's or owfeed's, since the run belongs
  to the caller and no such environment exists in owfeed. Nothing calls the publish
  half yet: `owfeed-packages` moved its pull-request check across and kept its
  hand-written `publish.yml`, so the question gets answered by a deliberate run
  rather than by a feed that stopped publishing.

  The check half being green is not evidence either way — it holds no secret and
  declares no environment. What the first run of it did prove is the value of
  running one: two defects in v0.1.6 that no amount of reading had found, both
  fixed in v0.1.7.

## Not built, and why

**An in-package EC signature from a package author.** `owfeed sign` no longer needs
a feed config, so the tooling is ready. Nobody uses it yet: it needs a new key and a
new repository secret, which is the maintainer's call. Until then the claim in
`CONTRIBUTING.md` that author signatures are additive — that a package carries both
its author's and the feed's — is true of the design and demonstrated by nothing.

**A consumer job in a feed's PR pipeline.** `owfeed smoke` proves the channel
works; nothing yet proves that what came through it functions. The obstacle used
to be the address — a router in a container has to reach an HTTP server on the
runner, and no literal is right on a CI runner, under Docker Desktop and in a VM
at once. owlab now takes `{host}` in a `--feed` URL and substitutes the answer per
tier, so the obstacle is gone and what remains is sequencing: it needs an owlab
release carrying that, and then the job belongs in the feed's own repository
rather than here.

## Known contradictions

One key, `vizzletf-release.pub`, covers two repositories while `keys/README.md` asks
for one key per upstream repository. `owfeed` checks the manifest's `repo` line, so a
manifest cannot be lifted between them and the shared key is tolerable — what
separate keys would buy is blast radius, not correctness. The doctrine now says this
out loud instead of being quietly contradicted by the table beneath it.

## Where to look

- [ECOSYSTEM.md](ECOSYSTEM.md) — the boundaries, the invariants, the contracts
- [artifact-contract.md](artifact-contract.md) · [manifest-format.md](manifest-format.md) — the two formats that cross those boundaries
- [the cookbook](https://owfeed.org/cookbook/) — the same path written for somebody
  who has not read any of the above *([по-русски](https://owfeed.org/cookbook/ru/))*
