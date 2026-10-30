// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kballard/go-shellquote"
	"mvdan.cc/sh/v3/syntax"
)

// This is classification for manual review, never an execution plan. Browser
// setup variables can be opaque, but every command must resolve without a Node
// Mocha consumer or an unknown executable before moving the step out of scope.
func reviewMochaBrowserStep(root, directory, command string) (commandResolution, bool) {
	file, ok := parseCIShell(command)
	if !ok {
		return commandResolution{}, false
	}
	// Do not hide test execution in exports, assignment substitutions, or
	// redirections. Only a literal `which <browser>` lookup is metadata.
	valid := true
	syntax.Walk(file, func(node syntax.Node) bool {
		switch part := node.(type) {
		case *syntax.CmdSubst:
			words, err := staticCommands(shellStatements(part.Stmts))
			if err != nil || len(words) != 1 || len(words[0]) != 2 || words[0][0] != "which" {
				valid = false
			}
		case *syntax.ProcSubst, *syntax.ArithmExp:
			valid = false
		}
		return true
	})
	if !valid {
		return commandResolution{}, false
	}
	var commands []string
	for _, stmt := range file.Stmts {
		if declaration, ok := stmt.Cmd.(*syntax.DeclClause); ok && declaration.Variant.Value == "export" {
			continue
		}
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		if !ok || len(stmt.Redirs) > 0 || stmt.Background {
			return commandResolution{}, false
		}
		if len(call.Args) == 0 {
			continue
		}
		var words []string
		for _, word := range call.Args {
			if literal := word.Lit(); literal != "" {
				words = append(words, literal)
				continue
			}
			text := shellText(word)
			// Only ordinary variable reads can be irrelevant browser arguments.
			if strings.ContainsAny(text, "`();<>") || !strings.Contains(text, "$") {
				return commandResolution{}, false
			}
			words = append(words, "browser-argument")
		}
		commands = append(commands, shellquote.Join(words...))
	}
	remaining := &scriptBudget{left: packageScriptExpansionLimit}
	result := resolveJestSequence(root, directory, strings.Join(commands, "\n"), nil, remaining, "mocha")
	if result.Matched || result.Reason != "" || !strings.Contains(result.ReviewReason, "browser") {
		return commandResolution{}, false
	}
	return commandResolution{Review: true, Reason: "Browser test entry points are outside Node Mocha validation; review browser execution separately. " + result.ReviewReason}, true
}

// Recognize a repository shell wrapper whose only test runner is mochify.
// A loop may select browsers; unknown commands/subprocesses keep it unresolved.
func mochaBrowserShell(root, directory, name string) bool {
	if !filepath.IsLocal(name) || filepath.Ext(name) != ".sh" {
		return false
	}
	path, err := filepath.EvalSymlinks(filepath.Join(directory, name))
	canonicalRoot, _ := filepath.EvalSymlinks(root)
	relative, relErr := filepath.Rel(canonicalRoot, path)
	if err != nil || relErr != nil || !filepath.IsLocal(relative) {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 16384 {
		return false
	}
	file, ok := parseCIShell(string(data))
	if !ok {
		return false
	}
	browser, unknown := false, false
	syntax.Walk(file, func(node syntax.Node) bool {
		if call, ok := node.(*syntax.CallExpr); ok && len(call.Args) > 0 {
			switch call.Args[0].Lit() {
			case "mochify":
				browser = true
			case "set", "echo", "printf":
			default:
				unknown = true
			}
		}
		return true
	})
	return browser && !unknown
}

func shellStatements(statements []*syntax.Stmt) string {
	var parts []string
	for _, statement := range statements {
		parts = append(parts, shellText(statement))
	}
	return strings.Join(parts, "\n")
}
