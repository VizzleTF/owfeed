# Cutting a release

Two files reference a release tag and have to be moved before the tag exists, so the
tag contains its own correct self-reference.

1. `.github/workflows/feed.yml` — both `uses: VizzleTF/owfeed/setup@vX.Y.Z` lines,
   and the two tags in the usage comment at the top.
2. Commit, then `git tag -a vX.Y.Z` and push the tag.

The release workflow runs the tests before it publishes anything, builds for
linux and darwin on amd64 and arm64, and attests each binary. `owfeed/setup` refuses
to install one that does not verify against its attestation, so a release whose
attestation step failed is a release nobody can install — which is the intended
outcome, not a bug to work around.

## Why exact tags rather than a floating `v1`

`actions/checkout@v4` is a moving target by design, and for a checkout that is a
reasonable trade. This tool signs feeds. A consumer who pins
`feed.yml@v0.1.0` should get exactly the `setup` that was reviewed alongside it,
because the alternative is that the code which fetches and verifies the signing tool
can change under a pin that looks exact.

The cost is this file. That is the right side of the trade.
