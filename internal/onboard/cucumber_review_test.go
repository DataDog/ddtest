package onboard

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestCucumberESMDetectionUsesSelectedInputs(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "nyc cucumber-js --import features/steps.mjs", "unused": "cucumber-js --import unused.mjs"})
	commands := ValidationCommands(root, "npm test", "cucumber")
	require.True(t, CucumberESMInputs(root, commands))
	writeScripts(t, root, map[string]string{"test": "nyc cucumber-js", "unused": "cucumber-js --import unused.mjs"})
	require.False(t, CucumberESMInputs(root, ValidationCommands(root, "npm test", "cucumber")))
}

func TestCucumberDeclaredLoaderIsRetainedOnlyForCucumber(t *testing.T) {
	workflow := ciWorkflow{}
	step := runtimeStep{Run: "cucumber-js", Env: map[string]string{"NODE_OPTIONS": "--experimental-loader @istanbuljs/esm-loader-hook"}}
	_, env, err := resolveVariantStep(workflow, runtimeJob{}, step, nil, "cucumber")
	require.NoError(t, err)
	require.Equal(t, "--experimental-loader @istanbuljs/esm-loader-hook", env["NODE_OPTIONS"])
	_, _, err = resolveVariantStep(workflow, runtimeJob{}, step, nil, "jest")
	require.Error(t, err)
	step.Env["NODE_OPTIONS"] = "--experimental-loader arbitrary-hook"
	_, _, err = resolveVariantStep(workflow, runtimeJob{}, step, nil, "cucumber")
	require.Error(t, err)
}

func TestCucumberESMDetectionReadsTheSelectedConfigOnly(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "nyc cucumber-js --config ci.cjs"})
	require.NoError(t, os.WriteFile(filepath.Join(root, "ci.cjs"), []byte(`module.exports={default:{import:['steps.mjs']}}`), 0600))
	require.True(t, CucumberESMInputs(root, ValidationCommands(root, "npm test", "cucumber")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ci.cjs"), []byte(`module.exports={default:{require:['steps.cjs']}}`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "cucumber.cjs"), []byte(`module.exports={default:{import:['unused.mjs']}}`), 0600))
	require.False(t, CucumberESMInputs(root, ValidationCommands(root, "npm test", "cucumber")))
}
