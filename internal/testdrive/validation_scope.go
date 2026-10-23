package testdrive

import "fmt"

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
	check := verdict{Status: "INCOMPLETE", Reason: "The selected command needs a current paired comparison, every supported feature, static CI checks and cleanup. Other configurations remain unvalidated."}
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
	check.Reason = "The selected command's paired comparison, all supported features, static CI checks and cleanup passed. Other configurations and actual CI execution remain unvalidated."
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
		coverage.NotExercised = len(result.Scope.Commands)
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
		executed.Reason = fmt.Sprintf("%d/%d executed configurations passed paired comparison, supported features, static checks, cleanup and report publication. Unvalidated configurations and actual CI execution remain separate.", executed.Passed, executed.Configurations)
	}
	return coverage, executed
}
