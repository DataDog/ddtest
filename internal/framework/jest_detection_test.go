package framework

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestJestDetectionOnlyReusesDirectScripts(t *testing.T) {
	original := settings.Get().Command
	settings.Get().Command = ""
	t.Cleanup(func() { settings.Get().Command = original })
	for _, test := range []struct {
		name, script string
		useScript    bool
	}{
		{"direct", "jest --coverage --verbose", true},
		{"quoted arguments", `jest --config "config with spaces.js"`, true},
		{"local executable", "./node_modules/.bin/jest --ci", true},
		{"other framework", "vitest run", false},
		{"unrelated", "node test.js", false},
		{"orchestrator", "npm-run-all build test:*", false},
		{"trailing command", "jest && eslint .", false},
		{"leading command", "eslint . && jest", false},
		{"semicolon", "jest;eslint .", false},
		{"pipe", "jest | tee output.txt", false},
		{"background", "jest & eslint .", false},
		{"fallback", "jest || echo failed", false},
		{"newline", "jest\neslint .", false},
		{"comment", "jest # appended flags would be ignored", false},
		{"redirect", "jest > output.txt", false},
		{"command substitution", "jest $(node config.js)", false},
		{"backtick substitution", "jest `node config.js`", false},
		{"environment wrapper", "cross-env NODE_ENV=test jest", false},
		{"node wrapper", "node node_modules/jest/bin/jest.js", false},
		{"npx wrapper", "npx jest", false},
		{"end of options", "jest --", false},
		{"quoted shell metacharacters", `jest --testNamePattern='a|b'`, false},
		{"invalid quotes", `jest "`, false},
		{"absent", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, local := range []bool{false, true} {
				t.Run(map[bool]string{false: "npx", true: "local"}[local], func(t *testing.T) {
					root := t.TempDir()
					t.Chdir(root)
					if local {
						require.NoError(t, os.MkdirAll(filepath.Dir(binJestPath), 0755))
						require.NoError(t, os.WriteFile(binJestPath, []byte("not executed"), 0755))
					}
					contents, err := json.Marshal(packageManifest{Scripts: map[string]string{"test": test.script}, DevDependencies: map[string]string{"jest": "^29", "vitest": "^3"}})
					require.NoError(t, err)
					path := filepath.Join(root, "package.json")
					require.NoError(t, os.WriteFile(path, contents, 0644))
					jest := NewJest()
					var command string
					var args []string
					jest.executor = &jestCommandExecutor{onExecution: func(name string, arguments []string) { command = name; args = slices.Clone(arguments) }}
					found, err := jest.Detect(root)
					require.NoError(t, err)
					require.True(t, found)
					require.Empty(t, command, "detection must not execute any command")
					wantCommand, wantArgs := "npx", []string{"jest"}
					if local {
						wantCommand, wantArgs = binJestPath, []string{}
					}
					if test.useScript {
						wantCommand, wantArgs = "npm", []string{"test", "--", "--runInBand"}
					}
					_, err = jest.DiscoverTestFiles(t.Context(), discovery.TestFileSet{Pattern: jest.TestPattern()})
					require.NoError(t, err)
					require.Equal(t, wantCommand, command)
					require.Equal(t, append(slices.Clone(wantArgs), "--listTests"), args)
					require.NoError(t, jest.RunTests(t.Context(), []string{"one.test.js"}, nil))
					require.Equal(t, wantCommand, command)
					require.Equal(t, append(slices.Clone(wantArgs), "--runTestsByPath", "one.test.js"), args)
					after, err := os.ReadFile(path)
					require.NoError(t, err)
					require.Equal(t, contents, after)
				})
			}
		})
	}
}

func TestJestDetectionPreservesExplicitCommand(t *testing.T) {
	original := settings.Get().Command
	settings.Get().Command = "custom-jest --config explicit.js"
	t.Cleanup(func() { settings.Get().Command = original })
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"jest":"29"},"scripts":{"test":"jest --config other.js"}}`), 0644))
	jest := NewJest()
	found, err := jest.Detect(root)
	require.NoError(t, err)
	require.True(t, found)
	command, args := jest.TestCommand(nil)
	require.Equal(t, "custom-jest", command)
	require.Equal(t, []string{"--config", "explicit.js"}, args)
}
