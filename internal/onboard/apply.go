// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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

// EditThenApplyGap is a SetupGap one source edit fixes, such as wrapping a
// Mocha command with NYC; onboard --apply then writes the setup itself.
type EditThenApplyGap struct{ SetupGap }

func (g *EditThenApplyGap) Unwrap() error { return &g.SetupGap }

func (g *SetupGap) Error() string { return g.Location + ": " + g.Reason }

// DirtyWorkflowGap names target files with uncommitted changes; committing or
// stashing them, or an explicit --allow-dirty, lets setup proceed.
type DirtyWorkflowGap struct{ Files []string }

func (g *DirtyWorkflowGap) Error() string {
	return "source edits: Target files have uncommitted changes (" + strings.Join(g.Files, ", ") + "). Review the preview, then commit/stash them or explicitly pass --allow-dirty with --apply --yes to preserve and extend those edits."
}

// NodeRuntimeGap means no CI Node runtime satisfies the selected tracer. The fix
// is a CI Node upgrade or an older tracer, not a hand-written CI hook.
type NodeRuntimeGap struct {
	Location, Requirement string
	ESM                   bool
	// Unsupported marks a job whose every row runs a Node no dd-trace release
	// supports; setup leaves it unedited instead of stopping.
	Unsupported bool
}

func (g *NodeRuntimeGap) Error() string {
	requirement := g.Requirement
	if g.ESM {
		requirement += " plus ESM registration (Node >=18.19 <19 or >=20.6)"
	}
	return g.Location + ": no CI runtime supports tracer Node requirement " + requirement + ". Upgrade the CI Node version (actions/setup-node) to one the tracer supports, or select an older tracer with --tracer-version."
}

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

const vitestPreload = "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"

// applyVitestSetup recognizes ordinary root-level GitHub Actions steps. It plans
// every edit before writing; unknown layouts require help rather than a rewrite.
// Commands, conditions, build steps, matrices and unrelated YAML stay byte-exact.
func applyVitestSetup(root, version string) ([]SetupChange, error) {
	return ApplyJavaScriptSetup(root, "vitest", version, "datadoghq.com", "")
}

// ApplyJavaScriptSetup shares the source editor across ordinary Node CLI jobs.
// Browser/worker-specific wiring requires an adapter, never a guessed preload.
func ApplyJavaScriptSetup(root, framework, version, site, nodeRequirement string) ([]SetupChange, error) {
	plan, err := PlanJavaScriptSetup(root, framework, version, site, nodeRequirement)
	if err != nil {
		return nil, err
	}
	return plan.Apply(false)
}

// PlanJavaScriptSetup resolves all source edits without changing the repository.
// An empty nodeRequirement means the tracer declares no Node range.
func PlanJavaScriptSetup(root, framework, version, site, nodeRequirement string) (*SetupPlan, error) {
	if !SupportsAutomaticSetup(framework) {
		return nil, &SetupGap{"CI initialization", "Automatic source edits are not supported for this framework's custom hooks; configure the existing CI entry point and use testdrive --all."}
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
	provider, err := currentCIProvider(root)
	if err != nil {
		return nil, err
	}
	workflows, err := provider.readWorkflows(root)
	if err != nil {
		return nil, err
	}
	if !provider.sharedSyntax() {
		return planProviderJavaScriptSetup(root, provider, workflows, framework, version, site, nodeRequirement)
	}
	edits, scopedOptions, err := scopedSetupEdits(root, framework)
	if err != nil {
		return nil, err
	}
	var impact []string
	coverageAdded := false
	for _, edit := range edits {
		coverageAdded = coverageAdded || bytes.Count(edit.after, []byte("nyc -- ")) > bytes.Count(edit.before, []byte("nyc -- "))
	}
	if len(edits) > 0 {
		if framework == "cucumber" {
			impact = append(impact, "Selected package scripts now use NYC coverage and set DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED=false, including local npm test. Existing commands and unselected scripts are preserved.")
		}
		impact = append(impact, "The scripts' new NODE_OPTIONS=\"${NODE_OPTIONS:-} …\" prefix needs a POSIX shell, so these scripts no longer run locally from Windows cmd.")
	}
	targets := 0
	for _, workflow := range workflows {
		edit, err := readSetupEdit(root, workflow.Path)
		if err != nil {
			return nil, err
		}
		// Parse the same LF text the line edits use, so CRLF files attach
		// comments to the same nodes and lines.
		text := strings.ReplaceAll(string(edit.before), "\r\n", "\n")
		var document yaml.Node
		if err := yaml.Unmarshal([]byte(text), &document); err != nil {
			return nil, err
		}
		if len(document.Content) == 0 {
			continue
		}
		newline := "\n"
		if strings.Contains(string(edit.before), "\r\n") {
			newline = "\r\n"
		}
		lines := strings.Split(text, "\n")
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
			if workflowCall(workflow.On) && !reusableSecretForwarded(workflows, workflow) {
				return nil, &SetupGap{location, "Reusable workflows are applied only when on.workflow_call.secrets declares DD_API_KEY and every caller in this repository passes it (secrets: inherit or secrets: {DD_API_KEY: ...}). Declare and forward the secret, or add the Datadog action to this workflow manually; callers in other repositories can't be verified."}
			}
			gap := func(reason string) error { return &SetupGap{location, reason} }
			// The action's tracer install and the preload are unverified on
			// Windows runners, so no framework edits them automatically. A job
			// whose runner is one matrix value keeps its Windows rows
			// uninstrumented with a guard on that value: the action skips them
			// and the guarded preload falls back to the original options.
			windows, windowsGuard, err := setupWindowsRunner(job)
			if err != nil {
				return nil, gap(err.Error())
			}
			if windows && (framework == "cucumber" || framework == "webdriverio") {
				return nil, gap("Windows npm scripts require portable scoped initialization; the automatic recipe uses POSIX shell syntax. Preserve these scripts and integrate their hooks explicitly.")
			}
			if windows && windowsGuard == "" {
				return nil, gap("Windows runners are not supported by automatic setup: the Datadog action's tracer installation and the NODE_OPTIONS preload are unverified on Windows. Configure this job manually and validate it on a Windows runner.")
			}
			if reference := setupYAMLReference(&document); reference != nil {
				return nil, &SetupGap{workflow.Path + " / " + setupReferenceJob(jobs, reference) + " / line " + strconv.Itoa(reference.Line), "YAML anchors or aliases require explicit CI edits; automatic edits could affect another consumer."}
			}
			node := setupMember(jobs, name)
			steps := setupMember(node, "steps")
			if job.Container != nil ||
				steps == nil || steps.Kind != yaml.SequenceNode || steps.Style != 0 || len(steps.Content) != len(job.Steps) {
				return nil, gap("Automatic edits require literal steps without a container or expanded composite; preserve this setup and configure its tracer/loader explicitly.")
			}
			requirement := nodeRequirement
			condition := ""
			unsupported := false
			for position, index := range selected {
				before := job
				before.Steps = job.Steps[:index]
				value, err := setupRuntimeCondition(root, workflow, before, requirement, setupNeedsESM(root, framework, workflow, job, job.Steps[index]))
				var runtimeGap *NodeRuntimeGap
				if errors.As(err, &runtimeGap) && runtimeGap.Unsupported {
					// Every row runs a Node no dd-trace release supports: the job
					// keeps running uninstrumented and validation reports it as not
					// tried. ddtest decides this; there is no tracer to choose.
					unsupported = true
					break
				}
				if errors.As(err, &runtimeGap) {
					runtimeGap.Location = location
					return nil, runtimeGap
				}
				if err != nil {
					return nil, gap(err.Error())
				}
				if position > 0 && value != condition {
					return nil, gap("Selected test steps use conflicting runtime partitions; configure their initialization explicitly.")
				}
				condition = value
			}
			if unsupported {
				continue
			}
			layers := []map[string]string{workflow.Env, job.Env}
			for _, i := range selected {
				layers = append(layers, job.Steps[i].Env)
			}
			for _, layer := range layers {
				for _, key := range []string{"DD_TRACE_PACKAGE", "DD_TRACE_ESM_IMPORT", "DD_CIVISIBILITY_ENABLED", "DD_TRACE_ENABLED"} {
					if _, ok := layer[key]; ok {
						return nil, gap("Custom inherited instrumentation requires review: " + key)
					}
				}
			}
			if condition != "" {
				selected = slices.DeleteFunc(selected, func(i int) bool { return job.Steps[i].If == "${{ !("+condition+") }}" })
				if len(selected) == 0 {
					return nil, gap("No supported test step remains after runtime partitioning; review the remaining legacy-only steps explicitly.")
				}
			}
			actionIndex := -1
			for i, step := range job.Steps {
				if strings.HasPrefix(strings.ToLower(step.Uses), "datadog/test-visibility-github-action@") {
					actionIndex = i
				}
			}
			if actionIndex >= 0 {
				actionNode := steps.Content[actionIndex]
				guarded := setupMember(actionNode, "continue-on-error")
				if guarded != nil && guarded.Value != "true" {
					return nil, gap("Existing Datadog action has an explicit failure policy; review missing-secret handling before changing it.")
				}
				if guarded == nil {
					actionNode.Content = append(actionNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "continue-on-error"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
					start, end := setupStepStart(actionNode, lines), setupNodeEnd(actionNode, lines)
					width := setupIndent(lines, actionNode)
					text, err := encodeSetupStep(actionNode, width, setupNestedIndent(actionNode, steps, jobs, width), newSetupComments(lines[start:end]))
					if err != nil {
						return nil, err
					}
					padding := len(lines[actionNode.Line-1]) - len(strings.TrimLeft(lines[actionNode.Line-1], " "))
					insertions = append(insertions, lineInsertion{line: start, remove: end - start, text: indentSetup(text, padding)})
				}
			}
			// Test steps sharing one guard on matrix/env values get the same guard
			// on the inserted action, so the tracer installs exactly where they run.
			stepGuard := ""
			if condition == "" {
				stepGuard = job.Steps[selected[0]].If
				for _, i := range selected {
					if job.Steps[i].If != stepGuard {
						stepGuard = ""
					}
				}
				if !simpleStepCondition(stepGuard) {
					stepGuard = ""
				}
			}
			expectedGuard := setupActionGuard(condition, stepGuard, windowsGuard)
			seen := false
			for i, step := range job.Steps {
				if !strings.HasPrefix(strings.ToLower(step.Uses), "datadog/test-visibility-github-action@") {
					continue
				}
				if reason := existingActionDifference(step, seen, i >= selected[0], condition, expectedGuard, version); reason != "" {
					return nil, gap("Existing Datadog setup differs: " + step.Uses + ": " + reason + "; otherwise review it manually without adding a second action.")
				}
				seen = true
			}
			for _, i := range selected {
				step, node := job.Steps[i], steps.Content[i]
				run := setupMember(node, "run")
				if (step.If != "" && step.If != stepGuard && (actionIndex < 0 || step.If != condition)) || len(step.Parents) != 0 || node.Kind != yaml.MappingNode || node.Style != 0 || node.Anchor != "" ||
					run == nil || run.Kind != yaml.ScalarNode || (framework != "mocha" && (run.Style == yaml.LiteralStyle || run.Style == yaml.FoldedStyle || strings.Contains(run.Value, "\n"))) {
					return nil, gap(fmt.Sprintf("Step %d needs explicit loader placement: conditional, composite or multiline commands are outside this applier.", i+1))
				}
				line := lines[node.Line-1]
				indent := len(line) - len(strings.TrimLeft(line, " "))
				if !strings.HasPrefix(line[indent:], "- ") {
					return nil, gap("Unrecognized YAML sequence formatting; configure the test step explicitly.")
				}
				preload := setupPreload(framework)
				if framework == "mocha" {
					resolution := resolveTestStep(root, workflow, job, step, "javascript", framework)
					for _, command := range resolution.Tests {
						if command.mochaESM {
							preload = vitestPreload
						}
					}
				}
				if scopedOptions != "" {
					preload = scopedOptions
				}
				// A step-level variable replaces inherited values. Copy only a
				// literal inherited option string; a self-reference would become
				// ambiguous during command discovery and CI expression evaluation.
				key := scopedEnvironment(framework)
				if _, overrides := step.Env[key]; !overrides {
					inherited := workflow.Env[key]
					if value, exists := job.Env[key]; exists {
						inherited = value
					}
					if strings.ContainsAny(inherited, "$`\n") {
						return nil, gap("Dynamic inherited " + key + " requires explicit initialization.")
					}
					if inherited != "" {
						preload = inherited + " " + preload
					}
				}
				covered, err := setupMochaCoverage(root, filepath.Join(root, stepDirectory(workflow, job, step)), framework, step.Run)
				if err != nil {
					return nil, gap(err.Error())
				}
				coverageAdded = coverageAdded || covered != step.Run
				if parts, err := staticCommands(step.Run); err == nil && len(parts) > 1 && key == "NODE_OPTIONS" {
					impact = append(impact, fmt.Sprintf("%s step %d also runs other commands (%s); its step-level NODE_OPTIONS preloads dd-trace into them too, such as builds.", location, i+1, step.Run))
				}
				width := setupIndent(lines, node)
				nested := setupNestedIndent(node, steps, jobs, width)
				start, end := setupStepStart(node, lines), setupNodeEnd(node, lines)
				comments := newSetupComments(lines[start:end])
				if condition != "" && actionIndex < 0 && step.ID != "" {
					return nil, gap("The test step has an id referenced by other steps; configure the runtime partition explicitly without duplicating or changing that id.")
				}
				partition := ""
				if actionIndex < 0 {
					partition = condition
				}
				text, err := setupStepText(node, width, nested, comments, scopedEnvironment(framework), preload, covered, partition, actionIndex >= 0)
				if err != nil {
					return nil, gap(err.Error())
				}
				if partition != "" {
					legacy := *node
					legacy.Content = slices.Clone(node.Content)
					legacy.Content = append(legacy.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "if"}, &yaml.Node{Kind: yaml.ScalarNode, Value: "${{ !(" + condition + ") }}"})
					legacyText, err := encodeSetupStep(&legacy, width, nested, comments)
					if err != nil {
						return nil, err
					}
					text += legacyText
				}
				insertions = append(insertions, lineInsertion{line: start, remove: end - start, text: indentSetup(text, indent)})

				if i == selected[0] && actionIndex < 0 {
					padding := strings.Repeat(" ", indent)
					guard := ""
					if expectedGuard == condition && condition != "" {
						guard = padding + strings.Repeat(" ", width) + "if: " + condition + "\n"
					} else if expectedGuard != "" {
						encoded, err := yaml.Marshal(expectedGuard)
						if err != nil {
							return nil, err
						}
						guard = padding + strings.Repeat(" ", width) + "if: " + strings.TrimSuffix(string(encoded), "\n") + "\n"
					}
					action := padding + "-" + strings.Repeat(" ", width-1) + "name: Configure Datadog Test Optimization\n" + guard + padding + strings.Repeat(" ", width) + "uses: " + setupActionV3 + " # v3.3.0\n" + padding + strings.Repeat(" ", width) + "continue-on-error: true # keep tests runnable when credentials are unavailable\n" + padding + strings.Repeat(" ", width) + "with:\n" + padding + strings.Repeat(" ", width+nested) + "languages: js\n" + padding + strings.Repeat(" ", width+nested) + "api_key: ${{ secrets.DD_API_KEY }}\n" + padding + strings.Repeat(" ", width+nested) + "site: " + site + "\n" + padding + strings.Repeat(" ", width+nested) + "js-tracer-version: " + version + "\n"
					insertions = append(insertions, lineInsertion{line: start, text: action})
				}
				targets++
			}
		}
		slices.SortStableFunc(insertions, func(a, b lineInsertion) int {
			if a.line != b.line {
				return b.line - a.line
			}
			return b.remove - a.remove
		})
		for _, insertion := range insertions {
			lines = slices.Replace(lines, insertion.line, insertion.line+insertion.remove, strings.TrimSuffix(insertion.text, "\n"))
		}
		edit.after = []byte(strings.ReplaceAll(strings.Join(lines, "\n"), "\n", newline))
		if len(insertions) > 0 && !bytes.Equal(edit.before, edit.after) {
			if err := validateSetupWorkflow(root, framework, edit.before, edit.after); err != nil {
				return nil, &SetupGap{workflow.Path, "Planned CI edits are not valid YAML; source files were preserved: " + err.Error()}
			}
			edits = append(edits, edit)
		}
	}
	if targets == 0 {
		return nil, &SetupGap{".github/workflows", "No literal " + framework + " CI step found. Identify its real CI entry point before applying onboarding."}
	}
	ignored, err := ignoreSetupEdits(root, coverageAdded)
	if err != nil {
		return nil, err
	}
	edits = append(edits, ignored...)
	if coverageAdded {
		impact = append(impact, "NYC coverage writes an untracked .nyc_output/ folder wherever the covered command runs, including local validation; it is added to .gitignore.")
	}
	return &SetupPlan{root: root, edits: edits, impact: impact}, nil
}

// ignoreSetupEdits excludes ddtest's report directory, and NYC's output when
// coverage was added, from Git and npm packaging.
func ignoreSetupEdits(root string, coverageAdded bool) ([]setupEdit, error) {
	var edits []setupEdit
	for _, path := range []string{".gitignore", ".npmignore"} {
		edit, err := readSetupEdit(root, path)
		if err != nil {
			return nil, err
		}
		if path == ".npmignore" && edit.before == nil {
			continue
		} // Creating it would disable npm's .gitignore fallback.
		// ddtest writes .testoptimization/ at the root only. NYC writes
		// .nyc_output/ in each covered command's directory, so its entry is
		// unanchored and a root-only pattern doesn't cover it.
		entries := []string{"/.testoptimization/"}
		if coverageAdded {
			entries = append(entries, ".nyc_output/")
		}
		for _, line := range strings.Split(strings.ReplaceAll(string(edit.before), "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			entries = slices.DeleteFunc(entries, func(entry string) bool {
				if entry == ".nyc_output/" {
					return strings.TrimSuffix(strings.TrimPrefix(line, "**/"), "/") == ".nyc_output"
				}
				return strings.Trim(line, "/") == ".testoptimization"
			})
		}
		if len(entries) == 0 {
			continue
		}
		newline := "\n"
		if strings.Contains(string(edit.before), "\r\n") {
			newline = "\r\n"
		}
		edit.after = slices.Clone(edit.before)
		if len(edit.after) > 0 && !bytes.HasSuffix(edit.after, []byte("\n")) {
			edit.after = append(edit.after, []byte(newline)...)
		}
		for _, entry := range entries {
			edit.after = append(edit.after, []byte(entry+newline)...)
		}
		edits = append(edits, edit)
	}
	return edits, nil
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

// setupYAMLReference returns the first anchor or alias in the document.
func setupYAMLReference(node *yaml.Node) *yaml.Node {
	if node.Anchor != "" || node.Kind == yaml.AliasNode {
		return node
	}
	for _, child := range node.Content {
		if reference := setupYAMLReference(child); reference != nil {
			return reference
		}
	}
	return nil
}

// setupReferenceJob names the job that contains node, so a refusal points at
// the anchor rather than the test job being edited.
func setupReferenceJob(jobs, node *yaml.Node) string {
	if jobs != nil && jobs.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(jobs.Content); i += 2 {
			if setupContains(jobs.Content[i+1], node) {
				return jobs.Content[i].Value
			}
		}
	}
	return "workflow"
}

func setupContains(parent, node *yaml.Node) bool {
	if parent == node {
		return true
	}
	return slices.ContainsFunc(parent.Content, func(child *yaml.Node) bool { return setupContains(child, node) })
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

// existingActionDifference names the first way an existing Datadog action
// differs from the setup this applier would write.
func existingActionDifference(step runtimeStep, duplicate, afterTests bool, condition, guard, version string) string {
	// Its optional site input defaults to datadoghq.com; --site only applies to
	// a new action, so an existing one keeps its own.
	switch {
	case duplicate:
		return "more than one Datadog action runs in this job"
	case afterTests:
		return "it runs after the test step"
	case !setupActionSupported(step.Uses):
		return "use a recognized v3 release or SHA"
	case !setupGuardMatches(step.If, guard) && !setupGuardMatches(step.If, condition):
		// The exact line keeps every Windows, unsupported-Node and runtime guard.
		if guard == "" {
			return fmt.Sprintf("its if condition %q would skip instrumented test rows; remove that if: line", step.If)
		}
		return fmt.Sprintf("its if condition %q differs from the guard ddtest writes; replace it with `if: %s`", step.If, guard)
	case step.With["languages"] != "js":
		return fmt.Sprintf("languages is %q, not js", step.With["languages"])
	case step.With["js-tracer-version"] == "":
		return "js-tracer-version is missing; pin " + version
	case step.With["js-tracer-version"] != version:
		return fmt.Sprintf("js-tracer-version is %s, not %s", step.With["js-tracer-version"], version)
	case step.With["api_key"] == "":
		return "api_key is missing"
	}
	return ""
}

// setupActionGuard is the if: expression of the inserted Datadog action: the
// runtime partition, the test steps' own guard and the Windows guard, in that
// order. The inserter and the re-apply check share it, so ddtest accepts its
// own output.
func setupActionGuard(condition, stepGuard, windowsGuard string) string {
	var guards []string
	for _, value := range []string{condition, stepGuard, windowsGuard} {
		if value = unwrapSetupExpression(value); value != "" {
			guards = append(guards, value)
		}
	}
	switch len(guards) {
	case 0:
		return ""
	case 1:
		return guards[0]
	}
	return "(" + strings.Join(guards, ") && (") + ")"
}

func unwrapSetupExpression(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "${{") && strings.HasSuffix(value, "}}") {
		value = strings.TrimSpace(value[3 : len(value)-2])
	}
	return value
}

// setupGuardMatches compares if: expressions with or without ${{ }}.
func setupGuardMatches(existing, expected string) bool {
	return unwrapSetupExpression(existing) == unwrapSetupExpression(expected)
}

// setupWindowsRunner reports whether any row of job runs on Windows. When the
// runner comes from one matrix value and other rows don't run on Windows,
// guard is a condition on that value that skips only the Windows rows, so the
// other rows can still be instrumented.
func setupWindowsRunner(job runtimeJob) (windows bool, guard string, err error) {
	rows, _, err := jobMatrix(job)
	if err != nil {
		return false, "", err
	}
	labels := runnerLabels(job.RunsOn)
	key := ""
	if len(labels) == 1 {
		if match := matrixRunnerLabel.FindStringSubmatch(labels[0]); match != nil {
			key = match[1]
		}
	}
	var skipped []string
	other := false
	for _, row := range rows {
		rowWindows := false
		for _, label := range labels {
			runner, err := runtimeValue(label, row)
			if err != nil {
				return false, "", err
			}
			rowWindows = rowWindows || strings.HasPrefix(strings.ToLower(runner), "windows")
		}
		if !rowWindows {
			other = true
			continue
		}
		windows = true
		value, ok := row[key].(string)
		if key == "" || !ok || strings.ContainsAny(value, "'\n") {
			key = ""
			continue
		}
		if !slices.Contains(skipped, value) {
			skipped = append(skipped, value)
		}
	}
	if !windows || !other || key == "" {
		return windows, "", nil
	}
	conditions := make([]string, 0, len(skipped))
	for _, value := range skipped {
		conditions = append(conditions, "matrix."+key+" != '"+value+"'")
	}
	return true, strings.Join(conditions, " && "), nil
}

var matrixRunnerLabel = regexp.MustCompile(`^\$\{\{\s*matrix\.([A-Za-z0-9_-]+)\s*\}\}$`)

// WindowsOnlyConfiguration reports whether every CI location of entry runs on
// a Windows runner. Automatic setup never edits Windows rows and this host
// can't replay their setup, so it stays a report blocker instead of stopping
// setup of the other configurations.
func WindowsOnlyConfiguration(root string, entry TestCommand) bool {
	if len(entry.locations) == 0 {
		return false
	}
	workflows, err := readWorkflows(root)
	if err != nil {
		return false
	}
	for _, location := range entry.locations {
		windows := false
		for _, workflow := range workflows {
			job, ok := workflow.Jobs[location.Job]
			if workflow.Path != location.Workflow || !ok {
				continue
			}
			for _, label := range runnerLabels(job.RunsOn) {
				if runner, err := runtimeValue(label, location.Matrix); err == nil && strings.HasPrefix(strings.ToLower(runner), "windows") {
					windows = true
				}
			}
		}
		if !windows {
			return false
		}
	}
	return true
}

// reusableSecretForwarded reports whether a workflow_call workflow declares
// DD_API_KEY and at least one local caller exists, each forwarding it.
func reusableSecretForwarded(workflows []ciWorkflow, workflow ciWorkflow) bool {
	on, _ := workflow.On.(map[string]any)
	call, _ := on["workflow_call"].(map[string]any)
	declared, _ := call["secrets"].(map[string]any)
	if _, ok := declared["DD_API_KEY"]; !ok {
		return false
	}
	callers := 0
	for _, caller := range workflows {
		for _, job := range caller.Jobs {
			target, _, _ := strings.Cut(job.Uses, "@")
			if !strings.HasPrefix(target, "./") || filepath.Clean(strings.TrimPrefix(target, "./")) != workflow.Path {
				continue
			}
			callers++
			secrets, _ := job.Secrets.(map[string]any)
			if _, forwarded := secrets["DD_API_KEY"]; job.Secrets != "inherit" && !forwarded {
				return false
			}
		}
	}
	return callers > 0
}

func workflowCall(on any) bool {
	switch value := on.(type) {
	case string:
		return value == "workflow_call"
	case []any:
		for _, event := range value {
			if event == "workflow_call" {
				return true
			}
		}
	case map[string]any:
		_, exists := value["workflow_call"]
		return exists
	}
	return false
}
