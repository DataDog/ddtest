# Datadog Test Runner

A command-line tool that runs tests with Datadog Test Optimization to automatically optimize your test suite for faster execution by identifying and skipping tests that are unlikely to fail based on code changes.

## Installation

### From Source

```bash
git clone https://github.com/DataDog/datadog-test-runner.git
cd datadog-test-runner
make build
```

This will create the `ddruntest` binary in the current directory.

## Usage

### Basic Command

```bash
./ddruntest [command] [flags]
```

### Available Commands

- `setup` - Prepare test optimization data by discovering test files and calculating skippable percentage
- `completion` - Generate autocompletion scripts for your shell
- `help` - Get help about any command

### Flags

- `--platform string` - Platform that runs tests (default: "ruby")
- `--framework string` - Test framework to use (default: "rspec")
- `-h, --help` - Show help information

### Examples

#### Setup Test Optimization

```bash
# Use default settings (Ruby with RSpec)
./ddruntest setup

# Specify platform and framework explicitly
./ddruntest setup --platform ruby --framework rspec

# Using environment variables
DD_TEST_OPTIMIZATION_RUNNER_PLATFORM=python ./ddruntest setup
DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK=pytest ./ddruntest setup
```

## Output Files

When you run `ddruntest setup`, the tool generates:

- `.dd/test-files.txt` - List of discovered test files
- `.dd/skippable-percentage.txt` - Percentage of tests that can be skipped

## Supported Platforms and Frameworks

### Currently Supported
- **Ruby**: RSpec framework

## Integration with Knapsack Pro

First, run `ddruntest setup --platform ruby --framework rspec`. Then set environment variable `KNAPSACK_PRO_TEST_FILE_LIST_SOURCE_FILE=.dd/test-files.txt` and knapsack_pro runner will only run the test files listed in `.dd/test-files.txt` - the ones that are not completely skipped by Datadog Test Impact Analysis.

## Development

### Prerequisites
- Go 1.24.5 or later

### Building
```bash
make build
```

### Testing
```bash
make test
```

### Formatting and Vetting
```bash
make fmt
make vet
```

### Running from Source
```bash
make run
```
