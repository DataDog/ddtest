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
	previousChain := 0
	for _, command := range commands {
		if previousChain != 0 && previousChain != command.Chain && len(result.Builds) > 0 {
			result.Setup = append(result.Setup, "Builds separated from tests by shell metadata or statement boundaries require shell exit-policy review; they are not conditional prerequisites.")
			result.Builds = nil
		}
		previousChain = command.Chain
		child := step
		child.Run = command.Text
		part := resolveTestStep(root, workflow, job, child, "javascript", framework)
		for i := range part.Tests {
			part.Tests[i].Prerequisites = append(slices.Clone(result.Builds), part.Tests[i].Prerequisites...)
			part.Tests[i].UnvalidatedSetup = append(slices.Clone(result.Setup), part.Tests[i].UnvalidatedSetup...)
			if result.Reason != "" {
				part.Tests[i].UnvalidatedSetup = append(part.Tests[i].UnvalidatedSetup, result.Reason)
			}
		}
		result.Tests = append(result.Tests, part.Tests...)
		result.Builds = append(result.Builds, part.Builds...)
		result.Setup = append(result.Setup, part.Setup...)
		result.ReviewReason = joinDiagnostic(result.ReviewReason, part.ReviewReason)
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

type metadataInvocation struct {
	Text  string
	Chain int
}

// printf -v mutates shell state, even when its option is quoted. Share this
// classification with the CI setup path instead of treating every printf as IO.
func outputOnlyCall(call *syntax.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	if call.Args[0].Lit() == "echo" {
		return true
	}
	if call.Args[0].Lit() != "printf" || len(call.Args) < 2 {
		return false
	}
	option, err := literalShellWord(call.Args[1])
	return err == nil && !strings.HasPrefix(option, "-v")
}

func metadataInvocations(command string) ([]metadataInvocation, bool) {
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
	var commands []metadataInvocation
	chain := 0
	addLiteral := func(stmt *syntax.Stmt) bool {
		text := printNode(stmt)
		literal, err := staticCommands(text)
		if err != nil {
			return false
		}
		for _, words := range literal {
			if len(words) > 0 && words[0] == "cd" {
				return false
			}
		}
		commands = append(commands, metadataInvocation{Text: text, Chain: chain})
		return len(commands) <= 64
	}
	isEcho := func(stmt *syntax.Stmt) bool {
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		return ok && outputOnlyCall(call)
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
						chain++
						valid = valid && addLiteral(nested)
					}
					chain++
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
		chain++
		if !visit(stmt, 0) {
			return nil, false
		}
	}
	return commands, true
}
