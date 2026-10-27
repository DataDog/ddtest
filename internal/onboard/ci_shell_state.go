// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"maps"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
	"mvdan.cc/sh/v3/syntax"
)

// GitLab before_script/script and Buildkite command lists share a shell.
// Jenkins sh and CircleCI run steps deliberately do not use this propagation.
func nativeShellState(root string, job runtimeJob) runtimeJob {
	job.Steps = slices.Clone(job.Steps)
	env := maps.Clone(job.Env)
	directory := "."
	for i := range job.Steps {
		step := &job.Steps[i]
		step.Env = mergeEnvironment(env, step.Env)
		if step.WorkingDirectory == "" {
			step.WorkingDirectory = directory
		}
		file, ok := parseCIShell(step.Run)
		if !ok || len(file.Stmts) != 1 {
			continue
		}
		stmt := file.Stmts[0]
		if stmt.Background || stmt.Negated || len(stmt.Redirs) > 0 {
			continue
		}
		if declaration, ok := stmt.Cmd.(*syntax.DeclClause); ok && declaration.Variant.Value == "export" {
			values := map[string]string{}
			literal := true
			for _, assignment := range declaration.Args {
				if assignment.Name == nil || assignment.Naked || assignment.Append || assignment.Index != nil || assignment.Array != nil {
					literal = false
					break
				}
				value := ""
				var err error
				if assignment.Value != nil {
					value, err = literalShellWord(assignment.Value)
				}
				if err != nil {
					literal = false
					break
				}
				values[assignment.Name.Value] = value
			}
			if literal {
				maps.Copy(env, values)
				step.Run = ""
			} else {
				job.Gaps = append(job.Gaps, step.Source+": shell export needs resolved values before it can be replayed locally")
			}
			continue
		}
		words, err := shellquote.Split(step.Run)
		if err == nil && len(words) == 2 && words[0] == "cd" && !strings.ContainsAny(words[1], "$`~") {
			next, err := repositoryDirectory(root, root, directory+"/"+words[1])
			if err != nil {
				job.Gaps = append(job.Gaps, step.Source+": "+repositoryError(root, err))
			} else {
				directory = repositoryPath(root, next)
				step.Run = ""
			}
		}
	}
	return job
}
