package testdrive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/kballard/go-shellquote"
)

type configurationResult struct {
	onboard.TestCommand
	Status        string         `json:"status"`
	Compatibility verdict        `json:"compatibility"`
	Preflight     *jestPreflight `json:"preflight,omitempty"`
	Error         string         `json:"error,omitempty"`
	Preparation   []buildResult  `json:"preparation,omitempty"`
}

func setSingleConfigurationScope(root string, result *validationResult, scope onboard.ValidationScope) {
	result.Scope = &scope
	var selected []onboard.TestCommand
	if result.Preflight != nil {
		selected = onboard.ValidationCommands(root, result.Preflight.Command, result.Framework)
	}
	for _, entry := range scope.Commands {
		item := configurationResult{TestCommand: entry, Status: "not exercised", Compatibility: verdict{Status: "not exercised", Reason: "Run testdrive --all after required setup to validate every discovered configuration."}}
		for _, command := range selected {
			if command.Command == entry.Command && command.Directory == entry.Directory && slices.EqualFunc(command.Prerequisites, entry.Prerequisites, onboard.EqualPreparation) && len(entry.UnvalidatedSetup) == 0 && maps.Equal(command.Environment, entry.Environment) {
				item.Compatibility = result.Compatibility
				item.Preflight = result.Preflight
				item.Error = result.Error
				item.Status = configurationStatus(*result)
			}
		}
		result.Configurations = append(result.Configurations, item)
	}
}

func configurationStatus(result validationResult) string {
	if result.CheckOnly {
		return "not exercised"
	}
	if result.Error != "" || result.Compatibility.Status != "compatible" || len(result.Features) == 0 {
		return "incomplete"
	}
	for _, feature := range result.Features {
		if feature.Status != "passed" {
			return "incomplete"
		}
	}
	return "passed"
}

func applyScopeVerdict(result *validationResult) {
	if result.Scope == nil {
		return
	}
	if len(result.Scope.Unresolved) > 0 {
		result.Success = false
		result.ChecksPassed = false
	}
	for _, configuration := range result.Configurations {
		if configuration.Status != "passed" {
			result.Success = false
			result.LocalSuccess = false
		}
	}
}

// All evidence comes from this invocation. No earlier success is reused after
// source, configuration, runtime or tracer edits. Each configuration has its own
// paired comparison and feature controls, and shares only the final report pair.
func (t *Testdrive) runAllConfigurations(ctx context.Context, output io.Writer) error {
	scope, err := onboard.DiscoverValidationScope(t.repositoryRoot, t.framework.Name())
	if err != nil {
		return err
	}
	if len(scope.Commands) == 0 {
		return fmt.Errorf("no explicit %s CI invocations discovered; review discovery and use --command for local validation", t.framework.Name())
	}
	result := validationResult{Session: planSession().ID(), Framework: t.framework.Name(), Tracer: t.tracerLabel, CheckOnly: t.checkOnly, Scope: &scope,
		Compatibility: verdict{Status: "compatible", Reason: "Every discovered configuration passed its own paired comparison; configurations were not compared against each other."}, Cleanup: &verdict{Status: "passed", Reason: "Temporary sessions removed for all configurations."}}
	var models []reportModel
	var runErrors []error
	for _, entry := range scope.Commands {
		item := configurationResult{TestCommand: entry, Status: "not exercised", Compatibility: verdict{Status: "not exercised", Reason: "Configuration did not execute."}}
		words, parseErr := shellquote.Split(entry.Command)
		if filepath.Clean(entry.Directory) != "." {
			parseErr = fmt.Errorf("configuration in %s needs validation from that package directory; --all currently executes root-package commands only", entry.Directory)
		}
		if len(words) == 0 || strings.Contains(words[0], "=") {
			parseErr = fmt.Errorf("configuration command needs explicit environment/wrapper review: %s", entry.Command)
		}
		if len(entry.UnvalidatedSetup) > 0 {
			parseErr = fmt.Errorf("configuration requires separate CI setup; paired execution was not started: %s", strings.Join(entry.UnvalidatedSetup, "; "))
		}
		if ctx.Err() != nil {
			parseErr = ctx.Err()
		}
		if parseErr != nil {
			item.Error = reportText(parseErr.Error())
			result.Compatibility = verdict{Status: "inconclusive", Reason: "At least one discovered configuration did not execute; see configurations."}
			runErrors = append(runErrors, parseErr)
			result.Configurations = append(result.Configurations, item)
			continue
		}
		_, _ = fmt.Fprintf(output, "\nConfiguration %d/%d: %s\n", len(result.Configurations)+1, len(scope.Commands), entry.Command)

		label := fmt.Sprintf("configuration %d: %s", len(result.Configurations)+1, entry.Command)
		for _, key := range slices.Sorted(maps.Keys(entry.Environment)) {
			label += " (" + key + "=" + entry.Environment[key] + ")"
		}
		child := *t
		child.allConfigurations = false
		child.session = planSession()
		if result.Selection != nil && result.Selection.Version != "" {
			child.tracerVersion = result.Selection.Version
		}
		child.command = filepath.Join(t.repositoryRoot, "node_modules", ".bin", words[0])
		child.args = words[1:]
		child.reportModels = &models
		// Rebind method values to this command, rather than the original drive.
		child.preflight = child.checkJestPreflight
		if t.framework.Name() == "vitest" {
			child.preflight = child.checkVitestPreflight
		}
		before := len(models)
		execution, childErr := child.runPreparedConfiguration(ctx, output, &item)
		prepareValidationResult(t.repositoryRoot, &execution)
		item.Status = configurationStatus(execution)
		if execution.Preflight == nil && childErr != nil {
			item.Status = "blocked"
		}
		item.Compatibility = execution.Compatibility
		item.Preflight = execution.Preflight
		item.Error = execution.Error
		if childErr != nil && item.Error == "" {
			item.Error = reportText(childErr.Error())
		}
		result.Configurations = append(result.Configurations, item)
		if childErr != nil {
			runErrors = append(runErrors, fmt.Errorf("%s: %w", entry.Command, childErr))
		}
		if execution.Compatibility.Status != "compatible" {
			result.Compatibility = verdict{Status: "inconclusive", Reason: "At least one discovered configuration did not pass its paired comparison; see configurations."}
		}
		if result.Preflight == nil {
			result.Preflight = execution.Preflight
		}
		if result.Selection == nil {
			result.Selection = execution.Selection
			result.Tracer = execution.Tracer
			result.TracerSource = execution.TracerSource
		}
		if result.Tracer != execution.Tracer {
			runErrors = append(runErrors, fmt.Errorf("tracer selection changed between configurations; select an exact --tracer-version"))
		}
		result.CIRuntime = execution.CIRuntime
		if result.CISelection == nil || execution.CISelection != nil && execution.CISelection.Status != "compatible" {
			result.CISelection = execution.CISelection
		}
		if execution.Cleanup != nil && execution.Cleanup.Status != "passed" {
			result.Cleanup = execution.Cleanup
		}
		for _, run := range execution.Runs {
			run.Configuration = label
			result.Runs = append(result.Runs, run)
		}
		for _, feature := range execution.Features {
			feature.Project = strings.TrimSuffix(label+" / "+feature.Project, " / ")
			result.Features = append(result.Features, feature)
		}
		if len(models) > before+1 {
			models[before] = models[len(models)-1]
			models = models[:before+1]
		}
		for i := before; i < len(models); i++ {
			labelConfigurationModel(&models[i], label)
		}
	}
	if t.checkOnly {
		result.Compatibility = verdict{Status: "not exercised", Reason: "Configuration checks only; run --all without --check-only for all paired executions and features."}
	}
	if len(models) > 0 {
		if err := writeConfigurationHTML(t.repositoryRoot, models); err != nil {
			runErrors = append(runErrors, err)
		} else {
			result.HTMLReportCurrent = true
		}
	}
	runErr := errors.Join(runErrors...)
	if runErr != nil {
		result.Error = runErr.Error()
	}
	return errors.Join(runErr, finishValidation(output, t.repositoryRoot, result))
}

func labelConfigurationModel(model *reportModel, command string) {
	for i := range model.Tests {
		model.Tests[i].Label = command + " › " + model.Tests[i].Label
	}
	for i := range model.Suites {
		model.Suites[i].Name = command + " › " + model.Suites[i].Name
	}
	for i := range model.Cards {
		model.Cards[i].Title = command + " — " + model.Cards[i].Title
	}
}

func writeConfigurationHTML(root string, models []reportModel) error {
	model := reportModel{NoTestEvents: true, Summary: fmt.Sprintf("%d configurations reported. See validation JSON for completeness, compatibility and feature verdicts.", len(models)), Artifacts: []reportArtifact{{Title: "Validation JSON", Href: validationFilename}}}
	var commands, outputs, failures []string
	for index, part := range models {
		if index == 0 {
			model.Runtime = reportRuntime{Framework: part.Runtime.Framework, Tracer: part.Runtime.Tracer}
			model.CoverageLevel = part.CoverageLevel
		} else if model.CoverageLevel != part.CoverageLevel {
			model.CoverageLevel = ""
		}
		model.NoTestEvents = model.NoTestEvents && part.NoTestEvents
		label := fmt.Sprintf("Configuration %d: %s", index+1, part.Runtime.Command)
		commands = append(commands, label)
		outputs = append(outputs, label+"\n"+part.Runtime.Output)
		if part.Runtime.Error != "" {
			failures = append(failures, label+": "+part.Runtime.Error)
		}
		// Suite links address positions in the combined test table. Clone the
		// rows because finding cards can share their source model's slices.
		offset := len(model.Tests)
		part.Suites = offsetSuiteTests(part.Suites, offset)
		part.Cards = slices.Clone(part.Cards)
		for i := range part.Cards {
			part.Cards[i].Suites = offsetSuiteTests(part.Cards[i].Suites, offset)
		}
		model.Cards = append(model.Cards, part.Cards...)
		model.Suites = append(model.Suites, part.Suites...)
		model.Tests = append(model.Tests, part.Tests...)
	}
	model.Runtime.Command = strings.Join(commands, "\n")
	model.Runtime.Output = strings.Join(outputs, "\n\n")
	model.Runtime.Error = strings.Join(failures, "\n")
	model.Facts = []reportFact{{Label: "Configurations reported", Value: fmt.Sprint(len(models))}, {Label: "Tests", Value: fmt.Sprint(len(model.Tests))}}
	var content bytes.Buffer
	if err := testdriveReport.Execute(&content, model); err != nil {
		return err
	}
	return writeValidation(htmlReportPath(root), content.Bytes())
}

func offsetSuiteTests(suites []reportSuite, offset int) []reportSuite {
	suites = slices.Clone(suites)
	for i := range suites {
		suites[i].Tests = slices.Clone(suites[i].Tests)
		for j := range suites[i].Tests {
			suites[i].Tests[j].TestIndex += offset
		}
	}
	return suites
}
