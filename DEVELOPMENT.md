# Development

## Prerequisites

- Go at the version specified in `go.mod` or later

## Building

From an existing checkout:

```bash
make build
```

From a fresh clone:

```bash
git clone https://github.com/DataDog/ddtest.git
cd ddtest
make build
```

## Testing

```bash
make test
```

## Formatting and Vetting

```bash
make fmt
make vet
```

## Running from Source

```bash
make run
```

## Automated dependency updates

Dependabot checks Go modules (including indirect dependencies) and GitHub Actions
daily. New versions have a two-day cooldown. Go minor and patch updates are grouped;
major dependency updates get separate PRs. Action updates remain grouped.

The **Update Go toolchain** workflow checks stable Go and golangci-lint releases
daily at 07:23 UTC and can also be run manually from the Actions tab on `main`.
It maintains one PR on `automation/go-toolchain`, updating `go.mod` and
`.golangci-lint-version`. CI and release workflows read those files, so version
changes do not need to be copied into workflows or documentation. An App token
from dd-octo-sts lets the update push trigger the normal CI suite. Updates require
review and merge; a newer Go release can still need a compatible linter release
or code changes before CI passes.

These automations become active after their configuration and trust policy are
merged to `main`. Check **Insights → Dependency graph → Dependabot** for dependency
update logs, and **Actions → Update Go toolchain** for toolchain update failures.
Existing published binaries receive fixes only through a subsequent release.
