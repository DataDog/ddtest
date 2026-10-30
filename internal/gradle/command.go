// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gradle

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// Executables run a Gradle build, through the wrapper or an installed Gradle.
var Executables = []string{"gradle", "gradlew", "gradle.bat", "gradlew.bat"}

// Command reports whether words invoke Gradle.
func Command(words []string) bool {
	return len(words) > 0 && slices.Contains(Executables, filepath.Base(words[0]))
}

// optionsWithValue are the Gradle CLI options whose next word is their value,
// never a task, as documented by gradle --help, plus the test task's --tests
// and help's --task.
var optionsWithValue = []string{
	"-b", "--build-file", "-c", "--settings-file", "-D", "--system-prop", "-F", "--dependency-verification",
	"-g", "--gradle-user-home", "-I", "--init-script", "-M", "--write-verification-metadata", "-P", "--project-prop",
	"-p", "--project-dir", "-x", "--exclude-task", "--console", "--include-build", "--max-workers", "--priority",
	"--project-cache-dir", "--warning-mode", "--tests", "--task",
}

// word is one argument of a Gradle command: a task, or an option with its value.
type word struct {
	task          string
	option, value string
	hasValue      bool
}

func parse(args []string) []word {
	var words []word
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case slices.Contains(optionsWithValue, arg):
			item := word{option: arg, hasValue: true}
			if i+1 < len(args) {
				item.value = args[i+1]
				i++
			}
			words = append(words, item)
		case strings.HasPrefix(arg, "-"):
			option, value, found := strings.Cut(arg, "=")
			// A short option may keep its value attached, as in -Dkey=value,
			// -xtest or -Ici/init.gradle.
			if !found && len(arg) > 2 && !strings.HasPrefix(arg, "--") && slices.Contains(optionsWithValue, arg[:2]) {
				option, value, found = arg[:2], arg[2:], true
			}
			words = append(words, word{option: option, value: value, hasValue: found})
		default:
			words = append(words, word{task: arg})
		}
	}
	return words
}

// Tasks lists the tasks a command requests, in order.
func Tasks(args []string) []string {
	var tasks []string
	for _, item := range parse(args) {
		if item.task != "" {
			tasks = append(tasks, item.task)
		}
	}
	return tasks
}

// excluded lists the tasks -x and --exclude-task remove.
func excluded(args []string) []string {
	var tasks []string
	for _, item := range parse(args) {
		if item.option == "-x" || item.option == "--exclude-task" {
			tasks = append(tasks, item.value)
		}
	}
	return tasks
}

// taskName is a task path's last segment, as test for :app:test.
func taskName(task string) string {
	return task[strings.LastIndex(task, ":")+1:]
}

// buildingPrefixes start the names of tasks that build, rather than run,
// tests, as assembleDebugAndroidTest or compileTestKotlin.
var buildingPrefixes = []string{"assemble", "compile", "bundle", "package", "process", "generate", "merge", "lint", "jar"}

// TestTask reports whether a task runs tests: test or a task named like it,
// such as integrationTest, or check and build, which depend on test.
func TestTask(task string) bool {
	name := strings.ToLower(taskName(task))
	if name == "check" || name == "build" {
		return true
	}
	return strings.HasSuffix(name, "test") && !slices.ContainsFunc(buildingPrefixes, func(prefix string) bool { return strings.HasPrefix(name, prefix) })
}

// nonTestPrefixes and nonTestTasks name tasks that, by convention, run no
// tests: cleaning, building, documentation, analysis and publishing.
var (
	nonTestPrefixes = append([]string{"clean", "javadoc", "dokka", "spotless", "ktlint", "detekt", "checkstyle", "pmd", "spotbugs", "sonar", "jacoco", "license", "dist", "publish", "install", "upload", "release"}, buildingPrefixes...)
	nonTestTasks    = []string{"dependencies", "help", "tasks", "properties", "projects", "wrapper", "buildenvironment", "init", "bootjar", "shadowjar", "bootrun", "run"}
)

// NonTestTask reports whether a task, by convention, runs no tests.
func NonTestTask(task string) bool {
	name := strings.ToLower(taskName(task))
	return slices.Contains(nonTestTasks, name) || slices.ContainsFunc(nonTestPrefixes, func(prefix string) bool { return strings.HasPrefix(name, prefix) })
}

// IsTestCommand reports whether a literal Gradle invocation requests a task
// that runs tests.
func IsTestCommand(words []string) bool {
	return Command(words) && slices.ContainsFunc(Tasks(words[1:]), TestTask)
}

// SkipsTests reports whether a command excludes test while requesting only
// tasks that run it through their dependencies, as in build -x test. Only the
// unqualified selector test excludes every project's test task; :app:test
// excludes one project's. A build whose lifecycle tasks run other test tasks
// still runs them; see OtherTestTasks.
func SkipsTests(args []string) bool {
	if !slices.Contains(excluded(args), "test") {
		return false
	}
	return !slices.ContainsFunc(Tasks(args), func(task string) bool {
		name := strings.ToLower(taskName(task))
		return strings.HasSuffix(name, "test") && name != "test"
	})
}

// reviewedOptions change which build, settings or logic a command runs, so
// a command that uses them needs review.
var reviewedOptions = []string{"-b", "--build-file", "-c", "--settings-file", "-I", "--init-script", "--include-build"}

// ProjectDirectory returns the directory, relative to the one the command runs
// in, whose build a command runs: its -p or --project-dir value, else ".".
// Options that select another build or settings file, or add build logic,
// are an error.
func ProjectDirectory(args []string) (string, error) {
	directory := "."
	for _, item := range parse(args) {
		if slices.Contains(reviewedOptions, item.option) {
			return "", fmt.Errorf("%s changes the build Gradle runs; ddtest reads only the project's own settings and build files", item.option)
		}
		if item.option == "-p" || item.option == "--project-dir" {
			if item.value == "" {
				return "", fmt.Errorf("%s needs a directory", item.option)
			}
			directory = item.value
		}
	}
	return filepath.Clean(directory), nil
}

// TestInvocation splits a command into what validation runs, its options and
// the tasks runsTests accepts, and the other tasks it omits, such as
// publishing, reporting or analysis. A task option such as --tests is kept
// with its task.
func TestInvocation(args []string, runsTests func(string) bool) (validated, omitted []string) {
	keep := true
	for _, item := range parse(args) {
		switch {
		case item.task != "":
			keep = runsTests(item.task)
			if keep {
				validated = append(validated, item.task)
			} else {
				omitted = append(omitted, item.task)
			}
		case item.option == "--tests" || item.option == "--rerun":
			// Options after a task belong to it.
			if keep {
				validated = append(validated, render(item)...)
			}
		default:
			validated = append(validated, render(item)...)
		}
	}
	return validated, omitted
}

// render writes a parsed word back as arguments.
func render(item word) []string {
	switch {
	case item.task != "":
		return []string{item.task}
	case slices.Contains(optionsWithValue, item.option) && item.hasValue && !strings.Contains(item.value, "="):
		return []string{item.option, item.value}
	case item.hasValue && len(item.option) == 2 && slices.Contains(optionsWithValue, item.option):
		return []string{item.option + item.value}
	case item.hasValue:
		return []string{item.option + "=" + item.value}
	}
	return []string{item.option}
}

// publishingTasks write artifacts to a repository or install them.
var publishingTasks = []string{"publish", "upload", "install", "release"}

// Publishes reports whether a command requests a task that publishes,
// uploads, installs or releases artifacts.
func Publishes(args []string) bool {
	return slices.ContainsFunc(Tasks(args), func(task string) bool {
		name := strings.ToLower(taskName(task))
		return slices.ContainsFunc(publishingTasks, func(prefix string) bool { return strings.HasPrefix(name, prefix) })
	})
}
