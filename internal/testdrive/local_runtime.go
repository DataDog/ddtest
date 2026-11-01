// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"cmp"
	"fmt"
	"slices"
)

// localRuntimeError reports that a CI path runs on a Node major this host does
// not provide, so the path cannot be validated here. ddtest never installs a
// runtime; validation resumes once the user supplies the named one.
type localRuntimeError struct {
	Source, Required, Local string
}

func (e *localRuntimeError) Error() string {
	return fmt.Sprintf("%s runs on Node %s; local Node is %s, so this path cannot be validated here. Put an existing Node %s executable first on PATH (a version manager or a user-provided install), then rerun the same command. Ask the user before downloading a runtime, never install one inside the repository, and keep CI, the tracer and the test scope unchanged.", e.Source, e.Required, e.Local, e.Required)
}

// Capability names the runtime a host needs to provide.
func (e *localRuntimeError) Capability() string {
	return "Node " + e.Required + " runtime first on PATH"
}

// AcceptSubstituteRuntimeFlag opts in to completing validation on a compatible
// local runtime that differs from the one CI uses.
const AcceptSubstituteRuntimeFlag = "--accept-substitute-runtime"

// runtimeSubstitution records validation on a compatible local runtime in
// place of the version a CI or framework path uses. Completion of that path
// needs explicit acceptance; CI-only differences are advisory for local commands.
type runtimeSubstitution struct {
	Source string `json:"source"`
	// CI and Local are runtime versions; the names predate other runtimes.
	CI       string `json:"ci_node"`
	Local    string `json:"local_node"`
	Reason   string `json:"reason"`
	Accepted bool   `json:"accepted"`
	// CIOnly compares against CI's runtime rather than a runtime required by
	// the selected framework's execution path.
	CIOnly bool `json:"ci_only,omitempty"`
	// Runtime names the runtime, and Remedy how to provide CI's version instead
	// of the substitute; empty means Node on PATH.
	Runtime string `json:"runtime,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
}

func (s *runtimeSubstitution) name() string { return cmp.Or(s.Runtime, "Node") }

// remedy is how to validate on CI's runtime instead.
func (s *runtimeSubstitution) remedy() string {
	return cmp.Or(s.Remedy, "put an existing Node "+s.CI+" first on PATH")
}

// caveat describes accepted evidence from the substitute.
func (s *runtimeSubstitution) caveat() string {
	return fmt.Sprintf("runtime fidelity: validated on substitute %s %s; %s runs on %s %s (accepted).", s.name(), s.Local, s.Source, s.name(), s.CI)
}

// substitutions lists every distinct substitute runtime the run validated on:
// --all keeps one per CI runtime its configurations use.
func (r *validationResult) substitutions() []runtimeSubstitution {
	if len(r.RuntimeSubstitutions) > 0 {
		return r.RuntimeSubstitutions
	}
	if r.RuntimeSubstitution != nil {
		return []runtimeSubstitution{*r.RuntimeSubstitution}
	}
	return nil
}

// addSubstitution records a substitution unless an equal one is recorded.
func (r *validationResult) addSubstitution(sub runtimeSubstitution) {
	if r.RuntimeSubstitution == nil {
		r.RuntimeSubstitution = &sub
	}
	if !slices.ContainsFunc(r.RuntimeSubstitutions, func(existing runtimeSubstitution) bool {
		return existing.Source == sub.Source && existing.CI == sub.CI && existing.Local == sub.Local
	}) {
		r.RuntimeSubstitutions = append(r.RuntimeSubstitutions, sub)
	}
}

func (s *runtimeSubstitution) blocker() string {
	return fmt.Sprintf("runtime fidelity: validated on substitute %s %s; %s runs on %s %s. Accept this evidence with %s, or %s and rerun", s.name(), s.Local, s.Source, s.name(), s.CI, AcceptSubstituteRuntimeFlag, s.remedy())
}
