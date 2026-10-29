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

// planSetupEdits declares the Datadog orb when the configuration does not,
// and inserts its step before each target's step. A step several jobs share
// (a reusable command, or a job a matrix or several workflows run) is
// edited only when every job running it is a target that needs the same
// insertion; otherwise the edit could change another job.
func (circleCI) planSetupEdits(root string, workflows []ciWorkflow, targets []ciSetupTarget, request ciSetupRequest) ([]setupEdit, []string, error) {
	config, err := readCircleConfig(root)
	if err != nil {
		return nil, nil, err
	}
	alias, declared, supported := circleOrbAlias(config)
	if declared && !supported {
		return nil, nil, &EditThenApplyGap{SetupGap{circleCIFile, "The configuration declares " + config.orbs[alias].reference + ", which is not a supported release of the Datadog orb. Change it to " + circleSetupReference + " (or an exact 2.x.y release), then rerun onboard --apply."}}
	}
	type insertion struct {
		origin circleStep
		after  []string
		jobs   []string
	}
	planned := map[*yaml.Node]*insertion{}
	for _, target := range targets {
		job, step, ok := gitLabTarget(workflows, target)
		location := target.Workflow + " / " + target.Job
		origin, isStep := step.origin.(circleStep)
		if ok && isStep && origin.owner.kind == "orb job" {
			return nil, nil, &EditThenApplyGap{SetupGap{location, "The test runs in " + circleOwnerDescription(origin.owner) + ", which ddtest does not edit. Add the Datadog orb step before its tests through a steps parameter the orb job offers (such as post_install_steps) in the workflow's entry, or run the tests in a job of the configuration, then rerun ddtest."}}
		}
		if !ok || !isStep || origin.item == nil {
			return nil, nil, &SetupGap{location, "The test step is not a CircleCI step ddtest can edit."}
		}
		at := circleCIFile + ":" + strconv.Itoa(origin.item.Line)
		if !origin.owner.editable || origin.owner.kind != "job" && origin.owner.kind != "command" {
			return nil, nil, &EditThenApplyGap{SetupGap{at, "The test step comes from " + circleOwnerDescription(origin.owner) + ", which ddtest does not edit. Move the test step into the job's steps or a reusable command, then rerun onboard --apply."}}
		}
		if origin.aliased {
			return nil, nil, &EditThenApplyGap{SetupGap{at, "This step is shared through a YAML anchor and alias (&name/*name), so one insertion cannot reach every job that runs it. Write the test step in each job's steps, then rerun onboard --apply."}}
		}
		if origin.setupProblem != "" {
			return nil, nil, &EditThenApplyGap{SetupGap{location, origin.setupProblem}}
		}
		occurrences := 0
		for _, other := range job.Steps {
			if current, ok := other.origin.(circleStep); ok && current.item == origin.item {
				occurrences++
			}
		}
		if occurrences > 1 {
			return nil, nil, &EditThenApplyGap{SetupGap{at, "Job " + target.Job + " runs this step more than once (through " + circleOwnerDescription(origin.owner) + "), so the setup would run more than once. Give the first test its own step, then rerun onboard --apply."}}
		}
		after := request.After
		if target.After != nil {
			after = target.After
		}
		if existing := planned[origin.item]; existing != nil {
			if !slices.Equal(existing.after, after) {
				return nil, nil, &SetupGap{at, "Jobs " + strings.Join(append(existing.jobs, target.Job), ", ") + " share this step but need different setup; give each its own step before applying."}
			}
			existing.jobs = append(existing.jobs, target.Job)
			continue
		}
		planned[origin.item] = &insertion{origin: origin, after: after, jobs: []string{target.Job}}
	}
	// Every job that can run and runs an edited step must be a target.
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			if disabledCondition(workflow.Jobs[name].If) {
				continue
			}
			for _, step := range workflow.Jobs[name].Steps {
				origin, ok := step.origin.(circleStep)
				if !ok || planned[origin.item] == nil || slices.Contains(planned[origin.item].jobs, name) {
					continue
				}
				return nil, nil, &EditThenApplyGap{SetupGap{circleCIFile + ":" + strconv.Itoa(origin.item.Line), "Job " + name + " also runs this step (through " + circleOwnerDescription(origin.owner) + ") but is not a job ddtest sets up for the selected tests; adding the setup there would change that job. Give the test jobs their own step, then rerun onboard --apply."}}
			}
		}
	}
	edit, err := readSetupEdit(root, circleCIFile)
	if err != nil {
		return nil, nil, err
	}
	text := strings.ReplaceAll(string(edit.before), "\r\n", "\n")
	newline := "\n"
	if strings.Contains(string(edit.before), "\r\n") {
		newline = "\r\n"
	}
	lines := strings.Split(text, "\n")
	type change struct {
		line int // insert before this zero-based line
		text []string
	}
	var changes []change
	for _, item := range planned {
		node := item.origin.item
		location := circleCIFile + ":" + strconv.Itoa(node.Line)
		if node.Line < 1 || node.Line > len(lines) {
			return nil, nil, &SetupGap{location, "The step's position could not be located."}
		}
		line := lines[node.Line-1]
		column := min(max(node.Column-1, 0), len(line))
		dash := strings.TrimRight(line[:column], " ")
		if !strings.HasSuffix(dash, "-") || strings.TrimSpace(strings.TrimSuffix(dash, "-")) != "" {
			return nil, nil, &EditThenApplyGap{SetupGap{location, "The step list is not a block list ddtest can extend; write it with one \"- step\" per line, then rerun onboard --apply."}}
		}
		request := request
		request.After = item.after
		pad := dash[:len(dash)-1]
		changes = append(changes, change{node.Line - 1, strings.Split(strings.TrimSuffix(circleSetupStep(request, alias, pad, circleNesting(node)), "\n"), "\n")})
	}
	if bump, err := circleVersionBump(edit.before); err != nil {
		return nil, nil, err
	} else if bump != nil {
		lines[bump.line] = bump.text
	}
	if !declared {
		declaration, err := circleOrbDeclaration(edit.before, alias)
		if err != nil {
			return nil, nil, err
		}
		changes = append(changes, change{declaration.line, declaration.text})
	}
	slices.SortStableFunc(changes, func(a, b change) int { return b.line - a.line })
	for _, current := range changes {
		lines = slices.Insert(lines, current.line, current.text...)
	}
	edit.after = []byte(strings.ReplaceAll(strings.Join(lines, "\n"), "\n", newline))
	var document yaml.Node
	if err := yaml.Unmarshal(edit.after, &document); err != nil {
		return nil, nil, &SetupGap{circleCIFile, "Planned CI edits are not valid YAML; source files were preserved: " + err.Error()}
	}
	var impact []string
	for _, item := range planned {
		if item.origin.owner.kind == "command" {
			impact = append(impact, "The Datadog orb step is added to the reusable command "+item.origin.owner.name+", which jobs "+strings.Join(item.jobs, ", ")+" run.")
		}
	}
	slices.Sort(impact)
	return []setupEdit{edit}, impact, nil
}

// circleOwnerDescription names where a step is written.
func circleOwnerDescription(owner circleOwner) string {
	switch owner.kind {
	case "command":
		if !owner.editable {
			return "the inline orb command " + owner.name
		}
		return "the reusable command " + owner.name
	case "job":
		if !owner.editable {
			return "the inline orb job " + owner.name
		}
		return "the job " + owner.name
	case "parameter":
		return "the steps parameter " + owner.name
	case "orb job":
		return "the orb job " + owner.name
	}
	return "a workflow's " + owner.kind
}

// circleOrbDeclaration is the insertion declaring the Datadog orb under
// alias: a new entry of the top-level orbs mapping, or a new orbs mapping
// after version.
func circleOrbDeclaration(data []byte, alias string) (struct {
	line int
	text []string
}, error) {
	var result struct {
		line int
		text []string
	}
	entry := alias + ": " + circleSetupReference
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil || len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return result, &SetupGap{circleCIFile, "The configuration is not a YAML mapping ddtest can edit."}
	}
	top := document.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		key, value := top.Content[i], top.Content[i+1]
		switch key.Value {
		case "orbs":
			if value.Kind == yaml.MappingNode && len(value.Content) > 0 && value.Style&yaml.FlowStyle == 0 {
				first := value.Content[0]
				result.line, result.text = first.Line-1, []string{strings.Repeat(" ", max(first.Column-1, 0)) + entry}
				return result, nil
			}
			if value.Tag == "!!null" && value.Value == "" {
				result.line, result.text = key.Line, []string{strings.Repeat(" ", key.Column+1) + entry}
				return result, nil
			}
			return result, &EditThenApplyGap{SetupGap{circleCIFile + ":" + strconv.Itoa(key.Line), "The orbs mapping is not a block mapping ddtest can extend; write one \"alias: namespace/orb@version\" per line, then rerun onboard --apply."}}
		}
	}
	for i := 0; i+1 < len(top.Content); i += 2 {
		if key, value := top.Content[i], top.Content[i+1]; key.Value == "version" && value.Kind == yaml.ScalarNode && value.Line == key.Line {
			result.line, result.text = key.Line, []string{"", "orbs:", "  " + entry}
			return result, nil
		}
	}
	result.line, result.text = 0, []string{"orbs:", "  " + entry, ""}
	return result, nil
}

// circleVersionBump moves a version 2 configuration to 2.1, the only
// version that accepts orbs. Version 2.1 reads a version 2 configuration
// the same way (workflows' version key included), except that it
// substitutes << parameters.x >>-style text, so a configuration holding such
// text is left for a person. nil when the configuration already is 2.1.
func circleVersionBump(data []byte) (*struct {
	line int
	text string
}, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil || len(document.Content) == 0 {
		return nil, nil
	}
	top := document.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		key, value := top.Content[i], top.Content[i+1]
		if key.Value != "version" || value.Kind != yaml.ScalarNode || !circleLegacyVersion(value.Value) {
			continue
		}
		location := circleCIFile + ":" + strconv.Itoa(key.Line)
		if circleReads(string(data)) {
			return nil, &EditThenApplyGap{SetupGap{location, "The configuration declares version 2, where CircleCI rejects orbs, and moving it to version 2.1 would substitute the << >> text it holds as parameters. Move it to version 2.1 yourself (escape that text as \\<< where it is literal), then rerun onboard --apply."}}
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		if value.Line != key.Line || value.Line > len(lines) {
			return nil, &EditThenApplyGap{SetupGap{location, "The configuration declares version 2, where CircleCI rejects orbs; set version: 2.1, then rerun onboard --apply."}}
		}
		line := lines[value.Line-1]
		start := value.Column - 1
		width := len(value.Value)
		if value.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
			width += 2
		}
		if start < 0 || start+width > len(line) {
			return nil, &EditThenApplyGap{SetupGap{location, "The configuration declares version 2, where CircleCI rejects orbs; set version: 2.1, then rerun onboard --apply."}}
		}
		return &struct {
			line int
			text string
		}{value.Line - 1, line[:start] + "2.1" + line[start+width:]}, nil
	}
	return nil, nil
}

// circleNesting is how many columns deeper than a step's name its
// parameters are written, as in the step itself (- run: then its command);
// 2 when the step shows none.
func circleNesting(item *yaml.Node) int {
	if item.Kind == yaml.MappingNode && len(item.Content) == 2 {
		if value := item.Content[1]; value.Kind == yaml.MappingNode && len(value.Content) > 0 && value.Style&yaml.FlowStyle == 0 && value.Content[0].Line > item.Content[0].Line {
			if nested := value.Content[0].Column - item.Content[0].Column; nested > 0 {
				return nested
			}
		}
	}
	return 2
}
