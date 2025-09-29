# AGENTS.md

This file provides guidance to AI agents when working with code in this repository.

## Project Overview

This is the Datadog Test Runner (`ddtest`), a command-line tool that implements Datadog Test Optimization to automatically identify and skip tests that are unlikely to fail based on code changes. The tool integrates with Datadog's CI Visibility to provide Test Impact Analysis.

## Dev Commands

### Build

```bash
make build
```

This creates the `ddtest` binary in the current directory.

### Testing

```bash
make test # Runs only tests, without linting
```

### Linting and Formatting

```bash
make lint    # Runs linter (automatically runs vet and fmt as well)
make vet     # Runs go vet
make fmt     # Formats code with go fmt
```

### Running from Source

```bash
make run              # Runs with go run main.go
go run main.go setup  # Run setup command directly
```

### Single Test Execution

```bash
go test ./internal/runner/  # Test specific package
go test -v ./...           # Verbose testing
```

## Development

- Uses `dd-trace-go/v2` for Datadog tracing and CI Visibility
- Use go 1.24
- Use `strings` and `slices` packages, don't write your own string manipulation functions
- Always run `make test` after any change. Any new functionality must be covered by test.
- Always run `make lint` after any change. Address any linting issues found.

## Architecture

### Core Components

- **main.go**: Entry point using cobra CLI framework
- **internal/ciprovider/**: Integrations with CI providers (GitHub Actions for example)
- **internal/cmd/**: Command definitions and CLI setup
- **internal/ext/**: Tools for interfacing with external world (OS)
- **internal/framework/**: Test framework-spcific code for test discovery and running tests (RSpec, etc.)
- **internal/platform/**: Platform-specific code for environment (Ruby, Python, etc.)
- **internal/runner/**: Core test runner logic and optimization
- **internal/settings/**: Configuration management with viper
- **internal/testoptimization/**: Datadog API integration

### Key Interfaces

- `Runner`: Main entrypoint for this tool
- `Platform`: Handles platform-specific test discovery and tagging
- `TestOptimizationClient`: Manages Datadog API communication for skippable tests
- `PlatformDetector`: Abstracts platform detection for testing
