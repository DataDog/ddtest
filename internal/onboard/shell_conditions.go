// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Select only integer conditions whose operands are known after matrix
// substitution. Unknown conditions retain their full original setup for review.
// This reads shell syntax as data; it never runs the condition or its commands.
func resolveStaticShellBranches(command string) string {
	file, ok := parseCIShell(command)
	if !ok {
		return command
	}
	changed := false
	var statements []*syntax.Stmt
	for _, stmt := range file.Stmts {
		branch, ok := stmt.Cmd.(*syntax.IfClause)
		if !ok || stmt.Background || stmt.Negated || stmt.Coprocess || stmt.Disown || len(stmt.Redirs) != 0 ||
			(branch.Else != nil && len(branch.Else.Cond) != 0) {
			statements = append(statements, stmt)
			continue
		}
		active, known := staticShellCondition(branch.Cond)
		if !known {
			statements = append(statements, stmt)
			continue
		}
		changed = true
		if active {
			statements = append(statements, branch.Then...)
		} else if branch.Else != nil {
			statements = append(statements, branch.Else.Then...)
		}
	}
	if !changed {
		return command
	}
	file.Stmts = statements
	return shellText(file)
}

func staticShellCondition(stmts []*syntax.Stmt) (bool, bool) {
	if len(stmts) != 1 {
		return false, false
	}
	stmt := stmts[0]
	if stmt.Background || stmt.Negated || stmt.Coprocess || stmt.Disown || len(stmt.Redirs) != 0 {
		return false, false
	}
	clause, ok := stmt.Cmd.(*syntax.TestClause)
	if !ok {
		return false, false
	}
	test, ok := clause.X.(*syntax.BinaryTest)
	if !ok {
		return false, false
	}
	x, ok1 := test.X.(*syntax.Word)
	y, ok2 := test.Y.(*syntax.Word)
	if !ok1 || !ok2 {
		return false, false
	}
	a, ok1 := staticShellInteger(x)
	b, ok2 := staticShellInteger(y)
	if !ok1 || !ok2 {
		return false, false
	}
	switch test.Op.String() {
	case "-lt":
		return a < b, true
	case "-le":
		return a <= b, true
	case "-gt":
		return a > b, true
	case "-ge":
		return a >= b, true
	case "-eq":
		return a == b, true
	case "-ne":
		return a != b, true
	default:
		return false, false
	}
}

func staticShellInteger(word *syntax.Word) (int64, bool) {
	valid := true
	syntax.Walk(word, func(node syntax.Node) bool {
		switch node.(type) {
		case *syntax.Word, *syntax.Lit, *syntax.SglQuoted, *syntax.DblQuoted, nil:
		case *syntax.CmdSubst:
			return false // The bounded callback validates its entire AST.
		default:
			valid = false
			return false
		}
		return true
	})
	if !valid {
		return 0, false
	}
	value, err := expand.Literal(&expand.Config{Env: expand.ListEnviron(), CmdSubst: staticVersionField}, word)
	if err != nil {
		return 0, false
	}
	// Bash interprets leading-zero numbers as octal. Leave those for review.
	digits := strings.TrimLeft(value, "+-")
	if len(digits) > 1 && digits[0] == '0' {
		return 0, false
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil
}

// cut over a literal here-string is a common read-only major/minor extraction.
// No external executable, variable expansion, pipeline or second command runs.
func staticVersionField(output io.Writer, subst *syntax.CmdSubst) error {
	unsupported := fmt.Errorf("unknown shell command substitution")
	if subst.Backquotes || len(subst.Stmts) != 1 {
		return unsupported
	}
	stmt := subst.Stmts[0]
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) != 0 || stmt.Background || stmt.Negated || stmt.Coprocess || stmt.Disown || len(stmt.Redirs) != 1 {
		return unsupported
	}
	var args []string
	for _, word := range call.Args {
		value, err := literalShellWord(word)
		if err != nil {
			return unsupported
		}
		args = append(args, value)
	}
	if len(args) != 3 || !slices.Equal(args[:2], []string{"cut", "-d."}) || (args[2] != "-f1" && args[2] != "-f2") {
		return unsupported
	}
	redir := stmt.Redirs[0]
	if redir.Op != syntax.WordHdoc || redir.N != nil {
		return unsupported
	}
	value, err := literalShellWord(redir.Word)
	if err != nil || strings.ContainsAny(value, "\r\n") {
		return unsupported
	}
	fields := strings.Split(value, ".")
	index := 0
	if args[2] == "-f2" {
		index = 1
	}
	if index >= len(fields) {
		return unsupported
	}
	_, err = io.WriteString(output, fields[index])
	return err
}
