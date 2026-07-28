# The ecosystem: owlab, owfeed, owfeed-packages

*Four repositories, one lifecycle. This document is the shared part: where the boundary
between them runs, what crosses it, and what each one promises never to do. It lives here
because contracts belong to whoever has more than one consumer, and every contract below
has owfeed on one side of it.*

Nothing here describes a dependency between the tools. There is no shared Go module and
there is not going to be one. What follows are file formats and CLI surfaces — the only
things two independently released binaries can agree on without having to be released
together.

## One line each

> **owlab** — does the package work.
> **owfeed** — can anyone trust these bytes.
> **owfeed-packages** — who exactly do we trust, and what follows from that.
> **luci-theme-footstrap** — what it looks like when all three are used as intended.

## Two axes

The organising question is not "which stage is this" but "whose key is in this
environment". Trust is the normative axis: invariants and CI gates are written against it,
because "there is no key in this job" is mechanically checkable and "this is the release
stage" is not. Lifecycle is the teaching axis: documentation and reading order follow it.

One axis alone does not work. Along lifecycle, owfeed is smeared across two stages —
`owfeed release` is the author's side, `owfeed sign` and `owfeed index` are the feed's, and
both are the same binary. Any rule of the form "this stage belongs to this repository"
produces an exception immediately.

|  | **T0 — no keys** | **T1 — author's key** | **T2 — feed's key** |
|---|---|---|---|
| **L1 inner loop** | `owlab up/sync/shell/exec/install`, `owlab build`, `owlab test` | — | — |
| **L2 release** (author's repo) | `owlab build` in CI, `owlab/action` on PRs | `owfeed sign` (EC, in-package), `owfeed release` (usign, manifest) | — |
| **L3 distribution** (the feed) | ingest scripts, `owfeed verify-artifact` (reads `keys/*.pub`) | — | `owfeed sign`, `owfeed index`, `owfeed publish` |
| **L4 consumption** | — | optional: a subscriber adds the author's key | `/etc/apk/keys/<feed>.pem` |

Three things follow, and they are the reason for the table:

* **T0 spans two stages.** owlab is the only inhabitant of the column with no keys, in
  development and in CI alike. That is its definition, not a gap in it.
* **owfeed sits in T1 and T2, in different repositories.** The boundary is drawn by whose
  key is in the environment, not by which binary is running. Hence the gate in §Keys: a
  workflow that can see both keys at once is a bug.
* **The T1→T2 transition exists in exactly one place** — `owfeed verify-artifact` against
  `keys/*.pub`. The whole third-party intake funnel is a procedure for adding a line to
  that directory.

```
        T0 ─ no keys ─────────┬─ T1 ─ author's key ┬─ T2 ─ feed's key ── consumer
                              │                    │
 owlab up/sync/test/build ────┤                    │
   "does the package work"    │                    │
                              │                    │
              owfeed sign ────┤                    │
              owfeed release ─┘ manifest.txt ──────┤
                "who signed this"                  │
                                                   │
        owfeed-packages: verify-artifact ──────────┤
                "we decided to trust this key"     │
                                                   │
                        owfeed sign/index/publish ─┘
                          "the feed vouches for the channel"
```

## Invariants

**owlab never:**

* stores, reads or creates a cryptographic key;
* generates a feed index or publishes anything outward;
* verifies a package signature — it installs with `--allow-untrusted`, because trust is
  not what it is checking;
* depends on owfeed, in `go.mod` or in a workflow.

**owfeed never:**

* compiles sources (see [Compilation](#compilation-belongs-to-owlab) — a decision, not a gap);
* logs into LuCI, renders a page, or asserts anything about how a package behaves;
* uploads: `owfeed publish` is a gate, and `actions/deploy-pages` does the upload;
* takes a key into a job that runs code from a pull request;
* depends on owlab in `go.mod`. At the workflow level, see [Dogfooding](#dogfooding).

**owfeed-packages never:**

* builds or rebuilds a package;
* accepts a package whose key is not already in `keys/` at ingest time;
* auto-merges a diff touching `keys/`, `tools/`, `.github/` or `owfeed.yml`;
* holds a write token for somebody else's repository, or hands its own key to anyone.

**luci-theme-footstrap never:**

* reimplements what owlab and owfeed already do. It is a worked example, not a showcase
  for hand-rolled CI.

## Contested capabilities

### Compilation belongs to owlab

`owlab build` compiles through the OpenWrt SDK. `owfeed` does not, and `build: sdk` is
**rejected by design** rather than unimplemented.

The reasons, strongest first:

1. **The feature has no consumer.** The flagship feed keeps `packages:` deliberately empty
   and its doctrine forbids building. Implementing `build: sdk` means writing owfeed's
   hardest component for a scenario its own reference feed prohibits.
2. **It drags the whole target table with it.** owlab already carries one — architectures,
   distro tables, `arch → feed path`, SDK tags. Duplicating that is the most expensive
   duplication available, because the table moves with every OpenWrt release.
3. **Compilation is a verification concern, not a distribution one.** `luci.mk` minifies JS
   and CSS on the way into the package, and "works unminified, breaks minified" is
   invisible until somebody builds the package. That is an argument from testing.
4. **`mkpkg` stays and does not contradict this.** Staging a finished tree is packaging,
   not compiling.

> **owfeed packages, it does not compile.**

The price, stated honestly: an author who does not want Docker or owlab has no build path
inside owfeed. They can use `openwrt/gh-action-sdk` or their own SDK invocation — the only
obligation is to leave the bytes in `dist/<arch>/`. The artifact contract is the interface.

### `owlab test` vs `owfeed smoke`

> **owlab asserts about the package. owfeed asserts about the channel.**

| | `owlab test` | `owfeed smoke` |
|---|---|---|
| Question | does the package work on this release | does the feed install without `--allow-untrusted` |
| Install | a local file, `--allow-untrusted` | from the feed, through the index, trust required |
| Assertions | `http`/`service`/`file`/`package`/`uci`/`exec`, LuCI login, traceback markers | `apk add <name>` returned, and `--allow-untrusted` was never said |
| Matrix | N routers × M releases × fixtures | one architecture × release line |
| Report | JSON `owlab.test/v1` | exit code |
| Keys | none | the feed's public half |

Derived invariants:

* **`owfeed smoke` never asserts HTTP against a LuCI page.** The moment it does it becomes
  a second owlab and has to carry fixtures, ssh and a login.
* **`owlab test` never asserts anything about signatures.** The moment it does it needs
  keys, and the T0 column collapses.

owfeed keeps its own Docker runner. Sharing owlab's would give owfeed's core gate a hard
dependency on a development tool, and `owfeed smoke` is a `docker run` against
`openwrt/rootfs:*` with no fixtures, no ssh and no LuCI session — cheaper than the
dependency. It also has to work for somebody who has never heard of owlab.

That leaves one hole: smoke proves the channel works, and says nothing about what arrived
through it. A feed's PR pipeline closes it with an `owlab/action` consumer job — a soft
dependency at the workflow level, described under [Dogfooding](#dogfooding).

### Release and architecture tables

Three sources, three different questions, and one genuine overlap: the rule that 25.12 and
later means apk, and resolving the newest point release in a line.

| Question | Owner | Contract |
|---|---|---|
| which architectures does release X have | **owfeed** | `owfeed.lock` — committed, `--frozen-lock` |
| how do I build or run architecture X | **owlab** | internal target table; outward, the `--arch` flag |
| which releases exist, and which are apk | **owlab** | `owlab releases --json` |

The defence against drift is not a shared library but a nightly cross-check that runs both
and fails when they disagree about the set of point releases or about apk vs opkg. The
duplication stays; it becomes observable.

`owfeed.lock` is the ecosystem's fact file. owlab may read one lying beside it. owlab never
writes one.

### The setup action and attestation verification

owlab and owfeed each ship their own `setup` action, and each verifies its release binary
with `gh attestation verify --signer-workflow` before the binary reaches `PATH`. These are
two copies of the same forty lines and they stay two copies.

Factoring them into a fourth repository would add a hop to the trust chain of the code
whose only job is checking the trust chain, and one compromise would then reach both
tools. Static, security-critical, rarely-changing code is exactly where duplication is
correct.

What is shared is the convention, not the code:

* the same input names — `version`, `verify`, `token`;
* the same behaviour on `version: latest` — a warning, not a refusal;
* the same verification shape — `--signer-workflow <owner>/<repo>/.github/workflows/release.yml`,
  never `--repo` alone;
* the same log output, naming what was verified.

### Signing, indexing and publishing

> **Everything involving a cryptographic signature, index generation, or the publish gate
> belongs to owfeed. Neither owlab, nor a feed's ingest scripts, nor an author's CI
> reimplements any of it.**

## Contracts

Five of them. Each is a file or a CLI surface; none is a shared Go package.

| Contract | Spec | Written by | Read by |
|---|---|---|---|
| artifact tree | [artifact-contract.md](artifact-contract.md) | `owlab build`, `owfeed build`, any SDK invocation | `owfeed sign`, `owfeed release`, `owfeed index`, feed ingest |
| release manifest | [manifest-format.md](manifest-format.md) | `owfeed release` | `owfeed verify-artifact`, feed ingest |
| test report | `owlab.test/v1` (owlab) | `owlab test --json`, `owlab/action` | an author's CI today; a feed-side consumer gate once it exists |
| keys | below | — | — |
| config | below | — | — |

The report contract is the one with a consumer still missing. An author's CI already fails
on it, but the feed-side use — install from the freshly built index and assert the page
renders — needs `owlab` to install from a feed rather than a file, and neither `owlab
install --feed` nor a `feed:` input on `owlab/action` exists yet. Until then `owfeed smoke`
proves the channel works and nothing proves what arrived through it.

### Keys

| Stage | Key | Scheme | Env / file | Where the secret lives |
|---|---|---|---|---|
| `owlab build` / `test` | — | — | — | nowhere; installs `--allow-untrusted` |
| author: `owfeed sign` | author's package key | EC prime256v1, SEC1 PEM | `OWFEED_SIGN_KEY` | author's repo secret |
| author: `owfeed release` | author's release key | usign ed25519 | `OWFEED_RELEASE_KEY` | author's repo secret |
| feed: `owfeed verify-artifact` | author's **public** half | usign pub | `keys/<repo>.pub`, pinned by key id | committed to git |
| feed: `owfeed sign` | feed's package key | EC prime256v1 | `OWFEED_SIGN_KEY` | `feed` environment |
| feed: `owfeed index` (opkg line) | feed's index key | usign ed25519 | `OWFEED_USIGN_KEY` | `feed` environment |
| consumer | feed's public half | via `owfeed install-snippet` | `/etc/apk/keys/<feed>.pem` | on the router |

`OWFEED_SIGN_KEY` means "the author's package key" in an author's repository and "the
feed's package key" in a feed's. That is fine because the repositories are different, and
it is written down here because the gate depends on it:

> **A workflow that can see an author's key and a feed's key at the same time is a bug.**
> No legitimate scenario produces one.

**An author's signature is never stripped.** apk signature blocks are additive: the package
a router installs carries the author's signature and the feed's. A subscriber who adds the
author's key verifies the author directly; a subscriber who trusts only the feed is
unaffected.

### Config

> **`owfeed.yml` describes a feed. A package repository carries only `owlab.yaml`.**

They are separate files and `owfeed.yml` does not grow an `owlab:` section.

`owlab.yaml` is a developer's machine ergonomics — edited often, reviewed by nobody.
`owfeed.yml` is release policy, and every diff to it should be read by a person. Merging a
file people edit without looking into a file people edit with review is a way to smuggle a
policy change in as a router addition. Beyond that, owfeed's validator rejects unknown
blocks by design; permitting an `owlab:` section means whitelisting it, which is coupling
the schemas.

Neither of the two commands an author runs needs a feed config. `owfeed release` never did;
`owfeed sign` used to, because it read the signing key from the config and the SDK release
from the lockfile beside it — so putting a signature on one file in `dist/noarch/` meant
writing a feed config and a 36-architecture lockfile first. It now takes `--key`, and
resolves the apk tool from the newest point release when no lockfile names one. A feed
still signs from its config, which is the pinned answer and the one to prefer.

## CI/CD composition

Four roles, each a composition of the pieces above.

**Author, own repository.** PR CI is T0 — `owlab build` produces `dist/<arch>/`,
`owlab/action` asserts the package works, no key exists anywhere in the workflow, so
fork PRs are safe by construction. The release job is T1 and is the only place the author's
keys appear: `owfeed sign` for the in-package signature, `owfeed release` for the signed
manifest, then the release upload.

**Feed operator.** Ingest runs in a job with no key, because the ingest scripts carry
values contributed by pull request. Signing, indexing and the publish gate run in a
separate job bound to the `feed` environment. `owfeed publish` refuses on error-level
findings and has no override flag; the upload itself is `actions/deploy-pages`.

**Third-party maintainer.** The intended funnel is: a request issue → an automated intake
check that runs `owfeed verify-artifact` against the claimed release *before* a person
looks → human review of the key addition, enforced by CODEOWNERS → one PR adding
`upstream.sh` and the public key → a scheduled bot for updates after that. The key from the
issue proves the release is internally coherent; it is not trust. Trust happens exactly
once, when the key is committed.

Of that, the last two steps exist. There is no request template and no intake workflow yet,
so a third party today reads CONTRIBUTING and opens a pull request by hand. `CODEOWNERS`
names `keys/` but is not enforced: a review requirement needs a reviewer, and with one
maintainer the author of a pull request cannot approve it — so the branch rule would block
every key addition permanently rather than gate it. It becomes a mechanism on the day there
is a second maintainer.

Conformance tiers follow from **what the signature covers**, not from how hard the author
tried:

| Tier | What the author publishes | What the signature covers | Automated updates |
|---|---|---|---|
| **A** | `owfeed release` — a signed `owfeed-manifest 1` | the whole inventory: sizes, hashes, repo, tag | yes |
| **B** | finished artifacts, each with a detached signature | each asset, but not the list of them | yes, while the set of containers is unchanged |
| **C** | unsigned binaries the feed packages itself | nothing but "it downloaded intact" | never |

Where an update is automatic it is still refused on a major version bump, on a diff
touching anything but the pins, and on the third update to one package inside a day —
the risk being a run of releases rather than one, since a stolen key signs each of them
perfectly and publishes faster than anyone reads the notifications.

**Consumer.** The feed is the default path. An author's own release channel remains valid
for routers that cannot add a repository, but the two must not be mixed on one router: on
25.12, `apk add ./file.apk` writes a pin into `/etc/apk/world` that survives sysupgrade and
outlives the feed. A self-updater that finds the feed configured is expected to refuse and
point at `apk upgrade`.

## Dogfooding

> **Dependencies go up the stack only, and only at the workflow level. At the code level
> there are none.**

```
owlab            ← depends on nothing (bottom layer)
  ↑
owfeed           ← may use owlab/action in a test-only job
  ↑
owfeed-packages  ← owfeed (reusable workflow) + owlab/action (consumer test)
  ↑
package repos    ← owlab (build/test) + owfeed (sign/release)
```

owfeed may use `owlab/action`, under three conditions: only in a test-only job, so an owlab
outage reddens owfeed's CI but never produces a broken owfeed binary; never in `go.mod`,
never in the reusable workflow other people call, never in `owfeed/setup`; and strictly one
directional. **owlab uses owfeed nowhere**, including at the workflow level, because owlab
is the bottom layer and a cycle would mean neither could be released first.

Concretely: owfeed's CI can build a test feed, serve it locally, and use `owlab/action` to
prove a package from that feed installs and renders its page. That catches a class of bug
`owfeed smoke` cannot catch by design — smoke checks that apk never asked for
`--allow-untrusted`, and checks nothing about whether what installed works.

owlab's CI does not use owfeed. Where owlab needs to confirm its `dist/<arch>/` output
matches the contract, it checks against the specification. The specification is a shared
artifact; a binary is not.

## Action pinning

Internal `VizzleTF/*` references are pinned to an exact tag, bumped as part of the release
procedure. External actions are pinned to a full commit SHA with the version in a trailing
comment.

The asymmetry has a reason: our own tag is under our control and the binary it installs is
verified by attestation anyway, so the tag is a convenience over a SHA rather than a trust
decision. Somebody else's tag is moved by somebody else.
