package onboard

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonOnboardingPrintsScopeAndInstructions(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprint(configured), func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "pytest.ini"), []byte("[pytest]\n"), 0644))
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0755))
			action := ""
			if configured {
				action = "      - uses: datadog/test-visibility-github-action@v3\n        with:\n          languages: python\n"
			}
			workflow := "jobs:\n  test:\n    steps:\n" + action + "      - run: python -m pytest\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte(workflow), 0644))
			t.Chdir(root)
			for _, run := range []func(*bytes.Buffer) error{
				func(output *bytes.Buffer) error { return Run(output) },
				func(output *bytes.Buffer) error { return Start(output) },
			} {
				var output bytes.Buffer
				require.NoError(t, run(&output))
				for _, text := range []string{"python -m pytest", ".github/workflows/ci.yml", "pass_env", "--all --check-only", "historical_report", "structured onboarding coordinator remains unsupported"} {
					require.Contains(t, output.String(), text)
				}
				require.NotContains(t, output.String(), "--agent --all")
				if configured {
					require.Contains(t, output.String(), "already appears")
				}
			}
		})
	}
}
