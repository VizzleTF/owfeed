# Changelog

Dates are when the tag was cut. Anything not listed is documentation or tests.

## v0.1.6 — 2026-07-28

- `owfeed releases` reports what the download server publishes per line and which
  package format that line takes. It exists to be compared against: owlab answers
  the same question from the same server and neither reads the other, which is
  deliberate, but until now owfeed had no way to state its answer outside a feed
  repository — `arch.LatestPoint` is internal and `owfeed lock` needs a config.
  Duplication that nothing can observe is just drift with a delay.
- A nightly cross-check against `owlab releases --all --json`, which files an
  issue when the two disagree about a newest point release or about apk vs opkg.
  One open issue, not one per night.
- `feed.yml` takes `dry-run: true` and runs the whole pipeline against throwaway
  keys without an environment and without publishing, so a feed's pull-request
  check and its publication are one workflow in two modes. Two hand-written
  pipelines mean a green pull request proves nothing about the one that publishes;
  the divergence between them was exactly what nothing tested.
- `feed.yml` reports `digest` (sha256 over the published tree), `published` and
  `page-url`, so a caller can do something after publication rather than guess.
- `feed.yml`'s publish job reads `OWFEED_SIGN_KEY` and `OWFEED_USIGN_KEY` from the
  environment it declares, falling back to the `sign-key` / `usign-key` inputs.
  Taking them only as `workflow_call` secrets meant the calling workflow had to
  read them, and a calling job has no environment — so the workflow whose purpose
  is keeping a signing key inside a protected environment was forcing every feed
  that adopted it to hold that key at repository scope instead. Existing callers
  are unaffected: the input still wins when no environment secret is set.

## v0.1.5 — 2026-07-28

- `owfeed sign` no longer requires a feed config. It takes `--key`, and resolves the
  apk tool from the newest point release when no lockfile names one, so an author
  can put the in-package signature on their own build without writing an
  `owfeed.yml` and a 36-architecture lockfile to sign one file. A feed still signs
  from its config, which is the pinned answer.
- `owfeed release --sign-also FILE` signs a file published beside the packages —
  an installer script, most often — with the same key, without adding it to the
  manifest. The manifest is an inventory of packages a feed ingests, and a feed
  has no use for an installer; the signature is for a person checking what they
  are about to run as root, out of band. Repeatable. This is what lets a package
  repository drop its own usign loop entirely.
- `build: sdk` is refused by design rather than reported as unimplemented, and
  the error points at owlab and at `docs/artifact-contract.md`. owfeed packages;
  it does not compile.
- A JSON Schema for `owfeed.yml`, generated from `internal/config` and published
  at `https://owfeed.org/schema/v1.json`. It describes shape, not the validator's
  rules, and marks the keys that parse but are refused. `owfeed init` gets its
  `$schema` modeline back once that URL answers.
- **Docs:** `docs/ECOSYSTEM.md` states the boundary between owlab and owfeed, and
  `docs/artifact-contract.md` and `docs/manifest-format.md` write down the two
  formats that cross it. Both had more than one consumer and no specification.

## v0.1.4 — 2026-07-28

- A feed that carries only other people's packages and builds nothing itself is now
  expressible: an empty `packages:` list is legal, and `owfeed build` says there is
  nothing to do instead of failing the config.
- **Fixed:** the doc-drift check demanded the install snippet's `<package>`
  placeholder appear verbatim in a README, which made good documentation the finding
  for any feed with no packages of its own.
- **Fixed:** `owfeed release` dated the manifest from `SOURCE_DATE_EPOCH`. A feed sets
  that to a constant so a package's identity depends only on its payload, and the
  result was a signed document dating a 2026 release to 2023. `OWFEED_RELEASE_DATE`
  overrides it for anyone who needs a fixed one.

## v0.1.3 — 2026-07-28

- **Fixed:** `owfeed build` demanded the index-signing key from any config with an
  ipk line, at load time. An author publishing packages for someone else's feed has
  an ipk line and never builds an index, so this made that whole shape unusable. The
  requirement moved to `owfeed index`, where the key is read.
- **Fixed:** `owfeed release` produced one asset name per package regardless of
  architecture. Release assets are flat and an apk's filename carries no
  architecture — in a feed the architecture is the directory — so a package built for
  twenty architectures published twenty files with one name and nineteen of them did
  not exist. Colliding names now carry their architecture; a noarch package keeps the
  name an installer already on a router looks it up by.

## v0.1.2 — 2026-07-28

- `owfeed/setup` prints what it verified. `gh attestation verify` writes to stderr
  and a composite step swallows it, so the only evidence the check ran was the
  absence of a failure.
- Every action pinned by commit, with Dependabot keeping the pins current.
- Immutable releases enabled: the tag is locked to a commit and assets cannot be
  replaced.
- **Fixed:** the documented reusable-workflow usage would have failed for anyone whose
  default `GITHUB_TOKEN` is read-only — which is the recommended setting. Permissions
  can only be narrowed down a call chain, never widened, so the caller has to grant
  them.

## v0.1.1 — 2026-07-28

- `owfeed/setup` passes `--signer-workflow`, not only `--repo`. Checking the
  repository alone accepts an attestation from any workflow in it holding
  `attestations: write`, so one merged pull request adding a workflow would have been
  enough to mint a valid attestation for arbitrary bytes.
- **New check OWF514:** `.nojekyll` is fetched from the live site.
  `actions/upload-pages-artifact` excludes dotfiles from v4, and `.nojekyll` is what
  stops Pages running Jekyll over a tree of binaries — so a feed can be correct
  everywhere owfeed can inspect and be deployed without the file that keeps it
  correct. The reusable workflow sets `include-hidden-files: true`.
- `actions/deploy-pages` has needed `actions: read` since v4, and a `permissions:`
  block silently withholds it.

## v0.1.0 — 2026-07-28

First tagged release. Builds, signs, indexes and publishes apk feeds for OpenWrt
25.12 and later, and opkg feeds for 24.10 and earlier, from one configuration.

- `build`, `sign`, `index`, `publish`, `doctor`, `verify`, `smoke`, `release`,
  `keygen`, `lock`, `init`, `install-snippet`.
- Both release lines from one config, with the signature scheme each package manager
  actually verifies: EC prime256v1 for apk, usign for opkg.
- `smoke` installs the built feed on a real OpenWrt image and fails if apk asks for
  `--allow-untrusted`.
- `verify` checks the published feed over its documented URL: redirects apk will not
  follow, cache skew, a version republished with different contents.
- `owfeed.lock` makes the architecture list a reviewable diff.
- Release binaries carry build provenance attestations; `owfeed/setup` refuses to
  install one that does not verify.
