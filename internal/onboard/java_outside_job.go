// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/gradle"
	"github.com/DataDog/ddtest/internal/maven"
	"mvdan.cc/sh/v3/syntax"
)

// A release, publishing, deploy or scan job never runs Java tests when nothing
// its steps run can start them: Maven outside the test lifecycle or with tests
// skipped, Gradle tasks that by convention run none, and tools that launch no
// build, such as release, deploy and scan tools, with the repository scripts
// they run read (see javaShellMayRunTests). Expressions choose values there,
// such as secrets or versions, so each is read as a plain word; one that
// chooses a command or task leaves an unknown word, which may run tests.
// Maven may also run goals MAVEN_ARGS or .mvn/maven.config add. Actions are
// not read, as resolveJavaStep reads none as a test step, except a Gradle
// action's arguments input (see javaGradleActionStep).
func (javaDiscovery) outsideFrameworkJob(root string, workflow ciWorkflow, job runtimeJob, row map[string]any, _ string) (bool, []string) {
	for _, step := range job.Steps {
		if active, err := stepCondition(step, row, conditionEnvironment(workflow, job, runtimeStep{}, row)); err == nil && !active {
			continue
		}
		if step.ResolutionError != "" {
			return false, nil
		}
		// A Gradle action runs its arguments input as a Gradle command.
		if command, resolved, ok := javaGradleActionStep(root, step, ""); ok && (resolved != nil && !resolved.Review || resolved == nil && javaStepMayRunTests(root, workflow, job, command)) {
			return false, nil
		}
		if strings.TrimSpace(step.Run) != "" && javaStepScriptsMayRunTests(root, workflow, job, step) {
			return false, nil
		}
	}
	return true, nil
}

// A step ddtest did not resolve may start Java tests only when its commands
// may: an action or unreadable configuration may run anything, and a shell
// step's commands are read as outsideFrameworkJob reads them.
func (javaDiscovery) stepMayStartTests(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep) (bool, bool) {
	if strings.TrimSpace(step.Run) == "" {
		return step.Uses != "" || step.ResolutionError != "", true
	}
	return javaStepMayRunTests(root, workflow, job, step), true
}

// javaStepMayRunTests reports whether a shell step's commands may start Java
// tests, with Maven goals MAVEN_ARGS or .mvn/maven.config may add.
func javaStepMayRunTests(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep) bool {
	return javaStepStartsTests(root, workflow, job, step, true)
}

// javaStepStartsTests is javaStepMayRunTests, where imageBuilds counts a
// Docker image build whose Dockerfile may run tests, as RUN mvn package
// does inside the image; without it, only the job's own processes count.
// Docker commands are read by the shared rules (see dockerMayStart).
func javaStepStartsTests(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, imageBuilds bool) bool {
	implicitGoals := workflow.Env["MAVEN_ARGS"] != "" || job.Env["MAVEN_ARGS"] != "" || step.Env["MAVEN_ARGS"] != "" ||
		javaExportsMavenArgs(job) || javaConfigGoals(root, filepath.Clean(stepDirectory(workflow, job, step)))
	directory, err := repositoryDirectory(root, root, stepDirectory(workflow, job, step))
	if err != nil {
		directory = ""
	}
	docker := func(args []string) bool {
		if !imageBuilds && javaDockerBuild(args) {
			return false
		}
		return dockerMayStart(root, directory, args, 0, func(body string) bool {
			return mayStartTestsIn(root, "", runtimeStep{Run: body}, javaMaven, 1)
		})
	}
	return javaMayRunTestsWith(workflowExpression.ReplaceAllLiteralString(step.Run, "${DDTEST_UNREAD}"), implicitGoals, docker)
}

// javaDockerBuild reports docker (or podman) arguments that build an image:
// build, buildx build, image build or builder build.
func javaDockerBuild(args []string) bool {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		option := args[0]
		args = args[1:]
		if slices.Contains([]string{"--context", "-c", "-H", "--host", "--config", "-l", "--log-level"}, option) && len(args) > 0 {
			args = args[1:]
		}
	}
	return len(args) > 0 && (args[0] == "build" || slices.Contains([]string{"buildx", "image", "builder"}, args[0]) && len(args) > 1 && args[1] == "build")
}

// javaImageBuildOnly reports a step whose commands may start tests only in
// the Docker images they build.
func javaImageBuildOnly(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep) bool {
	return !javaStepStartsTests(root, workflow, job, step, false) && javaStepStartsTests(root, workflow, job, step, true)
}

// javaUnreadValue stands for a value ddtest does not read, such as a CI
// expression a matrix row fills in or a shell expansion: it may name a test
// goal, a test task or a command that runs tests.
const javaUnreadValue = "verify"

// javaReleaseTools never start Maven, Gradle or a repository script: shell
// builtins and control, text utilities, and the signing, download, version
// control, archive and SDK tools release jobs run. Tools that run other
// commands, such as xargs or find -exec, are not among them.
var javaReleaseTools = append(slices.Clone(javaShellBuiltins),
	"exit", "export", "set", "test", "[", "sleep", "head", "tail", "wc", "cut", "tr", "sort", "uniq", "tee", "date", "basename", "dirname",
	"which", "sed", "awk", "grep", "jq", "gpg", "curl", "wget", "git", "gh", "base64", "sha256sum", "shasum", "md5sum", "zip", "unzip", "gzip", "xz", "bzip2", "sdk",
	// System package managers install packages; none runs the project.
	"apt", "apt-get", "apk", "yum", "dnf", "microdnf", "zypper", "dpkg")

// javaWrappers run the command that follows their own options and assignments.
var javaWrappers = []string{"env", "time", "nohup", "sudo", "exec", "command"}

// javaMayRunTests reports whether shell text may start Java tests; with
// implicitGoals, any Maven command may. Text it cannot parse may.
func javaMayRunTests(text string, implicitGoals bool) bool {
	return javaMayRunTestsWith(text, implicitGoals, func([]string) bool { return true })
}

// javaMayRunTestsWith is javaMayRunTests, with docker reporting whether a
// docker or podman command, by its arguments, may start tests.
func javaMayRunTestsWith(text string, implicitGoals bool, docker func(args []string) bool) bool {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(text), "")
	if err != nil {
		return true
	}
	// A shell that reads a downloaded installer from a pipe, as in
	// curl -s https://get.sdkman.io | bash, runs no repository code.
	installers := map[*syntax.CallExpr]bool{}
	// MAVEN_ARGS set in the script, inline or exported, adds goals to every
	// later Maven command.
	syntax.Walk(file, func(node syntax.Node) bool {
		if assign, ok := node.(*syntax.Assign); ok && assign.Name != nil && assign.Name.Value == "MAVEN_ARGS" {
			implicitGoals = true
		}
		return !implicitGoals
	})
	runs := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if runs {
			return false
		}
		switch node := node.(type) {
		case *syntax.BinaryCmd:
			if from, to := pipeCall(node.X), pipeCall(node.Y); node.Op == syntax.Pipe && from != nil && to != nil &&
				slices.Contains([]string{"curl", "wget"}, shellWord(from.Args[0])) {
				installers[to] = true
			}
		case *syntax.CallExpr:
			if len(node.Args) == 0 {
				return true
			}
			words := make([]string, 0, len(node.Args))
			for _, word := range node.Args {
				words = append(words, shellWord(word))
			}
			runs = javaCommandMayRunTests(words, installers[node], implicitGoals, docker)
		}
		return !runs
	})
	return runs
}

// pipeCall is the simple command one side of a pipe runs, if it is one.
func pipeCall(statement *syntax.Stmt) *syntax.CallExpr {
	if call, ok := statement.Cmd.(*syntax.CallExpr); ok && len(call.Args) > 0 {
		return call
	}
	return nil
}

// javaOneWordVariables hold one path the runner sets, or a CI expression's
// value (see javaStepMayRunTests), which GitHub fills in as a context value
// or secret: an option's value they fill in stays one word.
var javaOneWordVariables = []string{"DDTEST_UNREAD", "HOME", "GITHUB_WORKSPACE", "RUNNER_TEMP", "RUNNER_TOOL_CACHE", "CI_PROJECT_DIR", "CI_BUILDS_DIR", "BUILDKITE_BUILD_CHECKOUT_PATH", "CIRCLE_WORKING_DIRECTORY", "WORKSPACE"}

// shellWord is a word's text with every expansion, such as $HOME or $(date),
// read as javaUnreadValue. A word an expansion is part of, as surefire:$GOAL,
// is wholly unread unless it is an option whose value alone the expansion
// chooses, as -Pkey="$SECRET" or -Psigning.secretKeyRingFile=$HOME/secring.gpg.
// Elsewhere an unquoted one may split into more words, as goals or tasks,
// unless it reads one of javaOneWordVariables.
func shellWord(word *syntax.Word) string {
	var text strings.Builder
	unread, unquoted := false, false
	var write func(parts []syntax.WordPart, quoted bool)
	write = func(parts []syntax.WordPart, quoted bool) {
		for _, part := range parts {
			switch part := part.(type) {
			case *syntax.Lit:
				text.WriteString(part.Value)
			case *syntax.SglQuoted:
				text.WriteString(part.Value)
			case *syntax.DblQuoted:
				write(part.Parts, true)
			default:
				parameter, plain := part.(*syntax.ParamExp)
				value := plain && parameter.Param != nil && slices.Contains(javaOneWordVariables, parameter.Param.Value) &&
					strings.HasPrefix(text.String(), "-") && strings.Contains(text.String(), "=")
				text.WriteString(javaUnreadValue)
				unread, unquoted = true, unquoted || !quoted && !value
			}
		}
	}
	write(word.Parts, false)
	if unread && (unquoted || !strings.HasPrefix(text.String(), "-")) {
		return javaUnreadValue
	}
	return text.String()
}

// javaCommandMayRunTests reports whether one command, as words, may start Java
// tests; installer marks a shell that reads a downloaded script from a pipe,
// and implicitGoals Maven goals the command line does not show.
func javaCommandMayRunTests(words []string, installer, implicitGoals bool, docker func(args []string) bool) bool {
	for len(words) > 0 && slices.Contains(javaWrappers, filepath.Base(words[0])) {
		words = words[1:]
		for len(words) > 0 && (strings.HasPrefix(words[0], "-") || strings.Contains(words[0], "=")) {
			words = words[1:]
		}
	}
	if len(words) == 0 {
		return false
	}
	words = mavenDaemonAsMaven(words)
	args := words[1:]
	name := filepath.Base(words[0])
	switch {
	case gradle.Command(words):
		tasks := gradle.Tasks(args)
		// Without tasks, Gradle runs the project's default tasks.
		return !gradle.DryRun(args) && (len(tasks) == 0 || slices.ContainsFunc(tasks, func(task string) bool { return !gradle.NonTestTask(task) }))
	case mavenCommand(words):
		if implicitGoals {
			return true
		}
		if len(maven.Goals(args)) == 0 {
			return !maven.Metadata(args)
		}
		return maven.IsTestCommand(words[0], args) && !maven.SkipsTests(args)
	case javaRepositoryScript(words):
		return true
	// Git runs commands for git submodule foreach, git bisect run and
	// git rebase --exec.
	case name == "git":
		return slices.Contains(args, "foreach") || slices.Contains(args, "bisect") && slices.Contains(args, "run") ||
			slices.Contains(args, "rebase") && slices.ContainsFunc(args, func(arg string) bool { return arg == "-x" || strings.HasPrefix(arg, "--exec") })
	case name == "source" || name == ".":
		// A file the runner provides, outside the checkout, as SDKMAN's init script.
		return len(args) == 0 || !filepath.IsAbs(args[0])
	case name == "docker" || name == "podman":
		return docker(args)
	case name == "bash" || name == "sh":
		return !installer || slices.ContainsFunc(args, func(arg string) bool { return !strings.HasPrefix(arg, "-") || arg == "-c" })
	}
	return !slices.Contains(javaReleaseTools, words[0])
}
