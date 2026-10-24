package testdrive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

func TestSetupSelectionLetsTheApplierAddAMissingPreload(t *testing.T) {
	for name, env := range map[string]string{"no NODE_OPTIONS": "", "custom NODE_OPTIONS": "        env: {NODE_OPTIONS: '--conditions=customer'}\n"} {
		t.Run(name, func(t *testing.T) {
			drive := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
			require.NoError(t, os.MkdirAll(filepath.Join(drive.repositoryRoot, ".github/workflows"), 0700))
			workflow := "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: js, js-tracer-version: '6.16.0'}\n      - run: npm test\n" + env
			requireWriteFile(t, filepath.Join(drive.repositoryRoot, ".github/workflows/test.yml"), workflow)
			scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "npx jest"}}}
			selected, err := drive.selectSetupTracer(t.Context(), scope, false)
			if env != "" {
				require.ErrorContains(t, err, "Preserve custom initialization", "custom options still need review")
				return
			}
			require.NoError(t, err, "the planner adds the missing preload")
			require.Equal(t, "6.16.0", selected.Version)
		})
	}
}
