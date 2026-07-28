# `manifest.txt` — the release manifest format

*A signed inventory of what a release contains. Written by `owfeed release`, read by
`owfeed verify-artifact` and by any feed ingesting that release. Without it a consumer has
to trust whatever the hosting service says a release holds; a signature over an API
response is not available, and an API response is not a signature.*

Owned by owfeed and versioned with it.

## Format

```
owfeed-manifest 1
repo <owner>/<name>
tag <tag>
version <tag without a leading v>
date <RFC3339, UTC>
notes <sha256> <filename>
pkg <name> <format> <file> <size> <sha256> <arch>
pkg <name> <format> <file> <size> <sha256> <arch>
```

Line-oriented, space-separated, parsed positionally. The first line is the format
identifier. `notes` appears only when the release carries notes. `pkg` repeats, sorted by
package name and then architecture, so the file is stable across rebuilds.

### `pkg`

Seven tokens, fixed order:

| # | Field | Example | Meaning |
|---|---|---|---|
| 1 | literal | `pkg` | |
| 2 | `name` | `luci-theme-footstrap` | package name, no version |
| 3 | `format` | `apk` / `ipk` | |
| 4 | `file` | `luci-theme-footstrap-0.11.6-r1_x86_64.apk` | the **release asset** name, with `_<arch>` where flat assets would collide |
| 5 | `size` | `48213` | bytes — lets a consumer check free space before downloading |
| 6 | `sha256` | `e2e7bd…` | |
| 7 | `arch` | `noarch` / `all` / `x86_64` | the directory name from the [artifact contract](artifact-contract.md) |

Field 4 is the name in the release; field 7 says which directory it came from. A consumer
has both and never has to guess either — which matters, because the name a feed publishes
the package under is neither of them, it is the canonical name the index derives.

The architecture is field seven and not field three for a reason worth preserving. Readers
already deployed parse the first six positionally — `$1=="pkg" && $2==name && $3==ext {print $4, $5, $6}`
is running on routers that cannot be fixed remotely. So the first six fields keep the shape
those readers expect and the new field goes after them, where it costs nothing.

## Versioning

The first line is `<name> <integer>`.

* A reader **must** refuse an unknown `<name>` and **must** refuse an `<integer>` higher
  than the one it understands. Refusing is the point of the line — misreading a shape is
  worse than not reading it.
* Readers parse positionally and **ignore surplus trailing tokens**. Appending a new
  trailing field is therefore a compatible change and does not bump the version.
* Changing the meaning or order of an existing field, or adding a field a reader must
  understand to be correct, is `owfeed-manifest 2`.

## Reader obligations

These are not suggestions; each one has a failure it exists to prevent.

1. **Verify the signature before parsing.** Every value in the manifest steers a download.
   Parsing first means acting on text nobody has vouched for. The detached signature is
   `manifest.txt.sig`, verified against a key the reader pinned in advance — never a key
   fetched alongside the manifest.

2. **Check the first line.** A manifest in a shape you do not know must be refused by name,
   not discovered field by field. A reader that skips this will accept a foreign six-field
   manifest, read an empty seventh field, and fail somewhere far away with an error about a
   missing directory.

3. **Check `repo` and `tag` against what you expected.** A signature proves who wrote
   something, never what it is about. One key often signs releases for several
   repositories, so without these two checks a manifest lifted from another of the same
   author's releases verifies perfectly as this one.

4. **Check each downloaded file's size and hash against its `pkg` line.** The manifest's
   signature already covers both, so this is what makes the signature mean anything about
   the bytes on disk.

## Signatures

`owfeed release` writes a detached usign signature beside the manifest and beside every
package. The manifest's signature is the one a manifest-aware reader needs; the per-package
ones stay because a consumer already in the field may be fetching a single asset and know
nothing about manifests.

The signature comment is `<repo> <tag>`, which is what a verifying tool shows a human.

## Non-goals

* **Not a package index.** It describes one release of one repository. A feed's index is
  built by `owfeed index` from the artifact tree and has nothing to do with this file.
* **Not a trust root.** It says what a release contains, once you have already decided the
  signing key is one you trust. That decision is made elsewhere, deliberately, by a person.
* **Not reproducible-build metadata.** `owfeed release` records the release timestamp and
  deliberately does not honour `SOURCE_DATE_EPOCH`: a build pins that to a constant for
  reproducibility, and honouring it here would put that constant's date in a signed
  document describing a release made years later. `OWFEED_RELEASE_DATE` overrides it for
  tests.
