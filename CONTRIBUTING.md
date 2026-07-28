# Contributing

## The short version

```sh
go test ./...
gofmt -l .
golangci-lint run ./...
```

All three run in CI on every pull request. The tests include ones that pull real
container images and a real apk toolchain, because the failures worth catching here
are the ones that only appear against the real thing.

## What this project is fussy about

**Every claim is measured, not reasoned.** `docs/apk-behaviour.md` is the record: what
apk and opkg actually do, established by running them, with the command and the
result. A change that asserts new behaviour belongs there with evidence beside it. A
plausible mechanism that nobody observed is a guess, and this project has been wrong
about several.

**A check that cannot run counts as failed.** Not skipped, not warned about. A green
report that means "nothing was looked at" is worse than no report.

**Errors say four things**: what failed, the exact command that failed, how to fix
it, and the check ID. A message that only reports that something is wrong has done
half the job.

**Comments explain why, not what.** The code says what. The reason a line exists —
usually a bug it prevents, often one that shipped somewhere — is the part that cannot
be recovered by reading it.

## Adding a check

Give it a stable ID in the existing ranges: 1xx config, 2xx metadata, 3xx signing,
4xx index, 5xx transport, 6xx on-device, 7xx documentation. Say in its doc comment
what breaks in the field when it is not caught, because that is what tells the next
person whether it is worth its false positives.

A check that fires on a correct feed is worse than no check: it teaches people to
ignore the output. Test both directions — the thing it catches, and a healthy feed it
must stay quiet about.

## Things that need care

**Never commit a key.** `.gitignore` covers `*.pem`, `*.key` and `*.sec`, and
`owfeed keygen` refuses to write inside a git working tree without `--force`. A
published signing key cannot be revoked.

**Do not change what a released artifact is named** without checking who reads it. An
installer already on a router looks its asset up by name and cannot be fixed
remotely.

**Config changes are compatibility changes.** An unknown key is an error by design, so
a renamed field breaks every existing `owfeed.yml` at load time.

## Releasing

See [RELEASING.md](RELEASING.md).
