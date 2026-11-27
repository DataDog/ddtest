package bazeldoctor

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
)

// PostFallbackExit is the exit status of a post-process run that printed
// nothing and left the invocation to the Python uploader.
const PostFallbackExit = 75

const postUsage = `usage: %[1]s bazel post-process [--workers N] < request.json

Runs the rules_test_optimization uploader after it acquires its workspace
lock and loads its configuration and resources: BEP freshness, BEP artifact
staging, payload discovery, the doctor's output checks when the request
carries its configuration, the upload, which decodes each payload once,
retention, and the report. It prints the uploader's messages and summary
and exits with the uploader's status. DD_API_KEY, DD_SITE,
DD_TEST_OPTIMIZATION_AGENT_URL and DD_TEST_OPTIMIZATION_AGENTLESS_URL
configure delivery, as for upload. Exit status 75 means that the Python
uploader must run the invocation instead; nothing was printed or sent.
  --workers N   outputs and payloads processed at once (default: the available CPUs)
`

// PostCommand runs ddtest instrument bazel post-process with the arguments
// after the words post-process and returns the exit status.
func PostCommand(ctx context.Context, prog string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fail := func(status int, err error) int {
		_, _ = fmt.Fprintln(stderr, "[dd-uploader] ERROR:", err)
		if status == 2 {
			_, _ = fmt.Fprintf(stderr, postUsage, prog)
		}
		return status
	}
	flags := flag.NewFlagSet(prog+" bazel post-process", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	workers := flags.Int("workers", 0, "")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintf(stdout, postUsage, prog)
			return 0
		}
		return fail(2, err)
	}
	if flags.NArg() != 0 {
		return fail(2, errors.New("the request is read from standard input; give no arguments"))
	}
	decoder := json.NewDecoder(stdin)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var request PostRequest
	if err := decoder.Decode(&request); err != nil {
		// A field that this ddtest does not know comes from newer rules: the
		// Python uploader, which reads every field, runs the invocation.
		if _, newer := unknownRequestField(err); newer {
			return PostFallbackExit
		}
		return fail(2, fmt.Errorf("read post-process request: %w", err))
	}
	if request.Version > 1 {
		return PostFallbackExit
	}
	if request.Version != 1 {
		return fail(2, fmt.Errorf("unsupported post-process request schema_version %d", request.Version))
	}
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(200)
	}
	outcome := RunPost(ctx, request, stdout, stderr, *workers)
	if outcome.Fallback != "" {
		if strings.EqualFold(request.Config.LogLevel, "DEBUG") {
			_, _ = fmt.Fprintln(stderr, "[dd-uploader] DEBUG: ddtest leaves this invocation to the Python uploader:", outcome.Fallback)
		}
		return PostFallbackExit
	}
	return outcome.ExitCode
}
