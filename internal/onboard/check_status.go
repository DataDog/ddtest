// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

// These values are part of the versioned configuration-check report contract.
const (
	StatusCompatible                 string = "compatible"
	StatusIncompatible               string = "incompatible"
	StatusInconclusive               string = "inconclusive"
	StatusNotApplicable              string = "not applicable"
	StatusNotExercised               string = "not exercised"
	StatusExcluded                   string = "excluded"
	CodeMissingInstrumentation       string = "missing_instrumentation"
	CodeExcludedRuntime              string = "excluded_runtime"
	CodeUnsupportedNode              string = "unsupported_node"
	CodeUnverifiedExclusion          string = "unverified_exclusion"
	CodeUnverifiedInitialization     string = "unverified_initialization"
	CodeMissingInitialization        string = "missing_initialization"
	CodeRuntimeUnknown               string = "runtime_unknown"
	CodeNodeResolution               string = "node_resolution_failed"
	CodeTracerMetadata               string = "tracer_metadata_failed"
	CodeUnresolvedCommand            string = "unresolved_command"
	CodeUnresolvedCondition          string = "unresolved_condition"
	CodeInvalidMatrix                string = "invalid_matrix"
	CodeJestNotInstalled             string = "jest_not_installed"
	CodeUnverifiedTracerMajor        string = "unverified_tracer_major"
	CodeUnresolvedJestVersion        string = "unresolved_jest_version"
	CodeUnsupportedJestConfiguration string = "unsupported_jest_configuration"
	CodeNestedCIScope                string = "nested_ci_scope"
	// CodeUninstrumentedJob marks a CI command outside validation because its
	// job runs no Datadog action and no recognized step of the framework.
	CodeUninstrumentedJob string = "uninstrumented_job"
)
