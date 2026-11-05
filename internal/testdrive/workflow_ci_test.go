// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

type workflowCIInventory struct {
	validationLanguage
	jobs []onboard.RuntimeFinding
}

func (l workflowCIInventory) ciRuntimes(context.Context, string, string) onboard.RuntimeCheck {
	return onboard.RuntimeCheck{Jobs: l.jobs}
}

func TestWorkflowCITracerSelectionPreservesEveryJob(t *testing.T) {
	for _, language := range []validationLanguage{javascriptLanguage{}, pythonLanguage{}} {
		for _, tc := range []struct {
			name string
			pins []string
			want string
		}{
			{name: "no pinned setup"},
			{name: "one shared release", pins: []string{"1.2.3", "", "1.2.3"}, want: "1.2.3"},
			{name: "conflicting releases", pins: []string{"1.2.3", "1.2.4"}},
		} {
			t.Run(language.name()+"/"+tc.name, func(t *testing.T) {
				var jobs []onboard.RuntimeFinding
				for _, pin := range tc.pins {
					job := onboard.RuntimeFinding{Workflow: "ci.yml", Job: "tests"}
					if pin != "" {
						job.Tracer, job.TracerRequested = language.tracerPackage()+"@"+pin, pin
					}
					jobs = append(jobs, job)
				}
				previous := validationLanguages
				validationLanguages = []validationLanguage{workflowCIInventory{validationLanguage: language, jobs: jobs}}
				t.Cleanup(func() { validationLanguages = previous })
				root := t.TempDir()
				drive := &Testdrive{repositoryRoot: root, language: language.name(), framework: &framework.Vitest{}}
				result := onboard.AgentResult{NextCommand: []string{"ddtest", "testdrive", "--all"}}
				var output bytes.Buffer
				attempt := &workflowAttempt{result: &result, output: &output, options: WorkflowOptions{Format: "json"}, framework: "tests", recoveryCommand: result.NextCommand}
				pin, err := attempt.ciPinnedTracer(t.Context(), drive)
				if tc.name != "conflicting releases" {
					require.NoError(t, err)
					require.Equal(t, tc.want, pin)
					require.Empty(t, output.String())
					return
				}
				require.ErrorContains(t, err, "multiple_ci_tracers")
				require.Empty(t, pin, "a conflict must not silently choose either release")
				var handoff onboard.AgentResult
				require.NoError(t, json.Unmarshal(output.Bytes(), &handoff))
				require.Equal(t, "needs_input", handoff.Status)
				require.Equal(t, "ci.yml / tests", handoff.Location)
				require.Contains(t, handoff.Needs, "Preserve every configuration")
				require.Empty(t, handoff.NextCommand)
				require.NoFileExists(t, validationPath(root), "a selection conflict must not create validation evidence")
			})
		}
	}
}
