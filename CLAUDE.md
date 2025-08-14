# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is the Datadog Test Runner (`ddruntest`), a command-line tool that implements Datadog Test Optimization to automatically identify and skip tests that are unlikely to fail based on code changes. The tool integrates with Datadog's CI Visibility to provide Test Impact Analysis.

## Development Commands

### Build
```bash
make build
```
This creates the `ddruntest` binary in the current directory.

### Testing
```bash
make test
```
Runs the full test pipeline: lint → vet → test

### Linting and Formatting
```bash
make lint    # Runs golangci-lint with 5m timeout
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

## Architecture

### Core Components

- **main.go**: Entry point using cobra CLI framework
- **internal/cmd/**: Command definitions and CLI setup
- **internal/runner/**: Core test runner logic and optimization
- **internal/platform/**: Platform-specific code for environment  (Ruby, Python, etc.)
- **internal/framework/**: Test framework-spcific code for test discovery and running tests (RSpec, etc.)
- **internal/settings/**: Configuration management with viper
- **internal/testoptimization/**: Datadog API integration

### Key Interfaces

- `Runner`: Main entrypoint for this tool
- `Platform`: Handles platform-specific test discovery and tagging
- `TestOptimizationClient`: Manages Datadog API communication for skippable tests
- `PlatformDetector`: Abstracts platform detection for testing

### Data Flow

1. Settings loaded via viper with environment variable support (`DD_TEST_OPTIMIZATION_RUNNER_*`)
2. Platform detection determines test discovery strategy
3. Parallel execution:
   - Datadog API fetches skippable tests via CI Visibility
   - Framework discovers all available tests
4. Results merged to generate `.dd/test-files.txt` (non-skippable) and `.dd/skippable-percentage.txt`

### Output Files

- `.dd/test-files.txt`: List of test files that should not be skipped
- `.dd/skippable-percentage.txt`: Percentage of tests that can be skipped

## Integration Points

- Uses `dd-trace-go/v2` for Datadog tracing and CI Visibility
- Integrates with Knapsack Pro via `KNAPSACK_PRO_TEST_FILE_LIST_SOURCE_FILE=.dd/test-files.txt`
- Ruby platform uses external script (`internal/platform/scripts/ruby_env.rb`) for environment detection

## Memory

- Use go 1.24
- Use `strings` and `slices` packages, don't write your own string manipulation functions

- Always run unit tests after any change. Any new functionality must be covered by test.