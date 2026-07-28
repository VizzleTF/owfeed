# Cutting a release

The repository references its own release tag in places that have to be moved before
the tag exists, so the tag contains its own correct self-reference.

1. `.github/workflows/feed.yml` — every `uses: VizzleTF/owfeed/setup@vX.Y.Z` line,
   one per job, and the tags in the usage comments at the top. Do not count them
   from memory; `grep -c 'setup@v' .github/workflows/feed.yml` is the check.
2. `README.md` and `README_ru.md` — the download example and the action snippets.
3. `CHANGELOG.md` — the `## Unreleased` heading becomes the version and the date.
4. Commit, then `git tag -a vX.Y.Z` and push the tag.

`grep -rn 'v<previous>' --include='*.md' --include='*.yml' .` should afterwards
match nothing outside `CHANGELOG.md`, where the old version is history rather than
a pin.

The release workflow runs the tests before it publishes anything, builds for
linux and darwin on amd64 and arm64, and attests each binary. `owfeed/setup` refuses
to install one that does not verify against its attestation, so a release whose
attestation step failed is a release nobody can install — which is the intended
outcome, not a bug to work around.

## Why exact tags rather than a floating `v1`

`actions/checkout@v4` is a moving target by design, and for a checkout that is a
reasonable trade. This tool signs feeds. A consumer who pins
`feed.yml@vX.Y.Z` should get exactly the `setup` that was reviewed alongside it,
because the alternative is that the code which fetches and verifies the signing tool
can change under a pin that looks exact.

The cost is this file. That is the right side of the trade.
