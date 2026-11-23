package bazelinspect

import (
	"bufio"
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

// Command runs ddtest instrument bazel inspect with the arguments after the
// word inspect and returns the exit status.
func Command(prog string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fail := func(status int, err error) int {
		fmt.Fprintln(stderr, "[dd-test-optimization-doctor] ERROR:", err)
		if status == 2 {
			fmt.Fprintf(stderr, usage, prog)
		}
		return status
	}
	flags := flag.NewFlagSet(prog+" bazel inspect", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	workers := flags.Int("workers", 0, "")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(stdout, usage, prog)
			return 0
		}
		return fail(2, err)
	}
	if flags.NArg() != 0 {
		return fail(2, errors.New("the request is read from standard input; give no arguments"))
	}
	request, err := readRequest(stdin)
	if err != nil {
		return fail(1, err)
	}
	// As in bazel upload: the command decodes in bursts and exits. GOGC still
	// overrides.
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(200)
	}
	report := Inspect(request.Outputs, *workers)
	output := bufio.NewWriterSize(stdout, 1<<20)
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(report); err != nil {
		return fail(1, fmt.Errorf("write inspection report: %w", err))
	}
	if err := output.Flush(); err != nil {
		return fail(1, fmt.Errorf("write inspection report: %w", err))
	}
	return 0
}

func readRequest(input io.Reader) (Request, error) {
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, fmt.Errorf("read inspection request: %w", err)
	}
	if request.Version != 1 {
		return Request{}, fmt.Errorf("unsupported inspection request schema_version %d", request.Version)
	}
	return request, nil
}
