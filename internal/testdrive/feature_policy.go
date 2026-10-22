package testdrive

import (
	"slices"
	"strings"
)

// A policy describes what must be exercised, never an assumed passing result.
// Browser adapters add their not-supported/deferred exceptions with their PRs.
type featurePolicy struct {
	Name   string
	Status string
	Reason string
}

type frameworkPolicy struct {
	Paired, CheckOnly, ProbeRecovery, Preflight, Configurations bool
	SourceCoverage, ProbeBodyCounts                             bool
	ConfigurationFiles                                          []string
	Features                                                    []featurePolicy
}

var frameworkFeaturePolicies = map[string]frameworkPolicy{
	"cypress": {ProbeBodyCounts: true, Paired: true, CheckOnly: true, ProbeRecovery: true, Preflight: true, Configurations: true, Features: []featurePolicy{
		{Name: "auto-retries", Status: "required"}, {Name: "early-flake-detection", Status: "required"}, {Name: "skipping", Status: "deferred", Reason: cypressTIADeferredReason}, {Name: "quarantine", Status: "required"}, {Name: "disabled", Status: "required"}, {Name: "attempt-to-fix", Status: "required"},
	}},
	"playwright": {Paired: true, CheckOnly: true, ProbeRecovery: true, Preflight: true, Configurations: true, Features: []featurePolicy{
		{Name: "auto-retries", Status: "required"},
		{Name: "early-flake-detection", Status: "required"},
		{Name: "skipping", Status: "not supported", Reason: playwrightSkippingReason},
		{Name: "quarantine", Status: "required"},
		{Name: "disabled", Status: "required"},
		{Name: "attempt-to-fix", Status: "required"},
	}},
	"jest": {SourceCoverage: true, Paired: true, CheckOnly: true, ProbeRecovery: true, Preflight: true, Configurations: true, ConfigurationFiles: []string{"jest.config.js", "jest.config.cjs", "jest.config.mjs", "jest.config.ts", "jest.config.cts", "jest.config.mts", "jest.config.json"}, Features: []featurePolicy{
		{Name: "auto-retries", Status: "required"},
		{Name: "early-flake-detection", Status: "required"},
		{Name: "skipping", Status: "required"},
		{Name: "quarantine", Status: "required"},
		{Name: "disabled", Status: "required"},
		{Name: "attempt-to-fix", Status: "required"},
	}},
	"vitest": {SourceCoverage: true, Paired: true, CheckOnly: true, ProbeRecovery: true, Preflight: true, Configurations: true, ConfigurationFiles: []string{"vitest.config.js", "vitest.config.ts", "vitest.config.mjs", "vitest.config.mts", "vitest.config.cjs", "vitest.config.cts", "vite.config.js", "vite.config.ts", "vite.config.mjs", "vite.config.mts", "vite.config.cjs", "vite.config.cts"}, Features: []featurePolicy{
		{Name: "auto-retries", Status: "required"},
		{Name: "early-flake-detection", Status: "required"},
		{Name: "skipping", Status: "required"},
		{Name: "quarantine", Status: "required"},
		{Name: "disabled", Status: "required"},
		{Name: "attempt-to-fix", Status: "required"},
	}},
	"mocha": {SourceCoverage: true, Paired: true, CheckOnly: true, ProbeRecovery: true, Preflight: true, Configurations: true, Features: []featurePolicy{
		{Name: "auto-retries", Status: "required"},
		{Name: "early-flake-detection", Status: "required"},
		{Name: "skipping", Status: "required"},
		{Name: "quarantine", Status: "required"},
		{Name: "disabled", Status: "required"},
		{Name: "attempt-to-fix", Status: "required"},
	}},
}

func supportsValidationFramework(name string) bool {
	return frameworkFeaturePolicies[name].Paired
}
func featureSatisfied(framework string, feature featureResult) bool {
	for _, policy := range featurePolicies(framework) {
		if policy.Name == feature.Name {
			if policy.Status == "required" {
				return feature.Status == "passed"
			}
			return feature.Status == policy.Status
		}
	}
	return false
}

func featureChecksPassed(framework string, features []featureResult) bool {
	if !supportsValidationFramework(framework) {
		return false
	}
	projects := map[string]map[string]bool{}
	for _, feature := range features {
		if !featureSatisfied(framework, feature) {
			return false
		}
		if projects[feature.Project] == nil {
			projects[feature.Project] = map[string]bool{}
		}
		projects[feature.Project][feature.Name] = true
	}
	if len(projects) == 0 {
		return false
	}
	for _, features := range projects {
		for _, policy := range featurePolicies(framework) {
			if !features[policy.Name] {
				return false
			}
		}
	}
	return true
}

func featurePolicies(framework string) []featurePolicy {
	return frameworkFeaturePolicies[framework].Features
}

func probeRecoveryEnabled() bool {
	for _, policy := range frameworkFeaturePolicies {
		if policy.ProbeRecovery {
			return true
		}
	}
	return false
}

// ValidationFrameworkNames keeps CLI guidance aligned with registered adapters.
func ValidationFrameworkNames() string {
	var names []string
	for name, policy := range frameworkFeaturePolicies {
		if policy.Paired {
			names = append(names, displayName(name))
		}
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

func skippingPolicy(framework string) featurePolicy {
	for _, policy := range featurePolicies(framework) {
		if policy.Name == "skipping" {
			return policy
		}
	}
	return featurePolicy{Status: "required"}
}
