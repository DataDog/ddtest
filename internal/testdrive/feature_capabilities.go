// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"fmt"
	"strconv"
	"strings"
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
		Reason: fmt.Sprintf("%s does not advertise %s for %s (%s is absent or disabled). This feature is not exercised and is not counted as a passed check.", languageForFramework(framework).tracerPackage(), strings.ReplaceAll(feature, "-", " "), label, key)}, true
}
