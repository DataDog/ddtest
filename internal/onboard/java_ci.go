// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/maven"
	"github.com/kballard/go-shellquote"
)

// mavenExecutables run a Maven build; anything else is another tool.
var mavenExecutables = []string{"mvn", "mvnw", "mvn.cmd", "mvnw.cmd"}

func mavenCommand(words []string) bool {
	return len(words) > 0 && slices.Contains(mavenExecutables, filepath.Base(words[0]))
}

// resolveJavaStep resolves one CI step into the Maven command that validates
// framework. One Maven build runs every framework the reactor declares, so the
// step is tied to the selected framework's modules and the rest is reported.
func resolveJavaStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, framework string) commandResolution {
	if step.ResolutionError != "" {
		return unresolvedCommand(step.ResolutionError)
	}
	if strings.TrimSpace(step.Run) == "" {
		return commandResolution{}
	}
	directory := filepath.Clean(stepDirectory(workflow, job, step))
	if !filepath.IsLocal(directory) || strings.ContainsAny(directory, "$`~") {
		return unresolvedCommand("Maven working-directory needs explicit review: " + directory)
	}
	if workflowExpression.MatchString(step.Run) && mavenMention.MatchString(step.Run) {
		return unresolvedCommand("Maven command expression was not resolved; ddtest cannot tell which tests it runs: " + step.Run)
	}
	parts, parseErr := staticCommands(step.Run)
	if parseErr != nil {
		if mavenMention.MatchString(step.Run) {
			return unresolvedCommand("Maven command could not be resolved statically; ddtest cannot tell which tests it runs: " + step.Run)
		}
		return unresolvedJavaWrapper(root, workflow, job, step, nil, framework)
	}
	// A Maven command without goals, such as mvn -v, builds nothing.
	builds := slices.DeleteFunc(slices.Clone(parts), func(words []string) bool { return !mavenCommand(words) || len(maven.Goals(words[1:])) == 0 })
	tests := slices.DeleteFunc(slices.Clone(builds), func(words []string) bool { return !maven.IsTestCommand(words[0], words[1:]) })
	switch {
	case len(builds) == 0:
		return unresolvedJavaWrapper(root, workflow, job, step, parts, framework)
	case len(tests) == 0:
		return commandResolution{Review: true, Reason: "Maven goals outside the test lifecycle, such as documentation, lint or release goals, are outside " + framework + " test scope."}
	case len(parts) != 1:
		return unresolvedCommand("Maven test step requires a single explicit command; ddtest cannot replay its shell setup: " + step.Run)
	case maven.SkipsTests(tests[0][1:]):
		return commandResolution{Review: true, Reason: "Maven step skips tests, so it is outside " + framework + " test scope: " + step.Run}
	}
	words := tests[0]
	pom, err := maven.CommandPOM(words[1:])
	if err != nil {
		return unresolvedCommand("Maven POM selection needs review: " + err.Error())
	}
	projects, err := maven.ProjectsFrom(filepath.Join(root, directory), pom)
	if err != nil {
		return unresolvedCommand("Maven reactor could not be read statically, so the modules this step tests are unknown: " + err.Error())
	}
	validated, omitted := maven.TestInvocation(words[1:])
	selected := shellquote.Join(append([]string{words[0]}, validated...)...)
	var notes []string
	if len(omitted) > 0 {
		notes = append(notes, "This CI step also runs "+strings.Join(omitted, ", ")+". Local validation runs only "+selected+", the test lifecycle up to verify, and never installs or publishes artifacts. Report this to the user.")
	}
	if others := otherFrameworkModules(projects, framework); len(others) > 0 {
		notes = append(notes, "This Maven build also runs the tests of module(s) "+strings.Join(others, ", ")+", which declare other test frameworks and no "+framework+" dependency. Instrumenting this step reports those tests to Datadog too; local validation covers only the "+framework+" modules. Report this to the user.")
	}
	return commandResolution{Matched: true, ReviewReason: strings.Join(notes, " "), Tests: []TestCommand{{Directory: directory, Command: selected, StepScopeNotes: notes}}}
}

// mavenMention finds a Maven invocation inside shell syntax ddtest cannot parse.
var mavenMention = regexp.MustCompile(`(^|[\s/;&|(])mvnw?(\.cmd)?(\s|$)`)

// otherFrameworkModules lists the reactor modules that declare test
// frameworks, none of them framework.
func otherFrameworkModules(projects []maven.Project, framework string) []string {
	var modules []string
	for _, project := range projects {
		if declared := project.Frameworks(); len(declared) > 0 && !slices.Contains(declared, framework) {
			modules = append(modules, project.Directory+" ("+strings.Join(declared, ", ")+")")
		}
	}
	return modules
}

// unresolvedJavaWrapper classifies a step that runs no literal Maven command.
// A wrapper that may run tests stays in scope as unresolved; one in a job
// without the Datadog action or any Maven test step is reported as a caveat.
func unresolvedJavaWrapper(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, parts [][]string, framework string) commandResolution {
	if workflowExpression.MatchString(step.Run) && (strings.Contains(step.Run, "mvn") || strings.Contains(step.Run, "test")) {
		return unresolvedCommand("Maven command expression was not resolved; ddtest cannot tell which tests it runs: " + step.Run)
	}
	if slices.ContainsFunc(parts, javaRepositoryScript) {
		return unresolvedCommand("Repository script may run " + framework + " tests; ddtest cannot tell which ones: " + step.Run)
	}
	if !strings.Contains(step.Run, "test") && !strings.Contains(step.Run, "verify") {
		return commandResolution{}
	}
	if parts != nil && javaOutsideUninstrumentedJob(root, workflow, job, framework) {
		return commandResolution{Review: true, Code: CodeUninstrumentedJob, Reason: "Uninstrumented CI job with no recognized Maven test step: this command is outside validation and its tests, if any, are not reported to Datadog. If it runs " + framework + " tests, add the Datadog action to this job and rerun validation: " + step.Run}
	}
	return unresolvedCommand("Test wrapper was not resolved; ddtest cannot tell which " + framework + " tests it runs: " + step.Run)
}

// javaRepositoryScript reports a command that runs a script from the
// repository, such as ./ci/test.sh, which may start Maven itself.
func javaRepositoryScript(words []string) bool {
	return len(words) > 0 && (strings.HasPrefix(words[0], "./") || strings.HasPrefix(words[0], "bin/") || strings.HasPrefix(words[0], "scripts/") || slices.Contains([]string{"bash", "sh"}, words[0]) && len(words) > 1 && !strings.HasPrefix(words[1], "-"))
}

// javaOutsideUninstrumentedJob reports a job that neither runs the Datadog
// action nor any recognized Maven test step.
func javaOutsideUninstrumentedJob(root string, workflow ciWorkflow, job runtimeJob, framework string) bool {
	if len(job.Steps) == 0 {
		return false
	}
	// Siblings resolve without job context, so this check cannot recurse.
	sibling := job
	sibling.Steps = nil
	for _, step := range job.Steps {
		if uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@"); uses == githubAction {
			return false
		}
		if step.Run != "" && resolveJavaStep(root, workflow, sibling, step, framework).Matched {
			return false
		}
	}
	return true
}

// CodeUnsupportedJavaRuntime marks a CI JDK no dd-java-agent release supports,
// or a pre-release build; exclude it with an if: on the Datadog action.
const CodeUnsupportedJavaRuntime = "unsupported_runtime"

// javaVersion reads the major version of a setup-java java-version, accepting
// both 1.8 and 8 forms. Ranges, aliases and pre-releases are not numeric.
var javaVersion = regexp.MustCompile(`^(?:1\.)?([0-9]+)(?:\.[0-9]+)*$`)

// exactJavaRelease is one dd-java-agent release, never "latest".
var exactJavaRelease = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// javaRuntimeProblem explains why a CI JDK selection cannot be checked or is
// unsupported; empty when dd-java-agent supports it. The agent requires Java 8.
func javaRuntimeProblem(version string) (reason, code string) {
	if version == "" {
		return "Select a static java-version with actions/setup-java before this test step; the runner's default JDK is not checked.", ""
	}
	if strings.Contains(version, "-ea") || strings.HasSuffix(version, "ea") {
		return "Java " + version + " is an early-access build. Exclude it from instrumentation with an if: on the Datadog action, keeping its tests in CI.", CodeUnsupportedJavaRuntime
	}
	match := javaVersion.FindStringSubmatch(version)
	if match == nil {
		return "Java version " + strconv.Quote(version) + " is not a numeric selection; select a static JDK version to check it.", ""
	}
	if major, _ := strconv.Atoi(match[1]); major < 8 {
		return "Java " + version + " is older than Java 8, which dd-java-agent requires. Exclude it from instrumentation with an if: on the Datadog action, keeping its tests in CI.", CodeUnsupportedJavaRuntime
	}
	return "", ""
}

// JavaCIConfiguration inventories each CI Maven test step's JDK and the
// Datadog action's dd-java-agent selection, per matrix row.
func JavaCIConfiguration(root, framework string) RuntimeCheck {
	result := RuntimeCheck{Status: "not applicable", Reason: "No candidate Maven CI test commands found."}
	workflows, err := readWorkflows(root)
	if err != nil {
		return RuntimeCheck{Status: "inconclusive", Reason: err.Error()}
	}
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			rows, err := runtimeMatrix(job.Strategy.Matrix)
			if err != nil {
				result.Jobs = append(result.Jobs, RuntimeFinding{Workflow: workflow.Path, Job: name, Status: "inconclusive", Reason: err.Error()})
				continue
			}
			for _, row := range rows {
				active, conditionErr := possibleJobCondition(job.If, row)
				if conditionErr == nil && !active {
					continue
				}
				java, tracer, action := "", "", ""
				skippedAction := false
				var actionErr error
				for i, step := range job.Steps {
					uses, _, hasVersion := strings.Cut(strings.ToLower(step.Uses), "@")
					active, stepErr := stepCondition(step, row, conditionEnvironment(workflow, job, step, row))
					if stepErr == nil && !active {
						if selected, err := actionSelectsLanguage(step, row, "java"); uses == githubAction && hasVersion && err == nil && selected {
							skippedAction = true
						}
						continue
					}
					if uses == "actions/setup-java" {
						java, tracer, action, actionErr, skippedAction = "", "", "", nil, false
					}
					if uses == githubAction {
						tracer, action, actionErr = "", "", nil
					}
					if conditionErr == nil && stepErr == nil {
						if uses == "actions/setup-java" && hasVersion {
							java, _ = runtimeValue(step.With["java-version"], row)
						}
						if uses == githubAction && hasVersion {
							selected, err := actionSelectsLanguage(step, row, "java")
							actionErr = err
							if err == nil && selected {
								action = step.Uses
								tracer, _ = runtimeValue(step.With["java-tracer-version"], row)
							}
						}
					}
					resolved, _, resolveErr := resolveVariantStep(workflow, job, step, row, framework)
					resolution := resolveJavaStep(root, workflow, job, resolved, framework)
					if resolution.Review {
						result.Review = append(result.Review, RuntimeFinding{Workflow: workflow.Path, Job: name, Step: stepNumber(step, i), Command: resolved.Run, Status: "not checked", Reason: resolution.Reason, Code: resolution.Code})
						continue
					}
					if !resolution.Matched && resolution.Reason == "" {
						continue
					}
					finding := RuntimeFinding{Location: (commandLocation{Workflow: workflow.Path, Job: name, Matrix: row, Step: stepNumber(step, i), Command: step.Run}).String(), Workflow: workflow.Path, Job: name, Step: stepNumber(step, i), Command: resolved.Run, Runtime: java, Action: action, TracerRequested: tracer, Status: "inconclusive"}
					runtimeReason, runtimeCode := javaRuntimeProblem(java)
					switch {
					case conditionErr != nil || stepErr != nil || resolveErr != nil || actionErr != nil || resolution.Reason != "":
						reasons := []string{resolution.Reason}
						for _, err := range []error{conditionErr, stepErr, resolveErr, actionErr} {
							if err != nil {
								reasons = append(reasons, err.Error())
							}
						}
						finding.Reason = "Maven CI scope needs review: " + strings.TrimSpace(strings.Join(reasons, "; "))
					case action == "" && skippedAction:
						finding.Status, finding.Code = "excluded", "excluded_runtime"
						finding.Reason = "The Datadog action is explicitly skipped for this matrix entry. Its tests still run without instrumentation; this runtime is not validated with the tracer."
					case runtimeReason != "":
						finding.Code, finding.Reason = runtimeCode, runtimeReason
					case action == "":
						finding.Reason = "No Datadog action selects java before this test step, so its tests are not instrumented."
					case !exactJavaRelease.MatchString(tracer):
						finding.Reason = "The Datadog action's java-tracer-version " + strconv.Quote(tracer) + " is not an exact release; set an exact version such as 1.67.1."
					default:
						finding.Status, finding.Reason = "compatible", "dd-java-agent "+tracer+" supports Java "+java+"."
					}
					// An explicitly uninstrumented row has no instrumentation to lose.
					if reason := javaInstrumentationProblem(workflow, job, step, row); finding.Status != "excluded" && reason != "" {
						finding.Status, finding.Code, finding.Reason = "inconclusive", "", reason
					}
					result.Jobs = append(result.Jobs, finding)
				}
			}
		}
	}
	if len(result.Jobs) > 0 {
		result.Status, result.Reason = "compatible", "Every Maven CI test step selects a supported JDK and an exact dd-java-agent release."
		if slices.ContainsFunc(result.Jobs, func(job RuntimeFinding) bool { return job.Status == "inconclusive" }) {
			result.Status, result.Reason = "inconclusive", "Some Maven CI test steps need a static JDK, an exact dd-java-agent release or scope review."
		} else if !slices.ContainsFunc(result.Jobs, func(job RuntimeFinding) bool { return job.Status == "compatible" }) {
			result.Status, result.Reason = "inconclusive", "Every Maven CI test step excludes instrumentation; at least one instrumented step is required."
		}
	}
	return result
}

// javaInstrumentationProblem explains a test step whose environment turns
// test reporting off; a matrix value applies to its row only. dd-java-agent
// still reports tests with DD_TRACE_ENABLED=false, which only stops tracing.
func javaInstrumentationProblem(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) string {
	env := map[string]string{}
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		maps.Copy(env, layer)
	}
	for _, key := range []string{"DD_CIVISIBILITY_ENABLED"} {
		raw, set := env[key]
		if !set {
			continue
		}
		value, err := runtimeValue(raw, row)
		if err != nil {
			return "Java CI instrumentation setting " + key + " needs review: " + err.Error()
		}
		if value = strings.ToLower(strings.TrimSpace(value)); value == "false" || value == "0" {
			return "Java CI instrumentation is explicitly disabled by " + key
		}
	}
	return ""
}
