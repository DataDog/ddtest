package testdrive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestPrepareJavaScriptExplicitCommands(t *testing.T) {
	for _, tc := range []struct {
		framework, command, executable string
		args                           []string
	}{
		{"jest", "npx jest --ci", "jest", []string{"--ci"}},
		{"vitest", "npx vitest run", "vitest", []string{"run"}},
		{"mocha", "npx mocha", "npx", []string{"mocha"}},
		{"playwright", "npx playwright test", "npx", []string{"playwright", "test"}},
		{"cypress", "npx cypress run", "npx", []string{"cypress", "run"}},
		{"cucumber", "npx cucumber-js", "npx", []string{"cucumber-js"}},
		{"webdriverio", "npx wdio run wdio.conf.cjs", "npx", []string{"wdio", "run", "wdio.conf.cjs"}},
	} {
		t.Run(tc.framework, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			requireWriteFile(t, "package.json", `{"private":true}`)
			viper.Reset()
			viper.Set("framework", tc.framework)
			viper.Set("command", tc.command)
			settings.Init()
			t.Cleanup(func() { viper.Reset(); settings.Init() })
			drive, err := Prepare(PrepareOptions{TracerVersion: "6.20.1", CheckOnly: true})
			require.NoError(t, err)
			require.NoError(t, drive.discoveryError)
			require.Equal(t, tc.executable, filepath.Base(drive.command))
			require.Equal(t, tc.args, drive.args)
			require.NotNil(t, drive.preflight)
			require.NoDirExists(t, filepath.Join(root, ".testoptimization"), "preparation must remain read-only")
			require.NoDirExists(t, drive.session.Directory())
		})
	}
}

func TestPrepareVitestCommandEnvironmentOverridesCI(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, "package.json", `{"private":true,"scripts":{"test":"vitest run"}}`)
	require.NoError(t, os.MkdirAll(".github/workflows", 0700))
	requireWriteFile(t, ".github/workflows/test.yml", "jobs:\n  tests:\n    steps:\n      - run: npm test\n        env: {SHARED: ci, CI_ONLY: preserved}\n")
	viper.Reset()
	viper.Set("framework", "vitest")
	viper.Set("command", "SHARED='two words' npx vitest run")
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	drive, err := Prepare(PrepareOptions{TracerVersion: "6.20.1", CheckOnly: true})
	require.NoError(t, err)
	require.NoError(t, drive.discoveryError)
	require.Equal(t, "two words", drive.configurationEnvironment["SHARED"])
	require.Equal(t, "preserved", drive.configurationEnvironment["CI_ONLY"])
	require.Equal(t, map[string]string{"SHARED": "two words"}, drive.selectedPlan.Environment)
	// Adding inherited CI values must not mutate the selected command's layer.
	require.NotContains(t, drive.preflightEnvironment, "CI_ONLY")
}

func TestJavaScriptSelectionPreservesFallbackOnDiscoveryFailure(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true}`)
	for _, name := range (javascriptLanguage{}).frameworks() {
		t.Run(name, func(t *testing.T) {
			args := []string{"original", "two words"}
			selected, err := selectJavaScriptCommand(javascriptCommandRequest{
				root: root, framework: name, override: "echo 'unterminated",
			}, "fallback", args)
			require.Error(t, err)
			require.Equal(t, "fallback", selected.command)
			require.Equal(t, args, selected.args)
		})
	}
}
