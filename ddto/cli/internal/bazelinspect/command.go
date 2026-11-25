package bazelinspect

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
)

const usage = `usage: %[1]s bazel inspect [--workers N] < request.json > report.json

Lists the JSON and msgpack payloads and the bazel_target_metadata.json
sidecars of the test.outputs directories in the request, decodes them in
parallel, and writes one JSON report to standard output. The
rules_test_optimization doctor writes the request and validates the report.
  --workers N   outputs inspected at once (default: the available CPUs)
`

const discoverUsage = `usage: %[1]s bazel discover [--workers N] < request.json > discovery.json

Finds the test.outputs directories below the request's testlogs roots and the
payload files that the rules_test_optimization uploader sends from them,
walking and listing in parallel, and writes one JSON report to standard
output. The uploader writes the request and builds its tasks from the report.
  --workers N   directories listed at once (default: the available CPUs)
`

const freshnessUsage = `usage: %[1]s bazel freshness [--workers N] < request.json > freshness.json

Reads the BEP JSON files in the request, parsing their events in parallel, and
writes the freshness evidence that the rules_test_optimization doctor and
uploader derive from them as one JSON report to standard output.
  --workers N   lines parsed at once (default: the available CPUs)
`

// Command runs ddtest instrument bazel inspect with the arguments after the
// word inspect and returns the exit status.
func Command(prog string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return RunCommand(context.Background(), CommandSpec{Name: "inspect", Usage: usage, Prefix: "[dd-test-optimization-doctor]"},
		prog, args, stdin, stdout, stderr, func(_ context.Context, decoder *json.Decoder, workers int) (any, error) {
			var request Request
			if err := decoder.Decode(&request); err != nil {
				return nil, fmt.Errorf("read inspection request: %w", err)
			}
			if request.Version != 1 {
				return nil, fmt.Errorf("unsupported inspection request schema_version %d", request.Version)
			}
			return Inspect(request.Outputs, workers), nil
		})
}

// DiscoverCommand runs ddtest instrument bazel discover with the arguments
// after the word discover and returns the exit status.
func DiscoverCommand(prog string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return RunCommand(context.Background(), CommandSpec{Name: "discover", Usage: discoverUsage, Prefix: "[dd-uploader]"},
		prog, args, stdin, stdout, stderr, func(_ context.Context, decoder *json.Decoder, workers int) (any, error) {
			var request DiscoverRequest
			if err := decoder.Decode(&request); err != nil {
				return nil, fmt.Errorf("read discovery request: %w", err)
			}
			if request.Version != 1 {
				return nil, fmt.Errorf("unsupported discovery request schema_version %d", request.Version)
			}
			if request.MaxDepth < 0 {
				return nil, errors.New("max_depth must be non-negative")
			}
			return Discover(request, workers), nil
		})
}

// FreshnessCommand runs ddtest instrument bazel freshness with the arguments
// after the word freshness and returns the exit status.
func FreshnessCommand(prog string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return RunCommand(context.Background(), CommandSpec{Name: "freshness", Usage: freshnessUsage,
		Prefix: "[dd-test-optimization-doctor]"}, prog, args, stdin, stdout, stderr,
		func(_ context.Context, decoder *json.Decoder, workers int) (any, error) {
			var request BepRequest
			if err := decoder.Decode(&request); err != nil {
				return nil, fmt.Errorf("read freshness request: %w", err)
			}
			if request.Version != 1 {
				return nil, fmt.Errorf("unsupported freshness request schema_version %d", request.Version)
			}
			return EvaluateBep(request, workers), nil
		})
}

// CommandSpec names a request command and its usage and message prefix.
type CommandSpec struct {
	Name, Usage, Prefix string
}

// RunCommand runs a request command of ddtest instrument bazel: it parses
// --workers, reads one JSON request from stdin with handle, and writes the
// result to stdout as JSON.
func RunCommand(ctx context.Context, cmd CommandSpec, prog string, args []string, stdin io.Reader, stdout, stderr io.Writer,
	handle func(context.Context, *json.Decoder, int) (any, error)) int {
	fail := func(status int, err error) int {
		fmt.Fprintln(stderr, cmd.Prefix, "ERROR:", err)
		if status == 2 {
			fmt.Fprintf(stderr, cmd.Usage, prog)
		}
		return status
	}
	flags := flag.NewFlagSet(prog+" bazel "+cmd.Name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	workers := flags.Int("workers", 0, "")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(stdout, cmd.Usage, prog)
			return 0
		}
		return fail(2, err)
	}
	if flags.NArg() != 0 {
		return fail(2, errors.New("the request is read from standard input; give no arguments"))
	}
	decoder := json.NewDecoder(stdin)
	decoder.DisallowUnknownFields()
	// As in bazel upload: the command works in bursts and exits. GOGC still
	// overrides.
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(200)
	}
	result, err := handle(ctx, decoder, *workers)
	if err != nil {
		return fail(1, err)
	}
	output := bufio.NewWriterSize(stdout, 1<<20)
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		return fail(1, fmt.Errorf("write %s report: %w", cmd.Name, err))
	}
	if err := output.Flush(); err != nil {
		return fail(1, fmt.Errorf("write %s report: %w", cmd.Name, err))
	}
	return 0
}
