// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

type nativeSetup struct {
	Framework string `json:"framework"`
	Version   string `json:"version"`
	Site      string `json:"site"`
}

func nativeInstrumentationPrefix(framework, version, site string) string {
	metadata, _ := json.Marshal(nativeSetup{framework, version, site})
	variable := scopedEnvironment(framework)
	options := ` -r \"${DD_TRACE_PACKAGE}\" --import \"${DD_TRACE_ESM_IMPORT}\"`
	return "# ddtest-ci " + string(metadata) + "\n(\n" +
		"_ddtest_tracer_dir=\n" +
		"trap 'if [ -n \"$_ddtest_tracer_dir\" ]; then rm -rf -- \"$_ddtest_tracer_dir\"; fi' EXIT\n" +
		"if [ -n \"${DD_API_KEY:-}\" ] && _ddtest_tracer_dir=$(mktemp -d \"${TMPDIR:-/tmp}/ddtest-ci.XXXXXXXX\") && npm install --prefix \"$_ddtest_tracer_dir\" --no-save --package-lock=false --no-audit --no-fund dd-trace@" + version + "; then\n" +
		"  readonly _ddtest_tracer_dir\n" +
		"  export DD_TRACE_PACKAGE=\"$_ddtest_tracer_dir/node_modules/dd-trace/ci/init\"\n" +
		"  export DD_TRACE_ESM_IMPORT=\"$_ddtest_tracer_dir/node_modules/dd-trace/register.js\"\n" +
		"  export " + variable + "=\"${" + variable + ":-}" + options + "\"\n" +
		"  export DD_CIVISIBILITY_ENABLED=true\n" +
		"  export DD_SITE=" + site + "\nfi\n"
}

func unwrapNativeInstrumentation(command string) (framework, version, site, original string, ok bool) {
	header, _, found := strings.Cut(command, "\n")
	if !found || !strings.HasPrefix(header, "# ddtest-ci ") {
		return
	}
	var setup nativeSetup
	if json.Unmarshal([]byte(strings.TrimPrefix(header, "# ddtest-ci ")), &setup) != nil || !SupportsAutomaticSetup(setup.Framework) || !exactRelease(setup.Version) {
		return
	}
	prefix := nativeInstrumentationPrefix(setup.Framework, setup.Version, setup.Site)
	if !strings.HasPrefix(command, prefix) || !strings.HasSuffix(command, "\n)") {
		return
	}
	return setup.Framework, setup.Version, setup.Site, strings.TrimSuffix(strings.TrimPrefix(command, prefix), "\n)"), true
}

type nativeReplacement struct {
	start, end int
	text       string
}

func planNativeSetup(root, framework, version, site, requirement string, workflows []ciWorkflow) (*SetupPlan, error) {
	edits, _, err := scopedSetupEdits(root, framework)
	if err != nil {
		return nil, err
	}
	impact, coverageAdded := scopedSetupImpact(edits, framework)
	files := map[string]setupEdit{}
	replacements := map[string][]nativeReplacement{}
	targets := 0
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if job.If == "false" {
				continue
			}
			for _, step := range job.Steps {
				r := resolveTestStep(root, workflow, job, step, "javascript", framework)
				if !r.Matched {
					continue
				}
				location := workflow.Path + " / " + name
				node := job.Node
				if workflow.Provider == "jenkins" && step.Native != nil {
					node = step.Native.NodeVersion
				}
				if UnsupportedNode(node) {
					continue
				}
				if len(job.Gaps) > 0 {
					return nil, &SetupGap{location, strings.Join(job.Gaps, "; ")}
				}
				if r.Reason != "" || r.OtherFramework {
					return nil, &SetupGap{location, "The test command is unresolved or also runs another framework; preserve it and supply scoped initialization"}
				}
				if requirement != "" {
					node := job.Node
					if workflow.Provider == "jenkins" && step.Native != nil {
						node = step.Native.NodeVersion
					}
					status, reason := CompareNodeRequirement(node, requirement)
					if status != StatusCompatible {
						return nil, &SetupGap{location, "CI Node runtime: " + reason + "; identify its runtime or select a compatible tracer"}
					}
				}
				native := step.Native
				if native != nil && sharedNativeSource(root, workflows, framework, native) {
					return nil, &SetupGap{location, "This source command is shared with an unresolved or unselected framework consumer; configure that consumer explicitly"}
				}
				if native == nil || native.Blocked != "" {
					return nil, &SetupGap{location, "The test command is generated, inherited or computed; edit its owning source explicitly"}
				}
				if native.Tracer != "" {
					if native.Tracer != version || (native.Framework != "" && native.Framework != framework) {
						return nil, &SetupGap{location, "Existing CI tracer/framework selection differs; preserve it and resolve the conflict"}
					}
					targets++
					continue
				}
				edit, found := files[native.Path]
				if !found {
					edit, err = readSetupEdit(root, native.Path)
					if err != nil {
						return nil, err
					}
					files[native.Path] = edit
				}
				if strings.Contains(mergeEnvironment(job.Env, step.Env)["NODE_OPTIONS"], "dd-trace") {
					return nil, &SetupGap{location, "Existing custom tracer initialization is preserved; resolve its exact installation/version instead of adding a second tracer"}
				}
				value := wrapNativeCommand(framework, version, site, native.Original)
				var replacement nativeReplacement
				if native.Node != nil {
					replacement, err = yamlCommandReplacement(edit.before, native.Node, value)
				} else {
					if native.Start < 0 || native.End > len(edit.before) || native.Start >= native.End {
						return nil, &SetupGap{location, "Invalid source span"}
					}
					// A single-quoted Groovy literal never interpolates shell variables.
					encoded := strings.NewReplacer("\\", "\\\\", "'", "\\'", "\r", "\\r", "\n", "\\n").Replace(value)
					replacement = nativeReplacement{native.Start, native.End, "'" + encoded + "'"}
				}
				if err != nil {
					return nil, &SetupGap{location, err.Error()}
				}
				replacements[native.Path] = append(replacements[native.Path], replacement)
				targets++
			}
		}
	}
	if targets == 0 {
		return nil, &SetupGap{"CI setup", "No editable literal " + framework + " test command found in the selected provider"}
	}
	for _, path := range slices.Sorted(maps.Keys(replacements)) {
		edit := files[path]
		items := replacements[path]
		slices.SortFunc(items, func(a, b nativeReplacement) int { return b.start - a.start })
		previous := len(edit.before) + 1
		edit.after = slices.Clone(edit.before)
		for _, item := range items {
			if item.start == previous {
				continue
			} // Multiple consumers of the same literal share one edit.
			if item.end > previous {
				return nil, &SetupGap{path, "Overlapping inherited command edits require explicit setup"}
			}
			edit.after = append(append(append([]byte{}, edit.after[:item.start]...), []byte(item.text)...), edit.after[item.end:]...)
			previous = item.start
		}
		if strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".yaml") {
			var doc yaml.Node
			if err := yaml.Unmarshal(edit.after, &doc); err != nil {
				return nil, &SetupGap{path, "Planned CI edit is invalid YAML: " + err.Error()}
			}
		} else {
			if _, err := groovyTokens(string(edit.after)); err != nil {
				return nil, err
			}
		}
		if !bytes.Equal(edit.before, edit.after) {
			edits = append(edits, edit)
		}
	}
	ignore, err := setupIgnoreEdits(root, coverageAdded)
	if err != nil {
		return nil, err
	}
	edits = append(edits, ignore...)
	return &SetupPlan{root: root, edits: edits, impact: append(impact, "Only the selected CI commands are instrumented. The exact tracer is installed in a private temporary directory inside the test command's container/shell and removed on exit; package manifests and lockfiles are preserved. Missing DD_API_KEY or a failed tracer installation leaves the original test command uninstrumented. Expose DD_API_KEY in the selected provider's test-step environment through its secret binding; never paste it into chat.")}, nil
}

func yamlCommandReplacement(data []byte, node *yaml.Node, value string) (nativeReplacement, error) {
	if node.Kind != yaml.ScalarNode || node.Anchor != "" {
		return nativeReplacement{}, fmt.Errorf("aliased/non-scalar script needs explicit edits")
	}
	lines := strings.SplitAfter(string(data), "\n")
	line := node.Line - 1
	if line < 0 || line >= len(lines) {
		return nativeReplacement{}, fmt.Errorf("script source line is invalid")
	}
	offset := 0
	for _, text := range lines[:line] {
		offset += len(text)
	}
	column := node.Column - 1
	if column < 0 || column > len([]rune(lines[line])) {
		return nativeReplacement{}, fmt.Errorf("invalid script column")
	}
	start := offset + len(string([]rune(lines[line])[:column]))
	if start < offset || start >= offset+len(lines[line]) {
		return nativeReplacement{}, fmt.Errorf("script source column is invalid")
	}
	end := offset + len(strings.TrimRight(lines[line], "\r\n"))
	newline := ""
	if node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		indent := -1
		nextOffset := offset + len(lines[line])
		end = nextOffset
		for _, text := range lines[line+1:] {
			trimmed := strings.TrimSpace(text)
			spaces := len(text) - len(strings.TrimLeft(text, " "))
			if trimmed != "" {
				if indent < 0 {
					indent = spaces
				}
				if spaces < indent {
					break
				}
			}
			end = nextOffset + len(text)
			nextOffset = end
		}
		if end > start && data[end-1] == '\n' {
			newline = "\n"
			if end > 1 && data[end-2] == '\r' {
				newline = "\r\n"
			}
		}
	} else if node.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle) != 0 {
		quote := data[start]
		end = start + 1
		closed := false
		for end < len(data) {
			if quote == '"' && data[end] == '\\' {
				end += 2
				continue
			}
			if data[end] == quote {
				if quote == '\'' && end+1 < len(data) && data[end+1] == '\'' {
					end += 2
					continue
				}
				end++
				closed = true
				break
			}
			end++
		}
		if !closed {
			return nativeReplacement{}, fmt.Errorf("unterminated script scalar")
		}
	} else {
		text := string(data[start:end])
		if comment := strings.Index(text, " #"); comment >= 0 {
			end = start + comment
		}
		for end > start && (data[end-1] == ' ' || data[end-1] == '\t') {
			end--
		}
	}
	if node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) == 0 {
		var original string
		if err := yaml.Unmarshal(data[start:end], &original); err != nil || original != node.Value {
			return nativeReplacement{}, fmt.Errorf("script scalar spans an ambiguous flow/multiline layout; preserve it and edit explicitly")
		}
	}
	encoded := strconv.Quote(value)
	if node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 && node.LineComment != "" {
		encoded += " " + node.LineComment
	}
	return nativeReplacement{start, end, encoded + newline}, nil
}

func wrapNativeCommand(framework, version, site, original string) string {
	return nativeInstrumentationPrefix(framework, version, site) + original + "\n)"
}

func sharedNativeSource(root string, workflows []ciWorkflow, framework string, source *nativeCommand) bool {
	for _, workflow := range workflows {
		for _, job := range workflow.Jobs {
			for _, step := range job.Steps {
				other := step.Native
				if other == nil || other.Path != source.Path {
					continue
				}
				same := other.Start == source.Start && other.End == source.End
				if other.Node != nil && source.Node != nil {
					same = other.Node.Line == source.Node.Line && other.Node.Column == source.Node.Column
				}
				if !same {
					continue
				}
				resolution := resolveTestStep(root, workflow, job, step, "javascript", framework)
				if !resolution.Matched || resolution.OtherFramework || resolution.Reason != "" {
					return true
				}
			}
		}
	}
	return false
}
