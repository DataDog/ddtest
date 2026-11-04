// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func capabilityControl(tags map[string]string) validationRun {
	return validationRun{Facts: intake.Facts{
		Tests:  []intake.Test{{Module: "module", Suite: "probe", Name: probeName}},
		Events: []intake.Event{{Type: "test", Tags: tags}},
	}}
}

func TestMissingCapabilitiesAreSharedAcrossLanguages(t *testing.T) {
	for _, framework := range []string{"testng", "jest", "rspec", "pytest"} {
		t.Run(framework, func(t *testing.T) {
			control := capabilityControl(map[string]string{
				"test.module": "module", "test.suite": "probe", "test.name": probeName,
				"test.framework": framework, "test.framework_version": "1.2.3",
				capabilityPrefix + "test_impact_analysis": "1",
			})
			for _, feature := range []string{"auto-retries", "early-flake-detection", "quarantine", "disabled", "attempt-to-fix"} {
				unsupported, ok := unsupportedTracerFeature(framework, feature, control, control)
				require.True(t, ok, feature)
				require.Equal(t, "not supported", unsupported.Status)
				require.Contains(t, unsupported.Reason, displayName(framework)+" 1.2.3")
				require.Contains(t, unsupported.Reason, languageForFramework(framework).tracerPackage())
				require.Contains(t, unsupported.Reason, capabilityPrefix+featureCapabilities[feature])
				require.True(t, featureSatisfied(framework, unsupported))
			}
			_, unsupported := unsupportedTracerFeature(framework, "skipping", control, control)
			require.False(t, unsupported, "advertised features still require a passing probe")
		})
	}
}

func TestMissingCapabilityNeedsTheProbeControlsOwnAdvertisement(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*validationRun)
	}{
		{"legacy tracer", func(c *validationRun) { delete(c.Facts.Events[0].Tags, capabilityPrefix+"test_impact_analysis") }},
		{"another module", func(c *validationRun) { c.Facts.Events[0].Tags["test.module"] = "other" }},
		{"another framework suite", func(c *validationRun) { c.Facts.Events[0].Tags["test.suite"] = "other" }},
		{"another test", func(c *validationRun) { c.Facts.Events[0].Tags["test.name"] = "other" }},
		{"suite event", func(c *validationRun) { c.Facts.Events[0].Type = "test_suite_end" }},
		{"advertised", func(c *validationRun) { c.Facts.Events[0].Tags[capabilityPrefix+"auto_test_retries"] = "1" }},
		{"malformed capability", func(c *validationRun) { c.Facts.Events[0].Tags[capabilityPrefix+"auto_test_retries"] = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			control := capabilityControl(map[string]string{
				"test.module": "module", "test.suite": "probe", "test.name": probeName,
				capabilityPrefix + "test_impact_analysis": "1",
			})
			missing := control
			// Keep the second control independent when changing its tags.
			missing.Facts.Events = []intake.Event{{Type: "test", Tags: map[string]string{
				"test.module": "module", "test.suite": "probe", "test.name": probeName,
				capabilityPrefix + "test_impact_analysis": "1",
			}}}
			tc.edit(&missing)
			_, unsupported := unsupportedTracerFeature("jest", "auto-retries", control, missing)
			require.False(t, unsupported, "unknown or conflicting support must run the probe")
		})
	}
}

func TestTracerCapabilityDoesNotReplacePlannerSkipping(t *testing.T) {
	control := capabilityControl(map[string]string{
		"test.module": "module", "test.suite": "probe", "test.name": probeName,
		capabilityPrefix + "auto_test_retries": "1",
	})
	control.ExecutionPath = vitestNodeAPIPath
	_, unsupported := unsupportedTracerFeature("vitest", "skipping", control, control)
	require.False(t, unsupported)
}

func TestTestNGValidationSkipsOnlyUnadvertisedFeatures(t *testing.T) {
	drive, build, session := javaSimulation(t)
	drive.framework = framework.NewJVM("testng", nil)
	requireWriteFile(t, filepath.Join(drive.repositoryRoot, "pom.xml"), `<project><artifactId>fixture</artifactId><dependencies><dependency><groupId>org.testng</groupId><artifactId>testng</artifactId><version>7.4.0</version></dependency></dependencies></project>`)
	build.probeTags = map[string]string{
		"test.framework": "testng", "test.framework_version": "7.4.0",
		capabilityPrefix + "test_impact_analysis":    "1",
		capabilityPrefix + "test_management.disable": "1",
	}
	result := &validationResult{Framework: "testng"}
	require.NoError(t, drive.runFrameworkValidation(t.Context(), io.Discard, session, "/agent/dd-java-agent.jar", result))
	require.Equal(t, "compatible", result.Compatibility.Status)
	require.Len(t, result.Features, 6)
	for _, feature := range result.Features {
		if feature.Name == "skipping" || feature.Name == "disabled" {
			require.Equal(t, "passed", feature.Status)
		} else {
			require.Equal(t, "not supported", feature.Status, feature.Name)
			require.Contains(t, feature.Reason, "TestNG 7.4.0")
		}
	}
	require.Len(t, build.calls, 6, "only the pair, both controls, skipping and disabled run")
	require.True(t, featureChecksPassed("testng", result.Features))
	passed, total, unsupported := validationFeatureCounts("testng", result.Features)
	require.Equal(t, 2, passed)
	require.Equal(t, 2, total)
	require.Len(t, unsupported, 4, "unsupported features are excluded from passed checks")
}

func TestUnadvertisedFeaturesDoNotBlockCoordinatorCompletion(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"name":"fixture","version":"1.0.0"}`)
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Jest{}, language: "javascript", tracerVersion: "latest", executor: workflowCheckExecutor{}, command: "jest", nodeVersion: func() string { return "v24.0.0" }}
	drive.workflowRun = func(_ context.Context, output io.Writer) error {
		result := completeValidationFixture("jest")
		result.Session = "current"
		result.Features[0] = featureResult{Name: "auto-retries", Status: "not supported", Code: tracerFeatureUnsupported, Reason: "Tracer does not advertise auto retries for this framework version."}
		return finishValidation(output, root, result)
	}
	var output bytes.Buffer
	require.NoError(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json"}))
	report := readValidationReport(t, root)
	require.Equal(t, "COMPLETE LOCALLY", report.Summary.Status)
	require.Empty(t, report.Summary.BlockingChecks)
	require.Contains(t, report.Summary.FinalResponse, "auto-retries not supported")
	require.NotContains(t, strings.Join(report.Summary.BlockingChecks, " "), "auto-retries")
}
