// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestProviderSelectionBindsFactsAndResumeCommands(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"jest"}}`), 0600))
	config := settings.Get()
	previous := config.CIProvider
	t.Cleanup(func() { config.CIProvider = previous })
	config.CIProvider = "github"
	github, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	config.CIProvider = "gitlab"
	gitlab, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.NotEqual(t, github, gitlab, "facts for one provider cannot certify another")
	drive := &Testdrive{}
	resume := drive.workflowValidationResume("ddtest", "jest", "6.20.1", WorkflowOptions{})
	require.Contains(t, resume, "--ci-provider")
	require.Contains(t, resume, "gitlab")
}
