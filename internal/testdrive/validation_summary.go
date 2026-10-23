package testdrive

import (
	"fmt"
	"slices"
	"strings"
)

type validationSummary struct {
	ConfigurationCoverage *configurationCoverage `json:"configuration_coverage,omitempty"`
	ExecutedValidation    *executedValidation    `json:"executed_validation,omitempty"`
	SelectedValidation    *verdict               `json:"selected_validation,omitempty"`
	Status                string                 `json:"status"`
	LocalValidation       string                 `json:"local_validation"`
	CIConfiguration       string                 `json:"ci_configuration"`
	BlockingChecks        []string               `json:"blocking_checks"`
	// Disclosed limits of the local evidence; they do not block success.
	NotExercised []string `json:"not_exercised,omitempty"`
}

func summarizeValidation(result validationResult) validationSummary {
	summary := validationSummary{Status: "INCOMPLETE", LocalValidation: "INCOMPLETE", CIConfiguration: "not exercised"}
	if result.LocalSuccess {
		summary.LocalValidation = "PASSED"
	}
	if result.CheckOnly {
		summary.LocalValidation = "NOT EXERCISED"
	}
	if result.CheckOnly && result.Retained != nil && result.Retained.Result != nil {
		summary.LocalValidation = strings.ToUpper(executionStatus(result.Retained.Result)) + " EARLIER; NOT RERUN"
	}
	if result.Compatibility.Status == "suspected regression" && !result.CheckOnly {
		summary.Status, summary.LocalValidation = "FAILED", "FAILED"
	}
	if result.CIRuntime != nil {
		summary.CIConfiguration = result.CIRuntime.Status
	}
	if result.Success {
		summary.Status = "PASSED"
	}
	if result.Error != "" {
		summary.BlockingChecks = append(summary.BlockingChecks, result.Error)
	}
	for _, run := range result.Runs {
		for _, reason := range run.NotExercised {
			if !slices.Contains(summary.NotExercised, reason) {
				summary.NotExercised = append(summary.NotExercised, reason)
			}
		}
	}
	if result.CheckOnly {
		summary.BlockingChecks = append(summary.BlockingChecks, "configuration checks only; paired execution and features were not rerun")
	} else {
		if supportsValidationFramework(result.Framework) && !hasValidationPair(result) {
			summary.BlockingChecks = append(summary.BlockingChecks, "current baseline and reporting-only execution evidence is missing")
		}
		// A hang explains the missing results, coverage and probes; say it once.
		timeouts := timedOutRuns(result)
		summary.BlockingChecks = append(summary.BlockingChecks, timeouts...)
		if len(timeouts) == 0 && missingRealCoverage(result) {
			summary.BlockingChecks = append(summary.BlockingChecks, displayName(result.Framework)+" real-suite coverage was not observed: "+missingRealCoverageReason(result)+" Synthetic skipping probes do not validate coverage of customer source; preserve coverage thresholds.")
		}
		if result.Compatibility.Status != "" && result.Compatibility.Status != "compatible" {
			summary.BlockingChecks = append(summary.BlockingChecks, "compatibility: "+result.Compatibility.Status)
		}
		for _, feature := range result.Features {
			if !featureSatisfied(result.Framework, feature) && !slices.ContainsFunc(timeouts, func(timeout string) bool { return strings.HasPrefix(feature.Reason, timeout) }) {
				summary.BlockingChecks = append(summary.BlockingChecks, fmt.Sprintf("%s/%s: %s", feature.Project, feature.Name, feature.Status))
			}
		}
		if !featureChecksPassed(result.Framework, result.Features) {
			summary.BlockingChecks = append(summary.BlockingChecks, "not every project has every required feature result")
		}
		if result.Cleanup == nil {
			summary.BlockingChecks = append(summary.BlockingChecks, "owned cleanup was not exercised")
		} else if result.Cleanup.Status != "passed" {
			summary.BlockingChecks = append(summary.BlockingChecks, "owned cleanup did not pass")
		}
		// A run that stopped before any test command has no report to publish.
		if !result.HTMLReportCurrent && hasValidationExecution(result) {
			summary.BlockingChecks = append(summary.BlockingChecks, "no current HTML findings report")
		}
	}
	if result.CISelection != nil && result.CISelection.Status != "compatible" {
		summary.BlockingChecks = append(summary.BlockingChecks, "CI tracer agreement: "+result.CISelection.Status)
	}
	if result.CIRuntime != nil && result.CIRuntime.Status != "compatible" && result.CIRuntime.Status != "not applicable" {
		summary.BlockingChecks = append(summary.BlockingChecks, "CI runtime: "+result.CIRuntime.Status)
	}
	if result.Scope != nil {
		for _, reason := range result.Scope.Unresolved {
			summary.BlockingChecks = append(summary.BlockingChecks, "unresolved CI scope: "+reason)
		}
		for _, configuration := range result.Configurations {
			if configuration.Status != "passed" {
				summary.BlockingChecks = append(summary.BlockingChecks, "configuration "+configuration.Command+": "+configuration.Status)
			}
		}
	}
	// Success is scoped to an explicitly selected command; the overall summary
	// continues to expose every unvalidated repository configuration.
	if result.Scope != nil && (len(result.Scope.Unresolved) > 0 || slices.ContainsFunc(result.Configurations, func(c configurationResult) bool { return c.Status != "passed" })) && summary.Status != "FAILED" {
		summary.Status = "INCOMPLETE"
	}
	summary.ConfigurationCoverage, summary.ExecutedValidation = summarizeConfigurationCoverage(result)
	if result.FocusedCommand {
		check := selectedValidation(result)
		summary.SelectedValidation = &check
	}
	return summary
}
