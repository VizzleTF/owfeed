# What is built, and what is not

*[ECOSYSTEM.md](ECOSYSTEM.md) says where the boundaries between owlab, owfeed and
owfeed-packages run and why. This file says how much of that actually exists, as of
2026-07-28. It is deliberately separate: the boundaries are meant to outlive any
particular week, this is a snapshot and will go stale.*

The honest summary: the path from a source tree to a package on somebody's router
works end to end and has been walked with real artifacts on real routers. The gaps
that remain are named below, each with the reason it is still open rather than a
promise about when it closes.

## Working, and verified rather than assumed

Everything in this section was checked by running it — against live routers, the
published feed, or released binaries — not by reading the code.

| | Evidence |
|---|---|
| `owlab build` → `owfeed release` compose | Built luci-theme-footstrap through the SDK, produced a signed 7-field manifest, verified the signature back |
| `dist/<arch>/` agreed by three implementations | owlab's `archDirs`, owfeed's `buildIPK`, the feed's `fetch.sh` — apk to `noarch/`, ipk to `all/` |
| Install from a signed feed by name | `owlab test --feed` on 25.12: package installs, LuCI page renders. With the key removed the router reports it as not existing at all |
| Feed updates reaching a router | Released updater 1.2.0 → hourly bot opened a PR → auto-merge → publish → both branches offer `1.2.0-r1` |
| Self-updater migrating off a content pin | 0.11.5 pinned by hash → `check` answers `v0.11.6` from the local index → upgrade lands and leaves the package unpinned |
| Third-party intake | Run against a real release: signature verifies, and the six-field manifest is correctly refused as the wrong format |
| Schema published and generated | `owfeed.org/schema/v1.json` is byte-identical to the checked-in copy; the drift test fails when a field is added and the annotation guard fails when a key is renamed |
| Auto-merge tier rules | Six scenarios exercised in a real git repository: manifest/minor merges, major bump holds, `binaries` holds, no `SIG_KEY` holds, a diff touching `SIG_KEY_ID` holds, the daily ceiling holds |

## Built but not yet exercised in anger

- **`owfeed release --sign-also`** works and is wired into luci-theme-footstrap's
  release job, but that job only runs on a tag and no tag has been cut since. The
  first real proof arrives with the next theme release, where the verify step will
  demand `install.sh.sig` and fail loudly if it is missing.
- **The intake funnel** answers correctly when run by hand. No third party has used
  it yet, so the first genuine request is still the first genuine test.

## Not built, and why

**A consumer job in the feed's PR pipeline.** `owfeed smoke` proves the channel
works; nothing yet proves that what came through it functions. `owlab test --feed`
exists precisely for this, so the code is not the obstacle — the address is. The
router runs in a container and has to reach an HTTP server on the runner, which is
`172.17.0.1` on GitHub's runners and something else on a WSL development machine.
That could not be verified locally, and an unverified networking step in a live
pipeline fails in front of a contributor rather than in front of us.

**A nightly cross-check between owfeed and owlab.** Both know which OpenWrt point
releases exist and neither reads the other, so the duplication is deliberate and the
drift is not. The check cannot be written yet: owfeed has no command that reports
*its* view of the newest point release. `arch.LatestPoint` is internal and
`owfeed lock` only works inside a feed repository. What can be written today
compares owlab against a `curl` to downloads.openwrt.org, which is not a cross-check
— owlab already asks that server. Closing this needs a symmetric command first, and
that is a decision about CLI surface rather than a missing workflow.

**An in-package EC signature from a package author.** `owfeed sign` no longer needs
a feed config, so the tooling is ready. Nobody uses it yet: it needs a new key and a
new repository secret, which is the maintainer's call. Until then the claim in
`CONTRIBUTING.md` that author signatures are additive — that a package carries both
its author's and the feed's — is true of the design and demonstrated by nothing.

**CODEOWNERS as a mechanism.** `keys/` is named, and no branch rule enforces the
review. With a single maintainer a required review blocks every key addition
permanently instead of gating it, because the author of a pull request cannot approve
their own. Auto-merge cannot reach `keys/` regardless — it is only ever requested on
pull requests the update job itself opened, and that job writes one `upstream.sh`.
The review becomes a mechanism on the day there is a second maintainer, and until
then it is a convention.

**`fidelity: vm` in owlab.** Declared in the config schema, four call sites return
"not implemented yet". Either it gets built or it comes out of the schema; a field
that parses and then refuses is worse than one that does not exist.

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
