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
	"mvdan.cc/sh/v3/syntax"
)

// javaNonLaunchingTools start no Maven, Gradle or JVM build, nor another
// command that does, whatever their arguments: the shell builtins, text
// utilities and release tools javaReleaseTools lists, the check tools every
// language shares (see checkAndPackageTools), user management, and the
// deploy, scan and load-test tools that deploy, scan and image scripts run.
// Any other executable, such as the JVM, another build tool, a task runner,
// a package manager, a command runner (xargs, timeout, ssh) or one ddtest
// doesn't know, may.
var javaNonLaunchingTools = slices.Concat(javaReleaseTools, checkAndPackageTools, reportTools, []string{
	"read", "local", "declare", "typeset", "readonly", "unset", "shift", "return", "false", ":", "trap", "wait", "umask", "getopts",
	"rmdir", "chown", "stat", "mktemp", "readlink", "realpath", "uname", "whoami", "hostname", "nproc", "id", "printenv", "envsubst", "yq",
	"groupadd", "useradd", "usermod", "groupmod", "addgroup", "adduser", "update-ca-certificates",
	"helm", "kubectl", "trivy", "grype", "cosign", "checkov", "tfsec", "k6", "zap-baseline.py", "zap-full-scan.py", "zap-api-scan.py", "aws", "gcloud", "az",
})

// javaInterpreters run code ddtest doesn't read, a file or inline, which may
// start Maven, Gradle or the JVM; a language's own interpreter and tools are
// classified by the language (see discoveryLanguage.checkTool).
var javaInterpreters = []string{"node", "deno", "tsx", "ts-node", "perl", "php", "groovy", "kotlin", "scala"}

// javaShellMayRunTests reports whether shell text, such as a repository
// script's, run in directory may start Java tests. It needs evidence: a
// Maven or Gradle command that may run tests, a JVM or command launcher, an
// interpreter running code (see javaInterpreters), a
// container run, a docker build whose RUN instructions may, a script or
// expansion ddtest can't read (see shellScriptMayStart), or any executable
// ddtest doesn't know. Only tools known to launch nothing else, such as
// helm, trivy, hadolint or curl (see javaNonLaunchingTools), can't start a
// JVM build. Maven Daemon reads as Maven (see mavenDaemonAsMaven). With
// implicitGoals, or when the text mentions MAVEN_ARGS, any Maven command
// may.
func javaShellMayRunTests(root, directory, text string, args []string, implicitGoals bool, depth int) bool {
	implicitGoals = implicitGoals || strings.Contains(text, "MAVEN_ARGS")
	return shellScriptMayStart(root, directory, text, args, depth, func(call *syntax.CallExpr, start int) (bool, bool) {
		words := make([]string, 0, len(call.Args)-start)
		for _, word := range call.Args[start:] {
			words = append(words, shellWord(word))
		}
		words = mavenDaemonAsMaven(words)
		if gradle.Command(words) || mavenCommand(words) {
			return javaCommandMayRunTests(words, false, implicitGoals, javaUnreadDocker), true
		}
		if javaRepositoryScript(words[:1]) {
			return false, false // A repository script, read as one.
		}
		return javaLaunchMayRunTests(root, directory, call, start, words, implicitGoals, depth), true
	})
}

// javaLaunchMayRunTests reports whether one command of a script, as words
// after its wrappers, may start Java tests (see javaShellMayRunTests).
func javaLaunchMayRunTests(root, directory string, call *syntax.CallExpr, start int, words []string, implicitGoals bool, depth int) bool {
	switch name := filepath.Base(words[0]); name {
	case "docker", "podman":
		literal := make([]string, 0, len(call.Args)-start-1)
		for _, word := range call.Args[start+1:] {
			value, ok := shellLiteralWord(word)
			if !ok {
				value = "$" // An expansion: an image tag or a value ddtest doesn't read.
			}
			literal = append(literal, value)
		}
		return dockerMayStart(root, directory, literal, depth, func(body string) bool {
			// RUN runs in the image, whose files ddtest doesn't read.
			return javaShellMayRunTests(root, "", body, nil, implicitGoals, depth+1)
		})
	case "find":
		return slices.ContainsFunc(words, func(word string) bool { return slices.Contains([]string{"-exec", "-execdir", "-ok", "-okdir"}, word) })
	case "git":
		return javaCommandMayRunTests(words, false, false, javaUnreadDocker)
	default:
		if slices.Contains(javaInterpreters, name) || registeredScriptInterpreter(name) {
			// Any code it runs may; a version or help query doesn't.
			return len(words) == 1 || slices.ContainsFunc(words[1:], func(arg string) bool { return !slices.Contains(interpreterMetadataFlags, arg) })
		}
		// A language's interpreter or tool that runs code, such as
		// python3 ci/run_tests.py, may; one that only manages packages
		// doesn't.
		for _, language := range discoveryLanguages {
			if mayStart, handled := language.checkTool(root, directory, words); handled {
				return mayStart
			}
		}
		return !slices.Contains(javaNonLaunchingTools, name)
	}
}

// javaStepScriptsMayRunTests reports whether a CI step may start Java tests,
// reading the repository scripts it runs with the rest of its commands (see
// javaShellMayRunTests), with the Maven goals MAVEN_ARGS or
// .mvn/maven.config may add. Expressions are read as values ddtest doesn't
// know, as javaStepMayRunTests reads them.
func javaStepScriptsMayRunTests(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep) bool {
	relative := filepath.Clean(stepDirectory(workflow, job, step))
	directory, err := repositoryDirectory(root, root, relative)
	if err != nil {
		directory = ""
	}
	implicitGoals := workflow.Env["MAVEN_ARGS"] != "" || job.Env["MAVEN_ARGS"] != "" || step.Env["MAVEN_ARGS"] != "" || javaExportsMavenArgs(job) || javaConfigGoals(root, relative)
	run := repositoryRelativeCommands(step.Run, job.Checkout, relative)
	return javaShellMayRunTests(root, directory, workflowExpression.ReplaceAllLiteralString(run, "${DDTEST_UNREAD}"), nil, implicitGoals, 0)
}

// javaUnreadDocker is the docker reader of a script command already read as
// Maven, Gradle or git, which never runs docker: may start tests.
func javaUnreadDocker([]string) bool { return true }
