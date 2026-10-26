// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import "fmt"

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

// runtimeSubstitution records validation that ran on a compatible local Node
// in place of the major a CI path uses. The evidence is real, but it is not the
// CI runtime, so completion needs explicit acceptance.
type runtimeSubstitution struct {
	Source   string `json:"source"`
	CI       string `json:"ci_node"`
	Local    string `json:"local_node"`
	Reason   string `json:"reason"`
	Accepted bool   `json:"accepted"`
}

func (s *runtimeSubstitution) blocker() string {
	return fmt.Sprintf("runtime fidelity: validated on substitute Node %s; %s runs on Node %s. Accept this evidence with %s, or put an existing Node %s first on PATH and rerun", s.Local, s.Source, s.CI, AcceptSubstituteRuntimeFlag, s.CI)
}
