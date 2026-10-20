package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCucumberCoverageInsertionUsesExecutablePosition(t *testing.T) {
	for _, command := range []string{
		"./node_modules/.bin/cucumber-js features",
		`DEBUG=cucumber-js:* cucumber-js features`,
		"npx --no-install -- cucumber-js features",
		"npx cucumber-js features",
		"npx -- cucumber-js features",
		"cross-env DEBUG=cucumber-js:* cucumber-js features",
	} {
		t.Run(command, func(t *testing.T) {
			root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: npm test\n")
			manifest := map[string]any{"scripts": map[string]string{"test": command}, "devDependencies": map[string]string{"nyc": "18.0.0"}}
			data, err := json.Marshal(manifest)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), data, 0600))
			edits, _, err := scopedSetupEdits(root, "cucumber")
			require.NoError(t, err)
			require.Len(t, edits, 1)
			var updated struct{ Scripts map[string]string }
			require.NoError(t, json.Unmarshal(edits[0].after, &updated))
			script := updated.Scripts["test"]
			require.NotContains(t, script, "./node_modules/.bin/npx")
			if strings.Contains(command, "DEBUG=") {
				require.Contains(t, script, "DEBUG=cucumber-js:*")
			}
			require.Contains(t, script, "nyc -- ")
			if strings.Contains(command, "npx") {
				require.Contains(t, script, "npx --no-install")
			}
			remaining := 256
			resolution := resolveJestSequence(root, root, cucumberDiscoveryScript(script), nil, &remaining, "cucumber")
			require.Empty(t, resolution.Reason)
			require.Len(t, resolution.Tests, 1)
			require.True(t, resolution.Tests[0].usesNYC)
		})
	}
}

func TestBuildSequencingPreservesConditionalExecution(t *testing.T) {
	for _, separator := range []string{" && ", "; ", "\n"} {
		t.Run(separator, func(t *testing.T) {
			root := newJestRepository(t, "")
			writeScripts(t, root, map[string]string{"build": "tsc", "test": "jest"})
			remaining := 256
			result := resolveJestSequence(root, root, "npm run build"+separator+"jest", nil, &remaining, "jest")
			require.Len(t, result.Tests, 1)
			if separator == " && " {
				require.Empty(t, result.Tests[0].UnvalidatedSetup)
				require.Len(t, result.Tests[0].Prerequisites, 1)
			} else {
				require.NotEmpty(t, result.Tests[0].UnvalidatedSetup)
				require.Empty(t, result.Tests[0].Prerequisites)
			}
		})
	}
}

func TestCucumberSetupDoesNotScopeEnvironmentToAnEarlierCommand(t *testing.T) {
	root := newJestRepository(t, "")
	writeScripts(t, root, map[string]string{"test": "echo setup && nyc -- cucumber-js features"})
	path := filepath.Join(root, "package.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(data, &manifest))
	manifest["devDependencies"] = map[string]string{"nyc": "18.0.0"}
	before, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, before, 0600))
	_, _, err = scopedSetupEdits(root, "cucumber")
	var gap *SetupGap
	require.ErrorAs(t, err, &gap)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestShellDiscoveryUsesLiteralASTValues(t *testing.T) {
	commands, err := staticCommands(`DEBUG='cucumber-js:*' node "a path.js" 'x;y' a\ b && echo '$(literal)'`)
	require.NoError(t, err)
	require.Equal(t, [][]string{{"DEBUG=cucumber-js:*", "node", "a path.js", "x;y", "a b"}, {"echo", "$(literal)"}}, commands)
	for _, command := range []string{`echo "$(node run.js)"`, `echo "$HOME"`, `node a.js > out`, `node a.js &`} {
		_, err := staticCommands(command)
		require.Error(t, err, command)
	}
}
