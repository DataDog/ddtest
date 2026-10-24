package testdrive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/kballard/go-shellquote"
)

type configurationResult struct {
	onboard.TestCommand
	PairedExecution        bool               `json:"paired_execution"`
	ExecutionAttempted     bool               `json:"execution_attempted"`
	EnvironmentDifferences []string           `json:"environment_differences,omitempty"`
	Status                 string             `json:"status"`
	Compatibility          verdict            `json:"compatibility"`
	Preflight              *jestPreflight     `json:"preflight,omitempty"`
	Error                  string             `json:"error,omitempty"`
	Preparation            []buildResult      `json:"preparation,omitempty"`
	ServiceExecutions      []serviceExecution `json:"service_executions,omitempty"`
}

func setSingleConfigurationScope(root string, result *validationResult, scope onboard.ValidationScope, executionCommand string, executionEnvironment map[string]string) {
	setSelectedConfigurationScope(result, scope, onboard.ValidationCommands(root, executionCommand, result.Framework), executionEnvironment)
}

// setSelectedConfigurationScope credits the executed result only to discovered
// configurations with the same resolved plan as one of the selected entries.
func setSelectedConfigurationScope(result *validationResult, scope onboard.ValidationScope, selected []onboard.TestCommand, executionEnvironment map[string]string) {
	result.Scope = &scope
	for _, entry := range scope.Commands {
		item := configurationResult{TestCommand: entry, Status: "not exercised", Compatibility: verdict{Status: "not exercised", Reason: "Run testdrive --all after required setup to validate every discovered configuration."}}
		if ciInstrumentationDisabled(entry) {
			result.Configurations = append(result.Configurations, excludedConfiguration(entry))
			continue
		}
		if !hostConfiguration(entry) {
			item.Compatibility.Reason = nonHostConfigurationReason(entry)
			result.Configurations = append(result.Configurations, item)
			continue
		}
		for _, command := range selected {
			if sameConfiguration(command, entry) && len(entry.UnvalidatedSetup) == 0 {
				for key, required := range entry.Environment {
					actual, explicit := executionEnvironment[key]
					if !explicit {
						actual, explicit = command.Environment[key]
					}
					if !explicit {
						actual = os.Getenv(key)
					}
					if key == "NODE_OPTIONS" {
						actual = stripDatadogNodeOptions(actual)
					}
					if actual != required {
						item.EnvironmentDifferences = append(item.EnvironmentDifferences, key)
					}
				}
				if len(item.EnvironmentDifferences) > 0 {
					slices.Sort(item.EnvironmentDifferences)
					item.Compatibility.Reason = "CI environment differs for: " + strings.Join(item.EnvironmentDifferences, ", ") + "; run --all to validate those values."
					continue
				}
				item.Compatibility = result.Compatibility
				item.Preflight = result.Preflight
				item.Error = result.Error
				item.Status = configurationStatus(*result)
				item.PairedExecution = hasValidationPair(*result)
				item.ExecutionAttempted = hasValidationExecution(*result)
			}
		}
		result.Configurations = append(result.Configurations, item)
	}
}

// The same current-evidence predicate is used by a child, a focused command,
// and final publication. Scope completeness is applied separately.
func validationPassed(result validationResult) bool {
	return !missingRealCoverage(result) && result.Error == "" && result.Compatibility.Status == "compatible" && hasValidationPair(result) && featureChecksPassed(result.Framework, result.Features) && result.ChecksPassed && result.Cleanup != nil && result.Cleanup.Status == "passed" && result.HTMLReportCurrent
}
func configurationStatus(result validationResult) string {
	if result.CheckOnly {
		return "not exercised"
	}
	if !validationPassed(result) {
		return "incomplete"
	}
	return "passed"
}

// Managed startup, readiness and the action API are part of the plan, so a
// bare command never stands in for a configuration that also needs them.
func sameConfiguration(a, b onboard.TestCommand) bool {
	return a.Command == b.Command && a.Directory == b.Directory && slices.EqualFunc(a.Prerequisites, b.Prerequisites, onboard.EqualPreparation) &&
		reflect.DeepEqual(a.Cypress, b.Cypress) && (a.Platform == "" || b.Platform == "" || a.Platform == b.Platform)
}

// hostConfiguration reports whether a configuration's CI runner OS can be
// executed on this host; an unknown runner is assumed to match.
func hostConfiguration(entry onboard.TestCommand) bool {
	return entry.Platform == "" || entry.Platform == runtime.GOOS
}

func nonHostConfigurationReason(entry onboard.TestCommand) string {
	return entry.Platform + " Cypress configuration was not executed on this " + runtime.GOOS + " host. Use a matching runner; this variant remains unvalidated."
}

// Never guess between two CI variants with different environments.
func matchingConfigurationEnvironment(scope onboard.ValidationScope, scopeErr error, root, framework, command string) map[string]string {
	if scopeErr != nil {
		return nil
	}
	var matched bool
	var result map[string]string
	for _, selected := range onboard.ValidationCommands(root, command, framework) {
		for _, entry := range scope.Commands {
			if !sameConfiguration(selected, entry) || !hostConfiguration(entry) || len(entry.UnvalidatedSetup) > 0 {
				continue
			}
			candidate := mergeConfigurationEnvironment(entry.Environment, selected.Environment)
			if matched && !maps.Equal(result, candidate) {
				return nil
			}
			result = candidate
			matched = true
		}
	}
	return result
}

// A suspected regression in one configuration outranks configurations that
// could not be compared, so --all fails like a default or --command run.
func combineConfigurationCompatibility(current, next verdict) verdict {
	if current.Status == "suspected regression" {
		return current
	}
	return next
}

func applyScopeVerdict(result *validationResult) {
	if result.Scope == nil {
		return
	}
	if result.FocusedCommand {
		return
	}
	if len(result.Scope.Unresolved) > 0 {
		result.Success = false
		result.ChecksPassed = false
	}
	for _, configuration := range result.Configurations {
		if configuration.Status != "passed" && !configurationExcluded(configuration) {
			result.Success = false
		}
	}
}

// All evidence comes from this invocation. No earlier success is reused after
// source, configuration, runtime or tracer edits. Each configuration has its own
// paired comparison and feature controls, and shares only the final report pair.
func (t *Testdrive) runAllConfigurations(ctx context.Context, output io.Writer) error {
	var scope onboard.ValidationScope
	var err error
	if t.workflowScope != nil {
		scope = *t.workflowScope
	} else {
		scope, err = onboard.DiscoverValidationScope(t.repositoryRoot, t.framework.Name())
	}
	if err == nil && len(scope.Commands) == 0 {
		err = fmt.Errorf("no explicit %s CI invocations discovered; review discovery and use --command for local validation", t.framework.Name())
	}
	if err != nil {
		result := validationResult{Session: planSession().ID(), Framework: t.framework.Name(), Tracer: t.tracerLabel, CheckOnly: t.checkOnly, Scope: &scope, Error: err.Error(), Compatibility: verdict{Status: "not exercised", Reason: "Discovery failed; no test commands were executed."}}
		return errors.Join(err, finishValidation(output, t.repositoryRoot, result))
	}
	result := validationResult{Session: planSession().ID(), Framework: t.framework.Name(), Tracer: "not selected", CheckOnly: t.checkOnly, Scope: &scope,
		Compatibility: verdict{Status: "compatible", Reason: "Every discovered configuration passed its own paired comparison; configurations were not compared against each other."}, Cleanup: &verdict{Status: "passed", Reason: "Temporary sessions removed for all configurations."}}
	var models []reportModel
	var runErrors []error
	for _, entry := range scope.Commands {
		item := configurationResult{TestCommand: entry, Status: "not exercised", Compatibility: verdict{Status: "not exercised", Reason: "Configuration did not execute."}}
		if ciInstrumentationDisabled(entry) {
			result.Configurations = append(result.Configurations, excludedConfiguration(entry))
			continue
		}
		if !hostConfiguration(entry) {
			result.Compatibility = combineConfigurationCompatibility(result.Compatibility, verdict{Status: "inconclusive", Reason: "At least one discovered OS variant was not executed; see configurations."})
			item.Error = nonHostConfigurationReason(entry)
			result.Configurations = append(result.Configurations, item)
			continue
		}
		// Terminal lines, labels and errors never repeat a recording key.
		displayed, environment := entry.Command, entry.Environment
		if onboard.RedactsCommandKeys(t.framework.Name()) {
			displayed, environment = onboard.RedactCypressCommand(displayed), onboard.RedactCypressEnvironment(environment)
		}
		words, parseErr := shellquote.Split(entry.Command)
		if entry.Cypress != nil {
			words = []string{"node"}
			parseErr = nil
		}
		if _, err := localSetupDirectory(t.repositoryRoot, entry.Directory); err != nil {
			parseErr = err
		}
		if len(words) == 0 || strings.Contains(words[0], "=") {
			parseErr = fmt.Errorf("configuration command needs explicit environment/wrapper review: %s", displayed)
		}
		if len(entry.UnvalidatedSetup) > 0 {
			parseErr = fmt.Errorf("configuration requires separate CI setup; paired execution was not started: %s", strings.Join(entry.UnvalidatedSetup, "; "))
		}
		if ctx.Err() != nil {
			parseErr = ctx.Err()
		}
		if parseErr != nil {
			item.Error = reportText(parseErr.Error())
			item.Status = "blocked"
			result.Compatibility = combineConfigurationCompatibility(result.Compatibility, verdict{Status: "inconclusive", Reason: "At least one discovered configuration did not execute; see configurations."})
			runErrors = append(runErrors, fmt.Errorf("%s: %w", displayed, parseErr))
			result.Configurations = append(result.Configurations, item)
			continue
		}
		_, _ = fmt.Fprintf(output, "\nConfiguration %d/%d: %s\n", len(result.Configurations)+1, len(scope.Commands), displayed)

		label := fmt.Sprintf("configuration %d: %s", len(result.Configurations)+1, displayed)
		for _, key := range slices.Sorted(maps.Keys(environment)) {
			if environment[key] == "" {
				label += " (" + key + " cleared)"
			} else {
				label += " (" + key + "=" + environment[key] + ")"
			}
		}
		child := *t
		child.cypressSelected = entry
		child.cypress, child.cypressCommand, child.cypressPackage = entry.Cypress, entry.Command, entry.CypressPackage
		child.workflowRoot = t.repositoryRoot
		child.allConfigurations = false
		child.session = planSession()
		if result.Selection != nil && result.Selection.Source == "fallback" && result.Selection.Version != "" {
			child.tracerVersion = result.Selection.Version
		}
		child.command = words[0]
		child.args = words[1:]
		child.playwrightGuard = entry.ExitOnFailure
		child.reportModels = &models
		// Rebind method values to this command, rather than the original drive.
		child.preflight = child.frameworkPreflight()
		before := len(models)
		execution, childErr := child.runPreparedConfiguration(ctx, output, &item)
		prepareValidationResult(t.repositoryRoot, &execution)
		completeConfigurationItem(&item, execution, childErr)
		result.Configurations = append(result.Configurations, item)
		if childErr != nil {
			runErrors = append(runErrors, fmt.Errorf("%s: %w", displayed, childErr))
		}
		if execution.Compatibility.Status == "suspected regression" {
			result.Compatibility = verdict{Status: "suspected regression", Reason: "At least one discovered configuration differs between uninstrumented and reporting-only runs; see configurations."}
		} else if execution.Compatibility.Status != "compatible" {
			result.Compatibility = combineConfigurationCompatibility(result.Compatibility, verdict{Status: "inconclusive", Reason: "At least one discovered configuration did not pass its paired comparison; see configurations."})
		}
		if result.Preflight == nil {
			result.Preflight = execution.Preflight
		}
		if result.Selection == nil && execution.Selection != nil {
			result.Selection = execution.Selection
			result.Tracer = execution.Tracer
			result.TracerSource = execution.TracerSource
		}
		if execution.Tracer != "" && result.Tracer != execution.Tracer {
			runErrors = append(runErrors, fmt.Errorf("tracer selection changed between configurations; select an exact --tracer-version"))
		}
		if execution.CIRuntime != nil {
			result.CIRuntime = execution.CIRuntime
		}
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

		for i := before; i < len(models); i++ {
			labelConfigurationModel(&models[i], label)
		}
	}
	if t.checkOnly {
		result.Compatibility = verdict{Status: "not exercised", Reason: "Configuration checks only; run --all without --check-only for all paired executions and features."}
	}
	if len(models) > 0 {
		if err := writeConfigurationHTML(output, t.repositoryRoot, models); err != nil {
			runErrors = append(runErrors, err)
			for i := range result.Configurations {
				if result.Configurations[i].Status == "passed" {
					result.Configurations[i].Status = "incomplete"
					result.Configurations[i].Error = reportText(err.Error())
				}
			}
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

// Preflight and execution must resolve the same installed framework binary,
// without requiring the caller to add node_modules/.bin to PATH.
func (t *Testdrive) configurationExecutable(command string) string {
	if strings.ContainsAny(command, `/\`) ||
		slices.Contains([]string{"npm", "pnpm", "yarn", "node", "npx", "bun", "corepack", "ddtest"}, command) {
		return command
	}
	for directory := t.repositoryRoot; ; directory = filepath.Dir(directory) {
		candidate := filepath.Join(directory, "node_modules", ".bin", command)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		if t.configurationRoot == "" || directory == t.configurationRoot || directory == filepath.Dir(directory) {
			break
		}
	}
	return filepath.Join(t.repositoryRoot, "node_modules", ".bin", command)
}

func labelConfigurationModel(model *reportModel, command string) {
	model.Configuration = command
	if model.EvidenceName != "" {
		command += " / " + model.EvidenceName
	}
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

func writeConfigurationHTML(output io.Writer, root string, models []reportModel) error {
	model := reportModel{Aggregated: true, NoTestEvents: true, Summary: fmt.Sprintf("%d instrumented reporting snapshots retained, including repeated pairs. Aggregate execution counts include each primary reporting run once. See validation JSON for configuration completeness, compatibility and features.", len(models)), Artifacts: []reportArtifact{{Title: "Validation JSON", Href: validationFilename}}}
	var counts []*executionCounts
	var commands, outputs, failures []string
	for index, part := range models {
		if part.EvidenceRole == "" || part.EvidenceRole == roleReporting {
			counts = append(counts, part.ExecutionCounts)
		}
		if index == 0 {
			model.Runtime = reportRuntime{Framework: part.Runtime.Framework, Tracer: part.Runtime.Tracer}
			model.CoverageLevel = part.CoverageLevel
		} else if model.CoverageLevel != part.CoverageLevel {
			model.CoverageLevel = ""
		}
		model.NoTestEvents = model.NoTestEvents && part.NoTestEvents
		label := fmt.Sprintf("Reporting snapshot %d: %s / %s / %s", index+1, part.Configuration, part.EvidenceName, part.Runtime.Command)
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
	model.ExecutionCounts = combineExecutionCounts(counts)
	model.Facts = []reportFact{{Label: "Reporting snapshots retained", Value: fmt.Sprint(len(models))}, {Label: "Reported test executions", Value: fmt.Sprint(len(model.Tests)), Tooltip: sourceIdentityScope}}
	if model.ExecutionCounts != nil {
		model.Facts[1].Value = fmt.Sprint(model.ExecutionCounts.TestExecutions)
		identityFact := reportFact{Label: "Source-test identities", Value: "Unavailable", Tooltip: model.ExecutionCounts.Reason}
		if model.ExecutionCounts.SourceTestIdentities != nil {
			identityFact.Value, identityFact.Tooltip = fmt.Sprint(*model.ExecutionCounts.SourceTestIdentities), sourceIdentityScope
		}
		model.Facts = append(model.Facts, identityFact)
	}
	var content bytes.Buffer
	if err := testdriveReport.Execute(&content, model); err != nil {
		return err
	}
	if err := writeValidation(htmlReportPath(root), content.Bytes()); err != nil {
		return err
	}
	return writeHTMLReportLink(output, root)
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

func completeConfigurationItem(item *configurationResult, execution validationResult, childErr error) {
	item.PairedExecution = hasValidationPair(execution)
	item.ExecutionAttempted = hasValidationExecution(execution)
	item.Compatibility = execution.Compatibility
	item.Preflight = execution.Preflight
	item.Error = execution.Error
	if childErr != nil && item.Error == "" {
		item.Error = reportText(childErr.Error())
	}
	execution.Error = item.Error
	item.Status = configurationStatus(execution)
	if childErr != nil && execution.Preflight == nil {
		item.Status = "blocked"
	}
}

func ciInstrumentationDisabled(entry onboard.TestCommand) bool {
	return entry.CIInstrumentationExcluded || entry.Environment["DD_CIVISIBILITY_ENABLED"] == "false"
}

func excludedConfiguration(entry onboard.TestCommand) configurationResult {
	return configurationResult{TestCommand: entry, Status: "excluded", Compatibility: verdict{Status: "not exercised", Reason: "CI explicitly excludes Datadog instrumentation for this entry (disabled flags or a skipped conditional installer). Its original CI tests are preserved; no local paired execution or features are claimed."}}
}

func configurationExcluded(entry configurationResult) bool {
	return entry.Status == "excluded" && ciInstrumentationDisabled(entry.TestCommand)
}
