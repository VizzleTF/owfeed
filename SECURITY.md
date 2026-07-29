# Security

## Reporting

Open a [private advisory](https://github.com/owfeed/owfeed/security/advisories/new). Please do not
open a public issue for anything that would let someone publish under a feed's key.

## What owfeed protects, and what it does not

owfeed is a build and publish tool, not a runtime. Its security value is in what it
refuses to publish and in what it can prove about what it did publish. Three of those
claims are worth stating plainly, because two of them are limits.

**The signing key never enters a build job.** The reusable workflow splits build from
publish so that whatever a feed's own scripts do — and for a feed carrying other
people's packages those scripts arrive by pull request — happens before the key
exists. `owfeed publish` gates the upload, and there is no flag to skip it.

**A feed's key is a trust anchor for every package name, not just yours.** A key in
`/etc/apk/keys` validates an index claiming *any* name, so a compromised feed can
offer a higher version of `dropbear` or `base-files` and win the resolution. This is
a property of apk, not of owfeed, and no amount of care here changes it. It is why
the README's third section talks people out of running a feed at all where signed
release artifacts would do.

**apk has no revocation.** No CRL, no expiry, no signal that a key is dead. A
compromised feed key plus control of the feed's URL is a full compromise of every
subscriber, and there is no recovery path for a device that is offline when you find
out. owfeed makes rotation cheap enough to actually do — APKv3 matches keys by
identity, so keys coexist and a keyring package carries a new one to routers already
installed — but it does not claim revocation exists.

## Supply chain

Release binaries carry [build provenance
attestations](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations).
`owfeed/setup` verifies one against the attestation from this repository's release
workflow before the binary is executed or put on `PATH`, and refuses to install it
otherwise.

It passes `--signer-workflow`, not only `--repo`. Checking the repository alone would
accept an attestation produced by any workflow in it holding `attestations: write`,
so a single merged pull request adding a workflow would be enough to mint a valid
attestation for arbitrary bytes.

Releases are immutable: the tag is locked to a commit and assets cannot be replaced.
Every action used here is pinned by commit, with Dependabot proposing updates as
diffs.

## Verifying a download by hand

```sh
gh attestation verify owfeed-linux-amd64 -R owfeed/owfeed \
  --signer-workflow owfeed/owfeed/.github/workflows/release.yml
```

A `SHA256SUMS` file is published too, and on its own it is not a check: it is served
by the same host, from the same release, as the binary it describes. Whoever can
replace one can replace the other.
