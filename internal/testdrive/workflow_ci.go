// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"

	"github.com/DataDog/ddtest/internal/onboard"
)

// checkCIStatements asks runtime questions before setup confirmations, before
// either coordinated or standalone validation starts execution.
func (a *workflowAttempt) checkCIStatements(ctx context.Context, t *Testdrive) error {
	if required := onboard.RequiredCIRuntimes(ctx, t.repositoryRoot, t.language, a.framework); required != nil {
		return a.askCIQuestion(t, onboard.CIRuntimeRequiredResult(required))
	}
	return a.checkCIConfirmations(t)
}

// Setup edits can introduce a confirmation, so the coordinator checks again
// after applying them and preparing the selected tracer.
func (a *workflowAttempt) checkCIConfirmations(t *Testdrive) error {
	if required := onboard.RequiredCIConfirmations(t.repositoryRoot, t.language, a.framework); required != nil {
		return a.askCIQuestion(t, onboard.CIConfirmationRequiredResult(required))
	}
	return nil
}

func (a *workflowAttempt) askCIQuestion(t *Testdrive, asked onboard.AgentResult) error {
	a.result.Location, a.result.RequiredInput, a.result.Instruction = asked.Location, asked.RequiredInput, asked.Instruction
	return a.fail(t, "needs_input", asked.Code, asked.Reason, asked.Instruction)
}

// ciPinnedTracer selects only a release shared by every instrumented CI job.
// Callers keep their own preparation policy after this common decision.
func (a *workflowAttempt) ciPinnedTracer(ctx context.Context, t *Testdrive) (string, error) {
	pin := ""
	for _, job := range t.lang().ciRuntimes(ctx, t.repositoryRoot, a.framework).Jobs {
		if candidate := t.lang().ciPinnedTracer(job); candidate != "" {
			if pin != "" && pin != candidate {
				a.result.Location = job.Workflow + " / " + job.Job
				a.result.RequiredInput = []string{"one intended CI tracer release, or separate explicitly scoped runs"}
				return "", a.fail(t, "needs_input", "multiple_ci_tracers", "CI selects different tracer releases.", "Preserve every configuration and align the intended tracer selection explicitly.")
			}
			pin = candidate
		}
	}
	return pin, nil
}
