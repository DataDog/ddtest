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

## The Go instrumentation

`ddtest instrument` comes from its own Go module, `github.com/DataDog/ddtest/ddto`,
in `ddto/`; ddtest requires it with a `replace` of `./ddto`. `make test` and
`make lint` check ddtest's module. Check ddto's from its directory:

```bash
cd ddto
go vet ./...
go test ./...   # its integration tests build ddtest, from this checkout, as their driver
```

The **Go instrumentation** workflow runs ddto's complete suite on Linux, macOS
and Windows when `ddto/` or what runs it changes. See
[ddto/AGENTS.md](ddto/AGENTS.md#changing-this-repository) for its other checks.

## Formatting and Vetting

```bash
make fmt
make vet
```

## Release executables

```bash
make release VERSION=v1.2.3
```

`dist/` then holds `ddtest-<os>-<arch>` for Linux, macOS and Windows on `amd64`
and `arm64`, which carry the runtime that `ddtest instrument` provides, and the
install scripts with the `SHA256SUMS` that they check.

## Running from Source

```bash
make run
```

## Automated dependency updates

Dependabot checks Go modules and GitHub Actions daily. The **Update Go toolchain**
workflow checks Go and golangci-lint daily and supports manual runs from Actions.
All updates open PRs for review and merge.
