// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"path/filepath"
	"regexp"
	"slices"
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
	// Each command is classified against the build it selects.
	var tests []gradleRun
	excluded := false
	for _, words := range builds {
		args := words[1:]
		requested := gradle.Tasks(args)
		// A dry run executes no task action, and tasks that are by convention
		// free of tests need no build read; a command without tasks runs the
		// default tasks gradleNonTestStep checks.
		if gradle.DryRun(args) || !slices.ContainsFunc(requested, func(task string) bool { return !gradle.NonTestTask(task) }) {
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
		case slices.ContainsFunc(requested, testTasks.Runs):
			// Every test task it requests is excluded.
			excluded = true
		default:
			// A task that is neither a known test task nor conventionally free
			// of tests may be one that build logic ddtest does not read declares.
			if unknown := slices.DeleteFunc(slices.Clone(requested), gradle.NonTestTask); len(unknown) > 0 && testTasks.Unread {
				return unresolvedCommand("Gradle task " + strings.Join(unknown, ", ") + " may be a test task declared by build logic ddtest does not read: " + step.Run)
			}
		}
	}
	if len(tests) == 0 {
		reason := "Gradle tasks outside testing, such as assembly, documentation, analysis or publishing, are outside " + framework + " test scope."
		if excluded {
			reason = "Gradle step excludes the test tasks it requests, so it is outside " + framework + " test scope: " + step.Run
		}
		return gradleNonTestStep(root, step, directory, builds, framework, onlyBuilds, workflow, job, parts, reason)
	}
	if len(parts) != 1 {
		return unresolvedCommand("Gradle test step requires a single explicit command; ddtest cannot replay its shell setup: " + step.Run)
	}
	run := tests[0]
	validated, omitted := gradle.TestInvocation(run.words[1:], run.tasks.Runs)
	selected := shellquote.Join(append([]string{run.words[0]}, validated...)...)
	var notes []string
	if len(omitted) > 0 {
		notes = append(notes, "This CI step also runs "+strings.Join(omitted, ", ")+". Local validation runs only "+selected+", its test tasks, and never publishes artifacts. Report this to the user.")
	}
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
