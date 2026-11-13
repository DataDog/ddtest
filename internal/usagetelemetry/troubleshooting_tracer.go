// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package usagetelemetry

// TracerState describes observations at DDTest's local intake, not proof that
// every integration initialized or that the tracer consumed a response.
type TracerState struct {
	Initialization     string              `json:"initialization"`
	RequestedFeature   string              `json:"requested_feature,omitempty"`
	SettingsRequests   *int                `json:"settings_requests"`
	SkippableRequests  *int                `json:"skippable_requests"`
	KnownTestsRequests *int                `json:"known_tests_requests"`
	ManagementRequests *int                `json:"management_requests"`
	Capabilities       []FeatureCapability `json:"capabilities,omitempty"`
}

type FeatureCapability struct {
	Feature string `json:"feature"`
	State   string `json:"state"`
}

func (s *TracerState) sanitized() *TracerState {
	if s == nil {
		return nil
	}
	result := &TracerState{
		Initialization:   diagnosticEnum(s.Initialization, "unknown", "events_observed"),
		RequestedFeature: diagnosticEnum(s.RequestedFeature, "", "auto-retries", "early-flake-detection", "skipping", "quarantine", "disabled", "attempt-to-fix"),
		SettingsRequests: diagnosticOptionalCount(s.SettingsRequests), SkippableRequests: diagnosticOptionalCount(s.SkippableRequests),
		KnownTestsRequests: diagnosticOptionalCount(s.KnownTestsRequests), ManagementRequests: diagnosticOptionalCount(s.ManagementRequests),
	}
	for _, capability := range s.Capabilities[:min(len(s.Capabilities), 6)] {
		result.Capabilities = append(result.Capabilities, FeatureCapability{
			Feature: diagnosticEnum(capability.Feature, "auto-retries", "early-flake-detection", "skipping", "quarantine", "disabled", "attempt-to-fix"),
			State:   diagnosticEnum(capability.State, "unknown", "available", "unavailable", "mixed"),
		})
	}
	return result
}

func diagnosticVersions(versions []string) []string {
	var result []string
	for _, version := range versions[:min(len(versions), 8)] {
		result = append(result, diagnosticVersion(version))
	}
	return result
}

func diagnosticOptionalCount(value *int) *int {
	if value == nil {
		return nil
	}
	bounded := diagnosticCount(*value)
	return &bounded
}
