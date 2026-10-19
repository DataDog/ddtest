package onboard

import (
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

// TestCommand identifies an explicit framework invocation, not the surrounding
// build, publication or shell script. Locations retain every CI consumer.
type TestCommand struct {
	ID                   string           `json:"id,omitempty"`
	Services             []ServiceCommand `json:"services,omitempty"`
	LocalOverrides       []string         `json:"local_overrides,omitempty"`
	cucumberProbeCommand string
	Cypress              *CypressCommand `json:"cypress,omitempty"`
	Resolution           string          `json:"resolution,omitempty"`
	CICoverageCommand    string          `json:"ci_coverage_command,omitempty"`
	mochaProbeCommand    string
	usesNYC              bool
	Directory            string            `json:"directory"`
	Command              string            `json:"command"`
	ExitOnFailure        bool              `json:"exit_on_failure,omitempty"`
	Environment          map[string]string `json:"environment,omitempty"`
	Locations            []string          `json:"locations,omitempty"`
	Prerequisites        []BuildCommand    `json:"prerequisites,omitempty"`
	UnvalidatedSetup     []string          `json:"unvalidated_setup,omitempty"`
}

// BuildCommand is a statically resolved build preceding a test in the same
// command chain. It is executed without test instrumentation.
type BuildCommand struct {
	ExpectedFiles []string          `json:"expected_files,omitempty"`
	FactSource    string            `json:"fact_source,omitempty"`
	Environment   map[string]string `json:"environment,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	Directory     string            `json:"directory"`
	Command       string            `json:"command"`
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
			if framework == "webdriverio" && !webdriverIOJobCandidate(root, workflow, job) {
				continue
			}
			if framework == "pytest" && !pythonJobCandidate(root, workflow, job) {
				scope.Review = append(scope.Review, workflow.Path+" / "+name+": no pytest environment identified; review other entry points separately.")
				continue
			}
			if framework == "cypress" || framework == "cucumber" {
				candidate := false
				for _, step := range job.Steps {
					resolution := resolveTestStep(root, workflow, job, step, "javascript", framework)
					candidate = candidate || !resolution.Review && (resolution.Matched || resolution.Reason != "")
				}
				if !candidate {
					scope.Review = append(scope.Review, workflow.Path+" / "+name+": no "+framework+" invocation identified; action implementations and job conditions are outside this local validation scope.")
					continue
				}
			}
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
				var unresolved, review []string
				var commands []TestCommand
				for i, step := range job.Steps {
					uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")
					if framework == "cypress" && uses == githubAction && step.Run == "" && step.ResolutionError == "" {
						review = append(review, fmt.Sprintf("%s / %s / step %d: Datadog CI installation and its condition are checked separately by CI runtime analysis; local validation installs and initializes its own tracer.", workflow.Path, name, stepNumber(step, i)))
						continue
					}
					if ((framework == "playwright" || framework == "cypress" || framework == "webdriverio") && (uses == "actions/upload-artifact" || framework == "webdriverio" && uses == "stateful/vscode-server-action") || framework == "mocha" && slices.Contains([]string{"actions/upload-artifact", "coverallsapp/github-action"}, uses)) && step.Run == "" && step.ResolutionError == "" {
						reason := "artifact upload is outside local Playwright execution"
						if framework == "webdriverio" {
							reason = "CI artifact upload or interactive debugging is outside local WebdriverIO execution"
						}
						if framework == "mocha" {
							reason = "artifact/coverage upload is outside local Mocha execution"
						}
						if framework == "cypress" {
							reason = "artifact upload is outside local Cypress execution"
						}
						review = append(review, fmt.Sprintf("%s / %s / step %d: %s; its CI condition is not evaluated", workflow.Path, name, stepNumber(step, i), reason))
						continue
					}
					active, conditionErr := stepCondition(step, row, conditionEnvironment(workflow, job, step, row))
					if conditionErr == nil && !active {
						continue
					}
					jobLabel := name
					if len(row) > 0 {
						jobLabel += fmt.Sprint(" ", row)
					}
					location := fmt.Sprintf("%s / %s / step %d (%s)", workflow.Path, jobLabel, stepNumber(step, i), step.Run)
					resolved, env, envErr := resolveVariantStep(workflow, job, step, row, framework)
					resolution := resolveTestStep(root, workflow, job, resolved, "javascript", framework)
					if conditionErr != nil && resolved.Run != "" && resolution.Reason == "" && !resolution.Matched && len(resolution.Builds) == 0 {
						resolution.Review, resolution.Reason = true, "Condition affects only a recognized non-test command; review separately: "+conditionErr.Error()
					}
					if conditionErr != nil && !resolution.Review {
						pendingSetup = append(pendingSetup, conditionErr.Error())
						resolution.Reason = conditionErr.Error()
					}
					if envErr != nil && !resolution.Review {
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
						if framework == "mocha" && !entry.usesNYC {
							parts, err := staticCommands(entry.Command)
							if err == nil && len(parts) == 1 && len(parts[0]) > 0 && !strings.Contains(parts[0][0], "=") {
								entry.CICoverageCommand = "npx --no-install nyc -- " + entry.Command
							}
						}
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
						return reflect.DeepEqual(previous.Cypress, entry.Cypress) && previous.Directory == entry.Directory && previous.Command == entry.Command && previous.ExitOnFailure == entry.ExitOnFailure && slices.EqualFunc(previous.Prerequisites, entry.Prerequisites, EqualPreparation) && slices.Equal(previous.UnvalidatedSetup, entry.UnvalidatedSetup) && maps.Equal(previous.Environment, entry.Environment)
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
	scope.Unresolved = groupMatrixDiagnostics(scope.Unresolved)
	scope.Review = groupMatrixDiagnostics(scope.Review)
	return scope, nil
}

// ValidationCommands also resolves a selected local alias, for scope accounting.
func ValidationCommands(root, command, framework string) []TestCommand {
	if framework == "pytest" {
		return resolvePythonStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}).Tests
	}
	remaining := 256
	return resolveJestSequence(root, root, command, nil, &remaining, framework).Tests
}

func WriteValidationScope(output io.Writer, scope ValidationScope) {
	_, _ = fmt.Fprintln(output, "\nRequired local validation configurations (every CI consumer must also be instrumented):")
	for _, entry := range scope.Commands {
		_, _ = fmt.Fprintf(output, "  - [%s] %s\n", entry.Directory, entry.Command)
		if entry.Resolution != "" {
			_, _ = fmt.Fprintf(output, "      Resolved test command: %s\n", entry.Resolution)
		}
		if entry.CICoverageCommand != "" {
			_, _ = fmt.Fprintf(output, "      CI coverage command: %s\n      Apply this wrapper in CI; testdrive adds NYC locally only. Real source coverage is a separate check.\n", entry.CICoverageCommand)
		}
		if entry.ExitOnFailure {
			_, _ = fmt.Fprintln(output, "      Preserve CI failure guard: || exit 1")
		}
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

// Keep repeated setup/parse failures readable without hiding their affected
// matrix scope. Per-test locations and runtime findings retain all consumers.
func groupMatrixDiagnostics(messages []string) []string {
	type group struct {
		text string
		rows []string
	}
	groups := []group{}
	indexes := map[string]int{}
	for _, message := range messages {
		prefix, rest, matrix := strings.Cut(message, " map[")
		row, suffix, end := strings.Cut(rest, "] / step ")
		key := message
		if matrix && end {
			key = prefix + " / step " + suffix
		} else {
			row = ""
		}
		index, exists := indexes[key]
		if !exists {
			index = len(groups)
			indexes[key] = index
			groups = append(groups, group{text: key})
		}
		if row != "" && !slices.Contains(groups[index].rows, row) {
			groups[index].rows = append(groups[index].rows, row)
		}
	}
	result := make([]string, 0, len(groups))
	for _, group := range groups {
		text := group.text
		if len(group.rows) > 0 {
			text += fmt.Sprintf(" [affects %d matrix entries; e.g. %s]", len(group.rows), strings.Join(group.rows[:min(2, len(group.rows))], "; "))
		}
		result = append(result, text)
	}
	return result
}
