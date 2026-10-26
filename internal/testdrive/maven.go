// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/kballard/go-shellquote"
)

func (t *Testdrive) previewMaven(output io.Writer) {
	_, _ = fmt.Fprintf(output, "DDTest found Java / Maven and %s.\nSelected command: %s\n", t.framework.Name(), shellquote.Join(append([]string{t.command}, t.args...)...))
	if t.checkOnly {
		_, _ = fmt.Fprintln(output, "Inspect Maven configuration and the local Java runtime; no tests or tracer download. Save configuration findings without certifying earlier execution.")
		return
	}
	_, _ = fmt.Fprintln(output, "Run the selected Maven command in fresh temporary repository copies, first without instrumentation and then with reporting-only dd-java-agent. Compare native XML results and telemetry; retain HTML/JSON reports and remove owned copies and probes. Maven may populate its normal local dependency cache. Prepare external services before validation.")
	_, _ = fmt.Fprintln(output, "Framework selection does not filter Maven's tests. Other profiles, Gradle/SBT/Bazel, CI runtime compatibility and real Datadog processing are not certified by a local command.")
	if t.allConfigurations {
		_, _ = fmt.Fprintln(output, "--all is not supported for Maven; explicitly select each CI Maven command/profile with --command.")
	}
}

func (t *Testdrive) runMaven(ctx context.Context, output io.Writer) (runErr error) {
	history, err := preserveMavenFailure(t.repositoryRoot, t.checkOnly)
	if err != nil {
		return fmt.Errorf("preserve previous failed Maven comparison before starting another run: %w", err)
	}
	result := validationResult{PreviousFailure: history, Framework: t.framework.Name(), Tracer: t.tracerLabel, CheckOnly: t.checkOnly, Session: t.session.ID(), Compatibility: verdict{Status: "not exercised", Reason: "Maven validation has not executed."},
		CISelection: &verdict{Status: "inconclusive", Reason: "This Maven-first adapter validates the selected local command. Review CI Java/tracer versions and every Maven profile separately; CI configuration is not certified."}}
	defer func() {
		if runErr != nil {
			result.Error = runErr.Error()
		}
		runErr = errors.Join(runErr, finishValidation(output, t.repositoryRoot, result))
	}()
	if t.allConfigurations {
		return fmt.Errorf("maven --all is not implemented; validate each existing CI Maven command/profile with --command")
	}
	if raw := settings.GetCommand(); raw != "" {
		words, err := shellquote.Split(raw)
		if err != nil || len(words) == 0 {
			return fmt.Errorf("invalid Maven command quoting")
		}
	}
	if err := maven.ValidateTestdriveCommand(t.command, t.args); err != nil {
		return err
	}
	projects, err := maven.Projects(t.repositoryRoot)
	if err != nil {
		return err
	}
	result.MavenCoverage = discoverMavenCoverage(t.repositoryRoot)
	for _, project := range projects {
		_, _ = fmt.Fprintf(output, "Maven module %s (%s): %s\n", project.Directory, project.Artifact, strings.Join(project.Frameworks(), ", "))
	}
	if err := t.platform.SanityCheck(ctx); err != nil {
		return fmt.Errorf("java runtime unavailable: %w", err)
	}
	if err := t.session.create(); err != nil {
		return err
	}
	defer func() {
		err := t.session.Close()
		result.Cleanup = &verdict{Status: "passed", Reason: "Owned Maven copies, probes and tracer installation removed."}
		if err != nil {
			result.Cleanup = &verdict{Status: "failed", Reason: err.Error()}
		}
		runErr = errors.Join(runErr, err)
	}()
	if t.checkOnly {
		unavailableFeatures(&result, "Configuration checks only; native results and controlled feature probes were not executed.")
		return nil
	}
	agent := t.projectTracer
	result.TracerSource = "project DD_TRACER_FOLDER"
	if agent == "" {
		installation, err := t.platform.InstallTestdriveTracer(ctx, platform.TracerOptions{Directory: t.session.Directory(), Version: t.tracerVersion})
		if err != nil {
			return err
		}
		agent = installation.Path
		result.TracerSource = "temporary installation"
	}
	version, err := platform.JavaAgentVersion(agent)
	if err != nil {
		return err
	}
	result.Tracer = "dd-java-agent@" + version
	baseline, err := t.mavenRun(ctx, output, "baseline", "", intake.Scenario{}, "", nil)
	if err != nil {
		return err
	}
	result.Runs = append(result.Runs, baseline.summary())
	reporting, err := t.mavenRun(ctx, output, "reporting-only", agent, intake.Scenario{}, "", nil)
	if err != nil {
		return err
	}
	result.Runs = append(result.Runs, reporting.summary())
	native := compareMavenNative(baseline, reporting)
	telemetry := verdict{Status: "not exercised", Code: "native_comparison_blocked", Reason: "Establish matching native outcomes before telemetry reconciliation."}
	if native.Status == "compatible" {
		telemetry = verifyMavenTelemetry(reporting)
	}
	result.NativeComparison, result.TelemetryComparison = &native, &telemetry
	result.Compatibility = native
	if native.Status == "compatible" {
		result.Compatibility = telemetry
	}
	result.MavenCoverage.Modules = reporting.mavenCoverage
	if err := t.writeHTMLReport(output, &result, reporting); err != nil {
		return err
	}
	if result.Compatibility.Status != "compatible" {
		unavailableFeatures(&result, mavenFeatureBlockReason(result))
		return nil
	}
	return t.mavenFeatures(ctx, output, agent, projects, reporting, &result)
}

// Maven JVM options use double-quoted paths, not shell evaluation. Preserve all
// unrelated agents and options. Remove only JARs verified as Datadog agents.
func mavenJVMEnvironment(agent string) (map[string]string, error) {
	env := map[string]string{}
	for _, key := range []string{"MAVEN_OPTS", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS"} {
		words, err := shellquote.Split(os.Getenv(key))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", key, err)
		}
		var kept []string
		for _, word := range words {
			if path, ok := strings.CutPrefix(word, "-javaagent:"); ok {
				path, _, _ = strings.Cut(path, "=")
				if _, err := platform.JavaAgentVersion(path); err == nil {
					continue
				}
			}
			if strings.ContainsAny(word, " \t\r\n\"") {
				word = strconv.Quote(word)
			}
			kept = append(kept, word)
		}
		env[key] = strings.Join(kept, " ")
	}
	if agent != "" {
		// Maven expands MAVEN_OPTS as shell words, so literal surrounding quotes
		// become part of the Java argument. JVM-parsed JAVA_TOOL_OPTIONS supports
		// paths containing spaces and is inherited by Maven's test forks.
		if strings.ContainsAny(agent, " \t\r\n") {
			env["JAVA_TOOL_OPTIONS"] = strings.TrimSpace(env["JAVA_TOOL_OPTIONS"] + " " + strconv.Quote("-javaagent:"+agent))
		} else {
			env["MAVEN_OPTS"] = strings.TrimSpace(env["MAVEN_OPTS"] + " -javaagent:" + agent)
		}
	}
	return env, nil
}

// Every run starts with the same source tree and fresh build output. The
// original target directories are never read as validation evidence or removed.
func (t *Testdrive) mavenRun(ctx context.Context, output io.Writer, name, agent string, scenario intake.Scenario, probeMode string, probe *mavenProbe) (run validationRun, runErr error) {
	role := roleProbeFeature
	if probe == nil {
		role = roleBaseline
		if agent != "" {
			role = roleReporting
		}
	} else if scenario.Feature == "" {
		role = roleProbeControl
	}
	run = validationRun{Role: role, Name: name, Instrumented: agent != "", ProbeMode: probeMode, framework: t.framework.Name(), root: t.repositoryRoot}
	directory, err := os.MkdirTemp(t.session.Directory(), "maven-")
	if err != nil {
		return run, err
	}
	workspace := filepath.Join(directory, "repository")
	if err := copyConfiguration(ctx, t.repositoryRoot, workspace, true); err != nil {
		return run, err
	}
	if err := filepath.WalkDir(workspace, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "target" {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			return filepath.SkipDir
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		return nil
	}); err != nil {
		return run, err
	}
	command, args := t.command, slices.Clone(t.args)
	if filepath.IsAbs(command) {
		if relative, err := filepath.Rel(t.repositoryRoot, command); err == nil && filepath.IsLocal(relative) {
			command = filepath.Join(workspace, relative)
		}
	}
	if probe != nil {
		if err := probe.write(workspace); err != nil {
			return run, err
		}
		args = append(args, probe.args...)
	}
	scenario.CollectEvents = probe != nil
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
	env := testEnvironment(server.URL(), t.session.ID())
	jvm, err := mavenJVMEnvironment(agent)
	if err != nil {
		return run, err
	}
	maps.Copy(env, jvm)
	if agent != "" && t.framework.Name() == "scalatest" {
		// Maven's agent auto-configuration targets Surefire/Failsafe/Tycho,
		// not scalatest-maven-plugin's fork. Let every JVM inherit the agent.
		base, err := mavenJVMEnvironment("")
		if err != nil {
			return run, err
		}
		env["MAVEN_OPTS"] = base["MAVEN_OPTS"]
		env["JAVA_TOOL_OPTIONS"] = strings.TrimSpace(base["JAVA_TOOL_OPTIONS"] + " " + strconv.Quote("-javaagent:"+agent))
	}
	configureScenarioEnvironment(env, scenario.Feature)
	env["DD_CIVISIBILITY_CODE_COVERAGE_AGENTLESS_URL"] = server.URL()
	env["DD_TRACE_AGENT_URL"] = server.URL()
	env["DD_CIVISIBILITY_ENABLED"] = strconv.FormatBool(agent != "")
	env["DD_TRACE_ENABLED"] = "false"
	env["DDTEST_PROBE_MODE"] = probeMode
	env["DDTEST_PROBE_COUNTER"] = filepath.Join(directory, "probe-count")
	// Build plugins must not install a compiler extension into the baseline's
	// output. Each JVM is instrumented through Maven's native tracer integration.
	env["DD_CIVISIBILITY_COMPILER_PLUGIN_AUTO_CONFIGURATION_ENABLED"] = "false"
	run.Command = shellquote.Join(append([]string{command}, args...)...)
	run.NativeCommand = run.Command
	_, _ = fmt.Fprintf(output, "Running Maven %s: %s\n", name, run.Command)
	// Use an executor with an explicit working directory instead of changing
	// the parent process's working directory during concurrent intake handling.
	executor := &ext.DefaultCommandExecutor{Timeout: t.commandTimeout}
	data, commandErr := executor.CombinedOutputInDirectory(ctx, workspace, command, args, env)
	run.commandOutput, run.commandError = string(data), commandErr
	run.ExitCode = commandExitCode(commandErr)
	if err := server.Close(); err != nil {
		return run, err
	}
	closed = true
	if err := ctx.Err(); err != nil {
		return run, err
	}
	run.Facts, err = server.Facts()
	if err != nil {
		return run, err
	}
	tests, err := maven.ReadReports(workspace)
	if errors.Is(err, maven.ErrNoTestCases) && (scenario.Feature == "skipping" || scenario.Feature == "disabled") {
		err = nil
	}
	if err != nil {
		run.ResultError = err.Error()
	}
	for _, test := range tests {
		run.Tests = append(run.Tests, jestTest{File: test.Module + " / " + test.Suite, Name: test.Name, Status: test.Status, Failure: test.Failure})
	}
	projects, projectErr := maven.Projects(workspace)
	if projectErr != nil {
		return run, projectErr
	}
	run.mavenModules = mavenModuleDirectories(projects)
	if probe == nil {
		run.mavenCoverage = observeMavenModules(projects, tests, string(data))
	}
	if len(projects) == 1 && t.framework.Name() == "scalatest" {
		run.mavenModules["org.scalatest.tools.Runner"] = projects[0].Directory
	}
	if probe != nil {
		if data, err := os.ReadFile(env["DDTEST_PROBE_COUNTER"]); err == nil {
			count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			run.ProbeExecutions = &count
		}
	}
	if commandErr != nil {
		run.Diagnostic = commandDiagnostic(data)
		if probe == nil {
			writeCommandFailure(output, "Maven "+name, data)
		}
	}
	return markTimeout(run), nil
}

func compareMaven(baseline, reporting validationRun) verdict {
	if native := compareMavenNative(baseline, reporting); native.Status != "compatible" {
		return native
	}
	return verifyMavenTelemetry(reporting)
}

func compareMavenNative(baseline, reporting validationRun) verdict {
	if reason := pairTimeout(baseline, reporting); reason != "" {
		return verdict{Status: "inconclusive", Code: "command_timeout", Reason: reason}
	}
	if baseline.ResultError != "" || reporting.ResultError != "" || executedTests(baseline) == 0 {
		return verdict{Status: "inconclusive", Code: "native_results_unavailable", Reason: reportText("Both runs need fresh native results for executed tests. " + baseline.ResultError + " " + reporting.ResultError)}
	}
	if baseline.Facts.TestEventCount != 0 {
		return verdict{Status: "inconclusive", Code: "baseline_instrumented", Reason: "The baseline emitted test events; disable existing instrumentation before comparison."}
	}
	left, right := map[string]int{}, map[string]int{}
	for _, key := range outcomeKeys(baseline) {
		left[key]++
	}
	for _, key := range outcomeKeys(reporting) {
		right[key]++
	}
	difference := mavenDifferences(left, right, "baseline", "reporting-only")
	if difference.DifferenceCount > 0 {
		difference.Status, difference.Code = "suspected regression", "native_outcome_mismatch"
		difference.Reason = "Native Maven identities, outcomes, exception details or exit code differ. These differences remain unexplained; an uninstrumented subset rerun cannot clear them."
		return difference
	}
	return verdict{Status: "compatible", Code: "native_outcomes_match", Reason: "Native Maven identities, outcomes, exception details and exit code match. Matching existing failures are acceptable and do not block compatibility."}
}

// Count multiplicity before bounding diagnostics; neither truncation nor equal
// total event counts may conceal a missing, duplicated or renamed execution.
func mavenDifferences(expected, observed map[string]int, left, right string) verdict {
	keys := map[string]bool{}
	for key := range expected {
		keys[key] = true
	}
	for key := range observed {
		keys[key] = true
	}
	var result verdict
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		if expected[key] == observed[key] {
			continue
		}
		result.DifferenceCount++
		if len(result.Differences) < maxReportDifferences {
			result.Differences = append(result.Differences, reportText(fmt.Sprintf("%s: %s=%d, %s=%d", key, left, expected[key], right, observed[key])))
		}
	}
	return result
}

func verifyMavenTelemetry(run validationRun) verdict {
	if len(run.Facts.ConfigurationErrors) > 0 {
		return verdict{Status: "inconclusive", Code: "java_configuration_error", Reason: reportText("Java tracer configuration errors: " + strings.Join(run.Facts.ConfigurationErrors, "; "))}
	}
	expected, observed := map[string]int{}, map[string]int{}
	identity := func(module, suite, name, status string) string {
		return fmt.Sprintf("module=%q suite=%q test=%q status=%q", module, suite, name, status)
	}
	for _, test := range run.Tests {
		module, suite, _ := strings.Cut(test.File, " / ")
		status := map[string]string{"passed": "pass", "failed": "fail", "skipped": "skip"}[test.Status]
		expected[identity(module, suite, mavenTestName(test.Name), status)]++
	}
	missingNames := 0
	for _, test := range run.Facts.Tests {
		name := test.Module
		for _, plugin := range []string{"maven-surefire-plugin", "maven-failsafe-plugin", "tycho-surefire-plugin"} {
			if prefix, _, found := strings.Cut(test.Module, " "+plugin+" "); found {
				name = prefix
				break
			}
		}
		module, ok := run.mavenModules[name]
		if !ok || module == "ambiguous" {
			return verdict{Status: "inconclusive", Code: "java_module_unresolved", Reason: reportText("Java test.module cannot be reconciled with a unique Maven reactor module: " + test.Module)}
		}
		suite := test.Suite
		if _, title, ok := strings.Cut(suite, ".feature:"); ok {
			suite = title
		}
		for _, attempt := range test.Attempts {
			if attempt.Retry {
				return verdict{Status: "inconclusive", Code: "unexpected_java_retry", Reason: "Unexpected Java retry in reporting-only mode."}
			}
			if test.Name == "" {
				missingNames++
			}
			observed[identity(module, suite, mavenEventTestName(test), attempt.Status)]++
		}
	}
	difference := mavenDifferences(expected, observed, "native", "telemetry")
	if missingNames > 0 || len(expected) == 0 || difference.DifferenceCount > 0 {
		difference.Status, difference.Code = "inconclusive", "java_identity_mismatch"
		difference.Reason = "Native and telemetry source-test identities or outcome multiplicities do not reconcile. Equal aggregate totals are insufficient. Inspect the bounded differences; feature probes were not run."
		if missingNames > 0 {
			difference.Code = "java_test_name_missing"
			difference.Reason = fmt.Sprintf("%d Java test executions have an empty test.name. Native names cannot be reconciled with unnamed telemetry; equal totals and matching existing failures do not establish compatibility. Feature probes were not run.", missingNames)
		}
		return difference
	}
	return verdict{Status: "compatible", Code: "java_identities_match", Reason: "Java test identities, outcomes and multiplicities reconcile with native Maven results."}
}

// The Java tracer uses Maven's project display name, falling back to artifactId.
// Names can contain spaces and need not be unique across the reactor.
func mavenModuleDirectories(projects []maven.Project) map[string]string {
	modules := map[string]string{}
	for _, project := range projects {
		for _, name := range []string{project.Name, project.Artifact} {
			if name == "" || strings.Contains(name, "${") {
				continue
			}
			if previous, ok := modules[name]; ok && previous != project.Directory {
				modules[name] = "ambiguous"
			} else {
				modules[name] = project.Directory
			}
		}
	}
	return modules
}

var mavenParameterizedName = regexp.MustCompile(`^([\pL_$][\pL\pN_$]*)\([^)]*\)(\[[0-9]+\])$`)
var mavenInvocationIndex = regexp.MustCompile(`^\[[0-9]+\]`)

func mavenTestName(name string) string {
	if parts := mavenParameterizedName.FindStringSubmatch(name); parts != nil {
		return parts[1] + parts[2]
	}
	return strings.TrimSuffix(name, "()")
}

func mavenEventTestName(test intake.Test) string {
	name := mavenTestName(test.Name)
	var parameters struct {
		Metadata struct {
			Name string `json:"test_name"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(test.Parameters), &parameters) == nil {
		// JUnit 5 exposes the invocation index in its display name, while
		// Surefire adds it after the Java method signature. Preserve that index
		// so a duplicate event cannot substitute for a missing parameter case.
		if index := mavenInvocationIndex.FindString(parameters.Metadata.Name); index != "" && !strings.HasSuffix(name, index) {
			name += index
		}
	}
	return name
}
