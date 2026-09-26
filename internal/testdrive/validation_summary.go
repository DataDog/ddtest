// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

type validationSummary struct {
	Status                  string   `json:"status"`
	LocalValidation         string   `json:"local_validation"`
	CIConfiguration         string   `json:"ci_configuration"`
	CIExecution             string   `json:"ci_execution"`
	RealCredentialsRequired bool     `json:"real_credentials_required"`
	BlockingChecks          []string `json:"blocking_checks,omitempty"`
}

func summarizeValidation(result validationResult) validationSummary {
	summary := validationSummary{Status: "INCOMPLETE", LocalValidation: "INCOMPLETE", CIConfiguration: "not checked", CIExecution: "not exercised"}
	if result.Success {
		summary.Status = "COMPLETE LOCALLY"
	}
	if result.LocalSuccess {
		summary.LocalValidation = "PASSED"
	}
	if result.CheckOnly {
		summary.LocalValidation = "NOT EXERCISED"
		summary.BlockingChecks = append(summary.BlockingChecks, "paired execution and features not exercised (--check-only)")
	}
	if result.Error != "" {
		summary.BlockingChecks = append(summary.BlockingChecks, "execution error: "+reportText(result.Error))
	}
	if result.Preflight != nil && result.Preflight.Verdict.Status != "compatible" {
		summary.BlockingChecks = append(summary.BlockingChecks, "preflight "+result.Preflight.Verdict.Status)
	}
	if !result.CheckOnly {
		if result.Compatibility.Status != "compatible" {
			summary.BlockingChecks = append(summary.BlockingChecks, "compatibility "+result.Compatibility.Status)
		}
		for _, feature := range result.Features {
			if feature.Status != "passed" {
				label := feature.Name
				if feature.Project != "" {
					label = feature.Project + "/" + label
				}
				summary.BlockingChecks = append(summary.BlockingChecks, label+" "+feature.Status)
			}
		}
	}
	if result.CIRuntime != nil {
		summary.CIConfiguration = result.CIRuntime.Status
		if result.CIRuntime.Status != "compatible" && result.CIRuntime.Status != "not applicable" {
			summary.BlockingChecks = append(summary.BlockingChecks, "CI runtime "+result.CIRuntime.Status)
		}
	}
	if result.CISelection != nil && result.CISelection.Status != "compatible" {
		summary.CIConfiguration = result.CISelection.Status
		summary.BlockingChecks = append(summary.BlockingChecks, "CI tracer agreement "+result.CISelection.Status)
	}
	return summary
}
