// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// buildkiteEdit replaces lines [start, end) of a file (0-based) with lines.
type buildkiteEdit struct {
	start, end int
	lines      []string
}

// buildkiteInsertion is the setup one command statement needs.
type buildkiteInsertion struct {
	entry buildkiteEntry
	after []string
	jobs  []string
}

// planSetupEdits inserts the setup snippet before each target's test
// statement, in the same step: a new commands entry before a listed one, or
// lines inside a command block. A command several steps share (a YAML
// anchor, or matrix combinations needing different setup) is edited only
// when every step using it is a target needing the same insertion. A
// container that does not receive DD_API_KEY gets it in its plugin's
// environment list.
func (buildkite) planSetupEdits(root string, workflows []ciWorkflow, targets []ciSetupTarget, request ciSetupRequest) ([]setupEdit, []string, error) {
	type key struct {
		node *yaml.Node
		line int
	}
	planned := map[key]*buildkiteInsertion{}
	keys := map[*yaml.Node]*buildkiteExecutor{}
	var impact []string
	for _, target := range targets {
		job, step, ok := gitLabTarget(workflows, target)
		entry, isEntry := step.origin.(buildkiteEntry)
		location := target.Workflow + " / " + target.Job
		if !ok || !isEntry || entry.node == nil || entry.file == "" {
			return nil, nil, &SetupGap{location, "The test step is not a Buildkite command ddtest can edit."}
		}
		position := entry.file + ":" + strconv.Itoa(entry.fileLine())
		if entry.aliased {
			return nil, nil, &EditThenApplyGap{SetupGap{position, "This command is shared through a YAML anchor and alias (&name/*name), so one insertion cannot reach only the steps that run the tests. Write the commands in each test step, then rerun onboard --apply."}}
		}
		executor := entry.executor
		curl := gitLabCurlBefore(job, target.Step)
		if reason := executor.setupProblem(curl); reason != "" {
			passed := *executor
			passed.keyPassed = true
			if !executor.container || executor.plugin == nil || executor.pluginAliased || passed.setupProblem(curl) != "" {
				return nil, nil, &EditThenApplyGap{SetupGap{location, reason}}
			}
			keys[executor.plugin] = executor
		}
		after := request.After
		if target.After != nil {
			after = target.After
		}
		at := key{entry.node, entry.line}
		if existing := planned[at]; existing != nil {
			if !slices.Equal(existing.after, after) {
				return nil, nil, &SetupGap{position, "Steps " + strings.Join(append(existing.jobs, target.Job), ", ") + " share this command but need different setup; give each its own commands before applying."}
			}
			existing.jobs = append(existing.jobs, target.Job)
			continue
		}
		planned[at] = &buildkiteInsertion{entry: entry, after: after, jobs: []string{target.Job}}
	}
	// Every step that can run and uses an edited command must be a target.
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			if disabledCondition(workflow.Jobs[name].If) {
				continue
			}
			for _, step := range workflow.Jobs[name].Steps {
				entry, ok := step.origin.(buildkiteEntry)
				if !ok {
					continue
				}
				if item := planned[key{entry.node, entry.line}]; item != nil && !slices.Contains(item.jobs, name) {
					return nil, nil, &EditThenApplyGap{SetupGap{entry.file + ":" + strconv.Itoa(entry.node.Line), "Step " + name + " also runs this command but does not run the selected tests; adding the setup there would change that step. Give the test steps their own commands, then rerun onboard --apply."}}
				}
			}
		}
	}
	byFile := map[string][]*buildkiteInsertion{}
	for _, item := range planned {
		byFile[item.entry.file] = append(byFile[item.entry.file], item)
	}
	plugins := map[string][]*yaml.Node{}
	for node := range keys {
		for _, item := range planned {
			if item.entry.executor.plugin == node {
				plugins[item.entry.file] = append(plugins[item.entry.file], node)
				break
			}
		}
	}
	var edits []setupEdit
	for _, file := range slices.Sorted(maps.Keys(byFile)) {
		edit, err := readSetupEdit(root, file)
		if err != nil {
			return nil, nil, err
		}
		newline := "\n"
		if strings.Contains(string(edit.before), "\r\n") {
			newline = "\r\n"
		}
		lines := strings.Split(strings.ReplaceAll(string(edit.before), "\r\n", "\n"), "\n")
		var changes []buildkiteEdit
		for _, item := range byFile[file] {
			request := request
			request.After = item.after
			change, err := buildkiteInsert(lines, item.entry, request)
			if err != nil {
				return nil, nil, err
			}
			changes = append(changes, change)
		}
		for _, node := range plugins[file] {
			change, err := buildkitePassKey(lines, file, node)
			if err != nil {
				return nil, nil, err
			}
			changes = append(changes, change)
			impact = append(impact, file+":"+strconv.Itoa(node.Line)+": DD_API_KEY is added to the plugin's environment list, so the key reaches the container that runs the tests.")
		}
		slices.SortFunc(changes, func(a, b buildkiteEdit) int { return b.start - a.start })
		for i := 1; i < len(changes); i++ {
			if changes[i].end > changes[i-1].start {
				return nil, nil, &SetupGap{file, "Planned edits overlap; add the setup from onboard --instructions manually."}
			}
		}
		for _, change := range changes {
			lines = slices.Replace(lines, change.start, change.end, change.lines...)
		}
		edit.after = []byte(strings.ReplaceAll(strings.Join(lines, "\n"), "\n", newline))
		var document yaml.Node
		if err := yaml.Unmarshal(edit.after, &document); err != nil {
			return nil, nil, &SetupGap{file, "Planned CI edits are not valid YAML; source files were preserved: " + err.Error()}
		}
		edits = append(edits, edit)
	}
	return edits, impact, nil
}

// buildkiteInsert plans the snippet before one command statement.
func buildkiteInsert(lines []string, entry buildkiteEntry, request ciSetupRequest) (buildkiteEdit, error) {
	node := entry.node
	location := entry.file + ":" + strconv.Itoa(node.Line)
	if node.Line < 1 || node.Line > len(lines) {
		return buildkiteEdit{}, &SetupGap{location, "The command's position could not be located."}
	}
	line := lines[node.Line-1]
	column := min(max(node.Column-1, 0), len(line))
	prefix := line[:column]
	block := node.Style&yaml.LiteralStyle != 0
	switch {
	case entry.listed && entry.first:
		dash := strings.TrimRight(prefix, " ")
		if !strings.HasSuffix(dash, "-") || strings.TrimSpace(strings.TrimSuffix(dash, "-")) != "" {
			return buildkiteEdit{}, &EditThenApplyGap{SetupGap{location, "The command list is not a block list ddtest can extend; write it with one \"- command\" per line, then rerun onboard --apply."}}
		}
		return buildkiteEdit{start: node.Line - 1, end: node.Line - 1, lines: strings.Split(strings.TrimSuffix(buildkiteSetupText(request, dash[:len(dash)-1]), "\n"), "\n")}, nil
	case block:
		values := strings.Split(node.Value, "\n")
		index := node.Line - 1 + entry.line
		if entry.line < 1 || entry.line > len(values) || index >= len(lines) || strings.TrimSpace(lines[index]) != strings.TrimSpace(values[entry.line-1]) || strings.TrimSpace(values[entry.line-1]) == "" {
			return buildkiteEdit{}, &EditThenApplyGap{SetupGap{location, "The command block could not be mapped to its lines; write the test command as its own commands entry, then rerun onboard --apply."}}
		}
		indent := ""
		for _, content := range lines[node.Line:] {
			if strings.TrimSpace(content) != "" {
				indent = content[:len(content)-len(strings.TrimLeft(content, " "))]
				break
			}
		}
		var inserted []string
		for _, text := range strings.Split(buildkiteEscape(buildkiteRequestSnippet(request).shell()), "\n") {
			inserted = append(inserted, indent+text)
		}
		return buildkiteEdit{start: index, end: index, lines: inserted}, nil
	case !entry.listed && entry.first && !strings.Contains(node.Value, "\n") && node.Style&yaml.FoldedStyle == 0:
		// command: <command> becomes a list holding the setup and the command.
		key := strings.TrimRight(prefix, " ")
		if !strings.HasSuffix(key, ":") {
			break
		}
		pad := strings.Repeat(" ", len(line)-len(strings.TrimLeft(line, " "))+2)
		replacement := append([]string{key}, strings.Split(strings.TrimSuffix(buildkiteSetupText(request, pad), "\n"), "\n")...)
		return buildkiteEdit{start: node.Line - 1, end: node.Line, lines: append(replacement, pad+"- "+line[column:])}, nil
	}
	return buildkiteEdit{}, &EditThenApplyGap{SetupGap{location, "The test command is not the first command of its entry, and its entry is not a block (|) ddtest can extend; write the test command as its own commands entry, then rerun onboard --apply."}}
}

// buildkitePassKey adds DD_API_KEY to a docker or docker-compose plugin's
// environment list, or adds the list.
func buildkitePassKey(lines []string, file string, config *yaml.Node) (buildkiteEdit, error) {
	location := file + ":" + strconv.Itoa(config.Line)
	refuse := &EditThenApplyGap{SetupGap{location, "The step's container does not receive DD_API_KEY, and ddtest cannot extend its plugin's configuration safely; add DD_API_KEY to the plugin's environment list, then rerun onboard --apply."}}
	if config.Kind != yaml.MappingNode || config.Style&yaml.FlowStyle != 0 || len(config.Content) == 0 {
		return buildkiteEdit{}, refuse
	}
	for i := 0; i+1 < len(config.Content); i += 2 {
		if name := config.Content[i].Value; name != "environment" && name != "env" {
			continue
		}
		list := config.Content[i+1]
		if list.Kind != yaml.SequenceNode || list.Style&yaml.FlowStyle != 0 || len(list.Content) == 0 {
			return buildkiteEdit{}, refuse
		}
		last := list.Content[len(list.Content)-1]
		if last.Line < 1 || last.Line > len(lines) || strings.Contains(last.Value, "\n") {
			return buildkiteEdit{}, refuse
		}
		text := lines[last.Line-1]
		dash := strings.TrimRight(text[:min(max(last.Column-1, 0), len(text))], " ")
		if !strings.HasSuffix(dash, "-") || strings.TrimSpace(strings.TrimSuffix(dash, "-")) != "" {
			return buildkiteEdit{}, refuse
		}
		return buildkiteEdit{start: last.Line, end: last.Line, lines: []string{dash + " DD_API_KEY"}}, nil
	}
	first := config.Content[0]
	if first.Line < 1 || first.Line > len(lines) {
		return buildkiteEdit{}, refuse
	}
	indent := strings.Repeat(" ", max(first.Column-1, 0))
	if strings.TrimSpace(lines[first.Line-1][:min(len(indent), len(lines[first.Line-1]))]) != "" {
		return buildkiteEdit{}, refuse
	}
	return buildkiteEdit{start: first.Line - 1, end: first.Line - 1, lines: []string{indent + "environment:", indent + "  - DD_API_KEY"}}, nil
}
