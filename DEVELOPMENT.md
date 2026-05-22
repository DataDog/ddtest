# Development

## Prerequisites

- Go 1.26.2 or later

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
