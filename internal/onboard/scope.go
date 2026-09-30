package onboard

import (
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
)

// TestCommand identifies an explicit framework invocation, not the surrounding
// build, publication or shell script. Locations retain every CI consumer.
type TestCommand struct {
	Directory        string            `json:"directory"`
	Command          string            `json:"command"`
	Environment      map[string]string `json:"environment,omitempty"`
	Locations        []string          `json:"locations,omitempty"`
	Prerequisites    []BuildCommand    `json:"prerequisites,omitempty"`
	UnvalidatedSetup []string          `json:"unvalidated_setup,omitempty"`
}

// BuildCommand is a statically resolved build preceding a test in the same
// command chain. It is executed without test instrumentation.
type BuildCommand struct {
	Environment map[string]string `json:"environment,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	Directory   string            `json:"directory"`
	Command     string            `json:"command"`
}

type ValidationScope struct {
	Commands   []TestCommand `json:"commands"`
	Unresolved []string      `json:"unresolved,omitempty"`
	Review     []string      `json:"review,omitempty"`
}

func yarnImplicitInstall(words []string) bool {
	if len(words) == 0 || words[0] != "yarn" {
		return false
	}
	for _, word := range words[1:] {
		if !slices.Contains([]string{"--frozen-lockfile", "--non-interactive", "--ignore-scripts", "--ignore-optional", "--offline", "--immutable"}, word) {
			return false
		}
	}
	return true
}

// DiscoverValidationScope reads CI and package aliases without running them.
// Unknown fragments stay visible alongside known test invocations.
func DiscoverValidationScope(root, framework string) (ValidationScope, error) {
	var scope ValidationScope
	workflows, err := readWorkflows(root)
	if err != nil {
		return scope, err
	}
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			rows, err := runtimeMatrix(job.Strategy.Matrix)
			if err != nil {
				scope.Unresolved = append(scope.Unresolved, workflow.Path+" / "+name+": "+err.Error())
				continue
			}
			for _, row := range rows {
				active, err := runtimeCondition(job.If, row)
				if err != nil {
					scope.Unresolved = append(scope.Unresolved, workflow.Path+" / "+name+": "+err.Error())
					continue
				}
				if !active {
					continue
				}
				var pendingSetup []string
				var preparation []BuildCommand
				var unresolved, review []string
				var commands []TestCommand
				for i, step := range job.Steps {
					active, conditionErr := stepCondition(step, row)
					if conditionErr == nil && !active {
						continue
					}
					jobLabel := name
					if len(row) > 0 {
						jobLabel += fmt.Sprint(" ", row)
					}
					location := fmt.Sprintf("%s / %s / step %d (%s)", workflow.Path, jobLabel, stepNumber(step, i), step.Run)
					resolved, env, envErr := resolveVariantStep(workflow, job, step, row)
					resolution := resolveTestStep(root, workflow, job, resolved, "javascript", framework)
					if conditionErr != nil {
						pendingSetup = append(pendingSetup, conditionErr.Error())
						resolution.Reason = conditionErr.Error()
					}
					if envErr != nil {
						pendingSetup = append(pendingSetup, envErr.Error())
						resolution.Reason = envErr.Error()
					}
					if resolution.Review {
						for j := range resolution.Builds {
							resolution.Builds[j].Environment = maps.Clone(env)
						}
						preparation = append(preparation, resolution.Builds...)
						review = append(review, location+": "+resolution.Reason)
						continue
					}
					if resolution.Reason != "" {
						unresolved = append(unresolved, location+": "+resolution.Reason)
					}
					if resolution.ReviewReason != "" {
						review = append(review, location+": "+resolution.ReviewReason)
					}
					for j := range resolution.Builds {
						resolution.Builds[j].Environment = maps.Clone(env)
					}
					for _, entry := range resolution.Tests {
						for j := range entry.Prerequisites {
							entry.Prerequisites[j].Environment = maps.Clone(env)
						}
						entry.Prerequisites = append(slices.Clone(preparation), entry.Prerequisites...)
						entry.UnvalidatedSetup = append(slices.Clone(pendingSetup), entry.UnvalidatedSetup...)
						entry.Environment = env
						entry.Directory = filepath.ToSlash(filepath.Clean(entry.Directory))
						entry.Locations = []string{location}
						commands = append(commands, entry)
					}
					if resolution.Reason != "" {
						pendingSetup = append(pendingSetup, location+": "+resolution.Reason)
					}
					if !resolution.Matched {
						preparation = append(preparation, resolution.Builds...)
					}
				}
				scope.Unresolved = append(scope.Unresolved, unresolved...)
				scope.Review = append(scope.Review, review...)
				for _, entry := range commands {
					index := slices.IndexFunc(scope.Commands, func(previous TestCommand) bool {
						return previous.Directory == entry.Directory && previous.Command == entry.Command && slices.EqualFunc(previous.Prerequisites, entry.Prerequisites, EqualPreparation) && slices.Equal(previous.UnvalidatedSetup, entry.UnvalidatedSetup) && maps.Equal(previous.Environment, entry.Environment)
					})
					if index < 0 {
						scope.Commands = append(scope.Commands, entry)
					} else {
						scope.Commands[index].Locations = append(scope.Commands[index].Locations, entry.Locations...)
					}
				}
			}
		}
	}
	return scope, nil
}

// ValidationCommands also resolves a selected local alias, for scope accounting.
func ValidationCommands(root, command, framework string) []TestCommand {
	remaining := 256
	return resolveJestSequence(root, root, command, nil, &remaining, framework).Tests
}

func WriteValidationScope(output io.Writer, scope ValidationScope) {
	_, _ = fmt.Fprintln(output, "\nRequired local validation configurations (every CI consumer must also be instrumented):")
	for _, entry := range scope.Commands {
		_, _ = fmt.Fprintf(output, "  - [%s] %s\n", entry.Directory, entry.Command)
		for _, build := range entry.Prerequisites {
			_, _ = fmt.Fprintf(output, "      Preparation: [%s] %s\n", build.Directory, build.Command)
		}
		for _, setup := range entry.UnvalidatedSetup {
			_, _ = fmt.Fprintf(output, "      Unvalidated setup: %s\n", setup)
		}
		for _, location := range entry.Locations {
			_, _ = fmt.Fprintf(output, "      %s\n", location)
		}
	}
	_, _ = fmt.Fprintln(output, "--all replays listed preparation in separate temporary copies before each comparison, without instrumentation. --check-only executes no preparation. Unresolved setup stays unvalidated. Preserve CI commands.")
	for _, reason := range scope.Unresolved {
		_, _ = fmt.Fprintf(output, "  - Unresolved scope: %s\n", reason)
	}
	for _, reason := range scope.Review {
		_, _ = fmt.Fprintf(output, "  - Review separately: %s\n", reason)
	}
}

// File rewrites in earlier CI steps change what the same test command executes.
func mutatesCIFiles(command string) bool {
	commands, err := staticCommands(command)
	if err != nil {
		return false
	} // Already retained as unresolved setup.
	for _, words := range commands {
		if len(words) > 0 && slices.Contains([]string{"sed", "cp", "mv", "rm", "patch"}, words[0]) {
			return true
		}
		if len(words) > 1 && slices.Contains([]string{"pnpm", "yarn", "bun"}, words[0]) && words[1] == "add" {
			return true
		}
	}
	return false
}
