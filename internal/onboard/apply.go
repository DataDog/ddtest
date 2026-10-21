// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// SetupChange is a receipt for a source edit, not a claim that CI executed.
type SetupChange struct {
	Path   string `json:"path"`
	Before string `json:"before_sha256"`
	After  string `json:"after_sha256"`
}

// SetupGap identifies an edit the bounded applier cannot safely make.
type SetupGap struct{ Location, Reason string }

func (g *SetupGap) Error() string { return g.Location + ": " + g.Reason }

type setupEdit struct {
	path          string
	before, after []byte
	mode          os.FileMode
}
type lineInsertion struct {
	line   int
	text   string
	remove int
}

const vitestPreload = "${{ env.NODE_OPTIONS }} -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"

// ApplyVitestSetup recognizes ordinary root-level GitHub Actions steps. It plans
// every edit before writing; unknown layouts require help rather than a rewrite.
// Commands, conditions, build steps, matrices and unrelated YAML stay byte-exact.
func ApplyVitestSetup(root, version string) ([]SetupChange, error) {
	return ApplyJavaScriptSetup(root, "vitest", version, "datadoghq.com")
}

// ApplyJavaScriptSetup shares the source editor across ordinary Node CLI jobs.
// Browser/worker-specific wiring requires an adapter, never a guessed preload.
func ApplyJavaScriptSetup(root, framework, version, site string, nodeRequirement ...string) ([]SetupChange, error) {
	if !SupportsAutomaticSetup(framework) {
		return nil, &SetupGap{"CI initialization", "Automatic source edits are not supported for this framework's custom hooks; configure the existing CI entry point and use testdrive --agent --all."}
	}
	if site == "" {
		site = "datadoghq.com"
	}
	for _, c := range site {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '-' {
			return nil, fmt.Errorf("site must be a literal Datadog hostname")
		}
	}
	if !exactRelease(version) {
		return nil, fmt.Errorf("CI requires an exact numeric tracer release, got %q", version)
	}
	workflows, err := readWorkflows(root)
	if err != nil {
		return nil, err
	}
	edits, scopedOptions, err := scopedSetupEdits(root, framework)
	if err != nil {
		return nil, err
	}
	targets := 0
	for _, workflow := range workflows {
		edit, err := readSetupEdit(root, workflow.Path)
		if err != nil {
			return nil, err
		}
		var document yaml.Node
		if err := yaml.Unmarshal(edit.before, &document); err != nil {
			return nil, err
		}
		if len(document.Content) == 0 {
			continue
		}
		lines := strings.Split(string(edit.before), "\n")
		var insertions []lineInsertion
		jobs := setupMember(document.Content[0], "jobs")
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			var selected []int
			for i, step := range job.Steps {
				resolution := resolveTestStep(root, workflow, job, step, "javascript", framework)
				if framework == "webdriverio" && resolution.Reason != "" {
					rows, e := runtimeMatrix(job.Strategy.Matrix)
					if e != nil {
						return nil, e
					}
					for _, row := range rows {
						resolved, _, e := resolveVariantStep(workflow, job, step, row, framework)
						if e != nil {
							continue
						}
						r := resolveTestStep(root, workflow, job, resolved, "javascript", framework)
						if r.Matched {
							resolution.Matched = true
							resolution.OtherFramework = resolution.OtherFramework || r.OtherFramework
						}
					}
				}
				if resolution.Matched && resolution.OtherFramework && scopedEnvironment(framework) == "NODE_OPTIONS" {
					return nil, &SetupGap{workflow.Path + " / " + name, "The step also runs another framework; initialize only the selected framework leaf explicitly."}
				}
				if resolution.Matched {
					selected = append(selected, i)
				}
			}
			if len(selected) == 0 {
				continue
			}
			location := workflow.Path + " / " + name
			gap := func(reason string) error { return &SetupGap{location, reason} }
			if setupYAMLReferences(&document) {
				return nil, gap("YAML anchors or aliases require explicit CI edits; automatic edits could affect another consumer.")
			}
			node := setupMember(jobs, name)
			steps := setupMember(node, "steps")
			if job.Container != nil ||
				steps == nil || steps.Kind != yaml.SequenceNode || steps.Style != 0 || len(steps.Content) != len(job.Steps) {
				return nil, gap("Automatic edits require literal steps without a container or expanded composite; preserve this setup and configure its tracer/loader explicitly.")
			}
			requirement := ""
			if len(nodeRequirement) > 0 {
				requirement = nodeRequirement[0]
			}
			condition, err := setupRuntimeCondition(root, job, requirement)
			if err != nil {
				return nil, gap(err.Error())
			}
			for _, layer := range []map[string]string{workflow.Env, job.Env} {
				for _, key := range []string{"DD_TRACE_PACKAGE", "DD_TRACE_ESM_IMPORT", "DD_CIVISIBILITY_ENABLED", "DD_TRACE_ENABLED"} {
					if _, ok := layer[key]; ok {
						return nil, gap("Custom inherited instrumentation requires review: " + key)
					}
				}
			}
			if condition != "" {
				selected = slices.DeleteFunc(selected, func(i int) bool { return job.Steps[i].If == "${{ !("+condition+") }}" })
			}
			actionIndex := -1
			for i, step := range job.Steps {
				if strings.HasPrefix(strings.ToLower(step.Uses), "datadog/test-visibility-github-action@") {
					if actionIndex >= 0 || i >= selected[0] || step.If != condition || step.Uses != "datadog/test-visibility-github-action@v3" ||
						step.With["js-tracer-version"] != version || step.With["languages"] != "js" || step.With["api_key"] == "" || step.With["site"] == "" {
						return nil, gap("Existing Datadog setup differs from the selected release or recognized v3 action/runtime guard; align its version and initialization without adding a second action.")
					}
					actionIndex = i
				}
			}
			for _, i := range selected {
				step, node := job.Steps[i], steps.Content[i]
				run := setupMember(node, "run")
				if (step.If != "" && (actionIndex < 0 || step.If != condition)) || len(step.Parents) != 0 || (step.WorkingDirectory != "" && framework != "webdriverio") || node.Kind != yaml.MappingNode || node.Style != 0 || node.Anchor != "" ||
					run == nil || run.Kind != yaml.ScalarNode || (framework != "mocha" && (run.Style == yaml.LiteralStyle || run.Style == yaml.FoldedStyle || strings.Contains(run.Value, "\n"))) {
					return nil, gap(fmt.Sprintf("Step %d needs explicit loader placement: conditional, composite, alternate directory or multiline commands are outside this applier.", i+1))
				}
				line := lines[node.Line-1]
				indent := len(line) - len(strings.TrimLeft(line, " "))
				if !strings.HasPrefix(line[indent:], "- ") {
					return nil, gap("Unrecognized YAML sequence formatting; configure the test step explicitly.")
				}
				preload := setupPreload(framework)
				if scopedOptions != "" {
					preload = scopedOptions
				}
				covered, err := setupMochaCoverage(root, framework, step.Run)
				if err != nil {
					return nil, gap(err.Error())
				}
				if condition != "" && actionIndex < 0 {
					if step.ID != "" {
						return nil, gap("The test step has an id referenced by other steps; configure the runtime partition explicitly without duplicating or changing that id.")
					}
					// Preserve the original command for legacy rows; the supported copy
					// owns instrumentation and any coverage wrapper.
					copy, err := setupInstrumentedStep(node, preload, covered, condition)
					if err != nil {
						return nil, gap(err.Error())
					}
					insertions = append(insertions, lineInsertion{line: node.Line - 1, text: indentSetup(copy, indent)})
					insertions = append(insertions, lineInsertion{line: node.Line, text: strings.Repeat(" ", indent+2) + "if: ${{ !(" + condition + ") }}\n"})
				} else {
					envEdit, err := setupStepEnvironment(lines, node, scopedEnvironment(framework), preload, actionIndex >= 0)
					if err != nil {
						return nil, gap(err.Error())
					}
					if envEdit != nil {
						insertions = append(insertions, *envEdit)
					}
					if covered != step.Run {
						value := *run
						value.Value = covered
						text, err := yaml.Marshal(&value)
						if err != nil {
							return nil, err
						}
						padding := strings.Repeat(" ", run.Column-6)
						insertions = append(insertions, lineInsertion{line: run.Line - 1, remove: setupNodeEnd(run) - run.Line + 1, text: padding + "run: " + strings.ReplaceAll(strings.TrimSuffix(string(text), "\n"), "\n", "\n"+padding) + "\n"})
					}
				}

				if i == selected[0] && actionIndex < 0 {
					padding := strings.Repeat(" ", indent)
					guard := ""
					if condition != "" {
						guard = padding + "  if: " + condition + "\n"
					}
					action := padding + "- name: Configure Datadog Test Optimization\n" + guard + padding + "  uses: datadog/test-visibility-github-action@v3\n" + padding + "  with:\n" + padding + "    languages: js\n" + padding + "    api_key: ${{ secrets.DD_API_KEY }}\n" + padding + "    site: " + site + "\n" + padding + "    js-tracer-version: " + version + "\n"
					insertions = append(insertions, lineInsertion{line: node.Line - 1, text: action})
				}
				targets++
			}
		}
		slices.SortStableFunc(insertions, func(a, b lineInsertion) int { return b.line - a.line })
		for _, insertion := range insertions {
			lines = slices.Replace(lines, insertion.line, insertion.line+insertion.remove, strings.TrimSuffix(insertion.text, "\n"))
		}
		edit.after = []byte(strings.Join(lines, "\n"))
		if len(insertions) > 0 {
			edits = append(edits, edit)
		}
	}
	if targets == 0 {
		return nil, &SetupGap{".github/workflows", "No literal " + framework + " CI step found. Identify its real CI entry point before applying onboarding."}
	}
	for _, path := range []string{".gitignore", ".npmignore"} {
		edit, err := readSetupEdit(root, path)
		if err != nil {
			return nil, err
		}
		if path == ".npmignore" && edit.before == nil {
			continue
		} // Creating it would disable npm's .gitignore fallback.
		if slices.Contains(strings.Split(string(edit.before), "\n"), "/.testoptimization/") || slices.Contains(strings.Split(string(edit.before), "\n"), ".testoptimization/") {
			continue
		}
		edit.after = append(slices.Clone(edit.before), []byte("\n/.testoptimization/\n")...)
		edits = append(edits, edit)
	}
	// Recheck all inputs before the first write. Never overwrite intervening edits.
	for _, edit := range edits {
		current, err := readSetupEdit(root, edit.path)
		if err != nil {
			return nil, err
		}
		if string(current.before) != string(edit.before) {
			return nil, fmt.Errorf("%s changed during planning; rerun onboarding", edit.path)
		}
	}
	var changes []SetupChange
	for _, edit := range edits {
		path := filepath.Join(root, edit.path)
		file, err := os.CreateTemp(filepath.Dir(path), ".ddtest-setup-*")
		if err != nil {
			return changes, err
		}
		name := file.Name()
		err = file.Chmod(edit.mode)
		if err == nil {
			_, err = file.Write(edit.after)
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(name, path)
		}
		_ = os.Remove(name)
		if err != nil {
			return changes, fmt.Errorf("write %s: %w", edit.path, err)
		}
		changes = append(changes, SetupChange{Path: edit.path, Before: fmt.Sprintf("%x", sha256.Sum256(edit.before)), After: fmt.Sprintf("%x", sha256.Sum256(edit.after))})
	}
	return changes, nil
}

func setupMember(node *yaml.Node, key string) *yaml.Node {
	if node != nil && node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Value == key {
				return node.Content[i+1]
			}
		}
	}
	return nil
}

func setupYAMLReferences(node *yaml.Node) bool {
	if node.Anchor != "" || node.Kind == yaml.AliasNode {
		return true
	}
	for _, child := range node.Content {
		if setupYAMLReferences(child) {
			return true
		}
	}
	return false
}

func exactRelease(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

func readSetupEdit(root, path string) (setupEdit, error) {
	edit := setupEdit{path: path, mode: 0644}
	for _, parent := range strings.Split(filepath.ToSlash(path), "/") {
		root = filepath.Join(root, parent)
		info, err := os.Lstat(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return edit, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return edit, &SetupGap{path, "Refusing to edit through a symlink; configure this file explicitly."}
		}
		edit.mode = info.Mode().Perm()
	}
	data, err := os.ReadFile(root)
	if os.IsNotExist(err) {
		return edit, nil
	}
	edit.before = data
	return edit, err
}

func SupportsAutomaticSetup(framework string) bool {
	return slices.Contains([]string{"jest", "vitest", "mocha", "playwright", "cucumber", "webdriverio"}, framework)
}
