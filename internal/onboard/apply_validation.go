package onboard

import (
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

func setupNeedsESM(root, framework string, workflow ciWorkflow, job runtimeJob, step runtimeStep) bool {
	if slices.Contains([]string{"vitest", "cucumber", "webdriverio"}, framework) {
		return true
	}
	resolution := resolveTestStep(root, workflow, job, step, "javascript", framework)
	for _, command := range resolution.Tests {
		if command.mochaESM {
			return true
		}
	}
	return false
}

// Independently re-read the generated workflow, preserve unrelated steps and
// check each intended command and its bootstrap before any source file is written.
func validateSetupWorkflow(root, framework string, before, after []byte) error {
	if err := validateSetupComments(before, after); err != nil {
		return err
	}
	var original, planned ciWorkflow
	if err := yaml.Unmarshal(before, &original); err != nil {
		return err
	}
	if err := yaml.Unmarshal(after, &planned); err != nil {
		return err
	}
	for name, job := range original.Jobs {
		updated, exists := planned.Jobs[name]
		if !exists {
			return fmt.Errorf("job %s disappeared", name)
		}
		position := 0
		for _, old := range job.Steps {
			selected := resolveTestStep(root, original, job, old, "javascript", framework).Matched
			if position < len(updated.Steps) && selected && strings.HasPrefix(updated.Steps[position].Uses, githubAction+"@") && old.Uses == "" {
				position++
			}
			if position >= len(updated.Steps) {
				return fmt.Errorf("step disappeared from %s", name)
			}
			next := updated.Steps[position]
			position++
			if next.Run != "" && next.Uses != "" {
				return fmt.Errorf("one step contains both uses and run")
			}
			if !selected {
				if !reflect.DeepEqual(old, next) {
					return fmt.Errorf("unrelated step changed in %s", name)
				}
				continue
			}
			expected, err := setupMochaCoverage(root, filepath.Join(root, stepDirectory(original, job, old)), framework, old.Run)
			if err != nil {
				return err
			}
			if next.Run != expected {
				return fmt.Errorf("selected command changed unexpectedly in %s", name)
			}
			if old.If == "" && next.If != "" {
				if position >= len(updated.Steps) {
					return fmt.Errorf("legacy test step disappeared")
				}
				legacy := updated.Steps[position]
				position++
				if legacy.If != "${{ !("+next.If+") }}" {
					return fmt.Errorf("runtime partitions do not complement each other")
				}
				legacy.If = ""
				if !reflect.DeepEqual(old, legacy) {
					return fmt.Errorf("legacy command changed in %s", name)
				}
			} else if old.If != next.If {
				return fmt.Errorf("test condition changed in %s", name)
			}
			key := scopedEnvironment(framework)
			left, right := old, next
			left.Env, right.Env = maps.Clone(old.Env), maps.Clone(next.Env)
			delete(left.Env, key)
			delete(right.Env, key)
			if len(left.Env) == 0 {
				left.Env = nil
			}
			if len(right.Env) == 0 {
				right.Env = nil
			}
			left.Run, right.Run = "", ""
			left.If, right.If = "", ""
			if !reflect.DeepEqual(left, right) {
				return fmt.Errorf("unrelated test-step properties changed in %s", name)
			}
			bootstrap := next
			bootstrap.Env = maps.Clone(next.Env)
			bootstrap.Env["NODE_OPTIONS"] = next.Env[key]
			rows, err := runtimeMatrix(updated.Strategy.Matrix)
			if err != nil {
				return err
			}
			for _, row := range rows {
				active, err := stepCondition(next, row, conditionEnvironment(planned, updated, next, row))
				if err != nil {
					return err
				}
				if !active {
					continue
				}
				if reason := checkJestBootstrap(planned, updated, bootstrap, row, framework == "vitest" || framework == "cucumber" || framework == "webdriverio"); reason != "" {
					return fmt.Errorf("planned initialization: %s", reason)
				}
				noAction, err := effectiveBootstrapOptions(planned, updated, bootstrap, row, false)
				if err != nil {
					return err
				}
				if strings.Contains(noAction, "DD_ACTION_") {
					return fmt.Errorf("preload remains when the action is unavailable")
				}
			}
		}
		if position != len(updated.Steps) {
			return fmt.Errorf("unexpected extra steps in %s", name)
		}
	}
	return nil
}

// Edits add only trailing comments on generated lines, so every comment-only
// line must survive exactly once, and no whitespace-only line may appear.
func validateSetupComments(before, after []byte) error {
	count := func(data []byte) (map[string]int, int) {
		comments, blank := map[string]int{}, 0
		for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			text := strings.TrimSpace(line)
			if strings.HasPrefix(text, "#") {
				comments[text]++
			} else if text == "" && line != "" {
				blank++
			}
		}
		return comments, blank
	}
	original, originalBlank := count(before)
	planned, plannedBlank := count(after)
	for _, text := range slices.Sorted(maps.Keys(original)) {
		if planned[text] != original[text] {
			return fmt.Errorf("comment %q appears %d times instead of %d", text, planned[text], original[text])
		}
	}
	for _, text := range slices.Sorted(maps.Keys(planned)) {
		if original[text] == 0 {
			return fmt.Errorf("comment %q was added", text)
		}
	}
	if plannedBlank > originalBlank {
		return fmt.Errorf("whitespace-only lines were added")
	}
	return nil
}
