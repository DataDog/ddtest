package onboard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A Sauce Labs job reads its grid credentials from CI secrets that never exist
// locally (webdriverio/jasmine-boilerplate sauce.yaml). It must not block
// discovery: the entry is recorded as running on a remote grid.
func TestRemoteGridSecretsDoNotBlockWebdriverIODiscovery(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test:local":"wdio run wdio.local.conf.ts","test:sauce":"wdio run wdio.sauce.conf.ts"}}`), 0600))
	workflow := `env:
  SAUCE_USERNAME: ${{secrets.SAUCE_USERNAME}}
  SAUCE_ACCESS_KEY: ${{ secrets.SAUCE_ACCESS_KEY }}
jobs:
  build:
    steps:
      - run: npm install
      - run: npm run test:sauce
`
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/sauce.yaml"), []byte(workflow), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/ci.yaml"), []byte("jobs:\n  build:\n    steps:\n      - run: npm install\n      - run: npm run test:local\n"), 0600))

	scope, err := DiscoverValidationScope(root, "webdriverio")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	grids := map[string]string{}
	for _, entry := range scope.Commands {
		grids[entry.Command] = entry.RemoteGrid
		require.Empty(t, entry.UnvalidatedSetup, entry.Command)
		require.NotContains(t, entry.Environment, "SAUCE_ACCESS_KEY", "grid secrets are never forwarded locally")
	}
	require.Equal(t, map[string]string{"npm run test:local": "", "npm run test:sauce": "Sauce Labs"}, grids)
}

func TestRemoteGridSecretRequiresASecretReference(t *testing.T) {
	require.Equal(t, "Sauce Labs", remoteGridSecret("SAUCE_ACCESS_KEY", "${{ secrets.SAUCE_ACCESS_KEY }}"))
	require.Equal(t, "BrowserStack", remoteGridSecret("BROWSERSTACK_USERNAME", "${{secrets.BS_USER}}"))
	require.Equal(t, "LambdaTest", remoteGridSecret("LT_ACCESS_KEY", "${{ secrets.LT_ACCESS_KEY }}"))
	require.Empty(t, remoteGridSecret("SAUCE_REGION", "us"), "a literal value resolves normally")
	require.Empty(t, remoteGridSecret("SAUCE_ACCESS_KEY", "${{ secrets.A }}-${{ secrets.B }}"), "only a bare secret reference")
	require.Empty(t, remoteGridSecret("API_TOKEN", "${{ secrets.API_TOKEN }}"), "other secrets still need review")
}

// Other frameworks treat the same secret as any value only CI provides: only
// WebdriverIO can tell from its configuration that the run happens on the
// remote grid, so elsewhere the tests run without it and the report says so.
func TestRemoteGridSecretsOfOtherFrameworksAreValidatedWithout(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11.0.0"}}`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/ci.yaml"), []byte("jobs:\n  build:\n    steps:\n      - run: npm test\n        env:\n          SAUCE_ACCESS_KEY: ${{ secrets.SAUCE_ACCESS_KEY }}\n"), 0600))
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Empty(t, scope.Commands[0].RemoteGrid)
	require.Equal(t, []string{"SAUCE_ACCESS_KEY"}, scope.Commands[0].WithoutCIValues)
}
