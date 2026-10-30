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

var (
	// globalOptionsWithValue are Gradle's own options whose next word is their
	// value, never a task, as documented by gradle --help.
	globalOptionsWithValue = []string{
		"-b", "--build-file", "-c", "--settings-file", "-D", "--system-prop", "-F", "--dependency-verification",
		"-g", "--gradle-user-home", "-I", "--init-script", "-M", "--write-verification-metadata", "-P", "--project-prop",
		"-p", "--project-dir", "-x", "--exclude-task", "--console", "--include-build", "--max-workers", "--priority",
		"--project-cache-dir", "--warning-mode", "--configuration-cache-problems", "--update-locks",
	}
	// globalFlags are Gradle's own options without a value.
	globalFlags = []string{
		"-?", "-h", "--help", "-a", "--no-rebuild", "--build-cache", "--no-build-cache", "--configuration-cache", "--no-configuration-cache",
		"--configure-on-demand", "--no-configure-on-demand", "--continue", "--no-continue", "-d", "--debug", "--daemon", "--no-daemon",
		"--export-keys", "--foreground", "-i", "--info", "-m", "--dry-run", "--offline", "--parallel", "--no-parallel", "--profile",
		"-q", "--quiet", "--refresh-keys", "--rerun-tasks", "-s", "--stacktrace", "-S", "--full-stacktrace", "--scan", "--no-scan",
		"--status", "--stop", "-t", "--continuous", "-U", "--refresh-dependencies", "-v", "--version", "-V", "--show-version",
		"-w", "--warn", "--watch-fs", "--no-watch-fs", "--write-locks", "--problems-report", "--no-problems-report",
		"--property-upgrade-report", "--task-graph",
	}
	// optionsWithValue are also the test task's --tests and help's --task.
	optionsWithValue = append(slices.Clone(globalOptionsWithValue), "--tests", "--task")
)

// word is one argument of a Gradle command: a task, or an option with its
// value. An option after a task that is not one of Gradle's own belongs to
// that task, as --tests or --fail-fast to test.
type word struct {
	task          string
	option, value string
	hasValue      bool
	// of is the task an option belongs to.
	of string
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
		// A short option may keep its value attached, as in -Dkey=value,
		// -xtest or -Ici/init.gradle.
		case len(arg) > 2 && !strings.HasPrefix(arg, "--") && slices.Contains(optionsWithValue, arg[:2]):
			words = append(words, word{option: arg[:2], value: arg[2:], hasValue: true})
		case strings.HasPrefix(arg, "-"):
			option, value, found := strings.Cut(arg, "=")
			words = append(words, word{option: option, value: value, hasValue: found})
		default:
			words = append(words, word{task: arg})
		}
	}
	task := ""
	for i := range words {
		if words[i].task != "" {
			task = words[i].task
		} else if task != "" && !slices.Contains(globalOptionsWithValue, words[i].option) && !slices.Contains(globalFlags, words[i].option) {
			words[i].of = task
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
	nonTestPrefixes = append([]string{"clean", "javadoc", "dokka", "spotless", "ktlint", "detekt", "checkstyle", "pmd", "spotbugs", "sonar", "jacoco", "license", "dist", "publish", "install", "upload", "release", "native", "image", "jib", "docker", "shadow", "boot"}, buildingPrefixes...)
	nonTestTasks    = []string{"dependencies", "help", "tasks", "properties", "projects", "wrapper", "buildenvironment", "init", "run"}
)

// NonTestTask reports whether a task, by convention, runs no tests. A task
// TestTask accepts, such as nativeTest, never is.
func NonTestTask(task string) bool {
	if TestTask(task) {
		return false
	}
	name := strings.ToLower(taskName(task))
	return slices.Contains(nonTestTasks, name) || slices.ContainsFunc(nonTestPrefixes, func(prefix string) bool { return strings.HasPrefix(name, prefix) })
}

// excludes reports whether a -x selector removes a requested task: the same
// path, or an unqualified name, which selects the task of every project.
func excludes(selectors []string, task string) bool {
	return slices.ContainsFunc(selectors, func(selector string) bool {
		return selector == task || !strings.Contains(selector, ":") && selector == taskName(task)
	})
}

// RunsTests reports whether a command still runs a test task once its -x
// selectors apply: a requested test task they do not remove, or a lifecycle
// task, check or build, whose project's test task they do not remove, or
// whose build has other test tasks, which it runs regardless. :app:test is
// one project's task; an unqualified test selects every project's.
func RunsTests(args []string, tasks TestTasks) bool {
	selectors := excluded(args)
	for _, task := range Tasks(args) {
		if !tasks.Runs(task) || excludes(selectors, task) {
			continue
		}
		name := strings.ToLower(taskName(task))
		if name != "check" && name != "build" {
			return true
		}
		// build or :app:build runs test or :app:test.
		if tasks.Others() || !excludes(selectors, task[:len(task)-len(taskName(task))]+"test") {
			return true
		}
	}
	return false
}

// DryRun reports whether a command runs with every task action disabled.
func DryRun(args []string) bool {
	return slices.ContainsFunc(parse(args), func(item word) bool { return item.option == "-m" || item.option == "--dry-run" })
}

// SystemProperties are the system properties a command sets with -D or
// --system-prop; a later value replaces an earlier one.
func SystemProperties(args []string) map[string]string {
	properties := map[string]string{}
	for _, item := range parse(args) {
		if item.option == "-D" || item.option == "--system-prop" {
			key, value, _ := strings.Cut(item.value, "=")
			properties[key] = value
		}
	}
	return properties
}

// UserHomes are the Gradle user homes a command names, with -g,
// --gradle-user-home or the gradle.user.home system property, relative to the
// directory it runs in; none when the environment selects it.
func UserHomes(args []string) []string {
	var homes []string
	for _, item := range parse(args) {
		if item.option == "-g" || item.option == "--gradle-user-home" {
			homes = append(homes, item.value)
		}
	}
	if home, ok := SystemProperties(args)["gradle.user.home"]; ok {
		homes = append(homes, home)
	}
	return homes
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
// publishing, reporting or analysis. A task's options, such as --tests, are
// kept or omitted with it.
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
		case item.of != "":
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

// validationFlags make every validation run execute its tests: no task is up
// to date or taken from the build cache, a failing test task does not stop
// the others, and no daemon outlives the run or serves another one with the
// agent. The configuration cache is disabled with a system property, which
// releases without it ignore, so no run reuses another's configuration.
var validationFlags = []string{"--rerun-tasks", "--no-build-cache", "--continue", "--no-daemon", "-Dorg.gradle.configuration-cache=false"}

// contradictedFlags are options validationFlags override.
var contradictedFlags = []string{"--build-cache", "--daemon", "--configuration-cache"}

// ValidationArgs are a command's arguments for one validation run: its own,
// with validationFlags in place of the options they contradict.
func ValidationArgs(args []string) []string {
	var result []string
	for _, item := range parse(args) {
		if slices.Contains(contradictedFlags, item.option) || slices.Contains(validationFlags, item.option) {
			continue
		}
		result = append(result, render(item)...)
	}
	return append(result, validationFlags...)
}

// ProbeArgs are the arguments of a validation run that executes only the probe
// class, through the test task of the project at path: the command's own
// Gradle options, without its tasks, their options or its exclusions, any of
// which could select other tests, remove the probe's task or, moved before
// it, not be an option Gradle accepts.
func ProbeArgs(args []string, path, class string) []string {
	var options []string
	for _, item := range parse(args) {
		if item.task != "" || item.of != "" || item.option == "-x" || item.option == "--exclude-task" {
			continue
		}
		options = append(options, render(item)...)
	}
	return ValidationArgs(append(options, TaskPath(path, "test"), "--tests", class))
}
