# AGENTS.md

This file provides guidance to AI agents when working with code in this repository.

## Project Overview

This is the DDTest (`ddtest`), a command-line tool that implements Datadog Test Optimization to automatically identify and skip tests that are unlikely to fail based on code changes. The tool integrates with Datadog's CI Visibility to provide Test Impact Analysis.

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
go run main.go plan   # Run plan command directly
```

### Single Test Execution

```bash
go test ./internal/runner/  # Test specific package
go test -v ./...           # Verbose testing
```

## Development

- Uses `dd-trace-go/v2` for Datadog tracing and CI Visibility
- Use go 1.26.5
- Use `strings` and `slices` packages, don't write your own string manipulation functions
- Always run `make test` after any change. Any new functionality must be covered by test.
- Always run `make lint` after any change. Address any linting issues found.

## Pull Requests

When opening a pull request for this repository, use exactly these sections:

1. `What`: Briefly describe the user-visible behavior change.
2. `Why`: Explain the customer problem and motivation. Link to relevant external docs when they are part of the rationale.
3. `E2E testing`: Write the test plan that manual QA must perform to validate this PR. Include prerequisites and setup, ordered actions or commands, and the expected observable result for each scenario. Cover the main journey and relevant failure/edge cases; explain cleanup when needed.

The `E2E testing` section is an executable plan for a person, not a report of
checks already run. Automated unit/integration test commands, CI status, lint
results, and statements such as "tests passed" do not substitute for manual QA
steps. Keep automated validation results in the PR checks or a separate comment.
Use commands and features available at this PR's own branch. For component-only
changes without a public entry point, supply a minimal runnable manual harness
and explain what QA should inspect; do not depend on a later PR's CLI. For
documentation-only changes, describe the manual documentation/usage review.

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
