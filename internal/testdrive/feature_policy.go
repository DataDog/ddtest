package testdrive

import (
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
)

// A policy describes what must be exercised, never an assumed passing result.
// Browser adapters add their not-supported exceptions with their PRs.
type featurePolicy struct {
	Name   string
	Status string
	Reason string
}

type frameworkPolicy struct {
	Paired, CheckOnly, ProbeRecovery, Preflight, Configurations bool
	SourceCoverage, ProbeBodyCounts                             bool
	ConfigurationFiles                                          []string
	// Options whose value names a configuration file; nil means --config/-c.
	ConfigurationOptions []string
	Features             []featurePolicy
}

// configurationOptions returns the options whose value is a configuration
// file path, as opposed to inline settings.
func (p frameworkPolicy) configurationOptions() []string {
	if len(p.ConfigurationOptions) > 0 {
		return p.ConfigurationOptions
	}
	return []string{"--config", "-c"}
}

var frameworkFeaturePolicies = map[string]frameworkPolicy{
	"pytest": {SourceCoverage: true, ProbeBodyCounts: true, Paired: true, CheckOnly: true, Preflight: true, Configurations: true, ConfigurationFiles: []string{"pytest.ini", "pyproject.toml", "tox.ini", "setup.cfg"}, Features: []featurePolicy{
		{Name: "auto-retries", Status: "required"}, {Name: "early-flake-detection", Status: "required"}, {Name: "skipping", Status: "required"}, {Name: "quarantine", Status: "required"}, {Name: "disabled", Status: "required"}, {Name: "attempt-to-fix", Status: "required"},
	}},
	"webdriverio": {ProbeBodyCounts: true, Paired: true, CheckOnly: true, ProbeRecovery: true, Preflight: true, Configurations: true, Features: []featurePolicy{
		{Name: "auto-retries", Status: "required"}, {Name: "early-flake-detection", Status: "required"}, {Name: "skipping", Status: "not supported", Reason: webdriverIOSkippingReason}, {Name: "quarantine", Status: "required"}, {Name: "disabled", Status: "required"}, {Name: "attempt-to-fix", Status: "required"},
	}},
	"cucumber": {SourceCoverage: true, ProbeBodyCounts: true, Paired: true, CheckOnly: true, ProbeRecovery: true, Preflight: true, Configurations: true, ConfigurationFiles: []string{"cucumber.js", "cucumber.cjs", "cucumber.mjs", "cucumber.json", "cucumber.yaml", "cucumber.yml"}, Features: []featurePolicy{
		{Name: "auto-retries", Status: "required"}, {Name: "early-flake-detection", Status: "required"}, {Name: "skipping", Status: "required"}, {Name: "quarantine", Status: "required"}, {Name: "disabled", Status: "required"}, {Name: "attempt-to-fix", Status: "required"},
	}},
	"cypress": {SourceCoverage: true, ProbeBodyCounts: true, Paired: true, CheckOnly: true, ProbeRecovery: true, Preflight: true, Configurations: true,
		// Cypress --config/-c take inline settings such as retries=2.
		ConfigurationOptions: []string{"--config-file", "-C"}, Features: []featurePolicy{
			{Name: "auto-retries", Status: "required"}, {Name: "early-flake-detection", Status: "required"}, {Name: "skipping", Status: "required"}, {Name: "quarantine", Status: "required"}, {Name: "disabled", Status: "required"}, {Name: "attempt-to-fix", Status: "required"},
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
	return frameworkNamesWith(func(policy frameworkPolicy) bool { return policy.Paired })
}

// frameworkNamesWith lists the display names of frameworks whose policy
// matches, so user-facing lists follow the policy table.
func frameworkNamesWith(match func(frameworkPolicy) bool) string {
	var names []string
	for name, policy := range frameworkFeaturePolicies {
		if match(policy) {
			names = append(names, displayName(name))
		}
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// AutomaticSetupFrameworkNames lists validated frameworks that --apply edits.
func AutomaticSetupFrameworkNames() string {
	var names []string
	for name, policy := range frameworkFeaturePolicies {
		if policy.Paired && onboard.SupportsAutomaticSetup(name) {
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

// tracerFeatureLimitation explains a required feature the selected tracer is
// known not to apply for this framework setup, or returns "".
func tracerFeatureLimitation(result *validationResult, feature string) string {
	if result.Framework != "webdriverio" || result.Preflight == nil {
		return ""
	}
	return webdriverIOJasmineLimitation(result.Preflight.WebdriverIO, feature, result.Tracer)
}
