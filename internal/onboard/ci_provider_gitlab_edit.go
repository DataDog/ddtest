// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// planSetupEdits inserts the setup entry before each target's script entry.
// An entry several jobs share (a template, !reference or YAML anchor) is
// edited only when every job using it is a target that needs the same
// insertion; otherwise the edit could change another job.
func (gitLabCI) planSetupEdits(root string, workflows []ciWorkflow, targets []ciSetupTarget, request ciSetupRequest) ([]setupEdit, []string, error) {
	type insertion struct {
		entry gitLabStep
		after []string
		jobs  []string
	}
	planned := map[*yaml.Node]*insertion{}
	// Entries some job runs through a YAML alias (- *name).
	aliased := map[*yaml.Node]bool{}
	for _, workflow := range workflows {
		for _, job := range workflow.Jobs {
			for _, step := range job.Steps {
				if entry, ok := step.origin.(gitLabStep); ok && entry.aliased {
					aliased[entry.node] = true
				}
			}
		}
	}
	for _, target := range targets {
		job, step, ok := gitLabTarget(workflows, target)
		if !ok {
			return nil, nil, &SetupGap{target.Workflow + " / " + target.Job, "The test step is not a GitLab CI script entry ddtest can edit."}
		}
		entry, ok := step.origin.(gitLabStep)
		if !ok || entry.node == nil || entry.file == "" {
			return nil, nil, &SetupGap{target.Workflow + " / " + target.Job, "The test step is not a GitLab CI script entry ddtest can edit."}
		}
		if aliased[entry.node] {
			return nil, nil, &EditThenApplyGap{SetupGap{entry.file + ":" + strconv.Itoa(entry.node.Line), "This script entry is shared through a YAML anchor and alias (&name/*name), so one insertion cannot reach every job that runs it. Write the test command in each job's script, then rerun onboard --apply."}}
		}
		if reason := gitLabImageProblem(job.Image, gitLabCurlBefore(job, target.Step)); reason != "" {
			return nil, nil, &EditThenApplyGap{SetupGap{target.Workflow + " / " + target.Job, reason}}
		}
		after := request.After
		if target.After != nil {
			after = target.After
		}
		if existing := planned[entry.node]; existing != nil {
			if !slices.Equal(existing.after, after) {
				return nil, nil, &SetupGap{entry.file + ":" + strconv.Itoa(entry.node.Line), "Jobs " + strings.Join(append(existing.jobs, target.Job), ", ") + " share this script entry but need different setup; give each its own script before applying."}
			}
			existing.jobs = append(existing.jobs, target.Job)
			continue
		}
		planned[entry.node] = &insertion{entry: entry, after: after, jobs: []string{target.Job}}
	}
	// Every job that can run and uses an edited entry must be a target.
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			if disabledCondition(workflow.Jobs[name].If) {
				continue
			}
			for _, step := range workflow.Jobs[name].Steps {
				entry, ok := step.origin.(gitLabStep)
				if !ok || planned[entry.node] == nil || slices.Contains(planned[entry.node].jobs, name) {
					continue
				}
				return nil, nil, &EditThenApplyGap{SetupGap{entry.file + ":" + strconv.Itoa(entry.node.Line), "Job " + name + " also uses this script entry (through extends, !reference or a YAML anchor) but does not run the selected tests; adding the setup there would change that job. Give the test jobs their own script, then rerun onboard --apply."}}
			}
		}
	}
	byFile := map[string][]*insertion{}
	for _, item := range planned {
		byFile[item.entry.file] = append(byFile[item.entry.file], item)
	}
	var edits []setupEdit
	var impact []string
	for _, file := range slices.Sorted(maps.Keys(byFile)) {
		edit, err := readSetupEdit(root, file)
		if err != nil {
			return nil, nil, err
		}
		text := strings.ReplaceAll(string(edit.before), "\r\n", "\n")
		newline := "\n"
		if strings.Contains(string(edit.before), "\r\n") {
			newline = "\r\n"
		}
		lines := strings.Split(text, "\n")
		items := byFile[file]
		slices.SortFunc(items, func(a, b *insertion) int { return b.entry.node.Line - a.entry.node.Line })
		for _, item := range items {
			location := file + ":" + strconv.Itoa(item.entry.node.Line)
			node := item.entry.node
			if node.Line < 1 || node.Line > len(lines) {
				return nil, nil, &SetupGap{location, "The script entry's position could not be located."}
			}
			line := lines[node.Line-1]
			column := min(max(node.Column-1, 0), len(line))
			prefix := line[:column]
			request := request
			request.After = item.after
			if !item.entry.listed {
				// script: <command> becomes a list holding the setup and the command.
				key := strings.TrimRight(prefix, " ")
				indent := len(line) - len(strings.TrimLeft(line, " "))
				if !strings.HasSuffix(key, ":") || node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 || strings.Contains(node.Value, "\n") {
					return nil, nil, &EditThenApplyGap{SetupGap{location, "The script is a single string ddtest cannot rewrite safely; make it a list of commands, then rerun onboard --apply."}}
				}
				pad := strings.Repeat(" ", indent+2)
				replacement := key + "\n" + strings.TrimSuffix(gitLabSetupText(request, pad), "\n") + "\n" + pad + "- " + line[column:]
				lines = slices.Replace(lines, node.Line-1, node.Line, replacement)
				continue
			}
			dash := strings.TrimRight(prefix, " ")
			if !strings.HasSuffix(dash, "-") || strings.TrimSpace(strings.TrimSuffix(dash, "-")) != "" {
				return nil, nil, &EditThenApplyGap{SetupGap{location, "The script list is not a block list ddtest can extend; write it with one \"- command\" per line, then rerun onboard --apply."}}
			}
			pad := dash[:len(dash)-1]
			lines = slices.Insert(lines, node.Line-1, strings.TrimSuffix(gitLabSetupText(request, pad), "\n"))
		}
		edit.after = []byte(strings.ReplaceAll(strings.Join(lines, "\n"), "\n", newline))
		var document yaml.Node
		if err := yaml.Unmarshal(edit.after, &document); err != nil {
			return nil, nil, &SetupGap{file, "Planned CI edits are not valid YAML; source files were preserved: " + err.Error()}
		}
		if file != gitLabCIFile {
			impact = append(impact, fmt.Sprintf("%s is included by %s; any other project that includes it (include:project) receives the Datadog setup too.", file, gitLabCIFile))
		}
		edits = append(edits, edit)
	}
	return edits, impact, nil
}

// gitLabTarget finds a target's job and step.
func gitLabTarget(workflows []ciWorkflow, target ciSetupTarget) (runtimeJob, runtimeStep, bool) {
	for _, workflow := range workflows {
		if workflow.Path != target.Workflow {
			continue
		}
		job, ok := workflow.Jobs[target.Job]
		if !ok || target.Step < 0 || target.Step >= len(job.Steps) {
			return runtimeJob{}, runtimeStep{}, false
		}
		return job, job.Steps[target.Step], true
	}
	return runtimeJob{}, runtimeStep{}, false
}

// gitLabCurlBefore reports an entry before step that installs curl.
func gitLabCurlBefore(job runtimeJob, step int) bool {
	for _, earlier := range job.Steps[:step] {
		if gitLabInstallsCurl(earlier.Run) {
			return true
		}
	}
	return false
}
