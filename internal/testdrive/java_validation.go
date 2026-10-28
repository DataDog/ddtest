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
	root, pom, err := t.javaCommandPOM(args)
	if err != nil {
		return "", nil, err
	}
	projects, err := maven.ProjectsFrom(root, pom)
	return root, projects, err
}

// javaCommandPOM returns the checkout and the POM, relative to it, that the
// selected Maven command builds once Maven adds its implicit arguments.
func (t *Testdrive) javaCommandPOM(args []string) (string, string, error) {
	implicit, err := t.javaImplicitArgs(args)
	if err != nil {
		return "", "", err
	}
	pom, err := maven.CommandPOM(append(implicit, args...))
	if err != nil {
		return "", "", err
	}
	root := t.checkoutRoot()
	directory, err := filepath.Rel(root, t.repositoryRoot)
	if err != nil {
		return "", "", err
	}
	pom = filepath.Join(directory, pom)
	if !filepath.IsLocal(pom) {
		return "", "", fmt.Errorf("maven POM must stay inside the repository: %s", pom)
	}
	return root, pom, nil
}

// javaImplicitArgs are the arguments Maven adds to every command: those of the
// project's .mvn/maven.config, and those Maven 3.9+ reads from MAVEN_ARGS, with
// a configuration's own value first, split on whitespace as the mvn script
// does. Goals there that install, publish or run no tests are refused, since
// they would run with every validated command. So is a Datadog agent that
// .mvn/jvm.config attaches to every build, baseline included.
func (t *Testdrive) javaImplicitArgs(args []string) ([]string, error) {
	directory, err := t.javaMavenDirectory(args)
	if err != nil {
		return nil, err
	}
	var config []string
	if directory != "" {
		if config, err = readMavenConfig(filepath.Join(directory, "maven.config")); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(filepath.Join(directory, "jvm.config"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if options := string(data); withoutDatadogAgent(options) != options {
			return nil, errors.New(".mvn/jvm.config attaches dd-java-agent to every Maven build, so validation cannot run the build without it; remove the -javaagent option from .mvn/jvm.config, as CI attaches the agent with the GitHub action")
		}
	}
	value, configured := t.configurationEnvironment["MAVEN_ARGS"]
	if !configured {
		value = os.Getenv("MAVEN_ARGS")
	}
	var implicit []string
	for _, source := range []struct {
		name string
		args []string
	}{{".mvn/maven.config", config}, {"MAVEN_ARGS", strings.Fields(value)}} {
		if _, omitted := maven.TestInvocation(source.args); len(omitted) > 0 {
			goals := strings.Join(omitted, ", ")
			return nil, fmt.Errorf("%s adds %s to every Maven command, but local validation runs only the Maven test lifecycle up to verify and never installs or publishes artifacts; remove %s from %s", source.name, goals, goals, source.name)
		}
		implicit = append(implicit, source.args...)
	}
	return implicit, nil
}

// javaMavenDirectory returns the .mvn directory the mvn launcher finds: the
// nearest one up to the checkout, from what a separate -f or --file argument
// names, the only forms the launcher reads (a directory, or a POM's
// directory), else from the working directory. It is empty when there is none.
func (t *Testdrive) javaMavenDirectory(args []string) (string, error) {
	start := t.repositoryRoot
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-f" || args[i] == "--file" {
			if filepath.IsAbs(args[i+1]) {
				return "", fmt.Errorf("maven POM must be a repository path: %s", args[i+1])
			}
			start = filepath.Join(t.repositoryRoot, args[i+1])
			if info, err := os.Stat(start); err != nil || !info.IsDir() {
				start = filepath.Dir(start)
			}
		}
	}
	root := t.checkoutRoot()
	for directory := start; ; directory = filepath.Dir(directory) {
		if relative, err := filepath.Rel(root, directory); err != nil || !filepath.IsLocal(relative) {
			return "", nil
		}
		if info, err := os.Stat(filepath.Join(directory, ".mvn")); err == nil && info.IsDir() {
			return filepath.Join(directory, ".mvn"), nil
		}
		if directory == root {
			return "", nil
		}
	}
}

// readMavenConfig reads the arguments of a maven.config, skipping blank and
// # comment lines, as Maven 3.9 does, and splitting the others on whitespace,
// as earlier releases do.
func readMavenConfig(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var args []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); !strings.HasPrefix(line, "#") {
			args = append(args, strings.Fields(line)...)
		}
	}
	return args, err
}

// runJava runs the selected Maven command once in the disposable copy, with
// fresh build output, and reads its native Surefire/Failsafe reports. A probe
// run adds probeArgs, which select only the probe.
func (t *Testdrive) runJava(ctx context.Context, output io.Writer, session *workspace.Session, agent string, spec runSpec, probeArgs []string) (run validationRun, runErr error) {
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
		// The probe only needs Surefire: checks after the test phase, such as
		// license headers or coverage gates, would judge the probe file itself.
		args = append(maven.UpToTest(args), probeArgs...)
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
	// A skipped or disabled probe never starts, so no test case is reported;
	// older Surefire releases write no report for a skipped class at all.
	if (errors.Is(err, maven.ErrNoTestCases) || errors.Is(err, maven.ErrNoReports)) && (scenario.Feature == "skipping" || scenario.Feature == "disabled") {
		err = nil
	}
	switch {
	case err != nil:
		run.ResultError = err.Error()
	case spec.ProbePath == "" && maven.IncompleteBuild(run.ExitCode, projects, tests, string(data)):
		run.ResultError = javaIncompleteBuild
	}
	run.Tests = javaNativeTests(projects, tests, run.framework, spec.ProbePath != "")
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

// javaNativeTests identifies each native result as "<module> / <class>", and
// in a probe run marks the probe.
func javaNativeTests(projects []maven.Project, tests []maven.Test, framework string, probe bool) []nativeTest {
	labels := javaModuleLabels(projects)
	native := make([]nativeTest, 0, len(tests))
	for _, test := range tests {
		item := nativeTest{File: labels[test.Module] + " / " + test.Suite, Name: test.Name, Status: test.Status, Failure: test.Failure}
		if probe && test.Suite == javaProbeClass {
			name := javaTestName(test.Name, framework)
			item.probe, item.telemetryName = true, &name
		}
		native = append(native, item)
	}
	return native
}

// Surefire reports JUnit 5 methods with their signature and parameterized
// invocations with their index, as in check(TestInfo) or add(int, int)[2].
var javaSignature = regexp.MustCompile(`^([\pL_$][\pL\pN_$]*)\([^)]*\)(\[[0-9]+\])?$`)

// testNGInvocation is how Surefire reports a TestNG data-provider invocation,
// as in parse[a, 1](2); dd-java-agent reports the method name.
var testNGInvocation = regexp.MustCompile(`^([\pL_$][\pL\pN_$]*)\[.*\]\([0-9]+\)$`)

// javaTestName is how the agent names a test Surefire reports for framework.
// Other frameworks, such as Spock, report any name as written.
func javaTestName(name, framework string) string {
	if parts := javaSignature.FindStringSubmatch(name); parts != nil && (framework == "junit4" || framework == "junit5") {
		return parts[1] + parts[2]
	}
	if parts := testNGInvocation.FindStringSubmatch(name); parts != nil && framework == "testng" {
		return parts[1]
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

// javaFeatureURI is the feature file the agent puts before a Cucumber
// feature's name in its suite, as in classpath:calc.feature:Calculator;
// Surefire reports the name alone.
var javaFeatureURI = regexp.MustCompile(`^(?:classpath|file):.*?\.feature:`)

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
		expected[fmt.Sprintf("module %q, class %q, test %q, status %q", module, suite, javaTestName(test.Name, run.framework), statuses[test.Status])]++
	}
	for _, test := range run.Facts.Tests {
		module := javaPluginSuffix.ReplaceAllString(test.Module, "")
		suite := javaFeatureURI.ReplaceAllString(test.Suite, "")
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
			observed[fmt.Sprintf("module %q, class %q, test %q, status %q", module, suite, name, attempt.Status)]++
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

// spockProbeSource is javaProbeSource as a Spock specification. The failure is
// thrown from a setup block: a Spock condition would report other text.
const spockProbeSource = `package ddtest

import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.nio.file.Paths
import java.nio.file.StandardOpenOption

class DdtestValidationTest extends spock.lang.Specification {
    def ddtestProbe() {
        setup:
        def state = Paths.get(System.getenv("DDTEST_PROBE_STATE"))
        Files.write(state, "run\n".getBytes(StandardCharsets.UTF_8), StandardOpenOption.CREATE, StandardOpenOption.APPEND)
        def attempts = Files.readAllLines(state).size()
        def mode = System.getenv("DDTEST_PROBE_MODE")
        if (mode == "fail" || (mode == "fail-once" && attempts == 1)) {
            throw new AssertionError("ddtest intentional failure")
        }
    }
}
`

// javaProbeFile is one file of a probe, relative to the checkout.
type javaProbeFile struct{ path, source string }

// javaProbe is a framework's controlled probe in one module: the files to
// write, and the Maven arguments that select only it. Every probe reports the
// class javaProbeClass.
type javaProbe struct {
	files []javaProbeFile
	args  []string
}

// javaProbeSources are the probe sources of frameworks whose probe is one
// test class, relative to a module.
var javaProbeSources = map[string]javaProbeFile{
	"junit4": {filepath.Join("src", "test", "java", "ddtest", "DdtestValidationTest.java"), javaProbeSource("org.junit.Test")},
	"junit5": {filepath.Join("src", "test", "java", "ddtest", "DdtestValidationTest.java"), javaProbeSource("org.junit.jupiter.api.Test")},
	"testng": {filepath.Join("src", "test", "java", "ddtest", "DdtestValidationTest.java"), javaProbeSource("org.testng.annotations.Test")},
	"spock":  {filepath.Join("src", "test", "groovy", "ddtest", "DdtestValidationTest.groovy"), spockProbeSource},
}

// javaProbeFrameworks are the frameworks with paired validation; Surefire
// runs and reports each of them.
var javaProbeFrameworks = []string{"junit4", "junit5", "testng", "spock", "cucumber-java"}

// javaProbeFor returns framework's probe in module, or why it has none.
func javaProbeFor(checkout, module, framework string) (javaProbe, string, error) {
	if framework == "cucumber-java" {
		return javaCucumberProbe(checkout, module)
	}
	source := javaProbeSources[framework]
	return javaProbe{files: []javaProbeFile{{filepath.Join(module, source.path), source.source}}, args: javaProbeArguments}, "", nil
}

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
	probe, reason, err := javaProbeFor(checkout, module, t.framework.Name())
	if err != nil {
		return err
	}
	if reason != "" {
		unavailableFeatures(result, reason)
		return nil
	}
	copyRoot, err := os.OpenRoot(checkout)
	if err != nil {
		return err
	}
	var written []string
	defer func() {
		for _, path := range written {
			runErr = errors.Join(runErr, copyRoot.Remove(path))
		}
		runErr = errors.Join(runErr, copyRoot.Close())
	}()
	for _, file := range probe.files {
		if err := copyRoot.MkdirAll(filepath.Dir(file.path), 0700); err != nil {
			return err
		}
		// The copy owns the probe: refuse to overwrite a project file.
		created, err := copyRoot.OpenFile(file.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("write the validation probe: %w", err)
		}
		written = append(written, file.path)
		_, writeErr := created.WriteString(file.source)
		if err := errors.Join(writeErr, created.Close()); err != nil {
			return err
		}
	}
	probePath := filepath.Join(checkout, probe.files[0].path)
	return runProbeFeatures(func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
		return t.runJava(ctx, output, session, agent, runSpec{Name: name, Instrumented: true, Scenario: scenario, ProbePath: probePath, ProbeMode: mode}, probe.args)
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
	ci, substitutions, err := t.javaCIRuntime(info.Version)
	if err != nil {
		return err
	}
	result.CIRuntime = ci
	for _, substitution := range substitutions {
		result.addSubstitution(substitution)
		_, _ = fmt.Fprintf(output, "Runtime substitution: %s\n", substitution.Reason)
	}
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
// when this run validates its agent release on its JDK major, or on the local
// JDK as a substitute, which completes only once the user accepts it; one
// substitution is returned per other CI JDK major. Configuration-only checks
// run nothing, so they certify no row.
func (t *Testdrive) javaCIRuntime(localJava string) (*onboard.RuntimeCheck, []runtimeSubstitution, error) {
	ci := onboard.JavaCIConfiguration(t.checkoutRoot(), t.framework.Name(), t.selectedSite)
	if len(ci.Jobs) == 0 {
		return &ci, nil, nil
	}
	locations, err := t.javaRuntimeLocations()
	if err != nil {
		return nil, nil, err
	}
	ci.Jobs = slices.DeleteFunc(ci.Jobs, func(job onboard.RuntimeFinding) bool { return !slices.Contains(locations, job.Location) })
	if len(ci.Jobs) == 0 {
		ci.Status, ci.Reason = "inconclusive", "No CI consumer matches this Maven command and working directory; validate the discovered configurations with --all."
		return &ci, nil, nil
	}
	tracer := t.tracerVersion
	if t.projectTracer != "" {
		tracer, _ = platform.JavaAgentVersion(t.projectTracer)
	}
	var substitutions []runtimeSubstitution
	for i, job := range ci.Jobs {
		if job.Status != "not checked" {
			continue
		}
		ci.Jobs[i].Status = "inconclusive"
		switch {
		case t.checkOnly:
			ci.Jobs[i].Reason = "Full validation runs dd-java-agent " + job.TracerRequested + " on this host's Java " + localJava + "; configuration-only checks validate no JDK."
		case job.TracerRequested != tracer:
			ci.Jobs[i].Reason = "CI pins dd-java-agent " + job.TracerRequested + ", but this run validates " + cmp.Or(tracer, "another release") + "; rerun without --tracer-version to validate the CI pin."
		case javaMajor(job.Runtime) != javaMajor(localJava):
			ci.Jobs[i].Status, ci.Jobs[i].Reason = "compatible", "dd-java-agent "+tracer+" is validated on this host's Java "+localJava+" as a substitute for Java "+job.Runtime+"."
			if !slices.ContainsFunc(substitutions, func(existing runtimeSubstitution) bool { return javaMajor(existing.CI) == javaMajor(job.Runtime) }) {
				substitutions = append(substitutions, runtimeSubstitution{
					Source: job.Location, CI: job.Runtime, Local: localJava, Accepted: t.acceptSubstituteRuntime, Runtime: "Java",
					Remedy: "set JAVA_HOME to an existing Java " + job.Runtime + " JDK",
					Reason: "Local Java " + localJava + " runs dd-java-agent " + tracer + " in place of CI's Java " + job.Runtime + "; the agent release, the Maven command and the suite are the same.",
				})
			}
		default:
			ci.Jobs[i].Status, ci.Jobs[i].Reason = "compatible", "dd-java-agent "+tracer+" is validated on this host's Java "+localJava+"."
		}
	}
	summarizeJavaRuntime(&ci)
	return &ci, substitutions, nil
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

// javaTestSource matches files of a test source tree, such as src/test,
// src/integrationTest, src/testFixtures or the invoker's src/it, and build
// output; none of them is application code.
var javaTestSource = regexp.MustCompile(`(^|/)(src/(it|[^/]*[Tt]est[^/]*)|target)/`)

// javaSourceCoverage requires per-test coverage of repository application
// classes; test sources and build output do not qualify.
func javaSourceCoverage(root string, facts intake.Facts, native []nativeTest) *coverageSummary {
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	applicationFiles := func(files []string) []string {
		var kept []string
		for _, file := range files {
			local, _ := repositoryCoverageFile(root, file)
			if javaTestSource.MatchString(filepath.ToSlash(local)) {
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
