package onboard

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestFocusedCucumberRetainsCIBuild(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"build": "babel src --out-dir lib", "bdd": "cucumber-js features"})
	path := filepath.Join(root, ".github/workflows/ci.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte("jobs:\n  test:\n    steps:\n      - run: npm run build\n      - run: npm run bdd\n"), 0600))
	entry, err := CucumberExecutionCommand(root, "npx cucumber-js features")
	require.NoError(t, err)
	require.Equal(t, "npx cucumber-js features", entry.Command)
	require.NotEmpty(t, entry.Prerequisites)
	require.Equal(t, "babel src --out-dir lib", entry.Prerequisites[0].Command)
}

func TestFocusedCucumberRejectsDifferentBuildVariants(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"build": "babel src --out-dir lib"})
	path := filepath.Join(root, ".github/workflows/ci.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte("jobs:\n  built:\n    steps:\n      - run: npm run build\n      - run: cucumber-js features\n  source:\n    steps:\n      - run: cucumber-js features\n"), 0600))
	_, err := CucumberExecutionCommand(root, "npx cucumber-js features")
	require.ErrorContains(t, err, "different CI preparation")
}

func TestFocusedCucumberRetainsBuildAcrossCoverageOutputOptions(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"build": "rollup -c", "pretest": "npm run build", "test": "nyc --report-dir coverage/ci -- cucumber-js features --profile ci"})
	path := filepath.Join(root, ".github/workflows/ci.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte("jobs:\n  test:\n    steps:\n      - run: npm test\n"), 0600))
	entry, err := CucumberExecutionCommand(root, "nyc --report-dir coverage/local -- cucumber-js features --profile ci")
	require.NoError(t, err)
	require.NotEmpty(t, entry.Prerequisites)
	require.Contains(t, entry.Command, "coverage/local")
}
