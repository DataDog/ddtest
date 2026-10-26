package testdrive

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/kballard/go-shellquote"
)

//go:embed scripts/ruby_observer.rb
var rubyObserver string

// RubyGems is the authority for gem version requirements, including pessimistic
// constraints. Do not approximate them using Node's semver rules.
const rubyRequirementScript = `
def ruby_requirement_status(selector, requirements)
  return "inconclusive" unless selector.match?(/\A\d+(?:\.\d+){0,2}\z/)
  parts = selector.split(".").map(&:to_i)
  lower = Gem::Version.new((parts + [0] * (3 - parts.length)).join("."))
  if parts.length == 3
    return requirements.all? { |r| r.satisfied_by?(lower) } ? "compatible" : "incompatible"
  end
  upper_parts = parts.dup
  upper_parts[-1] += 1
  upper = Gem::Version.new((upper_parts + [0] * (3 - parts.length)).join("."))
  # Requirement truth changes only at its version boundaries. Check the first
  # stable patch and both sides of every boundary inside this selector's range.
  points = [lower]
  requirements.each do |requirement|
    requirement.requirements.each do |operator, version|
      boundaries = [version]
      boundaries << version.bump if operator == "~>"
      boundaries.each do |boundary|
        segments = boundary.release.segments
        return "inconclusive" unless segments.length <= 3 && segments.all? { |s| s.is_a?(Integer) }
        patch = segments + [0] * (3 - segments.length)
        after = patch.dup
        after[2] += 1
        points.concat([Gem::Version.new(patch.join(".")), Gem::Version.new(after.join("."))])
      end
    end
  end
  outcomes = points.uniq.select { |v| v >= lower && v < upper }.map { |v| requirements.all? { |r| r.satisfied_by?(v) } }.uniq
  return "compatible" if outcomes == [true]
  return "incompatible" if outcomes == [false]
  "inconclusive"
end
`

const rubyPreflightScript = rubyRequirementScript + `require "json"
framework = Gem::Specification.find_by_name(ARGV.fetch(0) == "rspec" ? "rspec-core" : "minitest")
tracer = Gem.loaded_specs["datadog-ci"]
# The CI action installs the tracer separately. Do not mistake a local Rails or
# development-tool version for the bundle resolved by every CI matrix entry.
specs = {}
pending = tracer ? [tracer] : []
until pending.empty?
  spec = pending.pop
  next if specs.key?(spec.name)
  specs[spec.name] = spec
  spec.runtime_dependencies.each { |d| pending << Gem.loaded_specs.fetch(d.name) }
end
requirements = specs.values.map(&:required_ruby_version)
def ruby_ci_requirement_status(selector, tracer, requirements)
  direct = ruby_requirement_status(selector, [tracer.required_ruby_version])
  return direct unless direct == "compatible"
  ruby_requirement_status(selector, requirements) == "compatible" ? "compatible" : "inconclusive"
end

jobs = JSON.parse(ARGV.fetch(1))
jobs.each do |job|
  next unless job["status"] == "not checked"
  ruby = job["ruby"].to_s
  job["tracer"] = tracer ? "datadog-ci@#{tracer.version}" : ""
  job["requirement"] = requirements.map(&:to_s).uniq.join("; ")
  if !tracer || job["tracer_requested"] != tracer.version.to_s
    job["status"], job["reason"] = "inconclusive", "Pin an exact datadog-ci release matching local validation; floating, different or missing tracer releases require separate validation."
  else
    job["status"] = ruby_ci_requirement_status(ruby, tracer, requirements)
    job["reason"] = case job["status"]
    when "compatible"
      "Every stable release selected by this Ruby version satisfies the locally resolved tracer and its runtime dependencies. The exact tracer release matches; CI bundle resolution, platform/engine support and framework execution remain untested."
    when "incompatible"
      "Selected CI Ruby does not satisfy this exact tracer release's own RubyGems requirement. Preserve its tests and select a compatible tracer release."
    else
      "This local dependency resolution does not certify the target Ruby selector/platform, or the selector spans a requirement boundary. Resolve the exact tracer on that CI Ruby/architecture before changing instrumentation scope; a different dependency/platform gem may be compatible."
    end
  end
end
supported = framework.version >= Gem::Version.new(ARGV.fetch(0) == "rspec" ? "3.0.0" : "5.0.0")
supported &&= !tracer || tracer.version >= Gem::Version.new("1.31.0")
puts JSON.generate(version: framework.version.to_s, tracer: tracer&.version&.to_s, ruby: RUBY_VERSION, supported: supported, jobs: jobs)`

func (t *Testdrive) checkRubyPreflight(ctx context.Context, output io.Writer, result *validationResult) error {
	check := &jestPreflight{Command: shellquote.Join(append([]string{t.command}, t.args...)...), Verdict: verdict{Status: "inconclusive", Reason: "The Ruby bundle has not been inspected."}}
	result.Preflight = check
	if t.checkOnly {
		gemfile := os.Getenv("BUNDLE_GEMFILE")
		if executor, ok := t.executor.(configurationExecutor); ok && executor.environment["BUNDLE_GEMFILE"] != "" {
			gemfile = executor.environment["BUNDLE_GEMFILE"]
		}
		if gemfile == "" {
			gemfile = "Gemfile"
		}
		lockfile := gemfile + ".lock"
		if filepath.Base(gemfile) == "gems.rb" {
			lockfile = filepath.Join(filepath.Dir(gemfile), "gems.locked")
		}
		if _, err := os.Stat(lockfile); err != nil {
			check.Verdict.Reason = "Run bundle install for the selected Gemfile before configuration-only validation; its lockfile is unavailable."
			return errors.New(check.Verdict.Reason)
		}
	}
	ci := onboard.RubyCIConfiguration(t.repositoryRoot, t.framework.Name())
	jobs, err := json.Marshal(ci.Jobs)
	if err != nil {
		return err
	}
	env := testEnvironment("http://127.0.0.1:1", "ruby-preflight")
	maps.Copy(env, t.rubyTracerEnv)
	env["RUBYOPT"] = stripDatadogRubyOptions(t.rubyOptions())
	env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	data, err := t.executor.CombinedOutput(ctx, "bundle", []string{"exec", "ruby", "-e", rubyPreflightScript, t.framework.Name(), string(jobs)}, env)
	if err != nil {
		check.Verdict.Reason = "Install the repository's Ruby dependencies first: " + commandDiagnostic(data)
		return fmt.Errorf("inspect Ruby bundle: %w: %s", err, check.Verdict.Reason)
	}
	var info struct {
		Version, Tracer, Ruby string
		Supported             bool
		Jobs                  []onboard.RuntimeFinding
	}
	if err := json.Unmarshal(data, &info); err != nil || info.Version == "" {
		return fmt.Errorf("ruby bundle did not produce framework metadata: %s", commandDiagnostic(data))
	}
	check.Version = info.Version
	check.Ruby = info.Ruby
	ci.Jobs = info.Jobs
	if len(ci.Jobs) > 0 {
		ci.Status, ci.Reason = "compatible", "Static Ruby CI selections agree with the installed bundle. Actual CI execution remains untested."
		for _, job := range ci.Jobs {
			if job.Status != "compatible" && job.Status != "excluded" {
				ci.Status, ci.Reason = job.Status, job.Reason
				break
			}
		}
		instrumented := false
		for _, job := range ci.Jobs {
			instrumented = instrumented || job.Status == "compatible"
		}
		if ci.Status == "compatible" && !instrumented {
			ci.Status, ci.Reason = "inconclusive", "Every CI entry excludes instrumentation; at least one compatible instrumented CI consumer is required."
		}
	}
	result.CIRuntime = &ci
	if !info.Supported {
		check.Verdict = verdict{Status: "incompatible", Reason: "Ruby validation requires RSpec >=3 or Minitest >=5 and datadog-ci >=1.31.0."}
		return errors.New(check.Verdict.Reason)
	}
	if info.Tracer != "" {
		t.projectTracer = fmt.Sprintf("* datadog-ci (%s)", info.Tracer)
		result.Tracer = "datadog-ci@" + info.Tracer
		check.Verdict = verdict{Status: "compatible", Reason: "RubyGems resolved the framework and tracer in the current bundle; paired execution and feature controls are still required."}
	} else {
		t.projectTracer = ""
		check.Verdict.Reason = "datadog-ci is missing from the project bundle. Full validation installs it; check-only does not install dependencies."
	}
	_, _ = fmt.Fprintf(output, "%s %s; Ruby %s; %s. Preflight: %s — %s\n", displayName(t.framework.Name()), info.Version, info.Ruby, result.Tracer, check.Verdict.Status, check.Verdict.Reason)
	return nil
}

func (t *Testdrive) rubyOptions() string {
	if executor, ok := t.executor.(configurationExecutor); ok {
		if options, ok := executor.environment["RUBYOPT"]; ok {
			return options
		}
	}
	return os.Getenv("RUBYOPT")
}

func (t *Testdrive) installRubyTracer(ctx context.Context, options platform.TracerOptions) (platform.TracerInstallation, error) {
	command, args, err := t.platform.TracerInstallCommand(options)
	if err != nil {
		return platform.TracerInstallation{}, err
	}
	// Keep the fallback bundle and its configuration inside the owned session.
	t.rubyTracerEnv = map[string]string{
		"BUNDLE_PATH":       filepath.Join(options.Directory, "bundle"),
		"BUNDLE_APP_CONFIG": filepath.Join(options.Directory, "bundle-config"),
	}
	env := maps.Clone(t.rubyTracerEnv)
	env["RUBYOPT"] = stripDatadogRubyOptions(t.rubyOptions())
	data, err := t.executor.CombinedOutput(ctx, command, args, env)
	if err != nil {
		return platform.TracerInstallation{}, fmt.Errorf("install temporary Ruby tracer: %w: %s", err, commandDiagnostic(data))
	}
	return platform.TracerInstallation{Env: maps.Clone(t.rubyTracerEnv)}, nil
}

func stripDatadogRubyOptions(options string) string {
	words, err := shellquote.Split(options)
	if err != nil {
		return options
	}
	var kept []string
	for i := 0; i < len(words); i++ {
		if (words[i] == "-r" || words[i] == "--require") && i+1 < len(words) && strings.HasPrefix(words[i+1], "datadog/") {
			i++
			continue
		}
		if strings.HasPrefix(words[i], "-rdatadog/") || strings.HasPrefix(words[i], "--require=datadog/") {
			continue
		}
		kept = append(kept, words[i])
	}
	return shellquote.Join(kept...)
}

func (t *Testdrive) runRuby(ctx context.Context, output io.Writer, session *Session, _ string, name string, instrumented bool, scenario intake.Scenario, probe, mode string) (run validationRun, runErr error) {
	run.Name, run.Instrumented, run.ProbeMode = name, instrumented, mode
	if probe != "" {
		run.Role = roleProbeControl
		if scenario.Feature != "" {
			run.Role = roleProbeFeature
		}
	}
	if scenario.Feature == "" {
		scenario.Feature = "reporting-only"
	}
	run.root, run.framework = t.repositoryRoot, t.framework.Name()
	directory := filepath.Join(session.Directory(), name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return run, err
	}
	observer := filepath.Join(directory, "ruby_observer.rb")
	if err := os.WriteFile(observer, []byte(rubyObserver), 0600); err != nil {
		return run, err
	}
	server, err := t.startIntake(directory, scenario)
	if err != nil {
		return run, err
	}
	closed := false
	defer func() {
		if !closed {
			runErr = errors.Join(runErr, server.Close())
		}
	}()
	env := t.environment("", server.URL(), session.ID()+" "+name)
	configureScenarioEnvironment(env, scenario.Feature)
	// Ruby's DRb appends a Unix socket name to TMPDIR. Nesting this under
	// the long session/run paths exceeds macOS's socket path limit.
	temporary, err := os.MkdirTemp("", "ddt-rb-")
	if err != nil {
		return run, err
	}
	defer func() { runErr = errors.Join(runErr, os.RemoveAll(temporary)) }()
	run.temporaryRoot = temporary
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		env[key] = temporary
	}
	env["RUBYOPT"] = stripDatadogRubyOptions(t.rubyOptions()) + " -rbundler/setup"
	if instrumented {
		env["RUBYOPT"] += " -rdatadog/ci/auto_instrument"
	} else {
		env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	}
	env["RUBYOPT"] += " -r" + shellquote.Join(observer)
	env["DDTEST_RUBY_FRAMEWORK"], env["DDTEST_RUBY_RESULTS"] = run.framework, filepath.Join(directory, "native-results")
	env["DDTEST_RUBY_PROBE"], env["DDTEST_PROBE_MODE"] = probe, mode
	env["DDTEST_PROBE_STATE"] = filepath.Join(directory, "probe-count")
	env["DDTEST_PROBE_OWNER"] = filepath.Join(directory, "probe-owner")
	command, args := t.executionCommand()
	run.Command = shellquote.Join(append([]string{command}, args...)...)
	_, _ = fmt.Fprintf(output, "Running %s...\n", name)
	data, commandErr := t.executor.CombinedOutput(ctx, command, args, env)
	run.commandOutput, run.commandError, run.ExitCode = string(data), commandErr, commandExitCode(commandErr)
	if commandErr != nil && probe == "" {
		writeCommandFailure(output, displayName(run.framework)+" "+name, data)
	}
	closeErr := server.Close()
	closed = true
	if closeErr != nil {
		return run, closeErr
	}
	if err := ctx.Err(); err != nil {
		return run, err
	}
	run.Facts, err = server.Facts()
	if err != nil {
		return run, err
	}
	readRubyResults(env["DDTEST_RUBY_RESULTS"], &run)
	if instrumented && probe == "" {
		run.Coverage = rubySourceCoverage(t.repositoryRoot, run.Facts, run.Tests)
	}
	if probe != "" {
		count := 0
		if data, err := os.ReadFile(env["DDTEST_PROBE_STATE"]); err == nil {
			count = strings.Count(string(data), "run\n")
		}
		run.ProbeExecutions = &count
	}
	if run.ExitCode != 0 || run.ResultError != "" || len(run.SuiteErrors) > 0 {
		run.Diagnostic = commandDiagnostic(data)
	}
	return run, nil
}

func rubySourceCoverage(root string, facts intake.Facts, native []jestTest) *coverageSummary {
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	var files []string
	for _, test := range facts.Tests {
		files = append(files, test.CoveredFiles...)
	}
	for _, suite := range facts.SuiteCoverages {
		files = append(files, suite.Files...)
	}
	for _, file := range files {
		if _, valid := cucumberCoverageFile(root, file); !valid {
			return &coverageSummary{Status: "invalid", TotalTests: facts.TestCount, Level: facts.CoverageLevel, Reason: "Ruby coverage names missing or outside-repository files; preserve repository-relative source paths."}
		}
	}

	// The shared file check accepts only existing repository source and excludes
	// native test files; Ruby coverage has the same filename contract as JS.
	coverage := cypressSourceCoverage(root, cypressTestCode{}, facts, native)
	switch coverage.Status {
	case "observed":
		coverage.Reason = "Ruby coverage includes existing repository application files; test-file coverage alone does not qualify."
	case "invalid":
		coverage.Reason = "Ruby coverage names missing or outside-repository files; inspect invalid_files in the report."
	default:
		coverage.Reason = "No repository application source coverage observed. Check datadog-ci coverage settings and the selected suite's application setup. Test-file coverage alone is insufficient."
	}
	return coverage
}

func readRubyResults(directory string, run *validationRun) {
	paths, _ := filepath.Glob(filepath.Join(directory, "*.json"))
	if len(paths) == 0 {
		run.ResultError = "Ruby did not produce completed native runner results"
		return
	}
	for _, path := range paths {
		var result struct {
			Complete bool
			Tests    []struct {
				File, Name, Status, Failure string
				TelemetryName               *string `json:"telemetry_name"`
				TelemetryScopedID           string  `json:"telemetry_scoped_id"`
			}
			Errors []string
		}
		data, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(data, &result) != nil || !result.Complete || result.Tests == nil {
			run.ResultError = "Ruby native runner results are missing, invalid or incomplete"
			return
		}
		for _, test := range result.Tests {
			run.Tests = append(run.Tests, jestTest{File: filepath.Clean(test.File), Name: test.Name, Status: test.Status, Failure: failureSignature(test.Failure), telemetryName: test.TelemetryName, telemetryScopedID: test.TelemetryScopedID})
		}
		run.SuiteErrors = append(run.SuiteErrors, result.Errors...)
	}
}

const rubyProbeBody = `
    File.open(ENV.fetch("DDTEST_PROBE_STATE"), "a") { |f| f.puts "run" }
    count = File.readlines(ENV.fetch("DDTEST_PROBE_STATE")).length
    mode = ENV.fetch("DDTEST_PROBE_MODE")
    raise "ddtest intentional failure" if mode == "fail" || (mode == "fail-once" && count == 1)
`

func (t *Testdrive) runRubyFeatures(ctx context.Context, output io.Writer, session *Session, baseline validationRun, result *validationResult) (runErr error) {
	if len(baseline.Tests) == 0 {
		unavailableFeatures(result, "No native Ruby tests were discovered; check the selected command and setup.")
		return nil
	}
	source := "RSpec.describe '' do\n  it('" + probeName + "') do\n" + rubyProbeBody + "  end\nend\n"
	suffix := "*_spec.rb"
	if t.framework.Name() == "minitest" {
		suffix = "*_test.rb"
		source = "require 'minitest/autorun'\nclass DDTestValidationProbe < Minitest::Test\n  def test_ddtest_validation_probe\n" + rubyProbeBody + "  end\nend\n"
	}
	probe, err := os.CreateTemp(filepath.Dir(baseline.Tests[0].File), "ddtest_validation_"+suffix)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, os.Remove(probe.Name())) }()
	_, writeErr := probe.WriteString(source)
	if err := errors.Join(writeErr, probe.Close()); err != nil {
		return err
	}
	return runProbeFeatures(func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
		return t.runRuby(ctx, output, session, "", name, true, scenario, probe.Name(), mode)
	}, result)
}
