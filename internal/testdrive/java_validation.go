// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"cmp"
	"context"
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

	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/kballard/go-shellquote"
)

// javaIncompleteBuild explains a failed build that native test failures do not account for.
const javaIncompleteBuild = "The Maven build stopped for a reason other than native test failures, such as compilation, setup or a later module's failure, so part of the reactor did not run. Inspect the Maven output"

// javaReactor reads the projects the selected Maven command builds. The
// checkout bounds the reactor, so a command run from a subdirectory may select
// ../pom.xml or declare sibling modules; project directories are relative to
// the returned checkout root.
func (t *Testdrive) javaReactor(args []string) (string, []maven.Project, error) {
	pom, err := maven.CommandPOM(args)
	if err != nil {
		return "", nil, err
	}
	root := t.checkoutRoot()
	directory, err := filepath.Rel(root, t.repositoryRoot)
	if err != nil {
		return "", nil, err
	}
	pom = filepath.Join(directory, pom)
	if !filepath.IsLocal(pom) {
		return "", nil, fmt.Errorf("maven POM must stay inside the repository: %s", pom)
	}
	projects, err := maven.ProjectsFrom(root, pom)
	return root, projects, err
}

// runJava runs the selected Maven command once in the disposable copy, with
// fresh build output, and reads its native Surefire/Failsafe reports.
func (t *Testdrive) runJava(ctx context.Context, output io.Writer, session *workspace.Session, agent string, spec runSpec) (run validationRun, runErr error) {
	run.Name, run.Role, run.Instrumented, run.ProbeMode = spec.Name, spec.Role, spec.Instrumented, spec.ProbeMode
	if spec.ProbePath != "" {
		run.Role = roleProbeControl
		if spec.Scenario.Feature != "" {
			run.Role = roleProbeFeature
		}
	}
	run.root, run.checkoutRoot, run.framework = t.repositoryRoot, t.checkoutRoot(), t.framework.Name()
	if !t.isolated {
		return run, errors.New("validation runs Maven only in a disposable copy of the repository")
	}
	command, args := t.executionCommand()
	checkout, projects, err := t.javaReactor(args)
	if err != nil {
		return run, err
	}
	// Reports from an earlier run would be read as this run's results. Only
	// the reactor's own target directories are build output; a directory named
	// target elsewhere is a project input.
	copyRoot, err := os.OpenRoot(checkout)
	if err != nil {
		return run, err
	}
	for _, project := range projects {
		if err := copyRoot.RemoveAll(filepath.Join(project.Directory, "target")); err != nil {
			return run, errors.Join(err, copyRoot.Close())
		}
	}
	if err := copyRoot.Close(); err != nil {
		return run, err
	}
	directory := filepath.Join(session.Directory(), spec.Name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return run, err
	}
	scenario := spec.Scenario
	if scenario.Feature == "" {
		scenario.Feature = "reporting-only"
	}
	// dd-java-agent treats a module without known tests as unusable for early
	// flake detection; the probe itself stays new.
	if scenario.Module != "" {
		scenario.KnownModules = []string{scenario.Module}
	}
	// dd-java-agent skips individual tests: it omits test_level and matches a
	// skippable test by its module, reported as test.bundle.
	scenario.SkippingLevel = "test"
	if scenario.Module != "" {
		scenario.SkippableConfigurations = map[string]string{"test.bundle": scenario.Module}
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
	if !spec.Instrumented {
		agent = ""
	}
	env := t.environment(agent, server.URL(), session.ID()+" "+spec.Name)
	configureScenarioEnvironment(env, scenario.Feature)
	env["DD_TRACE_AGENT_URL"] = server.URL()
	// ~/.mavenrc and /etc/mavenrc may attach agents or change options between runs.
	env["MAVEN_SKIP_RC"] = "true"
	if !spec.Instrumented {
		env["DD_CIVISIBILITY_ENABLED"] = "false"
	}
	if spec.ProbePath != "" {
		args = append(slices.Clone(args), javaProbeArguments...)
		env["DDTEST_PROBE_MODE"] = spec.ProbeMode
		env["DDTEST_PROBE_STATE"] = filepath.Join(directory, "probe-state")
	}
	run.Command = shellquote.Join(append([]string{command}, args...)...)
	_, _ = fmt.Fprintf(output, "Running %s...\n", spec.Name)
	data, commandErr := t.executor.CombinedOutput(ctx, command, args, env)
	run.commandOutput, run.commandError, run.ExitCode = string(data), commandErr, commandExitCode(commandErr)
	if commandErr != nil && spec.ProbePath == "" {
		writeCommandFailure(output, displayName(run.framework)+" "+spec.Name, data)
	}
	closeErr := server.Close()
	closed = true
	if closeErr != nil {
		return run, closeErr
	}
	if err := ctx.Err(); err != nil {
		return run, err
	}
	if run.Facts, err = server.Facts(); err != nil {
		return run, err
	}
	tests, err := maven.ReadReports(checkout, projects)
	// A skipped or disabled probe never starts, so no test case is reported.
	if errors.Is(err, maven.ErrNoTestCases) && (scenario.Feature == "skipping" || scenario.Feature == "disabled") {
		err = nil
	}
	switch {
	case err != nil:
		run.ResultError = err.Error()
	case spec.ProbePath == "" && maven.IncompleteBuild(run.ExitCode, projects, tests, string(data)):
		run.ResultError = javaIncompleteBuild
	}
	run.Tests = javaNativeTests(projects, tests, spec.ProbePath)
	if spec.Instrumented && spec.ProbePath == "" {
		run.Coverage = javaSourceCoverage(t.checkoutRoot(), run.Facts, run.Tests)
	}
	if spec.ProbePath != "" {
		count := 0
		if state, err := os.ReadFile(env["DDTEST_PROBE_STATE"]); err == nil {
			count = strings.Count(string(state), "run\n")
		}
		run.ProbeExecutions = &count
	}
	if run.ExitCode != 0 || run.ResultError != "" {
		run.Diagnostic = commandDiagnostic(data)
	}
	return run, nil
}

// javaModuleLabels maps each project directory to the name dd-java-agent
// reports as test.module: the
// project's <name>, or its artifactId. A name several projects share, or one
// built from properties, is replaced by the directory and cannot match.
func javaModuleLabels(projects []maven.Project) map[string]string {
	modules := maven.ModuleDirectories(projects)
	labels := map[string]string{}
	for _, project := range projects {
		label := project.Name
		if label == "" || strings.Contains(label, "${") {
			label = project.Artifact
		}
		if label == "" || modules[label] != project.Directory {
			label = project.Directory
		}
		labels[project.Directory] = label
	}
	return labels
}

// javaNativeTests identifies each native result as "<module> / <class>". The
// probe is identified by its source file, as the tracer reports it.
func javaNativeTests(projects []maven.Project, tests []maven.Test, probe string) []nativeTest {
	labels := javaModuleLabels(projects)
	native := make([]nativeTest, 0, len(tests))
	for _, test := range tests {
		item := nativeTest{File: labels[test.Module] + " / " + test.Suite, Name: test.Name, Status: test.Status, Failure: test.Failure}
		if probe != "" && test.Suite == javaProbeClass {
			name := javaTestName(test.Name)
			item.File, item.probe, item.telemetryName = probe, true, &name
		}
		native = append(native, item)
	}
	return native
}

// Surefire reports JUnit 5 methods with their signature and parameterized
// invocations with their index, as in check(TestInfo) or add(int, int)[2].
var javaSignature = regexp.MustCompile(`^([\pL_$][\pL\pN_$]*)\([^)]*\)(\[[0-9]+\])?$`)

func javaTestName(name string) string {
	if parts := javaSignature.FindStringSubmatch(name); parts != nil {
		return parts[1] + parts[2]
	}
	return name
}

// javaInvocationIndex is the [n] prefix of a JUnit 5 invocation's display name.
var javaInvocationIndex = regexp.MustCompile(`^\[[0-9]+\]`)

// javaTelemetryName adds the invocation index the tracer keeps in its
// parameters metadata, so one invocation cannot stand in for another.
func javaTelemetryName(test intake.Test) string {
	var parameters struct {
		Metadata struct {
			Name string `json:"test_name"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(test.Parameters), &parameters) == nil {
		if index := javaInvocationIndex.FindString(parameters.Metadata.Name); index != "" && !strings.HasSuffix(test.Name, index) {
			return test.Name + index
		}
	}
	return test.Name
}

// javaPluginSuffix is what the agent appends to a module name for each
// Surefire or Failsafe execution, as in "service maven-surefire-plugin default-test".
var javaPluginSuffix = regexp.MustCompile(` (maven-surefire-plugin|maven-failsafe-plugin) .*$`)

// verifyJavaTelemetry matches every native result to one reported test
// execution by module, class, method and status. Equal totals are not enough.
func verifyJavaTelemetry(run validationRun) verdict {
	if run.Facts.TestEventCount == 0 {
		return verdict{Status: "inconclusive", Reason: "The instrumented run sent no test events; matching native outcomes do not prove dd-java-agent instrumented the tests."}
	}
	if len(run.Facts.ConfigurationErrors) > 0 {
		return verdict{Status: "inconclusive", Reason: "dd-java-agent reported configuration errors: " + strings.Join(run.Facts.ConfigurationErrors, ", ")}
	}
	statuses := map[string]string{"passed": "pass", "failed": "fail", "skipped": "skip"}
	expected, observed := map[string]int{}, map[string]int{}
	for _, test := range run.Tests {
		module, suite, _ := strings.Cut(test.File, " / ")
		expected[fmt.Sprintf("module %q, class %q, test %q, status %q", module, suite, javaTestName(test.Name), statuses[test.Status])]++
	}
	for _, test := range run.Facts.Tests {
		module := javaPluginSuffix.ReplaceAllString(test.Module, "")
		name := javaTelemetryName(test)
		spans := map[uint64]bool{}
		for _, attempt := range test.Attempts {
			if attempt.Retry {
				return verdict{Status: "inconclusive", Code: "unexpected_retry", Reason: "Unexpected tracer-controlled retry telemetry in the reporting-only run."}
			}
			// A span sent twice is one execution.
			if attempt.SpanID != 0 && spans[attempt.SpanID] {
				continue
			}
			spans[attempt.SpanID] = true
			observed[fmt.Sprintf("module %q, class %q, test %q, status %q", module, test.Suite, name, attempt.Status)]++
		}
	}
	keys := maps.Clone(expected)
	maps.Copy(keys, observed)
	var differences []string
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		if difference := expected[key] - observed[key]; difference > 0 {
			differences = append(differences, fmt.Sprintf("Missing telemetry: %s (count %d)", key, difference))
		} else if difference < 0 {
			differences = append(differences, fmt.Sprintf("Unexpected telemetry: %s (count %d)", key, -difference))
		}
	}
	if len(differences) > 0 {
		return verdict{Status: "inconclusive", Reason: "Reported telemetry does not match the native Maven results.", Differences: differences[:min(len(differences), maxReportDifferences)]}
	}
	return verdict{}
}

// javaProbeClass is the probe's fully qualified class name.
const javaProbeClass = "ddtest.DdtestValidationTest"

// javaProbeArguments select only the probe: other modules and classes find
// no matching test, and integration tests are skipped.
var javaProbeArguments = []string{"-Dtest=" + javaProbeClass, "-Dsurefire.failIfNoSpecifiedTests=false", "-DfailIfNoTests=false", "-DskipITs"}

// javaProbeSource is a test class that records each execution and fails as
// DDTEST_PROBE_MODE asks. It compiles with Java 8.
func javaProbeSource(annotation string) string {
	return `package ddtest;

public class DdtestValidationTest {
    @` + annotation + `
    public void ddtestProbe() throws Exception {
        java.nio.file.Path state = java.nio.file.Paths.get(System.getenv("DDTEST_PROBE_STATE"));
        java.nio.file.Files.write(state, "run\n".getBytes(java.nio.charset.StandardCharsets.UTF_8), java.nio.file.StandardOpenOption.CREATE, java.nio.file.StandardOpenOption.APPEND);
        int attempts = java.nio.file.Files.readAllLines(state).size();
        String mode = System.getenv("DDTEST_PROBE_MODE");
        if ("fail".equals(mode) || ("fail-once".equals(mode) && attempts == 1)) {
            throw new AssertionError("ddtest intentional failure");
        }
    }
}
`
}

var javaProbeAnnotations = map[string]string{"junit4": "org.junit.Test", "junit5": "org.junit.jupiter.api.Test"}

// runJavaFeatures writes the probe into a module whose tests ran in the
// baseline, inside the copy, then runs the shared feature controls.
func (t *Testdrive) runJavaFeatures(ctx context.Context, output io.Writer, session *workspace.Session, agent string, baseline validationRun, result *validationResult) (runErr error) {
	// Every probe rebuilds the same reactor, so it would fail the same way.
	if baseline.ResultError != "" {
		unavailableFeatures(result, "Feature probes were not run: the uninstrumented Maven build did not produce usable results.")
		return nil
	}
	_, args := t.executionCommand()
	checkout, projects, err := t.javaReactor(args)
	if err != nil {
		return err
	}
	module, ok := javaProbeModule(projects, baseline.Tests, t.framework.Name())
	if !ok {
		unavailableFeatures(result, "No module of the selected Maven command ran native tests, so the controlled probe has nowhere to run.")
		return nil
	}
	path := filepath.Join(module, "src", "test", "java", "ddtest", "DdtestValidationTest.java")
	copyRoot, err := os.OpenRoot(checkout)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, copyRoot.Remove(path), copyRoot.Close()) }()
	if err := copyRoot.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// The copy owns the probe: refuse to overwrite a project file.
	probe, err := copyRoot.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("write the validation probe: %w", err)
	}
	_, writeErr := probe.WriteString(javaProbeSource(javaProbeAnnotations[t.framework.Name()]))
	if err := errors.Join(writeErr, probe.Close()); err != nil {
		return err
	}
	probePath := filepath.Join(checkout, path)
	return runProbeFeatures(func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
		return t.runJava(ctx, output, session, agent, runSpec{Name: name, Instrumented: true, Scenario: scenario, ProbePath: probePath, ProbeMode: mode})
	}, result)
}

// javaProbeModule picks the first module, in reactor order, whose tests ran
// in the baseline, preferring one that declares the selected framework.
func javaProbeModule(projects []maven.Project, tests []nativeTest, framework string) (string, bool) {
	labels := javaModuleLabels(projects)
	executed := map[string]bool{}
	for _, test := range tests {
		module, _, _ := strings.Cut(test.File, " / ")
		executed[module] = true
	}
	candidates := slices.DeleteFunc(slices.Clone(projects), func(project maven.Project) bool { return !executed[labels[project.Directory]] })
	if len(candidates) == 0 {
		return "", false
	}
	declared := slices.IndexFunc(candidates, func(project maven.Project) bool { return slices.Contains(project.Frameworks(), framework) })
	return candidates[max(declared, 0)].Directory, true
}

// checkJavaPreflight inspects the JDK Maven runs on, the reactor the selected
// command builds and the CI JDK and agent selections of this configuration,
// without running Maven or project code.
func (t *Testdrive) checkJavaPreflight(_ context.Context, output io.Writer, result *validationResult) error {
	check := &preflightCheck{Command: shellquote.Join(append([]string{t.command}, t.args...)...), Verdict: verdict{Status: "inconclusive", Reason: "The Maven project has not been inspected."}}
	result.Preflight = check
	info := javaEnvironmentInfo(t.configurationEnvironment)
	if info.Version == "" {
		check.Verdict.Reason = "No JDK was found: set JAVA_HOME to the JDK the project builds with, or put its java on PATH."
		return errors.New(check.Verdict.Reason)
	}
	check.Runtime, check.Executable = info.Version, info.Executable
	_, projects, err := t.javaReactor(t.args)
	if err != nil {
		check.Verdict.Reason = "The selected command's Maven reactor cannot be read: " + err.Error()
		return fmt.Errorf("inspect Maven reactor: %w", err)
	}
	ci, err := t.javaCIRuntime(info.Version)
	if err != nil {
		return err
	}
	result.CIRuntime = ci
	reason := fmt.Sprintf("Java %s; the selected command builds %d Maven %s. Paired execution and feature controls are still required.", info.Version, len(projects), plural(len(projects), "project", "projects"))
	if !slices.Contains(maven.Frameworks(projects), t.framework.Name()) {
		reason += " No POM in the repository declares " + displayName(t.framework.Name()) + "; it may come from a parent or BOM outside the repository."
	}
	if others := javaOtherFrameworkModules(projects, t.framework.Name()); len(others) > 0 {
		reason += " The build also runs module(s) " + strings.Join(others, ", ") + " of other frameworks; their tests are compared too, and feature probes run in a " + displayName(t.framework.Name()) + " module."
	}
	check.Verdict = verdict{Status: "compatible", Reason: reason}
	_, _ = fmt.Fprintf(output, "%s preflight: %s — %s\n", displayName(t.framework.Name()), check.Verdict.Status, check.Verdict.Reason)
	return nil
}

// javaOtherFrameworkModules lists the reactor modules that declare test
// frameworks, none of them framework.
func javaOtherFrameworkModules(projects []maven.Project, framework string) []string {
	var modules []string
	for _, project := range projects {
		if declared := project.Frameworks(); len(declared) > 0 && !slices.Contains(declared, framework) {
			modules = append(modules, project.Directory)
		}
	}
	return modules
}

// javaCIRuntime checks the CI rows of this configuration. A row is compatible
// only when this run validates its JDK major and agent release; other JDKs are
// validated by running testdrive on them. Configuration-only checks run
// nothing, so they certify no row.
func (t *Testdrive) javaCIRuntime(localJava string) (*onboard.RuntimeCheck, error) {
	ci := onboard.JavaCIConfiguration(t.checkoutRoot(), t.framework.Name())
	if len(ci.Jobs) == 0 {
		return &ci, nil
	}
	locations, err := t.javaRuntimeLocations()
	if err != nil {
		return nil, err
	}
	ci.Jobs = slices.DeleteFunc(ci.Jobs, func(job onboard.RuntimeFinding) bool { return !slices.Contains(locations, job.Location) })
	if len(ci.Jobs) == 0 {
		ci.Status, ci.Reason = "inconclusive", "No CI consumer matches this Maven command and working directory; validate the discovered configurations with --all."
		return &ci, nil
	}
	tracer := t.tracerVersion
	if t.projectTracer != "" {
		tracer, _ = platform.JavaAgentVersion(t.projectTracer)
	}
	for i, job := range ci.Jobs {
		if job.Status != "not checked" {
			continue
		}
		ci.Jobs[i].Status = "inconclusive"
		switch {
		case t.checkOnly:
			ci.Jobs[i].Reason = "Full validation runs dd-java-agent " + job.TracerRequested + " on this host's Java " + localJava + "; configuration-only checks validate no JDK."
		case javaMajor(job.Runtime) != javaMajor(localJava):
			ci.Jobs[i].Reason = "This run validates Java " + localJava + ". Validate Java " + job.Runtime + " by running testdrive with JAVA_HOME set to that JDK."
		case job.TracerRequested != tracer:
			ci.Jobs[i].Reason = "CI pins dd-java-agent " + job.TracerRequested + ", but this run validates " + cmp.Or(tracer, "another release") + "; rerun without --tracer-version to validate the CI pin."
		default:
			ci.Jobs[i].Status, ci.Jobs[i].Reason = "compatible", "dd-java-agent "+tracer+" is validated on this host's Java "+localJava+"."
		}
	}
	summarizeJavaRuntime(&ci)
	return &ci, nil
}

// javaRuntimeLocations are the CI locations of the configuration being run:
// those --all supplies, or the CI consumers of this command and directory.
func (t *Testdrive) javaRuntimeLocations() ([]string, error) {
	if len(t.configurationLocations) > 0 {
		return t.configurationLocations, nil
	}
	root := t.checkoutRoot()
	directory, err := filepath.Rel(root, t.repositoryRoot)
	if err != nil {
		return nil, err
	}
	scope, err := onboard.DiscoverValidationScope(root, t.framework.Name())
	if err != nil {
		return nil, err
	}
	command := shellquote.Join(append([]string{t.command}, t.args...)...)
	var locations []string
	for _, entry := range scope.Commands {
		if entry.Command == command && filepath.Clean(entry.Directory) == directory {
			locations = append(locations, entry.Locations...)
		}
	}
	return locations, nil
}

// javaMajor is a Java version's major release, reading 1.8 as 8.
func javaMajor(version string) string {
	major, rest, _ := strings.Cut(version, ".")
	if major == "1" {
		major, _, _ = strings.Cut(rest, ".")
	}
	major, _, _ = strings.Cut(major, "_")
	return major
}

func summarizeJavaRuntime(ci *onboard.RuntimeCheck) {
	if len(ci.Jobs) == 0 {
		return
	}
	ci.Status, ci.Reason = "compatible", "Every CI JDK and dd-java-agent selection of this configuration is validated by this run. Actual CI execution remains untested."
	instrumented := false
	for _, job := range ci.Jobs {
		instrumented = instrumented || job.Status == "compatible"
		if job.Status != "compatible" && job.Status != "excluded" {
			ci.Status, ci.Reason = "inconclusive", job.Reason
			return
		}
	}
	if !instrumented {
		ci.Status, ci.Reason = "inconclusive", "Every CI entry excludes instrumentation; at least one compatible instrumented CI consumer is required."
	}
}

// mergeJavaRuntimeChecks folds one configuration's checked rows into the
// --all inventory.
func mergeJavaRuntimeChecks(current, next *onboard.RuntimeCheck) *onboard.RuntimeCheck {
	if current == nil {
		return next
	}
	for _, finding := range next.Jobs {
		index := slices.IndexFunc(current.Jobs, func(job onboard.RuntimeFinding) bool {
			return finding.Location != "" && job.Location == finding.Location
		})
		if index >= 0 {
			current.Jobs[index] = finding
		} else {
			current.Jobs = append(current.Jobs, finding)
		}
	}
	summarizeJavaRuntime(current)
	return current
}

// javaSourceCoverage requires per-test coverage of repository application
// classes; Maven test sources and build output do not qualify.
func javaSourceCoverage(root string, facts intake.Facts, native []nativeTest) *coverageSummary {
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	applicationFiles := func(files []string) []string {
		var kept []string
		for _, file := range files {
			local, _ := repositoryCoverageFile(root, file)
			if strings.Contains("/"+filepath.ToSlash(local), "/src/test/") || strings.Contains("/"+filepath.ToSlash(local), "/target/") {
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
		coverage.Reason = "Java coverage includes existing repository application sources; test-source coverage alone does not qualify."
	case "invalid":
		coverage.Reason = "Java coverage names missing or outside-repository files; inspect invalid_files in the report."
	default:
		coverage.Reason = "No repository application source coverage observed. dd-java-agent reports the classes each test runs; check that tests execute application classes from the repository. Test-source coverage alone is insufficient."
	}
	return coverage
}
