package onboard

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/kballard/go-shellquote"
)

// Ruby tasks can execute arbitrary code. Replay only a single explicit entry
// point; retain ambiguous shell setup for review instead of guessing its scope.
func resolveRubyStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, framework string) commandResolution {
	if step.ResolutionError != "" {
		return unresolvedCommand(step.ResolutionError)
	}
	if strings.TrimSpace(step.Run) == "" {
		return commandResolution{}
	}
	directory := filepath.Clean(stepDirectory(workflow, job, step))
	if !filepath.IsLocal(directory) || strings.ContainsAny(directory, "$`~") {
		return unresolvedCommand("Ruby working-directory needs explicit review: " + directory)
	}
	parts, parseErr := staticCommands(step.Run)
	matched := environment.LooksLikeTestCommand(filepath.Join(root, directory), step.Run, "ruby", framework)
	if parseErr == nil && len(parts) == 1 {
		words := parts[0]
		if len(words) > 2 && filepath.Base(words[0]) == "bundle" && words[1] == "exec" {
			words = words[2:]
		}
		if len(words) > 0 {
			name := filepath.Base(words[0])
			if name == "rspec" && framework != "rspec" || name == "rails" && framework != "minitest" {
				return commandResolution{Review: true, Reason: "Explicit command belongs to the other Ruby framework; validate it separately."}
			}
			if name == "ruby" && framework == "minitest" {
				for _, word := range words[1:] {
					matched = matched || strings.Contains(word, "_test.rb") || strings.Contains(word, "minitest")
				}
			}
		}
		if len(words) > 0 && filepath.Base(words[0]) == "rake" {
			if !environment.LooksLikeTestCommand(filepath.Join(root, directory), step.Run, "ruby", framework) {
				return commandResolution{Review: true, Reason: "Ruby maintenance task is outside test validation."}
			}
			other := "test"
			if framework == "minitest" {
				other = "spec"
			}
			for _, word := range words[1:] {
				if word == other || strings.HasPrefix(word, other+":") {
					return commandResolution{Review: true, Reason: "Rake " + other + " task belongs to the other Ruby framework; validate it separately."}
				}
			}
			task := "spec"
			if framework == "minitest" {
				task = "test"
			}
			known := false
			for _, word := range words[1:] {
				known = known || word == task || strings.HasPrefix(word, task+":")
			}
			if !known {
				return unresolvedCommand("Default/custom Rake task needs explicit review; select the actual " + framework + " test entry point with --command.")
			}
		}
	}
	if !matched {
		if strings.Contains(step.Run, "test") || strings.Contains(step.Run, "spec") {
			return unresolvedCommand("Ruby test wrapper was not resolved; validate its actual entry point with --command: " + step.Run)
		}
		if strings.Contains(step.Run, "bundle exec") || strings.Contains(step.Run, "bin/") {
			return commandResolution{Review: true, Reason: "Ruby task or wrapper was not identified as " + framework + "; inspect it separately if it runs tests."}
		}
		return commandResolution{}
	}
	parts, err := staticCommands(step.Run)
	if err != nil || len(parts) != 1 || len(parts[0]) == 0 || strings.Contains(parts[0][0], "=") {
		return unresolvedCommand("Ruby test entry point requires a single explicit command; preserve shell setup and validate with --command from the prepared environment: " + step.Run)
	}
	return commandResolution{Matched: true, Tests: []TestCommand{{Directory: directory, Command: shellquote.Join(parts[0]...)}}}
}

// RubyValidationCommand retains the CI entry point when there is one choice.
func RubyValidationCommand(root, framework, override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return override, nil
	}
	scope, err := DiscoverValidationScope(root, framework)
	if err != nil {
		return "", err
	}
	if len(scope.Commands) > 1 {
		return "", fmt.Errorf("multiple %s CI configurations found; inspect --all --check-only or select one with --command", framework)
	}
	if len(scope.Commands) == 1 {
		entry := scope.Commands[0]
		if entry.Directory != "." || len(entry.Environment) > 0 || len(entry.UnvalidatedSetup) > 0 {
			return "", fmt.Errorf("ruby CI command requires its working-directory/environment/setup; use --all or validate an explicit --command from that prepared environment")
		}
		return entry.Command, nil
	}
	return "", nil
}

// RubyCIConfiguration inventories static Ruby selections. The caller checks
// requirements with RubyGems itself, against the actual installed bundle.
func RubyCIConfiguration(root, framework string) RuntimeCheck {
	result := RuntimeCheck{Status: "not applicable", Reason: "No candidate Ruby CI commands found."}
	workflows, err := readWorkflows(root)
	if err != nil {
		return RuntimeCheck{Status: "inconclusive", Reason: err.Error()}
	}
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			rows, err := runtimeMatrix(job.Strategy.Matrix)
			if err != nil {
				result.Jobs = append(result.Jobs, RuntimeFinding{Workflow: workflow.Path, Job: name, Status: "inconclusive", Reason: err.Error()})
				continue
			}
			for _, row := range rows {
				active, conditionErr := possibleJobCondition(job.If, row)
				if conditionErr == nil && !active {
					continue
				}
				ruby, tracer, action := "", "", ""
				skippedAction := false
				for i, step := range job.Steps {
					uses, _, hasVersion := strings.Cut(strings.ToLower(step.Uses), "@")
					active, stepErr := stepCondition(step, row, conditionEnvironment(workflow, job, step, row))
					if stepErr == nil && !active {
						languages := strings.Fields(step.With["languages"])
						if uses == githubAction && hasVersion && (slices.Contains(languages, "ruby") || slices.Contains(languages, "all")) {
							skippedAction = true
						}
						continue
					}
					if uses == "ruby/setup-ruby" {
						ruby, tracer, action = "", "", ""
						skippedAction = false
					}
					if uses == githubAction {
						tracer, action = "", ""
					}
					if conditionErr == nil && stepErr == nil {
						if uses == "ruby/setup-ruby" && hasVersion {
							ruby, _ = runtimeValue(step.With["ruby-version"], row)
							// A missing version may come from .ruby-version, but
							// the runner's implicit defaults are not certified here.
						}
						if uses == githubAction && hasVersion {
							languages := strings.Fields(step.With["languages"])
							if slices.Contains(languages, "ruby") || slices.Contains(languages, "all") {
								action = step.Uses
								tracer, _ = runtimeValue(step.With["ruby-tracer-version"], row)
							}
						}
					}
					resolved, _, resolveErr := resolveVariantStep(workflow, job, step, row, framework)
					resolution := resolveRubyStep(root, workflow, job, resolved, framework)
					if resolution.Review {
						result.Review = append(result.Review, RuntimeFinding{Workflow: workflow.Path, Job: name, Step: stepNumber(step, i), Command: resolved.Run, Status: "not checked", Reason: resolution.Reason})
						continue
					}
					if !resolution.Matched && resolution.Reason == "" {
						continue
					}
					finding := RuntimeFinding{Workflow: workflow.Path, Job: name, Step: stepNumber(step, i), Command: resolved.Run, Ruby: ruby, Action: action, TracerRequested: tracer, Status: "inconclusive", Reason: "Select a static Ruby version and exact ruby-tracer-version in a Datadog action before this test step; validate other installs separately."}
					if conditionErr != nil || stepErr != nil || resolveErr != nil || resolution.Reason != "" {
						reasons := []string{resolution.Reason}
						for _, err := range []error{conditionErr, stepErr, resolveErr} {
							if err != nil {
								reasons = append(reasons, err.Error())
							}
						}
						finding.Reason = "Ruby CI scope needs review: " + strings.TrimSpace(strings.Join(reasons, "; "))
					} else if ruby != "" && tracer != "" && action != "" {
						finding.Status, finding.Reason = "not checked", "Check the selected Ruby with the bundled tracer's RubyGems requirements and compare tracer releases."
					} else if action == "" && skippedAction {
						finding.Status, finding.Code = "excluded", "excluded_runtime"
						finding.Reason = "The Datadog action is explicitly skipped for this matrix entry. Its tests still run without instrumentation; this runtime is not validated with the tracer."
					}
					if reason := rubyBootstrapProblem(workflow, job, step, framework); reason != "" {
						finding.Status, finding.Reason = "inconclusive", reason
					}
					result.Jobs = append(result.Jobs, finding)
				}
			}
		}
	}
	if len(result.Jobs) > 0 {
		result.Status, result.Reason = "inconclusive", "Ruby CI runtime and tracer selections need validation against the local bundle."
	}
	return result
}

func rubyBootstrapProblem(workflow ciWorkflow, job runtimeJob, step runtimeStep, frameworks ...string) string {
	env := map[string]string{}
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		maps.Copy(env, layer)
	}
	if options, overridden := env["RUBYOPT"]; overridden {
		if !strings.Contains(options, "datadog/ci/auto_instrument") && !strings.Contains(options, "${{ env.RUBYOPT }}") && !strings.Contains(options, "${{env.RUBYOPT}}") {
			return "The Ruby test environment overrides the action's RUBYOPT without its Datadog preload. Preserve the instrumentation and the customer's Ruby options."
		}
	}
	keys := []string{"DD_CIVISIBILITY_ENABLED", "DD_TRACE_ENABLED"}
	if len(frameworks) > 0 {
		keys = append(keys, "DD_TRACE_"+strings.ToUpper(frameworks[0])+"_ENABLED")
	}
	for _, key := range keys {
		if value := strings.ToLower(env[key]); value == "false" || value == "0" {
			return "Ruby CI instrumentation is explicitly disabled by " + key
		}
	}
	return ""
}
