package onboard

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
)

// TestCommand identifies an explicit framework invocation, not the surrounding
// build, publication or shell script. Locations retain every CI consumer.
type TestCommand struct {
	Resolution       string            `json:"resolution,omitempty"`
	Directory        string            `json:"directory"`
	Command          string            `json:"command"`
	Environment      map[string]string `json:"environment,omitempty"`
	locations        []commandLocation
	Locations        []string       `json:"locations,omitempty"`
	Prerequisites    []BuildCommand `json:"prerequisites,omitempty"`
	UnvalidatedSetup []string       `json:"unvalidated_setup,omitempty"`
}

// BuildCommand is a statically resolved build preceding a test in the same
// command chain. Discovery records preparation without executing it.
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
	var allUnresolved, allReview []scopeDiagnostic
	if framework != "jest" {
		return scope, fmt.Errorf("CI command discovery is not available for %s", framework)
	}

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
				active, err := possibleJobCondition(job.If, row)
				if err != nil {
					scope.Unresolved = append(scope.Unresolved, workflow.Path+" / "+name+": "+err.Error())
					continue
				}
				if !active {
					continue
				}
				var pendingSetup []string
				var preparation []BuildCommand
				var unresolved, review []scopeDiagnostic
				var commands []TestCommand
				for i, step := range job.Steps {

					active, conditionErr := stepCondition(step, row, conditionEnvironment(workflow, job, runtimeStep{}, row))
					if conditionErr == nil && !active {
						continue
					}
					location := commandLocation{Workflow: workflow.Path, Job: name, Matrix: maps.Clone(row), Step: stepNumber(step, i), Command: step.Run}
					resolved, env, envErr := resolveVariantStep(workflow, job, step, row, framework)
					resolution := resolveTestStep(root, workflow, job, resolved, "javascript", framework)
					for _, setup := range resolution.Setup {
						unresolved = append(unresolved, scopeDiagnostic{location, setup})
					}
					if conditionErr != nil && resolved.Run != "" && resolution.Reason == "" && !resolution.Matched && len(resolution.Builds) == 0 {
						resolution.Review, resolution.Reason = true, "Condition affects only a recognized non-test command; review separately: "+conditionErr.Error()
					}
					if len(resolution.Builds) > 0 && (conditionErr != nil || envErr != nil) {
						resolution.Review = false
					}
					if conditionErr != nil && !resolution.Review {
						pendingSetup = append(pendingSetup, conditionErr.Error())
						resolution.Reason = joinDiagnostic(resolution.Reason, conditionErr.Error())
					}
					if envErr != nil && !resolution.Review {
						pendingSetup = append(pendingSetup, envErr.Error())
						resolution.Reason = joinDiagnostic(resolution.Reason, envErr.Error())
					}
					if resolution.Review {
						for j := range resolution.Builds {
							resolution.Builds[j].Environment = mergeEnvironment(env, resolution.Builds[j].Environment)
						}
						preparation = append(preparation, resolution.Builds...)
						pendingSetup = append(pendingSetup, resolution.Setup...)
						review = append(review, scopeDiagnostic{location, resolution.Reason})
						continue
					}
					if resolution.Reason != "" {
						unresolved = append(unresolved, scopeDiagnostic{location, resolution.Reason})
					}
					if resolution.ReviewReason != "" {
						review = append(review, scopeDiagnostic{location, resolution.ReviewReason})
					}
					for j := range resolution.Builds {
						resolution.Builds[j].Environment = mergeEnvironment(env, resolution.Builds[j].Environment)
					}
					for _, entry := range resolution.Tests {

						for j := range entry.Prerequisites {
							entry.Prerequisites[j].Environment = mergeEnvironment(env, entry.Prerequisites[j].Environment)
						}
						entry.Prerequisites = append(slices.Clone(preparation), entry.Prerequisites...)
						entry.UnvalidatedSetup = append(slices.Clone(pendingSetup), entry.UnvalidatedSetup...)
						entry.Environment = mergeEnvironment(env, entry.Environment)
						entry.Directory = filepath.ToSlash(filepath.Clean(entry.Directory))
						entry.locations = []commandLocation{location}
						commands = append(commands, entry)
					}
					if resolution.Reason != "" {
						pendingSetup = append(pendingSetup, location.String()+": "+resolution.Reason)
					}
					preparation = append(preparation, resolution.Builds...)
					pendingSetup = append(pendingSetup, resolution.Setup...)
				}
				allUnresolved = append(allUnresolved, unresolved...)
				allReview = append(allReview, review...)
				for _, entry := range commands {
					index := slices.IndexFunc(scope.Commands, func(previous TestCommand) bool {
						return previous.Directory == entry.Directory && previous.Command == entry.Command && slices.EqualFunc(previous.Prerequisites, entry.Prerequisites, EqualPreparation) && slices.Equal(previous.UnvalidatedSetup, entry.UnvalidatedSetup) && maps.Equal(previous.Environment, entry.Environment)
					})
					if index < 0 {
						scope.Commands = append(scope.Commands, entry)
					} else {
						scope.Commands[index].locations = append(scope.Commands[index].locations, entry.locations...)
					}
				}
			}
		}
	}
	scope.Unresolved = append(scope.Unresolved, groupMatrixDiagnostics(allUnresolved)...)
	scope.Review = append(scope.Review, groupMatrixDiagnostics(allReview)...)
	for i := range scope.Commands {
		for _, location := range scope.Commands[i].locations {
			scope.Commands[i].Locations = append(scope.Commands[i].Locations, location.String())
		}
	}
	return scope, nil
}

// ValidationCommands also resolves a selected local alias, for scope accounting.
func ValidationCommands(root, command, framework string) []TestCommand {
	remaining := 256
	return resolveJestSequence(root, root, command, nil, &remaining, framework).Tests
}

// File rewrites in earlier CI steps change what the same test command executes.
func mutatesCIFiles(command string) bool {
	commands, err := staticCommands(command)
	if err != nil {
		return false
	} // Already retained as unresolved setup.
	for _, words := range commands {
		words, _, err = commandEnvironment(words)
		if err != nil {
			continue
		}
		if len(words) > 0 && slices.Contains([]string{"sed", "cp", "mv", "rm", "patch", "rimraf", "rsync"}, words[0]) {
			return true
		}
		if len(words) > 1 && slices.Contains([]string{"pnpm", "yarn", "bun"}, words[0]) && words[1] == "add" {
			return true
		}
	}
	return false
}
