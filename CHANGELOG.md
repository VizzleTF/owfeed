# Changelog

Dates are when the tag was cut. Anything not listed is documentation or tests.

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
