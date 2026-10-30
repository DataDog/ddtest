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

// A release or publishing job never runs Java tests when nothing its steps run
// can start them: only release tools, Maven outside the test lifecycle or with
// tests skipped, and Gradle tasks that by convention run none. Expressions
// choose values there, such as secrets or versions, so each is read as a plain
// word; one that chooses a command or task leaves an unknown word, which may
// run tests. Actions are not read, as resolveJavaStep reads none as a test step.
func (javaDiscovery) outsideFrameworkJob(_ string, workflow ciWorkflow, job runtimeJob, row map[string]any, _ string) bool {
	for _, step := range job.Steps {
		if active, err := stepCondition(step, row, conditionEnvironment(workflow, job, runtimeStep{}, row)); err == nil && !active {
			continue
		}
		if step.ResolutionError != "" {
			return false
		}
		if strings.TrimSpace(step.Run) != "" && javaMayRunTests(workflowExpression.ReplaceAllString(step.Run, "expression")) {
			return false
		}
	}
	return true
}

// javaReleaseTools never start Maven, Gradle or a repository script: shell
// builtins and control, text utilities, and the signing, download, version
// control, archive and SDK tools release jobs run. Tools that run other
// commands, such as xargs or find -exec, are not among them.
var javaReleaseTools = append(slices.Clone(javaShellBuiltins),
	"exit", "export", "set", "test", "[", "sleep", "head", "tail", "wc", "cut", "tr", "sort", "uniq", "tee", "date", "basename", "dirname",
	"which", "sed", "awk", "grep", "jq", "gpg", "curl", "wget", "git", "gh", "base64", "sha256sum", "shasum", "md5sum", "zip", "unzip", "gzip", "sdk")

// javaWrappers run the command that follows their own options and assignments.
var javaWrappers = []string{"env", "time", "nohup", "sudo", "exec", "command"}

// javaMayRunTests reports whether shell text may start Java tests. Text it
// cannot parse may.
func javaMayRunTests(text string) bool {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(text), "")
	if err != nil {
		return true
	}
	// A shell that reads a downloaded installer from a pipe, as in
	// curl -s https://get.sdkman.io | bash, runs no repository code.
	installers := map[*syntax.CallExpr]bool{}
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
			runs = javaCommandMayRunTests(words, installers[node])
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

// shellWord is a word's text with every expansion, such as $HOME or $(date),
// read as x: it may choose a value, never a literal command or task.
func shellWord(word *syntax.Word) string {
	var text strings.Builder
	var write func(parts []syntax.WordPart)
	write = func(parts []syntax.WordPart) {
		for _, part := range parts {
			switch part := part.(type) {
			case *syntax.Lit:
				text.WriteString(part.Value)
			case *syntax.SglQuoted:
				text.WriteString(part.Value)
			case *syntax.DblQuoted:
				write(part.Parts)
			default:
				text.WriteString("x")
			}
		}
	}
	write(word.Parts)
	return text.String()
}

// javaCommandMayRunTests reports whether one command, as words, may start Java
// tests; installer marks a shell that reads a downloaded script from a pipe.
func javaCommandMayRunTests(words []string, installer bool) bool {
	for len(words) > 0 && slices.Contains(javaWrappers, filepath.Base(words[0])) {
		words = words[1:]
		for len(words) > 0 && (strings.HasPrefix(words[0], "-") || strings.Contains(words[0], "=")) {
			words = words[1:]
		}
	}
	if len(words) == 0 {
		return false
	}
	args := words[1:]
	name := filepath.Base(words[0])
	switch {
	case gradle.Command(words):
		tasks := gradle.Tasks(args)
		// Without tasks, Gradle runs the project's default tasks.
		return !gradle.DryRun(args) && (len(tasks) == 0 || slices.ContainsFunc(tasks, func(task string) bool { return !gradle.NonTestTask(task) }))
	case mavenCommand(words):
		if len(maven.Goals(args)) == 0 {
			return !maven.Metadata(args)
		}
		return maven.IsTestCommand(words[0], args) && !maven.SkipsTests(args)
	case javaRepositoryScript(words):
		return true
	case name == "source" || name == ".":
		// A file the runner provides, outside the checkout, as SDKMAN's init script.
		return len(args) == 0 || !filepath.IsAbs(args[0])
	case name == "bash" || name == "sh":
		return !installer || slices.ContainsFunc(args, func(arg string) bool { return !strings.HasPrefix(arg, "-") || arg == "-c" })
	}
	return !slices.Contains(javaReleaseTools, words[0])
}
