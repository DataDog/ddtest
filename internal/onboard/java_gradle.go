// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"cmp"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/gradle"
	"github.com/kballard/go-shellquote"
)

// gradleMention finds a Gradle invocation inside shell syntax ddtest cannot parse.
var gradleMention = regexp.MustCompile(`(^|[\s/;&|(])gradlew?(\.bat)?(\s|$)`)

// Build tools of a Java CI test step.
const (
	javaMaven  = "maven"
	javaGradle = "gradle"
)

// javaBuildToolName is how reports name a build tool; Java when unknown.
func javaBuildToolName(tool string) string {
	switch tool {
	case javaGradle:
		return "Gradle"
	case javaMaven:
		return "Maven"
	}
	return "Java"
}

// javaStepBuildTool is the build tool a resolved step runs, from its test
// command or, unresolved, from the tool its text names; empty when it names
// neither or both.
func javaStepBuildTool(resolution commandResolution, run string) string {
	if len(resolution.Tests) > 0 {
		if words, err := shellquote.Split(resolution.Tests[0].Command); err == nil && gradle.Command(words) {
			return javaGradle
		}
		return javaMaven
	}
	switch maven, gradle := mavenMention.MatchString(run), gradleMention.MatchString(run); {
	case gradle && !maven:
		return javaGradle
	case maven && !gradle:
		return javaMaven
	}
	return ""
}

// gradleRun is one Gradle command of a step that runs tests, with the build it
// selects.
type gradleRun struct {
	words    []string
	projects []gradle.Project
	tasks    gradle.TestTasks
}

// resolveGradleStep resolves a CI step that runs Gradle into the command that
// validates framework: its options and test tasks. One Gradle build runs every
// framework its projects declare; the projects of other frameworks are reported.
func resolveGradleStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, directory string, parts [][]string, framework string) commandResolution {
	// One tool's tests are not accounted for by the other's resolution.
	if slices.ContainsFunc(parts, mavenCommand) {
		return unresolvedCommand("A step that runs both Maven and Gradle requires explicit review; ddtest cannot tell which tests it runs: " + step.Run)
	}
	builds := slices.DeleteFunc(slices.Clone(parts), func(words []string) bool { return !gradle.Command(words) })
	onlyBuilds := !slices.ContainsFunc(parts, func(words []string) bool {
		return !gradle.Command(words) && !slices.Contains(javaShellBuiltins, words[0])
	})
	// Each command is classified against the build it selects. The commands
	// that run no tests are kept, and uncertain notes one that may, or that
	// validation cannot leave out: a command without tasks, one that requests
	// tasks not known to run no tests, and one whose options change the build
	// it runs, as an init script, which may prepare what the tests need.
	var tests []gradleRun
	var others []string
	excluded, uncertain := false, false
	for _, words := range builds {
		args := words[1:]
		requested := gradle.Tasks(args)
		// A dry run executes no task action, and tasks that are by convention
		// free of tests need no build read; a command without tasks runs the
		// default tasks gradleNonTestStep checks.
		if gradle.DryRun(args) || !slices.ContainsFunc(requested, func(task string) bool { return !gradle.NonTestTask(task) }) {
			_, reason := gradleInvokedDirectory(directory, args)
			uncertain = uncertain || len(requested) == 0 && !gradle.DryRun(args) || reason != ""
			others = append(others, shellquote.Join(words...))
			continue
		}
		invoked, reason := gradleInvokedDirectory(directory, args)
		if reason != "" {
			return unresolvedCommand(reason)
		}
		build, err := gradle.Build(root, invoked)
		if err != nil {
			return unresolvedCommand("Gradle build selection needs review: " + err.Error())
		}
		projects, err := gradle.Projects(root, build)
		if err != nil {
			return unresolvedCommand("Gradle build could not be read statically, so the projects this step tests are unknown: " + err.Error())
		}
		testTasks := gradle.DeclaredTestTasks(root, build, projects)
		switch {
		case gradle.RunsTests(args, testTasks):
			tests = append(tests, gradleRun{words: words, projects: projects, tasks: testTasks})
			continue
		case slices.ContainsFunc(requested, testTasks.Runs):
			// Every test task it requests is excluded; its other tasks are
			// classified as any command's.
			excluded = true
			fallthrough
		default:
			// A task that is neither a known test task nor conventionally free
			// of tests may be one that build logic ddtest does not read declares.
			unknown := slices.DeleteFunc(slices.Clone(requested), func(task string) bool { return testTasks.Runs(task) || testTasks.NonTest(task) })
			if len(unknown) > 0 && testTasks.Unread {
				return unresolvedCommand("Gradle task " + strings.Join(unknown, ", ") + " may be a test task declared by build logic ddtest does not read: " + step.Run)
			}
			uncertain = uncertain || len(unknown) > 0
		}
		others = append(others, shellquote.Join(words...))
	}
	if len(tests) == 0 {
		reason := "Gradle tasks outside testing, such as assembly, documentation, analysis or publishing, are outside " + framework + " test scope."
		if excluded {
			reason = "Gradle step excludes the test tasks it requests, so it is outside " + framework + " test scope: " + step.Run
		}
		return gradleNonTestStep(root, step, directory, builds, framework, onlyBuilds, workflow, job, parts, reason)
	}
	// A sequence of Gradle commands, as ./gradlew assemble && ./gradlew check,
	// is validated by its one test command when the others run no tests.
	if len(parts) != 1 && (len(tests) != 1 || len(builds) != len(parts) || uncertain) {
		return unresolvedCommand("Gradle test step requires a single explicit command; ddtest cannot replay its shell setup: " + step.Run)
	}
	run := tests[0]
	validated, omitted := gradle.TestInvocation(run.words[1:], run.tasks.Runs)
	selected := shellquote.Join(append([]string{run.words[0]}, validated...)...)
	var notes []string
	if len(omitted) > 0 {
		notes = append(notes, "This CI step also runs "+strings.Join(omitted, ", ")+". Local validation runs only "+selected+", its test tasks, and never publishes artifacts. Report this to the user.")
	}
	if len(others) > 0 {
		notes = append(notes, "This CI step also runs Gradle commands that run no tests: "+strings.Join(others, "; ")+". Local validation runs only "+selected+" and never publishes artifacts. Report this to the user.")
	}
	notes = append(notes, outputPipeNotes(step.Run, selected)...)
	if others := otherGradleFrameworkProjects(run.projects, framework); len(others) > 0 {
		notes = append(notes, "This Gradle build also has project(s) "+strings.Join(others, ", ")+", which declare other test frameworks and no "+framework+" dependency. Whether this step runs their tests depends on the tasks it selects. Instrumenting this step reports any selected tests to Datadog too; local validation covers only the "+framework+" projects. Report this to the user.")
	}
	return commandResolution{Matched: true, ReviewReason: strings.Join(notes, " "), Tests: []TestCommand{{Directory: directory, Command: selected, StepScopeNotes: notes}}}
}

// gradleNonTestStep classifies a step whose Gradle commands run no test task:
// outside test scope, for reason, unless a taskless command may run default
// tasks or another command in the sequence may run tests.
func gradleNonTestStep(root string, step runtimeStep, directory string, builds [][]string, framework string, onlyBuilds bool, workflow ciWorkflow, job runtimeJob, parts [][]string, reason string) commandResolution {
	if !onlyBuilds {
		return unresolvedJavaWrapper(root, workflow, job, step, parts, framework)
	}
	// Without tasks, Gradle runs the default tasks of the project it selects.
	for _, words := range builds {
		if len(gradle.Tasks(words[1:])) > 0 || gradle.DryRun(words[1:]) {
			continue
		}
		invoked, problem := gradleInvokedDirectory(directory, words[1:])
		if problem != "" {
			return unresolvedCommand(problem)
		}
		if gradle.DefaultTasks(root, invoked) {
			return unresolvedCommand("Gradle default tasks could not be read statically, so ddtest cannot tell whether this step runs tests: " + step.Run)
		}
	}
	return commandResolution{Review: true, Reason: reason}
}

// gradleInvokedDirectory is the repository directory whose build a Gradle
// command run in directory selects, or why it needs review.
func gradleInvokedDirectory(directory string, args []string) (string, string) {
	project, err := gradle.ProjectDirectory(args)
	if err != nil {
		return "", "Gradle build selection needs review: " + err.Error()
	}
	if filepath.IsAbs(project) {
		return "", "Gradle build selection needs review: absolute project directory " + project
	}
	invoked := filepath.Join(directory, project)
	if !filepath.IsLocal(invoked) && invoked != "." {
		return "", "Gradle build selection needs review: " + project + " leaves the repository"
	}
	return invoked, ""
}

// otherGradleFrameworkProjects lists the build's projects that declare test
// frameworks, none of them framework.
func otherGradleFrameworkProjects(projects []gradle.Project, framework string) []string {
	var others []string
	for _, project := range projects {
		if declared := project.Frameworks(); len(declared) > 0 && !slices.Contains(declared, framework) {
			others = append(others, project.Path+" ("+strings.Join(declared, ", ")+")")
		}
	}
	return others
}

// javaGradleActionMajor is the major release a tag of a Gradle action names,
// as v3 or v3.5.0.
var javaGradleActionMajor = regexp.MustCompile(`^v([0-9]+)(?:\.[0-9]+)*$`)

// ciCommandActionReader is a language that reads some actions as the command
// they run, which runs with the step's environment like a run step's
// command (see resolveVariantStep).
type ciCommandActionReader interface {
	// commandAction reports an action step the language reads as a command.
	commandAction(step runtimeStep) bool
}

// A Gradle action with an arguments input runs a Gradle command.
func (javaDiscovery) commandAction(step runtimeStep) bool { return javaGradleArgumentsAction(step) }

// javaGradleArgumentsAction reports a step of a Gradle action with an
// arguments input (see javaGradleActionStep), which runs a Gradle command
// with the step's environment, as a run step does.
func javaGradleArgumentsAction(step runtimeStep) bool {
	name, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(step.Uses)), "@")
	return (name == "gradle/gradle-build-action" || name == "gradle/actions/setup-gradle") && strings.TrimSpace(step.With["arguments"]) != ""
}

// javaGradleActionStep reads a GitHub Actions step of gradle/gradle-build-action,
// or gradle/actions/setup-gradle before v4, which removed the input, whose
// arguments input is a Gradle command line: the action runs the build's
// wrapper, or Gradle on PATH without one, in build-root-directory, splitting
// the arguments as a shell would without expanding them. ok is false for any
// other step, or one without arguments, which only sets up Gradle; resolved
// is set when the step is decided without running it as a command.
func javaGradleActionStep(root string, step runtimeStep, framework string) (command runtimeStep, resolved *commandResolution, ok bool) {
	if !javaGradleArgumentsAction(step) {
		return step, nil, false
	}
	name, ref, _ := strings.Cut(strings.ToLower(strings.TrimSpace(step.Uses)), "@")
	arguments := strings.TrimSpace(step.With["arguments"])
	unresolved := func(reason string) (runtimeStep, *commandResolution, bool) {
		resolution := unresolvedCommand(reason)
		return step, &resolution, true
	}
	if name == "gradle/actions/setup-gradle" {
		match := javaGradleActionMajor.FindStringSubmatch(ref)
		if match == nil {
			return unresolved("Gradle action " + step.Uses + " is not a release ddtest can read; setup-gradle v4 and later ignore its arguments input, so ddtest cannot tell whether this step runs " + framework + " tests")
		}
		if major, _ := strconv.Atoi(match[1]); major >= 4 {
			return step, &commandResolution{Review: true, Reason: "setup-gradle v4 and later ignore the arguments input, so this step only sets up Gradle and runs no " + framework + " tests: " + step.Uses}, true
		}
	}
	if strings.Contains(arguments, "${{") || strings.Contains(step.With["build-root-directory"], "${{") {
		return unresolved("Gradle action input expression was not resolved; ddtest cannot tell which tests it runs: " + arguments)
	}
	if executable := strings.TrimSpace(step.With["gradle-executable"]); executable != "" {
		return unresolved("Gradle action runs " + executable + " in place of the build's wrapper; local validation cannot run that Gradle")
	}
	if version := strings.TrimSpace(step.With["gradle-version"]); version != "" && version != "wrapper" {
		return unresolved("Gradle action downloads Gradle " + version + " in place of the build's wrapper; local validation cannot run that Gradle")
	}
	words, err := shellquote.Split(arguments)
	if err != nil {
		return unresolved("Gradle action arguments could not be read: " + err.Error())
	}
	directory := filepath.Clean(cmp.Or(strings.TrimSpace(step.With["build-root-directory"]), "."))
	executable := "gradle"
	if filepath.IsLocal(directory) || directory == "." {
		if _, err := os.Stat(filepath.Join(root, directory, "gradlew")); err == nil {
			executable = "./gradlew"
		}
	}
	command = step
	command.Uses, command.With = "", nil
	command.Run, command.WorkingDirectory = shellquote.Join(append([]string{executable}, words...)...), directory
	return command, nil, true
}
