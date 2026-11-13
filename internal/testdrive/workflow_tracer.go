// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"errors"
	"fmt"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
)

func (t *Testdrive) selectWorkflowTracer(ctx context.Context, scope onboard.ValidationScope, explicit bool) (string, error) {
	ci := onboard.CheckCIRuntimes(ctx, t.repositoryRoot, t.framework.Name()).WithSiteCaveats(t.selectedSite)
	if t.workflowPlan != nil {
		t.workflowPlan.CIRuntime = &ci
	}
	var selected platform.JSSelection
	t.workflowReportOnly = nil
	for _, entry := range scope.Commands {
		// Entries partial apply leaves unedited are reported, never preflighted.
		if t.setupStaysReported(entry, true) {
			continue
		}
		// One tracer serves every configuration: later entries check the release
		// the first one selected instead of recommending their own.
		probe, pinned := t, explicit
		if selected.Version != "" {
			pinnedDrive := *t
			pinnedDrive.tracerVersion = selected.Version
			probe, pinned = &pinnedDrive, true
		}
		var candidate platform.JSSelection
		var reportOnly []string
		err := workflowPreflight(ctx, func() (err error) {
			candidate, reportOnly, err = probe.selectSetupTracerReporting(ctx, onboard.ValidationScope{Commands: []onboard.TestCommand{entry}}, pinned)
			return err
		})
		if err != nil {
			return selected.Version, err
		}
		t.workflowReportOnly = append(t.workflowReportOnly, reportOnly...)
		if candidate.Version == "" {
			continue
		}
		if selected.Version != "" && selected.Version != candidate.Version {
			return selected.Version, fmt.Errorf("tracer selection changed across configurations")
		}
		selected = candidate
		if t.workflowPlan != nil {
			t.workflowPlan.TracerNode = selected.Node
			t.workflowPlan.Tracer = selected.Version
		}
	}
	if selected.Version == "" {
		if len(t.workflowReportOnly) > 0 {
			return "", noRunnableConfiguration(t.workflowReportOnly)
		}
		return "", fmt.Errorf("no configurations enabled for instrumentation; explicit CI opt-outs are not validations")
	}
	return selected.Version, nil
}
func workflowMetadataCapability(metadata *platform.MetadataError) string {
	switch metadata.Code {
	case "EACCES", "EPERM", "EROFS", "ENOSPC":
		return "private temporary storage"
	}
	return "package metadata"
}
func workflowMetadataRetryable(code string) bool {
	switch code {
	case "EAI_AGAIN", "ENETUNREACH", "ECONNREFUSED", "ECONNRESET", "ETIMEDOUT", "FETCH_ERROR":
		return true
	}
	return false
}

// Only idempotent metadata preflight may retry, and only one recognized
// transient transport failure. Tests, auth and permission failures never retry.
func workflowPreflight(ctx context.Context, run func() error) error {
	err := run()
	var metadata *platform.MetadataError
	if errors.As(err, &metadata) && workflowMetadataRetryable(metadata.Code) && ctx.Err() == nil {
		return run()
	}
	return err
}
