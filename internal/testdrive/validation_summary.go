package testdrive

import (
	"fmt"
	"strings"
)

type validationSummary struct {
	Status          string   `json:"status"`
	LocalValidation string   `json:"local_validation"`
	CIConfiguration string   `json:"ci_configuration"`
	BlockingChecks  []string `json:"blocking_checks"`
}

func summarizeValidation(result validationResult) validationSummary {
	summary := validationSummary{Status: "INCOMPLETE", LocalValidation: "INCOMPLETE", CIConfiguration: "not exercised"}
	if result.LocalSuccess {
		summary.LocalValidation = "PASSED"
	}
	if result.CheckOnly {
		summary.LocalValidation = "NOT EXERCISED"
	}
	if result.Retained != nil && result.Retained.Result != nil {
		summary.LocalValidation = strings.ToUpper(executionStatus(result.Retained.Result)) + " EARLIER; NOT RERUN"
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
	if result.CheckOnly {
		summary.BlockingChecks = append(summary.BlockingChecks, "configuration checks only; paired execution and features were not rerun")
	} else {
		if result.Compatibility.Status != "compatible" {
			summary.BlockingChecks = append(summary.BlockingChecks, "compatibility: "+result.Compatibility.Status)
		}
		for _, feature := range result.Features {
			if !featureSatisfied(result.Framework, feature) {
				summary.BlockingChecks = append(summary.BlockingChecks, fmt.Sprintf("%s/%s: %s", feature.Project, feature.Name, feature.Status))
			}
		}
		if !featureChecksPassed(result.Framework, result.Features) {
			summary.BlockingChecks = append(summary.BlockingChecks, "not every project has every required feature result")
		}
		if result.Cleanup == nil || result.Cleanup.Status != "passed" {
			summary.BlockingChecks = append(summary.BlockingChecks, "owned cleanup did not pass")
		}
		if !result.HTMLReportCurrent {
			summary.BlockingChecks = append(summary.BlockingChecks, "no current HTML findings report")
		}
	}
	if result.CISelection != nil && result.CISelection.Status != "compatible" {
		summary.BlockingChecks = append(summary.BlockingChecks, "CI tracer agreement: "+result.CISelection.Status)
	}
	if result.CIRuntime != nil && result.CIRuntime.Status != "compatible" && result.CIRuntime.Status != "not applicable" {
		summary.BlockingChecks = append(summary.BlockingChecks, "CI runtime: "+result.CIRuntime.Status)
	}
	return summary
}
