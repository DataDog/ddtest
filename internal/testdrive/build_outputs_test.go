package testdrive

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

// Modeled on moment/luxon: npm run build runs a local Node script whose
// outputs package.json entry points name under the ignored build/ directory.
func TestSetupFactsTemplateSuggestsIgnoredEntryPointOutputs(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", root).Run())
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/build\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{
  "main": "build/node/luxon.js",
  "module": "build/es6/luxon.mjs",
  "browser": "build/cjs-browser/luxon.js",
  "types": "src/luxon.d.ts",
  "exports": {".": {"import": "./build/es6/luxon.mjs", "require": "./build/node/luxon.js"}, "./package.json": "./package.json"},
  "scripts": {"build": "node tasks/build.js"}
}`), 0600))
	template := &onboard.SetupFacts{Configurations: []onboard.ConfigurationFact{{Directory: ".", Prerequisites: []onboard.PrerequisiteFact{
		{Directory: ".", Argv: []string{"npm", "run", "build"}, ExpectedFiles: []string{}},
		{Directory: ".", Argv: []string{"node", "tasks/other.js"}, ExpectedFiles: []string{"kept.js"}},
	}}}}
	suggestPrerequisiteOutputs(t.Context(), root, template)
	prerequisites := template.Configurations[0].Prerequisites
	// The unignored source declaration file is not a build output.
	require.Equal(t, []string{"build/node/luxon.js", "build/es6/luxon.mjs", "build/cjs-browser/luxon.js"}, prerequisites[0].ExpectedFiles)
	require.Equal(t, []string{"kept.js"}, prerequisites[1].ExpectedFiles, "a supplied list is never replaced")
}

func TestSetupFactsTemplateLeavesOutputsEmptyWithoutIgnoredEntryPoints(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", root).Run())
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"main": "index.js"}`), 0600))
	require.Equal(t, []string{}, packageEntryOutputs(t.Context(), root, "."))
	require.Equal(t, []string{}, packageEntryOutputs(t.Context(), root, "../outside"))
}
