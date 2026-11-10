// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/usagetelemetry"
)

const capabilityPrefix = "_dd.library_capabilities."

var featureCapabilities = map[string]string{
	"auto-retries":          "auto_test_retries",
	"early-flake-detection": "early_flake_detection",
	"skipping":              "test_impact_analysis",
	"quarantine":            "test_management.quarantine",
	"disabled":              "test_management.disable",
	"attempt-to-fix":        "test_management.attempt_to_fix",
}

func observedTracerState(facts intake.Facts) *usagetelemetry.TracerState {
	state := &usagetelemetry.TracerState{Initialization: "unknown"}
	if facts.RequestsObserved {
		state.SettingsRequests, state.SkippableRequests = &facts.SettingsRequests, &facts.SkippableRequests
		state.KnownTestsRequests, state.ManagementRequests = &facts.KnownTestsRequests, &facts.ManagementRequests
	}
	if facts.TestEventCount > 0 {
		state.Initialization = "events_observed"
	}
	for _, feature := range []string{"auto-retries", "early-flake-detection", "skipping", "quarantine", "disabled", "attempt-to-fix"} {
		capability := usagetelemetry.FeatureCapability{Feature: feature, State: "unknown"}
		advertised := false
		for _, event := range facts.Events {
			if event.Type != "test" {
				continue
			}
			value, ok := event.Tags[capabilityPrefix+featureCapabilities[feature]]
			if !ok {
				continue
			}
			advertised = true
			version, err := strconv.Atoi(value)
			if err != nil || version < 0 {
				continue
			}
			observed := "available"
			if version == 0 {
				observed = "unavailable"
			}
			if capability.State == "unknown" {
				capability.State = observed
			} else if capability.State != observed {
				capability.State = "mixed"
			}
		}
		if advertised {
			state.Capabilities = append(state.Capabilities, capability)
		}
	}
	return state
}

// unsupportedTracerFeature uses the probe's own controls, never capabilities
// from another framework or module in the build. Both controls must advertise
// capabilities: older tracers without these tags still run every probe.
func unsupportedTracerFeature(framework, feature string, controls ...validationRun) (featureResult, bool) {
	capability, known := featureCapabilities[feature]
	if !known || len(controls) == 0 {
		return featureResult{}, false
	}
	key := capabilityPrefix + capability
	label := displayName(framework)
	for _, control := range controls {
		// Planner skipping is provided by ddtest on this execution path.
		if feature == "skipping" && control.ExecutionPath == vitestNodeAPIPath {
			return featureResult{}, false
		}
		if len(control.Facts.Tests) != 1 {
			return featureResult{}, false
		}
		identity := control.Facts.Tests[0]
		advertised := false
		for _, event := range control.Facts.Events {
			tags := event.Tags
			if event.Type != "test" || tags["test.module"] != identity.Module || tags["test.suite"] != identity.Suite || tags["test.name"] != identity.Name {
				continue
			}
			if value, exists := tags[key]; exists {
				version, err := strconv.Atoi(value)
				if err != nil || version != 0 {
					return featureResult{}, false
				}
			}
			for tag := range tags {
				advertised = advertised || strings.HasPrefix(tag, capabilityPrefix)
			}
			label = displayName(framework)
			if name := tags["test.framework"]; name != "" {
				label = displayName(name)
			}
			if version := tags["test.framework_version"]; version != "" {
				label += " " + version
			}
		}
		if !advertised {
			return featureResult{}, false
		}
	}
	return featureResult{Name: feature, Status: "not supported", Code: tracerFeatureUnsupported,
		Evidence: &usagetelemetry.FeatureEvidence{FailureCategory: "capability_not_advertised"},
		Reason:   fmt.Sprintf("%s does not advertise %s for %s (%s is absent or disabled). This feature is not exercised and is not counted as a passed check.", languageForFramework(framework).tracerPackage(), strings.ReplaceAll(feature, "-", " "), label, key)}, true
}
