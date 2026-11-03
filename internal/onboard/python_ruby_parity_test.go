// Uses an explicitly supplied existing environment; never installs dependencies.
package onboard

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonOnboardingPrintsScopeAndInstructions(t *testing.T) {
	for _, tc := range []struct{ name, action, code, next string }{
		{"no setup", "", "ci_setup_required", "apply_source_edit"},
		// No api_key and no exact release: the instructions show how to fix it.
		{"incomplete setup", "      - uses: datadog/test-visibility-github-action@v3\n        with:\n          languages: python\n", "ci_setup_required", "apply_source_edit"},
		{"complete setup", "      - uses: datadog/test-visibility-github-action@v3\n        with:\n          languages: python\n          api_key: ${{ secrets.DD_API_KEY }}\n          python-tracer-version: '4.16.1'\n", "", "run_command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "pytest.ini"), []byte("[pytest]\n"), 0644))
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0755))
			workflow := "jobs:\n  test:\n    steps:\n" + tc.action + "      - run: python -m pytest\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte(workflow), 0644))
			t.Chdir(root)
			var output bytes.Buffer
			require.NoError(t, Run(&output))
			for _, text := range []string{"python -m pytest", ".github/workflows/ci.yml", "pass_env", "--all --check-only", "historical_report", "validation is standalone"} {
				require.Contains(t, output.String(), text)
			}
			require.NotContains(t, output.String(), "--agent --all")
			if tc.action != "" {
				require.Contains(t, output.String(), "already appears")
			}

			// A coding agent gets the structured handoff: the setup to apply
			// first, then testdrive --agent, never the manual steps or a
			// framework_unsupported dead end.
			output.Reset()
			err := Start(&output)
			var result AgentResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
			require.Equal(t, tc.code, result.Code)
			require.Equal(t, tc.next, result.NextAction)
			require.Equal(t, []string{"testdrive", "--framework", "pytest", "--all", "--agent", "--yes"}, append(result.NextCommand, result.ResumeCommand...)[1:7])
			if tc.code == "" {
				require.NoError(t, err)
				require.Equal(t, "ready", result.Status)
				require.Contains(t, result.Reason, "already runs the Datadog setup action")
				require.Contains(t, result.Reason, "never COMPLETE LOCALLY")
				return
			}
			require.Error(t, err)
			for _, text := range []string{"python -m pytest", "pass_env", "resume_command", "final_response unchanged", "never COMPLETE LOCALLY", "<exact ddtrace release>"} {
				require.Contains(t, result.Instruction, text)
			}
			require.NotContains(t, result.Instruction, "--check-only")
			require.NotContains(t, result.Instruction, "<resolved-release>")
		})
	}
}
