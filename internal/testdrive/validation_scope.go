package testdrive

import "fmt"

// A selected local command validates its own execution. CI inventory remains
// advisory; --all, check-only and the onboarding coordinator keep CI gates.
func (result validationResult) focusedLocalCommand() bool {
	return result.FocusedCommand && !result.CheckOnly && result.Workflow == nil
}

func (result validationResult) ciPreservationBlocking() bool {
	// Unknown CI inventory is advisory for local execution. Keep the saved
	// baseline so actual matrix removal still blocks when CI can be compared.
	return result.CIPreservation != nil && result.CIPreservation.Status != "passed" &&
		(!result.focusedLocalCommand() || result.CIPreservation.Status != "inconclusive")
}

func hasValidationPair(result validationResult) bool {
	if result.CheckOnly {
		return false
	}
	baseline, reporting := false, false
	for _, run := range result.Runs {
		if run.Command == "" || run.ExitCode == nil {
			continue
		}
		baseline = baseline || run.Role == roleBaseline && !run.Instrumented
		reporting = reporting || run.Role == roleReporting && run.Instrumented
	}
	return baseline && reporting
}

type configurationCoverage struct {
	Discovered   int `json:"discovered"`
	Excluded     int `json:"excluded"`
	RemoteOnly   int `json:"remote_only,omitempty"`
	Executed     int `json:"executed"`
	Paired       int `json:"paired"`
	Validated    int `json:"validated"`
	Blocked      int `json:"blocked"`
	NotExercised int `json:"not_exercised"`
	Incomplete   int `json:"incomplete"`
}

type executedValidation struct {
	Status         string `json:"status"`
	Configurations int    `json:"configurations"`
	Passed         int    `json:"passed"`
	Reason         string `json:"reason"`
}

func hasValidationExecution(result validationResult) bool {
	if result.CheckOnly {
		return false
	}
	for _, run := range result.Runs {
		if run.Command != "" && run.ExitCode != nil && (run.Role == roleBaseline || run.Role == roleReporting) {
			return true
		}
	}
	return false
}

// A focused pass certifies only the requested command. Repository completeness
// and every unexecuted configuration retain their independent verdicts.
func selectedValidation(result validationResult) verdict {
	check := verdict{Status: "INCOMPLETE", Reason: "The selected command needs a current paired comparison, every supported feature, applicable configuration checks and cleanup. Other configurations remain unvalidated."}
	if result.CheckOnly {
		check.Status = "NOT EXERCISED"
		return check
	}
	if !validationPassed(result) {
		return check
	}
	if result.LastExecution != nil && !result.LastExecution.Current {
		return check
	}
	check.Status = "PASSED"
	check.Reason = "The selected command's paired comparison, all supported features, applicable configuration checks and cleanup passed. Other configurations and actual CI execution remain unvalidated."
	for _, policy := range featurePolicies(result.Framework) {
		if policy.Status != "required" {
			check.Reason += " Policy exception: " + policy.Name + " (" + policy.Status + ")."
		}
	}
	return check
}

func summarizeConfigurationCoverage(result validationResult) (*configurationCoverage, *executedValidation) {
	if !supportsValidationFramework(result.Framework) {
		return nil, nil
	}
	coverage := &configurationCoverage{Discovered: len(result.Configurations)}
	// Check-only lists discovered commands without per-configuration results;
	// count them the same way a full run does.
	if len(result.Configurations) == 0 && result.Scope != nil {
		coverage.Discovered = len(result.Scope.Commands)
		for _, command := range result.Scope.Commands {
			if ciInstrumentationDisabled(command) {
				coverage.Excluded++
			} else {
				coverage.NotExercised++
			}
		}
	}
	executed := &executedValidation{Status: "NOT EXERCISED", Reason: "No current paired execution was completed. Earlier results do not validate this invocation."}
	for _, configuration := range result.Configurations {
		if configuration.PairedExecution {
			coverage.Paired++
		}
		if configuration.ExecutionAttempted {
			coverage.Executed++
			executed.Configurations++
			if configuration.Status == "passed" && configuration.PairedExecution {
				executed.Passed++
			}
		}
		if configurationExcluded(configuration) {
			coverage.Excluded++
			continue
		}
		if configuration.RemoteGrid != "" {
			coverage.RemoteOnly++
			continue
		}
		switch configuration.Status {
		case "passed":
			coverage.Validated++
		case "blocked":
			coverage.Blocked++
		case "not exercised":
			coverage.NotExercised++
		default:
			coverage.Incomplete++
		}
	}
	// A custom command need not match a discovered CI alias. Its actual pair is
	// still reported, without counting unrelated aliases as validated.
	if executed.Configurations == 0 && hasValidationExecution(result) {
		executed.Configurations = 1
		if hasValidationPair(result) && configurationStatus(result) == "passed" {
			executed.Passed = 1
		}
	}
	if result.CheckOnly || result.LastExecution != nil && !result.LastExecution.Current {
		executed.Configurations, executed.Passed = 0, 0
		return coverage, executed
	}
	if executed.Configurations > 0 {
		executed.Status = "INCOMPLETE"
		if executed.Passed == executed.Configurations {
			executed.Status = "PASSED"
		}
		executed.Reason = fmt.Sprintf("%d/%d executed configurations passed paired comparison, supported features, applicable configuration checks, cleanup and report publication. Unvalidated configurations and actual CI execution remain separate.", executed.Passed, executed.Configurations)
	}
	return coverage, executed
}

// remoteOnlyCount names configurations that run only on a remote browser grid.
// validatedText counts validated configurations against those local
// validation must cover. CI opt-outs (configurations excluded from
// instrumentation) are not unvalidated work; they are labeled separately.
// Execution and pairing read as one fact when they agree.
func (coverage *configurationCoverage) validatedText() string {
	text := fmt.Sprintf("%d/%d validated", coverage.Validated, coverage.Discovered-coverage.Excluded)
	if coverage.Excluded > 0 {
		text += fmt.Sprintf(" (%d more excluded from instrumentation in CI)", coverage.Excluded)
	}
	if coverage.Paired == coverage.Executed {
		return text + fmt.Sprintf("; %d executed with paired comparison", coverage.Executed)
	}
	return text + fmt.Sprintf("; %d executed, %d with paired comparison", coverage.Executed, coverage.Paired)
}

func remoteOnlyCount(coverage *configurationCoverage) string {
	if coverage.RemoteOnly == 0 {
		return ""
	}
	return fmt.Sprintf(", %d remote grid only (CI runs them)", coverage.RemoteOnly)
}
