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
	"regexp"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
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
def ruby_ci_requirement_status(selector, tracer, specs)
  requirements = specs.map(&:required_ruby_version)
  direct = ruby_requirement_status(selector, [tracer.required_ruby_version])
  return [direct, requirements, []] unless direct == "compatible"
  return ["compatible", requirements, []] if ruby_requirement_status(selector, requirements) == "compatible"

  # A precompiled native gem can require a newer Ruby than the same release's
  # portable source gem. Resolve that exact platform variant, without changing
  # the application bundle or assuming another dependency version is available.
  portable = []
  resolved = specs.map do |spec|
    next spec if ruby_requirement_status(selector, [spec.required_ruby_version]) == "compatible" || spec.platform.to_s == "ruby"
    require "rubygems/source"
    require "rubygems/remote_fetcher"
    require "rubygems/name_tuple"
    require "timeout"
    source = Gem::Source.new("https://rubygems.org")
    candidate = Timeout.timeout(10) { source.fetch_spec(Gem::NameTuple.new(spec.name, spec.version, Gem::Platform::RUBY)) }
    dependencies = ->(item) { item.runtime_dependencies.map { |d| [d.name, d.requirement.to_s] }.sort }
    unless candidate.name == spec.name && candidate.version == spec.version && candidate.platform.to_s == "ruby" && dependencies.call(candidate) == dependencies.call(spec)
      return ["inconclusive", requirements, []]
    end
    portable << "#{spec.name}@#{spec.version} (ruby)"
    candidate
  end
  selected = resolved.map(&:required_ruby_version)
  status = ruby_requirement_status(selector, selected) == "compatible" ? "compatible" : "inconclusive"
  [status, selected, portable]
rescue StandardError, LoadError
  # Missing/offline/unverifiable metadata cannot certify another CI target.
  ["inconclusive", requirements, []]
end

jobs = JSON.parse(ARGV.fetch(1))
jobs.each do |job|
  next unless job["status"] == "not checked"
  ruby = job["runtime"].to_s
  job["tracer"] = tracer ? "datadog-ci@#{tracer.version}" : ""
  job["requirement"] = requirements.map(&:to_s).uniq.join("; ")
  if !tracer || job["tracer_requested"] != tracer.version.to_s
    job["status"], job["reason"] = "inconclusive", "Pin an exact datadog-ci release matching local validation; floating, different or missing tracer releases require separate validation."
  else
    job["status"], selected_requirements, portable = ruby_ci_requirement_status(ruby, tracer, specs.values)
    job["requirement"] = selected_requirements.map(&:to_s).uniq.join("; ")
    job["reason"] = case job["status"]
    when "compatible"
      evidence = portable.empty? ? "the locally resolved tracer and its runtime dependencies" : "the exact tracer and same-version portable gem metadata (#{portable.join(', ')}), with unchanged runtime dependency requirements"
      "Every stable release selected by this Ruby version satisfies #{evidence}. The exact tracer release matches; CI bundle resolution, native compilation, platform/engine support and framework execution remain untested."
    when "incompatible"
      "Selected CI Ruby does not satisfy this exact tracer release's own RubyGems requirement. Preserve its tests and select a compatible tracer release."
    else
      "This local dependency resolution does not certify the target Ruby selector/platform, or the selector spans a requirement boundary. Resolve the exact tracer on that CI Ruby/architecture before changing instrumentation scope; a different dependency/platform gem may be compatible."
    end
  end
end
supported = framework.version >= Gem::Version.new(ARGV.fetch(0) == "rspec" ? "3.0.0" : "5.0.0")
supported &&= !tracer || tracer.version >= Gem::Version.new("1.31.0")
metadata = JSON.generate(version: framework.version.to_s, tracer: tracer&.version&.to_s, ruby: RUBY_VERSION, supported: supported, jobs: jobs)
ENV["DDTEST_RUBY_PREFLIGHT_RESULT"] ? File.write(ENV["DDTEST_RUBY_PREFLIGHT_RESULT"], metadata) : puts(metadata)`

func (t *Testdrive) checkRubyPreflight(ctx context.Context, output io.Writer, result *validationResult) error {
	check := &preflightCheck{Command: shellquote.Join(append([]string{t.command}, t.args...)...), Verdict: verdict{Status: "inconclusive", Reason: "The Ruby bundle has not been inspected."}}
	result.Preflight = check
	if t.checkOnly {
		directory, err := filepath.Rel(t.checkoutRoot(), t.repositoryRoot)
		if err != nil {
			return err
		}
		gemfile, err := rubyConfigurationGemfile(t.checkoutRoot(), directory, t.configurationEnvironment, len(t.configurationLocations) == 0)
		if err != nil {
			return err
		}
		lockfile := filepath.Join(t.checkoutRoot(), rubyLockfile(gemfile))
		if _, err := os.Stat(lockfile); err != nil {
			check.Verdict.Reason = "Run bundle install for the selected Gemfile before configuration-only validation; its lockfile is unavailable."
			return errors.New(check.Verdict.Reason)
		}
	}
	ci := onboard.RubyCIConfiguration(t.checkoutRoot(), t.framework.Name())
	if len(ci.Jobs) > 0 {
		locations, err := t.rubyRuntimeLocations()
		if err != nil {
			return err
		}
		ci.Jobs = slices.DeleteFunc(ci.Jobs, func(job onboard.RuntimeFinding) bool { return !slices.Contains(locations, job.Location) })
		if len(ci.Jobs) == 0 {
			ci.Status, ci.Reason = "inconclusive", "No CI consumer matches this Ruby command, working directory and bundle; validate its discovered configuration with --all."
		}
	}
	jobs, err := json.Marshal(ci.Jobs)
	if err != nil {
		return err
	}
	env := testEnvironment("http://127.0.0.1:1", "ruby-preflight")
	maps.Copy(env, t.tracerEnvironment)
	env["RUBYOPT"] = stripDatadogRubyOptions(t.rubyOptions())
	env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	// Gems, the Gemfile and preserved Ruby options may print diagnostics, so
	// the metadata goes through a private file rather than the output.
	resultFile, err := os.CreateTemp("", "ddtest-ruby-preflight-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(resultFile.Name()) }()
	if err := resultFile.Close(); err != nil {
		return err
	}
	env["DDTEST_RUBY_PREFLIGHT_RESULT"] = resultFile.Name()
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
	metadata, err := os.ReadFile(resultFile.Name())
	if err != nil || json.Unmarshal(metadata, &info) != nil || info.Version == "" {
		return fmt.Errorf("ruby bundle did not produce framework metadata: %s", commandDiagnostic(data))
	}
	check.Version = info.Version
	check.Runtime = info.Ruby
	ci.Jobs = info.Jobs
	summarizeRubyRuntime(&ci)
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

const rubyCopyBundleConfig = `require "bundler"; require "fileutils"
source = Bundler.app_config_path.join("config")
target = File.join(ARGV.fetch(0), "config")
FileUtils.mkdir_p(File.dirname(target), mode: 0700)
FileUtils.cp(source, target) if source.file?
# Local settings precede BUNDLE_PATH in Bundler. Override only installation
# routing in the copied settings, retaining mirrors, credentials and build flags.
settings = Bundler::Settings.new(Pathname.new(ARGV.fetch(0)))
settings.set_local("path.system", nil)
settings.set_local("disable_shared_gems", nil)
settings.set_local("path", ARGV.fetch(1))
# The copied Gemfile gains datadog-ci, so frozen or deployment settings would
# reject the change. Only the disposable copy is unfrozen.
settings.set_local("frozen", "false")
settings.set_local("deployment", "false")
File.chmod(0600, target)
`

func (t *Testdrive) installRubyTracer(ctx context.Context, options platform.TracerOptions) (platform.TracerInstallation, error) {
	command, args, err := t.platform.TracerInstallCommand(options)
	if err != nil {
		return platform.TracerInstallation{}, err
	}
	// Read local settings through Bundler's own Gemfile/BUNDLE_APP_CONFIG rules
	// before replacing their directory. Credentials stay in private session IO.
	env := maps.Clone(options.Environment)
	if env == nil {
		env = map[string]string{}
	}
	env["RUBYOPT"] = ""
	configuration := filepath.Join(options.Directory, "bundle-config")
	if _, err := t.executor.CombinedOutput(ctx, "ruby", []string{"-e", rubyCopyBundleConfig, configuration, filepath.Join(options.Directory, "bundle")}, env); err != nil {
		return platform.TracerInstallation{}, fmt.Errorf("copy effective Bundler configuration: %w", err)
	}
	// Keep the fallback bundle and its configuration inside the owned session.
	t.tracerEnvironment = map[string]string{
		"BUNDLE_PATH":       filepath.Join(options.Directory, "bundle"),
		"BUNDLE_APP_CONFIG": configuration,
	}
	maps.Copy(env, t.tracerEnvironment)
	// The environment outranks local settings; keep it from refreezing the copy.
	env["BUNDLE_FROZEN"], env["BUNDLE_DEPLOYMENT"] = "false", "false"
	// Gem compilation changes directory and must not initialize the test app.
	env["RUBYOPT"] = ""
	data, err := t.executor.CombinedOutput(ctx, command, args, env)
	if err != nil {
		return platform.TracerInstallation{}, fmt.Errorf("install temporary Ruby tracer: %w: %s", err, commandDiagnostic(data))
	}
	return platform.TracerInstallation{Env: maps.Clone(t.tracerEnvironment)}, nil
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

// Keep an existing CI preload's position: application configuration may depend
// on Datadog already being initialized. Standalone runs inject it before those
// preloads, while baseline runs remove only Datadog's instrumentation.
func rubyRunOptions(options string, instrumented bool) string {
	prefix := "-rbundler/setup"
	if !instrumented {
		return strings.TrimSpace(prefix + " " + stripDatadogRubyOptions(options))
	}
	words, _ := shellquote.Split(options)
	hasAuto := false
	for i, word := range words {
		path := ""
		if (word == "-r" || word == "--require") && i+1 < len(words) {
			path = words[i+1]
		} else if value, found := strings.CutPrefix(word, "--require="); found {
			path = value
		} else if value, found := strings.CutPrefix(word, "-r"); found {
			path = value
		}
		hasAuto = hasAuto || strings.TrimSuffix(path, ".rb") == "datadog/ci/auto_instrument"
	}
	if !hasAuto {
		prefix += " -rdatadog/ci/auto_instrument"
	}
	return strings.TrimSpace(prefix + " " + options)
}

func (t *Testdrive) runRuby(ctx context.Context, output io.Writer, session *workspace.Session, _ string, name string, instrumented bool, scenario intake.Scenario, probe, mode string) (run validationRun, runErr error) {
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
	// datadog-ci treats a module without known tests as unusable for early
	// flake detection.
	scenario.KnownModules = (rubyLanguage{}).frameworks()
	run.root, run.checkoutRoot, run.framework = t.repositoryRoot, t.checkoutRoot(), t.framework.Name()
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
	env["DD_CIVISIBILITY_GIT_METADATA_UPLOAD_ENABLED"] = "false"
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
	env["RUBYOPT"] = rubyRunOptions(t.rubyOptions(), instrumented)
	if !instrumented {
		env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	}
	libraryPath := os.Getenv("RUBYLIB")
	if value, ok := t.configurationEnvironment["RUBYLIB"]; ok {
		libraryPath = value
	}
	env["RUBYLIB"] = directory
	if libraryPath != "" {
		env["RUBYLIB"] += string(os.PathListSeparator) + libraryPath
	}
	// RUBYOPT splits on whitespace; it does not interpret shell quoting.
	env["RUBYOPT"] += " -rruby_observer.rb"
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
		run.Coverage = rubySourceCoverage(t.checkoutRoot(), run.Facts, run.Tests)
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

func rubySourceCoverage(root string, facts intake.Facts, native []nativeTest) *coverageSummary {
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
		if _, valid := repositoryCoverageFile(root, file); !valid {
			return &coverageSummary{Status: "invalid", TotalTests: facts.TestCount, Level: facts.CoverageLevel, Reason: "Ruby coverage names missing or outside-repository files; preserve repository-relative source paths."}
		}
	}

	// Keep test helpers/support and gems installed inside the repository out of
	// application evidence without discarding their actual coverage from the
	// retained intake/report.
	applicationFiles := func(files []string) []string {
		var kept []string
		for _, file := range files {
			local, _ := repositoryCoverageFile(root, file)
			parts := strings.Split(filepath.ToSlash(local), "/")
			if slices.Contains(parts, "vendor") || slices.Contains(parts, ".bundle") || slices.ContainsFunc(parts[:len(parts)-1], installedGemDirectory) ||
				slices.Contains(parts, "test") || slices.Contains(parts, "tests") || slices.Contains(parts, "spec") ||
				slices.Contains([]string{"test_helper.rb", "spec_helper.rb", "rails_helper.rb"}, filepath.Base(local)) ||
				strings.HasSuffix(local, "_test.rb") || strings.HasSuffix(local, "_spec.rb") {
				continue
			}
			kept = append(kept, file)
		}
		return kept
	}
	facts.Tests = slices.Clone(facts.Tests)
	for i := range facts.Tests {
		facts.Tests[i].CoveredFiles = applicationFiles(facts.Tests[i].CoveredFiles)
	}
	facts.SuiteCoverages = slices.Clone(facts.SuiteCoverages)
	for i := range facts.SuiteCoverages {
		facts.SuiteCoverages[i].Files = applicationFiles(facts.SuiteCoverages[i].Files)
	}
	coverage := applicationSourceCoverage(root, facts, native)
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
				ClassName                   string  `json:"class_name"`
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
			native := nativeTest{File: filepath.Clean(test.File), Name: test.Name, Status: test.Status, Failure: failureSignature(test.Failure), telemetryName: test.TelemetryName, NativeID: test.TelemetryScopedID, telemetryClass: test.ClassName}
			native.probe = test.ClassName == rubyProbeClass || test.ClassName == "" && test.Name == rubyProbeMethod
			if test.ClassName != "" {
				native.Name = test.ClassName + "#" + test.Name
				if native.telemetryName == nil {
					name := test.Name
					native.telemetryName = &name
				}
			}
			run.Tests = append(run.Tests, native)
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

const (
	rubyProbeClass  = "DDTestValidationProbe"
	rubyProbeMethod = "test_ddtest_validation_probe"
)

func (t *Testdrive) runRubyFeatures(ctx context.Context, output io.Writer, session *workspace.Session, baseline validationRun, result *validationResult) (runErr error) {
	if len(baseline.Tests) == 0 {
		unavailableFeatures(result, "No native Ruby tests were discovered; check the selected command and setup.")
		return nil
	}
	directory, ok := t.rubyProbeDirectory(baseline.Tests)
	if !ok {
		unavailableFeatures(result, "No native Ruby test file belongs to the repository outside installed gems; the controlled probe has nowhere to run.")
		return nil
	}
	source := "RSpec.describe '' do\n  it('" + probeName + "') do\n" + rubyProbeBody + "  end\nend\n"
	suffix := "*_spec.rb"
	if t.framework.Name() == "minitest" {
		suffix = "*_test.rb"
		source = "require 'minitest/autorun'\nclass " + rubyProbeClass + " < Minitest::Test\n  def " + rubyProbeMethod + "\n" + rubyProbeBody + "  end\nend\n"
	}
	probe, err := os.CreateTemp(directory, "ddtest_validation_"+suffix)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, os.Remove(probe.Name())) }()
	_, writeErr := probe.WriteString(source)
	if err := errors.Join(writeErr, probe.Close()); err != nil {
		return err
	}
	return runProbeFeatures(func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
		// datadog-ci reports and skips by suite, not by source file.
		scenario.SuiteSelector = scenario.Suite
		return t.runRuby(ctx, output, session, "", name, true, scenario, probe.Name(), mode)
	}, result)
}

// rubyProbeDirectory picks the directory of a native test file that belongs
// to the repository, never one inside an installed gem or bundle, where a
// probe would change installed code.
func (t *Testdrive) rubyProbeDirectory(tests []nativeTest) (string, bool) {
	root := t.checkoutRoot()
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	var bundles []string
	for _, env := range []map[string]string{t.configurationEnvironment, t.tracerEnvironment} {
		if path := env["BUNDLE_PATH"]; path != "" {
			bundles = append(bundles, path)
		}
	}
	if path := os.Getenv("BUNDLE_PATH"); path != "" {
		bundles = append(bundles, path)
	}
	for _, test := range tests {
		file := test.File
		if !filepath.IsAbs(file) {
			file = filepath.Join(t.repositoryRoot, file)
		}
		if canonical, err := filepath.EvalSymlinks(filepath.Dir(file)); err == nil {
			file = filepath.Join(canonical, filepath.Base(file))
		}
		relative, err := filepath.Rel(root, file)
		if err != nil || !filepath.IsLocal(relative) {
			continue
		}
		if slices.ContainsFunc(bundles, func(bundle string) bool {
			if !filepath.IsAbs(bundle) {
				bundle = filepath.Join(t.repositoryRoot, bundle)
			}
			if canonical, err := filepath.EvalSymlinks(bundle); err == nil {
				bundle = canonical
			}
			inside, err := filepath.Rel(bundle, file)
			return err == nil && filepath.IsLocal(inside)
		}) {
			continue
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if slices.ContainsFunc(parts, func(part string) bool { return part == "vendor" || part == ".bundle" }) || slices.ContainsFunc(parts[:len(parts)-1], installedGemDirectory) {
			continue
		}
		return filepath.Dir(file), true
	}
	return "", false
}

// An installed gem's directory is named <gem>-<version>, as in gems/rake-13.4.2.
var installedGemDirectory = regexp.MustCompile(`^[A-Za-z0-9_.-]+-[0-9]+(\.[0-9A-Za-z]+)*(-[a-z0-9_]+)*$`).MatchString

// Ruby mismatches say how many executions each side saw, and why forked
// tests may lose their telemetry.
func verifyRubyTelemetry(run validationRun) verdict {
	result := defaultTelemetry(run)
	if len(result.Differences) == 0 {
		return result
	}
	native, reported := 0, 0
	for _, test := range run.Tests {
		if test.Status == "passed" || test.Status == "failed" {
			native++
		}
	}
	// Count executions like the verifier: a span sent twice is one execution,
	// and a retry replaces the attempt before it.
	for _, test := range run.Facts.Tests {
		spans := map[uint64]bool{}
		var final *intake.TestRun
		count := func() {
			if final != nil && (final.Status == "pass" || final.Status == "fail") {
				reported++
			}
		}
		for i, attempt := range test.Attempts {
			if attempt.SpanID != 0 {
				if spans[attempt.SpanID] {
					continue
				}
				spans[attempt.SpanID] = true
			}
			if !attempt.Retry {
				count()
			}
			final = &test.Attempts[i]
		}
		count()
	}
	result.Code = "ruby_telemetry_mismatch"
	result.Reason = fmt.Sprintf("Ruby telemetry mismatch: %d native executions, %d reporting executions. Inspect the missing/unexpected identities below. If affected tests use fork/exit! isolation, check child-process tracer flushing; feature probe success cannot replace missing real-suite telemetry.", native, reported)
	return result
}
