package testdrive

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func prepareCypressScopeFixture(t *testing.T, files map[string]string) (string, *Testdrive, onboard.ValidationScope) {
	t.Helper()
	root := t.TempDir()
	for name, contents := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
		requireWriteFile(t, filepath.Join(root, name), contents)
	}
	t.Chdir(root)
	viper.Reset()
	viper.Set("framework", "cypress")
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	drive, err := Prepare(PrepareOptions{TracerVersion: "6.20.1"})
	require.NoError(t, err)
	require.NoError(t, drive.discoveryError)
	scope, err := onboard.DiscoverValidationScope(root, "cypress")
	require.NoError(t, err)
	return root, drive, scope
}

func TestCypressDefaultModeCreditsTheDiscoveredConfiguration(t *testing.T) {
	cypressPackage := `{"devDependencies":{"cypress":"15.1.0"},"scripts":{"test":"cypress run"}}`
	for name, files := range map[string]map[string]string{
		"action build": {
			"package.json":             cypressPackage,
			"build.cjs":                "",
			".github/workflows/ci.yml": "jobs:\n  e2e:\n    steps:\n      - uses: cypress-io/github-action@v7\n        with: {build: node build.cjs, command: npm test}\n",
		},
		"working directory": {
			"package.json":             `{}`,
			"frontend/package.json":    cypressPackage,
			".github/workflows/ci.yml": "jobs:\n  e2e:\n    steps:\n      - run: npm test\n        working-directory: frontend\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			root, drive, scope := prepareCypressScopeFixture(t, files)
			require.Len(t, scope.Commands, 1)
			require.True(t, drive.cypressDiscovered)
			result := completeValidationFixture("cypress")
			drive.setConfigurationScope(&result, scope)
			require.Len(t, result.Configurations, 1)
			require.Equal(t, "passed", result.Configurations[0].Status, "the executed plan is the discovered one")

			// Its command text alone resolves to a different plan.
			result = completeValidationFixture("cypress")
			setSingleConfigurationScope(root, &result, scope, drive.scopeCommand, drive.configurationEnvironment)
			require.Equal(t, "not exercised", result.Configurations[0].Status)
		})
	}
}

func TestCypressFocusedCommandDoesNotCreditManagedStartup(t *testing.T) {
	root, _, scope := prepareCypressScopeFixture(t, map[string]string{
		"package.json":             `{"devDependencies":{"cypress":"15.1.0"},"scripts":{"test":"cypress run","start":"node server.cjs"}}`,
		".github/workflows/ci.yml": "jobs:\n  e2e:\n    steps:\n      - uses: cypress-io/github-action@v7\n        with: {command: npm test, start: npm start, wait-on: 'http://localhost:3000'}\n",
	})
	require.Len(t, scope.Commands, 1)
	require.NotEmpty(t, scope.Commands[0].Cypress.Start)
	result := completeValidationFixture("cypress")
	setSingleConfigurationScope(root, &result, scope, "npm test", nil)
	require.Equal(t, "not exercised", result.Configurations[0].Status, "npm test alone skips the action's startup and readiness")
	require.Nil(t, matchingConfigurationEnvironment(scope, nil, root, "cypress", "npm test"))
}

func TestCypressOSMatrixCreditsOnlyTheHostRow(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no hosted runner row matches this host")
	}
	_, drive, scope := prepareCypressScopeFixture(t, map[string]string{
		"package.json":             `{"devDependencies":{"cypress":"15.1.0"},"scripts":{"test":"cypress run"}}`,
		".github/workflows/ci.yml": "jobs:\n  e2e:\n    runs-on: ${{ matrix.os }}\n    strategy:\n      matrix: {os: [ubuntu-latest, macos-latest, windows-latest]}\n    steps:\n      - run: npm test\n        shell: bash\n",
	})
	require.Len(t, scope.Commands, 3)
	require.Equal(t, runtime.GOOS, drive.cypressSelected.Platform)
	result := completeValidationFixture("cypress")
	drive.setConfigurationScope(&result, scope)
	statuses := map[string]string{}
	for _, configuration := range result.Configurations {
		statuses[configuration.Platform] = configuration.Status
		if configuration.Platform != runtime.GOOS {
			require.Contains(t, configuration.Compatibility.Reason, "was not executed on this "+runtime.GOOS+" host")
		}
	}
	want := map[string]string{"linux": "not exercised", "darwin": "not exercised", "windows": "not exercised"}
	want[runtime.GOOS] = "passed"
	require.Equal(t, want, statuses)
	applyScopeVerdict(&result)
	require.False(t, result.Success, "other runner OSes remain unvalidated")
}
