// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

func TestSelectedProviderBindsFingerprintAndFollowUps(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"jest"}}`), 0600))
	t.Cleanup(func() { onboard.UseCIProvider("") })

	onboard.UseCIProvider("")
	unselected, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	onboard.UseCIProvider("github")
	github, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	again, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	onboard.UseCIProvider("other")
	other, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.Equal(t, github, again)
	require.NotEqual(t, unselected, github)
	require.NotEqual(t, github, other, "facts and reports for one provider never certify another")

	onboard.UseCIProvider("github")
	drive := &Testdrive{}
	resume := drive.workflowValidationResume("ddtest", "jest", "6.20.1", WorkflowOptions{})
	index := slices.Index(resume, "--ci-provider")
	require.Positive(t, index, resume)
	require.Equal(t, "github", resume[index+1])
}

func TestOnboardMissingScopePreservesCIProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	onboard.UseCIProvider("github")
	t.Cleanup(func() { onboard.UseCIProvider("") })
	drive := &Testdrive{framework: &framework.Jest{}, language: "javascript", tracerVersion: "latest"}
	var output bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "json"}), "scope_unresolved")
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Contains(t, result.NextCommand, "--instructions")
	index := slices.Index(result.NextCommand, "--ci-provider")
	require.Positive(t, index, result.NextCommand)
	require.Equal(t, "github", result.NextCommand[index+1])
}
