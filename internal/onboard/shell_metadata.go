// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Inspect a small set of shell metadata forms that the literal-command scanner
// rejects. The syntax parser is never an interpreter. Substitutions are followed
// as commands, not discarded as echo text, and never used as executable names.
func resolveMetadataStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, frameworks ...string) (commandResolution, bool) {
	commands, ok := metadataInvocations(step.Run)
	if !ok {
		return commandResolution{}, false
	}
	framework := "jest"
	if len(frameworks) > 0 {
		framework = frameworks[0]
	}
	var result commandResolution
	allReviewed := true
	for _, command := range commands {
		child := step
		child.Run = command
		part := resolveTestStep(root, workflow, job, child, "javascript", framework)
		result.Matched = result.Matched || part.Matched
		if part.Evidence != "" {
			result.Evidence += part.Evidence + "; "
		}
		if part.Reason != "" {
			result.Reason += part.Reason + "; "
			allReviewed = allReviewed && part.Review
		}
	}
	result.Evidence = strings.TrimSuffix(result.Evidence, "; ")
	result.Reason = strings.TrimSuffix(result.Reason, "; ")
	result.Review = !result.Matched && result.Reason != "" && allReviewed
	// SingleJest stays false: appended Jest flags cannot be forwarded through
	// echo, redirection or conditional metadata checks.
	return result, true
}

func metadataInvocations(command string) ([]string, bool) {
	if len(command) > 16384 {
		return nil, false
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, false
	}
	printNode := func(node syntax.Node) string {
		var out strings.Builder
		_ = syntax.NewPrinter().Print(&out, node)
		return out.String()
	}
	var commands []string
	addLiteral := func(stmt *syntax.Stmt) bool {
		text := printNode(stmt)
		if _, err := staticCommands(text); err != nil {
			return false
		}
		commands = append(commands, text)
		return len(commands) <= 64
	}
	isEcho := func(stmt *syntax.Stmt) bool {
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		return ok && len(call.Args) > 0 && slices.Contains([]string{"echo", "printf"}, call.Args[0].Lit())
	}
	isDiff := func(stmt *syntax.Stmt) bool {
		text := printNode(stmt)
		words, err := staticCommands(text)
		return err == nil && len(words) == 1 && slices.Equal(words[0], []string{"git", "diff", "--quiet", "--exit-code"})
	}
	var visit func(*syntax.Stmt, int) bool
	visit = func(stmt *syntax.Stmt, depth int) bool {
		if depth > 16 || stmt.Background || stmt.Negated || stmt.Coprocess || stmt.Disown {
			return false
		}
		if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok {
			if len(stmt.Redirs) != 0 {
				return false
			}
			if binary.Op == syntax.OrStmt {
				if !isDiff(binary.X) || !isEcho(binary.Y) {
					return false
				}
			} else if binary.Op != syntax.AndStmt {
				return false
			}
			return visit(binary.X, depth+1) && visit(binary.Y, depth+1)
		}
		if !isEcho(stmt) {
			return addLiteral(stmt)
		}
		call := stmt.Cmd.(*syntax.CallExpr)
		if len(call.Assigns) != 0 {
			return false
		}
		for _, redirect := range stmt.Redirs {
			target := printNode(redirect.Word)
			if redirect.Op != syntax.AppOut || redirect.N != nil || !slices.Contains([]string{"$GITHUB_OUTPUT", "${GITHUB_OUTPUT}", `"$GITHUB_OUTPUT"`, `"${GITHUB_OUTPUT}"`}, target) {
				return false
			}
		}
		valid := true
		for _, word := range call.Args[1:] {
			syntax.Walk(word, func(node syntax.Node) bool {
				if !valid {
					return false
				}
				switch part := node.(type) {
				case nil, *syntax.Word, *syntax.Lit:
				case *syntax.SglQuoted:
					valid = !part.Dollar
				case *syntax.DblQuoted:
					valid = !part.Dollar
				case *syntax.CmdSubst:
					if part.TempFile || part.ReplyVar {
						valid = false
					}
					for _, nested := range part.Stmts {
						valid = valid && addLiteral(nested)
					}
					return false
				default:
					valid = false
				}
				return valid
			})
		}
		return valid
	}
	for _, stmt := range file.Stmts {
		if !visit(stmt, 0) {
			return nil, false
		}
	}
	return commands, true
}
