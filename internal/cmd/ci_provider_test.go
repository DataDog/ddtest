// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestProviderChoicePrecedesOnboardAndTestdriveExecution(t *testing.T) {
	for _, name := range []string{"onboard", "testdrive"} {
		t.Run(name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			before := settings.Get().CIProvider
			settings.Get().CIProvider = ""
			t.Cleanup(func() { settings.Get().CIProvider = before })
			require.NoError(t, os.MkdirAll(".github/workflows", 0700))
			require.NoError(t, os.WriteFile("package.json", []byte(`{"scripts":{"test":"jest"}}`), 0600))
			require.NoError(t, os.WriteFile(".github/workflows/ci.yml", []byte("jobs:\n  test:\n    steps: [{run: npm test}]\n"), 0600))
			require.NoError(t, os.WriteFile(".gitlab-ci.yml", []byte("test: {script: npm test}\n"), 0600))
			var command *cobra.Command
			if name == "onboard" {
				command = newOnboardCommand(nil)
				command.SetArgs([]string{"--agent"})
			} else {
				command = newTestdriveCommand()
				command.SetArgs([]string{"--agent", "--check-only", "--yes"})
			}
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&bytes.Buffer{})
			require.Error(t, command.ExecuteContext(t.Context()))
			var result onboard.AgentResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
			require.Equal(t, "ci_provider_required", result.Code)
			require.Equal(t, "ask_user", result.NextAction)
			require.Empty(t, result.NextCommand)
			require.Equal(t, []string{"github", "gitlab"}, result.RequiredInput)
			require.NoDirExists(t, filepath.Join(".testoptimization"))
			require.NoDirExists(t, "node_modules")
		})
	}
}
