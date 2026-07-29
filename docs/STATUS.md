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
| `feed.yml` publishing | owfeed-packages publishes through it now. Signed with the `feed` environment's own keys, gated, deployed, and the live feed still serves three packages on both lines |
| A called job's `environment:` resolves against the **caller** | Probed: an environment variable set to a different value in each repository returned the caller's, and the caller's environment-scoped signing secrets reached the called job at their real lengths |
| The report contract, consumed | luci-theme-footstrap installs the published theme BY NAME out of the signed index and asserts its pages render — the whole path, from a source tree to a router, checked by CI rather than by hand |

## Built but not yet exercised in anger

- **`owfeed release --sign-also`** works and is wired into luci-theme-footstrap's
  release job, but that job only runs on a tag and no tag has been cut since. The
  first real proof arrives with the next theme release, where the verify step will
  demand `install.sh.sig` and fail loudly if it is missing.
- **The intake funnel** answers correctly when run by hand. No third party has used
  it yet, so the first genuine request is still the first genuine test.
- **`feed.yml`'s `published` and `page-url` outputs.** Both now have a value on
  every publish; nothing reads them yet, which is the part still untested.

## Not built, and why

**An in-package EC signature from a package author.** `owfeed sign` no longer needs
a feed config, so the tooling is ready. Nobody uses it yet: it needs a new key and a
new repository secret, which is the maintainer's call. Until then the claim in
`CONTRIBUTING.md` that author signatures are additive — that a package carries both
its author's and the feed's — is true of the design and demonstrated by nothing.

**A consumer job inside `feed.yml` itself.** The claim is now proved, but from the
package side: luci-theme-footstrap installs the published theme by name and
asserts its pages render. What that does not cover is a feed's own pull request —
the tree being built there is not published yet, so proving it works means serving
`out/` over HTTP from the runner and pointing owlab at it. `{host}` in a `--feed`
URL exists for exactly that. Whether it belongs here as a `consumer-test:` input
or in each feed's own workflow is a question about a public interface, and there
is one consumer of it so far. Contracts belong to whoever has more than one.

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
