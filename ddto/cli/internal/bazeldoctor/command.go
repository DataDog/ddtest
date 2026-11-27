package bazeldoctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/DataDog/ddtest/ddto/cli/internal/bazelinspect"
)

const usage = `usage: %[1]s bazel validate [--workers N] < request.json > result.json

Validates the Bazel test outputs in the request as the rules_test_optimization
doctor does, writes the requested report and support bundle, and writes one
JSON result to standard output: how the run ended and the messages that the
doctor prints. A result with status "fallback" means that the doctor must
validate the outputs itself. The rules' doctor writes the request and prints
the messages.
  --workers N   outputs inspected at once (default: the available CPUs)
`

// Command runs ddtest instrument bazel validate with the arguments after the
// word validate and returns the exit status: 0 when it wrote the result,
// whatever the validation found.
func Command(ctx context.Context, prog string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return bazelinspect.RunCommand(ctx, bazelinspect.CommandSpec{Name: "validate", Usage: usage, Prefix: "[dd-test-optimization-doctor]"},
		prog, args, stdin, stdout, stderr, func(ctx context.Context, decoder *json.Decoder, workers int) (any, error) {
			var request Request
			if err := decoder.Decode(&request); err != nil {
				// Newer rules send a field that this ddtest does not know:
				// the Python doctor validates instead.
				if field, newer := unknownRequestField(err); newer {
					return Result{Status: "fallback", Reason: "request field " + field + " is newer than this ddtest"}, nil
				}
				return nil, fmt.Errorf("read validation request: %w", err)
			}
			if request.Version > 1 {
				return Result{Status: "fallback", Reason: fmt.Sprintf("request schema_version %d is newer than this ddtest", request.Version)}, nil
			}
			if request.Version != 1 {
				return nil, fmt.Errorf("unsupported validation request schema_version %d", request.Version)
			}
			return Run(ctx, request, workers)
		})
}
