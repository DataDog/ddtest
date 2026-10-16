// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import "fmt"

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

func supportsValidationFramework(name string) bool {
	switch name {
	case "jest", "vitest", "playwright", "mocha", "cypress", "cucumber", "webdriverio":
		return true
	}
	return false
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
		baseline = baseline || run.Name == "baseline" && !run.Instrumented
		reporting = reporting || run.Name == "reporting-only" && run.Instrumented
	}
	return baseline && reporting
}

func hasValidationExecution(result validationResult) bool {
	if result.CheckOnly {
		return false
	}
	for _, run := range result.Runs {
		if run.Command != "" && run.ExitCode != nil && (run.Name == "baseline" || run.Name == "reporting-only") {
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
	if !hasValidationPair(result) || configurationStatus(result) != "passed" || !result.ChecksPassed || result.Cleanup == nil || result.Cleanup.Status != "passed" || !result.HTMLReportCurrent {
		return check
	}
	if result.LastExecution != nil && !result.LastExecution.Current {
		return check
	}
	projects := map[string]map[string]bool{}
	for _, feature := range result.Features {
		if projects[feature.Project] == nil {
			projects[feature.Project] = map[string]bool{}
		}
		projects[feature.Project][feature.Name] = featureSatisfied(result.Framework, feature)
	}
	if len(projects) == 0 {
		return check
	}
	for _, features := range projects {
		for _, name := range jestFeatures {
			if !features[name] {
				return check
			}
		}
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
		executed.Reason = fmt.Sprintf("%d/%d executed configurations passed their paired comparison and supported feature checks. CI configuration and unvalidated configurations have separate verdicts.", executed.Passed, executed.Configurations)
	}
	return coverage, executed
}
