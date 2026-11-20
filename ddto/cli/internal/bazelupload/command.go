// Package bazelupload is ddtest instrument bazel upload: after Bazel tests, it
// enriches, validates and uploads the test, coverage and telemetry payloads
// that the rules_test_optimization uploader lists in a plan.
package bazelupload

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
)

const usage = `usage: %[1]s bazel upload --plan=FILE [options]

Enriches, validates and uploads the payloads that the plan lists, which are
relative to --root. The rules_test_optimization uploader writes the plan
after the tests.
  --plan FILE                   the JSON upload plan
  --root DIR                    the directory of the plan's sources (default .)
  --report-json FILE            writes the per-source report to FILE
  --dry-run                     prepares the requests without sending them or
                                changing the sources
  --keep-payloads               keeps the sources after delivery
  --batch-test-payloads         sends compatible test payloads together
  --gzip                        compresses test requests (default true)
  --workers N                   sources prepared at once (default: the
                                available CPUs); requests in flight are
                                twice as many, at least 32
  --validate-enrichment         fails a test payload without the expected tags
  --expected-enriched-tag TAG   an expected tag, repeatable; replaces the
                                default git.repository_url, git.commit.sha,
                                bazel.target and bazel.package
DD_API_KEY and DD_SITE configure agentless delivery, and
DD_TEST_OPTIMIZATION_AGENT_URL delivery through the Agent's EVP proxy.
DD_TEST_OPTIMIZATION_AGENTLESS_URL replaces the agentless intake.
DD_TEST_OPTIMIZATION_KEEP_PAYLOADS, _BATCH_TEST_PAYLOADS, _GZIP, _WORKERS and
_HTTP_KEEP_ALIVE apply when the matching option is not given.
`

var defaultExpectedTags = []string{"git.repository_url", "git.commit.sha", "bazel.target", "bazel.package"}

// commandLine holds the parsed arguments of one invocation.
type commandLine struct {
	flags              *flag.FlagSet
	options            Options
	plan, root, report string
	validateEnrichment bool
}

func newCommandLine(prog string) *commandLine {
	line := &commandLine{flags: flag.NewFlagSet(prog+" bazel upload", flag.ContinueOnError)}
	flags, options := line.flags, &line.options
	flags.SetOutput(io.Discard)
	flags.StringVar(&line.plan, "plan", "", "")
	flags.StringVar(&line.root, "root", ".", "")
	flags.StringVar(&line.report, "report-json", "", "")
	flags.BoolVar(&options.DryRun, "dry-run", false, "")
	flags.BoolVar(&options.KeepPayloads, "keep-payloads", false, "")
	flags.BoolVar(&options.Batch, "batch-test-payloads", false, "")
	flags.BoolVar(&options.Gzip, "gzip", true, "")
	flags.IntVar(&options.Workers, "workers", 0, "")
	flags.BoolVar(&line.validateEnrichment, "validate-enrichment", false, "")
	flags.Func("expected-enriched-tag", "", func(tag string) error {
		options.ExpectedTags = append(options.ExpectedTags, tag)
		return nil
	})
	return line
}

// Command runs ddtest instrument bazel upload with the arguments after the
// word upload and returns the exit status. prog names the command in messages.
func Command(ctx context.Context, prog string, args []string, stdout, stderr io.Writer) int {
	fail := func(status int, err error) int {
		fmt.Fprintln(stderr, "[dd-uploader] ERROR:", err)
		if status == 2 {
			fmt.Fprintf(stderr, usage, prog)
		}
		return status
	}
	line := newCommandLine(prog)
	if err := line.flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(stdout, usage, prog)
			return 0
		}
		return fail(2, err)
	}
	if line.flags.NArg() != 0 || line.plan == "" {
		return fail(2, errors.New("give --plan and no other arguments"))
	}
	options := &line.options
	if err := applyEnvironment(line.flags, options); err != nil {
		return fail(2, err)
	}
	switch {
	case !line.validateEnrichment:
		options.ExpectedTags = nil
	case len(options.ExpectedTags) == 0:
		options.ExpectedTags = defaultExpectedTags
	}
	// The command decodes payloads in bursts and then exits. On a plan the size
	// of a dd-source run, 200 was as fast as 400 with a third less peak memory,
	// and 8% faster than Go's default of 100. GOGC still overrides.
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(200)
	}
	report, err := runPlan(ctx, line.plan, line.root, *options)
	if err != nil {
		return fail(1, err)
	}
	fmt.Fprint(stdout, report.summary())
	if line.report != "" {
		if err := writeReport(line.report, report); err != nil {
			return fail(1, fmt.Errorf("write upload report: %w", err))
		}
	}
	return report.ExitCode
}

// applyEnvironment reads the rules uploader's variables for the options that
// the command line leaves unset, and the delivery settings.
func applyEnvironment(flags *flag.FlagSet, options *Options) error {
	given := make(map[string]bool)
	flags.Visit(func(set *flag.Flag) { given[set.Name] = true })
	// Workers reuse their connections unless the variable turns it off.
	options.KeepAlive = true
	for _, setting := range []struct {
		option, variable string
		value            *bool
	}{
		{"keep-payloads", "DD_TEST_OPTIMIZATION_KEEP_PAYLOADS", &options.KeepPayloads},
		{"batch-test-payloads", "DD_TEST_OPTIMIZATION_BATCH_TEST_PAYLOADS", &options.Batch},
		{"gzip", "DD_TEST_OPTIMIZATION_GZIP", &options.Gzip},
		{"", "DD_TEST_OPTIMIZATION_HTTP_KEEP_ALIVE", &options.KeepAlive},
	} {
		if setting.option != "" && given[setting.option] {
			continue
		}
		if err := environmentBool(setting.variable, setting.value); err != nil {
			return err
		}
	}
	if value := strings.TrimSpace(os.Getenv("DD_TEST_OPTIMIZATION_WORKERS")); value != "" && !given["workers"] {
		workers, err := strconv.Atoi(value)
		if err != nil || workers < 1 {
			return errors.New("DD_TEST_OPTIMIZATION_WORKERS must be a positive integer")
		}
		options.Workers = workers
	}
	options.APIKey = os.Getenv("DD_API_KEY")
	options.Site = os.Getenv("DD_SITE")
	options.AgentURL = os.Getenv("DD_TEST_OPTIMIZATION_AGENT_URL")
	options.AgentlessURL = os.Getenv("DD_TEST_OPTIMIZATION_AGENTLESS_URL")
	return nil
}

// environmentBool accepts the rules uploader's spellings and leaves value
// unchanged when the variable is empty.
func environmentBool(variable string, value *bool) error {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(variable))) {
	case "":
	case "true", "yes", "1":
		*value = true
	case "false", "no", "0":
		*value = false
	default:
		return fmt.Errorf("%s must be true or false", variable)
	}
	return nil
}

func runPlan(ctx context.Context, planPath, rootPath string, options Options) (Report, error) {
	input, err := os.Open(planPath)
	if err != nil {
		return Report{}, fmt.Errorf("open upload plan: %w", err)
	}
	plan, err := readPlan(input)
	_ = input.Close()
	if err != nil {
		return Report{}, err
	}
	rootPath, err = filepath.Abs(rootPath)
	if err != nil {
		return Report{}, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return Report{}, fmt.Errorf("open upload root: %w", err)
	}
	defer func() { _ = root.Close() }()
	return upload(ctx, root, plan, options)
}

// writeReport replaces path with the report only once the report is complete.
func writeReport(path string, report Report) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".ddtest-report-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	writeErr := json.NewEncoder(file).Encode(report)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
