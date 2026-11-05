package releaseworkflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestNodeFixtureCacheUsesTheWholeLockfileAndExactRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture preparation runs in Ubuntu bash")
	}
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("fixture preparation requires sha256sum")
	}
	data, err := os.ReadFile("../../.github/actions/node-test-fixture/action.yml")
	require.NoError(t, err)
	var action struct {
		Runs struct {
			Steps []struct {
				ID, Run, If, Uses string
				With              map[string]string
			}
		}
	}
	require.NoError(t, yaml.Unmarshal(data, &action))
	steps := action.Runs.Steps
	require.Len(t, steps, 4)
	require.Contains(t, steps[1].With["key"], "runner.os")
	require.Contains(t, steps[1].With["key"], "runner.arch")
	require.Empty(t, steps[1].With["restore-keys"])
	require.Equal(t, "steps.modules.outputs.cache-hit != 'true'", steps[2].If)
	require.Contains(t, steps[2].Run, "npm ci")
	require.Equal(t, steps[1].With["key"], steps[3].With["key"])
	require.Equal(t, steps[1].With["path"], steps[3].With["path"])
	require.Equal(t, steps[2].If, steps[3].If)
	tools, fixture := t.TempDir(), t.TempDir()
	lock := filepath.Join(t.TempDir(), "lock.json")
	require.NoError(t, os.WriteFile(lock, []byte(`{"dependencies":{"nested":"1.0.0"}}`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tools, "node"), []byte("#!/bin/sh\necho \"$FIXTURE_NODE\"\n"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tools, "npm"), []byte("#!/bin/sh\nif [ \"$1\" = install ]; then cp \"$FIXTURE_LOCK\" package-lock.json; else echo \"$FIXTURE_NPM\"; fi\n"), 0755))
	dependencies := "example@1.0.0"
	key := func(node, npm string) string {
		t.Helper()
		output := filepath.Join(t.TempDir(), "output")
		cmd := exec.CommandContext(t.Context(), "bash", "-eu", "-c", steps[0].Run)
		cmd.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
			"FIXTURE_DIRECTORY="+fixture, "FIXTURE_DEPENDENCIES="+dependencies, "FIXTURE_LOCK="+lock,
			"FIXTURE_NODE="+node, "FIXTURE_NPM="+npm, "GITHUB_OUTPUT="+output)
		result, err := cmd.CombinedOutput()
		require.NoError(t, err, string(result))
		data, err := os.ReadFile(output)
		require.NoError(t, err)
		return string(data)
	}
	original := key("24.0.0", "11.0.0")
	require.Equal(t, original, key("24.0.0", "11.0.0"))
	require.NotEqual(t, original, key("24.0.1", "11.0.0"))
	require.NotEqual(t, original, key("24.0.0", "11.0.1"))
	require.NoError(t, os.WriteFile(lock, []byte(`{"dependencies":{"nested":"1.0.1"}}`), 0600))
	dependencies = ""
	require.Equal(t, original, key("24.0.0", "11.0.0"), "a supplied lockfile must not be resolved again")
	dependencies = "example@1.0.0"
	require.NotEqual(t, original, key("24.0.0", "11.0.0"))
}
