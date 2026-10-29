// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
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

// resolveGradleStep resolves a CI step that runs Gradle into the command that
// validates framework: its options and test tasks. One Gradle build runs every
// framework its projects declare; the projects of other frameworks are reported.
func resolveGradleStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, directory string, parts [][]string, framework string) commandResolution {
	builds := slices.DeleteFunc(slices.Clone(parts), func(words []string) bool { return !gradle.Command(words) })
	tests := slices.DeleteFunc(slices.Clone(builds), func(words []string) bool { return !gradle.IsTestCommand(words) })
	onlyBuilds := !slices.ContainsFunc(parts, func(words []string) bool {
		return !gradle.Command(words) && !mavenCommand(words) && !slices.Contains(javaShellBuiltins, words[0])
	})
	switch {
	case len(tests) == 0 && onlyBuilds:
		// Without tasks, Gradle runs the build's default tasks.
		for _, words := range builds {
			if len(gradle.Tasks(words[1:])) == 0 && gradleDefaultTasks(root, directory) {
				return unresolvedCommand("Gradle default tasks could not be read statically, so ddtest cannot tell whether this step runs tests: " + step.Run)
			}
		}
		return commandResolution{Review: true, Reason: "Gradle tasks outside testing, such as assembly, documentation, analysis or publishing, are outside " + framework + " test scope."}
	case len(tests) == 0:
		// Another command in the sequence may still run tests.
		return unresolvedJavaWrapper(root, workflow, job, step, parts, framework)
	case len(parts) != 1:
		return unresolvedCommand("Gradle test step requires a single explicit command; ddtest cannot replay its shell setup: " + step.Run)
	case gradle.SkipsTests(tests[0][1:]):
		return commandResolution{Review: true, Reason: "Gradle step excludes the test task, so it is outside " + framework + " test scope: " + step.Run}
	}
	words := tests[0]
	project, err := gradle.ProjectDirectory(words[1:])
	if err != nil {
		return unresolvedCommand("Gradle build selection needs review: " + err.Error())
	}
	invoked := filepath.Join(directory, project)
	if !filepath.IsLocal(invoked) && invoked != "." {
		return unresolvedCommand("Gradle build selection needs review: " + project + " leaves the repository")
	}
	build, err := gradle.Build(root, invoked)
	if err != nil {
		return unresolvedCommand("Gradle build selection needs review: " + err.Error())
	}
	projects, err := gradle.Projects(root, build)
	if err != nil {
		return unresolvedCommand("Gradle build could not be read statically, so the projects this step tests are unknown: " + err.Error())
	}
	validated, omitted := gradle.TestInvocation(words[1:])
	selected := shellquote.Join(append([]string{words[0]}, validated...)...)
	var notes []string
	if len(omitted) > 0 {
		notes = append(notes, "This CI step also runs "+strings.Join(omitted, ", ")+". Local validation runs only "+selected+", its test tasks, and never publishes artifacts. Report this to the user.")
	}
	if others := otherGradleFrameworkProjects(projects, framework); len(others) > 0 {
		notes = append(notes, "This Gradle build also has project(s) "+strings.Join(others, ", ")+", which declare other test frameworks and no "+framework+" dependency. Whether this step runs their tests depends on the tasks it selects. Instrumenting this step reports any selected tests to Datadog too; local validation covers only the "+framework+" projects. Report this to the user.")
	}
	return commandResolution{Matched: true, ReviewReason: strings.Join(notes, " "), Tests: []TestCommand{{Directory: directory, Command: selected, StepScopeNotes: notes}}}
}

// gradleDefaultTasks reports a build file in directory that declares default
// tasks, which a command without tasks runs.
func gradleDefaultTasks(root, directory string) bool {
	for _, name := range gradle.BuildFiles {
		data, err := os.ReadFile(filepath.Join(root, directory, name))
		if err == nil && strings.Contains(string(data), "defaultTasks") {
			return true
		}
	}
	return false
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
