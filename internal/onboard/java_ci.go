// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/maven"
	"github.com/kballard/go-shellquote"
	"mvdan.cc/sh/v3/syntax"
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
	// Matrix expansion resolves expressions per row; one left here may
	// select the tests, so it stays in the required scope.
	if workflowExpression.MatchString(step.Run) {
		return unresolvedCommand("CI command expression was not resolved; ddtest cannot tell which tests it runs: " + step.Run)
	}
	parts, parseErr := staticCommands(step.Run)
	// Release steps read project metadata, as in VERSION=$(mvn help:evaluate …),
	// unless an inherited MAVEN_ARGS adds goals.
	if parseErr != nil && javaMetadataScript(step.Run) && workflow.Env["MAVEN_ARGS"] == "" && job.Env["MAVEN_ARGS"] == "" && step.Env["MAVEN_ARGS"] == "" {
		return commandResolution{}
	}
	if parseErr != nil {
		if mavenMention.MatchString(step.Run) {
			return unresolvedCommand("Maven command could not be resolved statically; ddtest cannot tell which tests it runs: " + step.Run)
		}
		return unresolvedJavaWrapper(root, workflow, job, step, nil, framework)
	}
	// A command without goals runs the POM's default goal; mvn -v and other
	// metadata commands, or a POM without one, build nothing.
	for i, words := range parts {
		if !mavenCommand(words) || len(maven.Goals(words[1:])) > 0 || maven.Metadata(words[1:]) {
			continue
		}
		goal, err := javaDefaultGoal(root, directory, words[1:])
		if err != nil {
			return unresolvedCommand("Maven default goal could not be read statically, so ddtest cannot tell whether this step runs tests: " + err.Error())
		}
		parts[i] = append(slices.Clone(words), strings.Fields(goal)...)
	}
	builds := slices.DeleteFunc(slices.Clone(parts), func(words []string) bool { return !mavenCommand(words) || len(maven.Goals(words[1:])) == 0 })
	tests := slices.DeleteFunc(slices.Clone(builds), func(words []string) bool { return !maven.IsTestCommand(words[0], words[1:]) })
	onlyMaven := !slices.ContainsFunc(parts, func(words []string) bool {
		return !mavenCommand(words) && !slices.Contains(javaShellBuiltins, words[0])
	})
	switch {
	case len(builds) == 0 && onlyMaven:
		return commandResolution{}
	case len(builds) == 0:
		return unresolvedJavaWrapper(root, workflow, job, step, parts, framework)
	case len(tests) == 0 && onlyMaven:
		return commandResolution{Review: true, Reason: "Maven goals outside the test lifecycle, such as documentation, lint or release goals, are outside " + framework + " test scope."}
	case len(tests) == 0:
		// Another command in the sequence may still run tests.
		return unresolvedJavaWrapper(root, workflow, job, step, parts, framework)
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
	pomPath := filepath.Join(directory, pom)
	if !filepath.IsLocal(pomPath) {
		return unresolvedCommand("Maven POM selection needs review: " + pom + " leaves the repository")
	}
	// The checkout bounds the reactor, including sibling modules and POMs
	// reached through symlinks; the step's directory only locates the POM.
	projects, err := maven.ProjectsFrom(root, pomPath)
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
		notes = append(notes, "This Maven reactor also declares module(s) "+strings.Join(others, ", ")+", which declare other test frameworks and no "+framework+" dependency. Whether this step runs their tests depends on its active profiles and reactor selectors. Instrumenting this step reports any selected tests to Datadog too; local validation covers only the "+framework+" modules. Report this to the user.")
	}
	return commandResolution{Matched: true, ReviewReason: strings.Join(notes, " "), Tests: []TestCommand{{Directory: directory, Command: selected, StepScopeNotes: notes}}}
}

// javaDefaultGoal reads the default goal of the POM a goal-less command builds.
func javaDefaultGoal(root, directory string, args []string) (string, error) {
	pom, err := maven.CommandPOM(args)
	if err != nil {
		return "", err
	}
	pomPath := filepath.Join(directory, pom)
	if !filepath.IsLocal(pomPath) {
		return "", fmt.Errorf("%s leaves the repository", pom)
	}
	projects, err := maven.ProjectsFrom(root, pomPath)
	if err != nil {
		return "", err
	}
	if projects[0].DefaultGoal == "" && projects[0].Parent != nil {
		return "", fmt.Errorf("%s may inherit a default goal from its parent POM", projects[0].POM)
	}
	return projects[0].DefaultGoal, nil
}

// mavenMention finds a Maven invocation inside shell syntax ddtest cannot parse.
var mavenMention = regexp.MustCompile(`(^|[\s/;&|(])mvnw?(\.cmd)?(\s|$)`)

// scriptMention finds a repository script inside shell syntax ddtest cannot
// parse, such as ./ci/integration.sh | tee build.log.
var scriptMention = regexp.MustCompile(`(^\s*|[;&|(]\s*)((\.\.?/)?[\w.-]+/[\w./-]+|[\w.-]+\.sh(\s|$)|(ba)?sh\s+[^-\s])`)

// javaShellBuiltins only print or move files around maintenance goals.
var javaShellBuiltins = []string{"cd", "cp", "mv", "mkdir", "rm", "ln", "echo", "printf", "cat", "ls", "touch", "tar", "pwd", "true"}

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
	if slices.ContainsFunc(parts, javaRepositoryScript) || parts == nil && scriptMention.MatchString(step.Run) {
		return unresolvedCommand("Repository script may run " + framework + " tests; ddtest cannot tell which ones: " + step.Run)
	}
	// Maven hidden behind an assignment or env wrapper may run tests.
	if mavenMention.MatchString(step.Run) && !slices.ContainsFunc(parts, mavenCommand) {
		return unresolvedCommand("Maven test entry point requires a single explicit command; ddtest cannot replay its environment or shell setup: " + step.Run)
	}
	if !strings.Contains(step.Run, "test") && !strings.Contains(step.Run, "verify") {
		return commandResolution{}
	}
	if parts != nil && javaOutsideUninstrumentedJob(root, workflow, job, framework) {
		return commandResolution{Review: true, Code: CodeUninstrumentedJob, Reason: "Uninstrumented CI job with no recognized Maven test step: this command is outside validation and its tests, if any, are not reported to Datadog. If it runs " + framework + " tests, add the Datadog action to this job and rerun validation: " + step.Run}
	}
	return unresolvedCommand("Test wrapper was not resolved; ddtest cannot tell which " + framework + " tests it runs: " + step.Run)
}

// javaMetadataScript reports a script whose commands only print or move files
// and run Maven for help goals, such as help:evaluate, or its version, which
// read the project without building it.
func javaMetadataScript(run string) bool {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(run), "")
	if err != nil {
		return false
	}
	metadata, readsMaven := true, false
	syntax.Walk(file, func(node syntax.Node) bool {
		// MAVEN_ARGS, exported or not, adds goals to every later Maven call.
		if assign, ok := node.(*syntax.Assign); ok && assign.Name != nil && assign.Name.Value == "MAVEN_ARGS" {
			metadata = false
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		var words []string
		for _, word := range call.Args {
			literal := word.Lit()
			if literal == "" {
				// Only arguments of printing commands may expand.
				literal = "\x00"
			}
			words = append(words, literal)
		}
		switch {
		case mavenCommand(words):
			readsMaven = true
			goals := maven.Goals(words[1:])
			help := len(goals) > 0 && !slices.ContainsFunc(goals, func(goal string) bool { return !strings.HasPrefix(goal, "help:") })
			// An assignment such as MAVEN_ARGS=test may add goals.
			metadata = metadata && len(call.Assigns) == 0 && !slices.Contains(words, "\x00") && (help || maven.Metadata(words[1:]))
		case !slices.Contains(javaShellBuiltins, words[0]):
			metadata = false
		}
		return true
	})
	return metadata && readsMaven
}

// javaRepositoryScript reports a command that runs a script from the
// repository, such as ./ci/test.sh or ci/test.sh, which may start Maven itself.
func javaRepositoryScript(words []string) bool {
	if len(words) == 0 {
		return false
	}
	command := words[0]
	return strings.Contains(command, "/") && !filepath.IsAbs(command) || strings.HasSuffix(command, ".sh") ||
		slices.Contains([]string{"bash", "sh"}, command) && len(words) > 1 && !strings.HasPrefix(words[1], "-")
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
// Datadog action's dd-java-agent selection, per matrix row. A step is complete
// only when its action is a supported release with an API key that reports to
// site (empty: any site).
func JavaCIConfiguration(root, framework, site string) RuntimeCheck {
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
				lastTest := javaLastTestStep(root, workflow, job, row, framework)
				java, tracer, action, buildSystem := "", "", "", ""
				var inputs map[string]string
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
						java, tracer, action, actionErr, inputs, skippedAction = "", "", "", nil, nil, false
					}
					// An action for other languages leaves Java's MAVEN_OPTS in place.
					if selected, err := actionSelectsLanguage(step, row, "java"); uses == githubAction && (err != nil || selected) {
						tracer, action, buildSystem, actionErr, inputs = "", "", "", nil, nil
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
								var buildErr error
								buildSystem, buildErr = runtimeValue(step.With["java-instrumented-build-system"], row)
								if buildErr != nil {
									actionErr = buildErr
								}
								inputs = map[string]string{}
								for key, value := range step.With {
									inputs[key], _ = runtimeValue(value, row)
									if inputs[key] == "" && strings.Contains(value, "${{") {
										// A secret or other expression is set even
										// though its value is unknown statically.
										inputs[key] = value
									}
								}
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
					// Discovery leaves an unresolved step after the job's last
					// possible test outside the test path, such as a report upload.
					if !resolution.Matched && i > lastTest && !javaMayStartTests(root, workflow, job, resolved, framework) {
						continue
					}
					finding := RuntimeFinding{Location: (commandLocation{Workflow: workflow.Path, Job: name, Matrix: row, Step: stepNumber(step, i), Command: step.Run}).String(), Workflow: workflow.Path, Job: name, Step: stepNumber(step, i), Command: resolved.Run, Runtime: java, Action: action, TracerRequested: tracer, setupInputs: inputs, Status: "inconclusive"}
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
					case setupActionProblem(finding, site) != "":
						finding.Code, finding.Reason = CodeIncompleteCISetup, setupActionProblem(finding, site)
					default:
						// Release notes, not version numbers, say which JDKs an agent
						// release supports, so the pair is validated by running it.
						finding.Status, finding.Reason = "not checked", "Validate dd-java-agent "+tracer+" on Java "+java+"; a static selection does not establish that this release supports this JDK."
					}
					// An explicitly uninstrumented row has no instrumentation to lose.
					if reason := javaInstrumentationProblem(workflow, job, step, row, action != "", buildSystem); finding.Status != "excluded" && reason != "" {
						finding.Status, finding.Code, finding.Reason = "inconclusive", "", reason
					}
					result.Jobs = append(result.Jobs, finding)
				}
			}
		}
	}
	if len(result.Jobs) > 0 {
		result.Status, result.Reason = "inconclusive", "Maven CI JDK and dd-java-agent selections need validation."
	}
	return result
}

// javaLastTestStep is the index of the job's last step that runs or may run
// framework's tests in a matrix row, as discovery finds it; -1 when there is
// none.
func javaLastTestStep(root string, workflow ciWorkflow, job runtimeJob, row map[string]any, framework string) int {
	last := -1
	for i, step := range job.Steps {
		resolved, _, _ := resolveVariantStep(workflow, job, step, row, framework)
		resolution := resolveJavaStep(root, workflow, job, resolved, framework)
		if !resolution.Review && (resolution.Matched || resolution.Reason != "" && javaMayStartTests(root, workflow, job, resolved, framework)) {
			last = i
		}
	}
	return last
}

// javaMayStartTests reports a step that may start tests, including one that
// runs Maven where discovery cannot follow it, as in find … -exec mvn test.
func javaMayStartTests(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, framework string) bool {
	return stepMayStartTests(root, workflow, job, step, framework) || mavenMention.MatchString(step.Run)
}

// javaAgentVariable is where the Datadog action, per its
// java-instrumented-build-system input, adds -javaagent for a Maven build.
func javaAgentVariable(buildSystem string) string {
	switch buildSystem {
	case "", "maven":
		return "MAVEN_OPTS"
	case "all":
		return "JAVA_TOOL_OPTIONS"
	}
	return ""
}

// javaInstrumentationProblem explains a test step that runs without the
// agent the action configured: the action instruments another build system,
// the step replaces the variable that carries the agent, or test reporting
// is off. Only a step's own env replaces what the action exported; workflow
// and job values are read by the action and kept. A matrix value applies to
// its row only. dd-java-agent still reports tests with DD_TRACE_ENABLED=false,
// which only stops tracing.
func javaInstrumentationProblem(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, instrumented bool, buildSystem string) string {
	if instrumented {
		variable := javaAgentVariable(buildSystem)
		if variable == "" {
			return "The Datadog action's java-instrumented-build-system " + strconv.Quote(buildSystem) + " does not instrument Maven; this test step runs without the agent."
		}
		// Another agent, such as JaCoCo's, does not keep the Datadog one, and
		// step env values are literal: $MAVEN_OPTS is not expanded.
		if value, set := step.Env[variable]; set && !strings.Contains(value, "dd-java-agent") && !strings.Contains(value, "${{ env."+variable+" }}") && !strings.Contains(value, "${{env."+variable+"}}") {
			return "This test step sets " + variable + ", replacing the -javaagent option the Datadog action added. Include ${{ env." + variable + " }} in the value to keep the agent."
		}
	}
	env := map[string]string{}
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		maps.Copy(env, layer)
	}
	if raw, set := env["DD_CIVISIBILITY_ENABLED"]; set {
		value, err := runtimeValue(raw, row)
		if err != nil {
			return "Java CI instrumentation setting DD_CIVISIBILITY_ENABLED needs review: " + err.Error()
		}
		if value = strings.ToLower(strings.TrimSpace(value)); value == "false" || value == "0" {
			return "Java CI instrumentation is explicitly disabled by DD_CIVISIBILITY_ENABLED"
		}
	}
	return ""
}
