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

Dependabot checks Go modules and GitHub Actions daily. The **Update Go toolchain**
workflow checks Go and golangci-lint daily and supports manual runs from Actions.
All updates open PRs for review and merge.
