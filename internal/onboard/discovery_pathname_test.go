// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writePathnameFixture(t *testing.T, scripts map[string]string, files ...string) string {
	t.Helper()
	root := t.TempDir()
	data, err := json.Marshal(map[string]any{"scripts": scripts})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), data, 0600))
	for _, file := range files {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.Dir(file)), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, file), nil, 0600))
	}
	return root
}

// npm runs scripts with sh: unquoted patterns expand in the package directory
// without globstar or dotglob, and that argument list is what Mocha receives.
func TestMochaScriptPathnamePatternsExpandWithShellSemantics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
		args   []string
		probe  string
	}{
		{"current directory", "mocha *.test.js", []string{"a.test.js", "b.test.js"}, "mocha a.test.js b.test.js"},
		{"double star is a single level", "mocha --exit test/**/*.spec.js", []string{"--exit", "test/unit/x.spec.js"}, "mocha --exit test/unit/x.spec.js"},
		{"character class", "mocha test/[a-t]*.spec.js", []string{"test/top.spec.js"}, "mocha test/top.spec.js"},
		{"after cd", "cd test && mocha *.spec.js", []string{"top.spec.js"}, "mocha top.spec.js"},
		// Mocha expands a quoted pattern itself, with its own globstar rules.
		{"quoted pattern stays literal", "mocha 'test/**/*.spec.js'", []string{"test/**/*.spec.js"}, `mocha test/\*\*/\*.spec.js`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := writePathnameFixture(t, map[string]string{"test": tc.script}, "a.test.js", "b.test.js", ".hidden.test.js", "helper.js", "test/top.spec.js", "test/unit/x.spec.js", "test/unit/deep/y.spec.js")
			command, err := MochaValidationCommand(root, "")
			require.NoError(t, err)
			require.Equal(t, "npm test", command)
			require.Equal(t, tc.args, MochaRunnerArguments(root, "npm test"))
			plan := MochaProbePlan(root, "npm test")
			require.Equal(t, tc.probe, plan.Command)

			remaining := packageScriptExpansionLimit
			resolved := resolveJestSequence(root, root, "npm test", nil, &remaining, "mocha")
			require.Empty(t, resolved.Reason)
			require.Len(t, resolved.Tests, 1)
			// Full-suite runs keep the alias, so the shell still expands it.
			require.Equal(t, "npm test", resolved.Tests[0].Command)
		})
	}
}

func TestDynamicPathnameArgumentsStayUnresolved(t *testing.T) {
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "x.test.js"), nil, 0600))
	for _, script := range []string{
		"mocha $DIR/*.test.js",
		"mocha test/{a,b}.test.js",
		"mocha $(ls *.test.js)",
		"mocha ~/tests/*.test.js",
		"mocha missing/*.test.js",
		"mocha \\*.test.js",
		"mocha " + outside + "/*.test.js",
		"mocha ../*.test.js",
		"mocha [",
	} {
		t.Run(script, func(t *testing.T) {
			parent := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(parent, "y.test.js"), nil, 0600))
			root := filepath.Join(parent, "repo")
			require.NoError(t, os.Mkdir(root, 0700))
			data, _ := json.Marshal(map[string]any{"scripts": map[string]string{"test": script}})
			require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), data, 0600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "a.test.js"), nil, 0600))
			remaining := packageScriptExpansionLimit
			resolved := resolveJestSequence(root, root, "npm test", nil, &remaining, "mocha")
			if script == "mocha \\*.test.js" {
				// An escaped star is a literal file name, not a pattern.
				require.Empty(t, resolved.Reason)
				require.Equal(t, []string{"*.test.js"}, resolved.Tests[0].mochaRunnerArgs)
				return
			}
			require.Contains(t, resolved.Reason, "dynamic shell syntax")
			_, err := MochaValidationCommand(root, "")
			require.Error(t, err)
		})
	}
}

func TestPathnameExpansionIsBounded(t *testing.T) {
	files := []string{}
	for i := range pathnameExpansionLimit + 1 {
		files = append(files, filepath.Join("t", string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676))+".test.js"))
	}
	root := writePathnameFixture(t, map[string]string{"test": "mocha t/*.test.js"}, files...)
	remaining := packageScriptExpansionLimit
	resolved := resolveJestSequence(root, root, "npm test", nil, &remaining, "mocha")
	require.Contains(t, resolved.Reason, "exceeds")
}

// The shared sequence resolver serves every JavaScript runner.
func TestJestPathnamePatternsExpandWithShellSemantics(t *testing.T) {
	root := writePathnameFixture(t, map[string]string{"test": "jest src/*.test.js"}, "src/a.test.js", "src/b.test.js", "src/c.js")
	for _, command := range []string{"npx jest src/*.test.js", "npm test"} {
		remaining := packageScriptExpansionLimit
		resolved := resolveJestSequence(root, root, command, nil, &remaining, "jest")
		require.Empty(t, resolved.Reason, command)
		require.True(t, resolved.Matched)
		require.Len(t, resolved.Tests, 1)
		require.Equal(t, "jest src/a.test.js src/b.test.js", resolved.Tests[0].Command)
	}
	result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "npx jest src/*.test.js"}, "javascript", "jest")
	require.Empty(t, result.Reason)
	require.True(t, result.Matched)
}

// A formatter's patterns select no tests, so a chained companion tool stays
// review-only even when its pattern matches nothing at discovery time.
func TestTestScriptChainedWithPatternFormatter(t *testing.T) {
	root := writePathnameFixture(t, map[string]string{"test": "mocha *.test.js", "format:write": "prettier --write ../schema.json **/*.js **/*.yml"}, "schema.test.js")
	remaining := packageScriptExpansionLimit
	resolved := resolveJestSequence(root, root, "npm test && npm run format:write", nil, &remaining, "mocha")
	require.Empty(t, resolved.Reason)
	require.Len(t, resolved.Tests, 1)
	require.Equal(t, []string{"schema.test.js"}, resolved.Tests[0].mochaRunnerArgs)
	require.Contains(t, resolved.ReviewReason, "prettier --write ../schema.json **/*.js **/*.yml")

	// A runner whose pattern matches nothing is not review-only.
	root = writePathnameFixture(t, map[string]string{"test": "mocha missing/*.test.js && prettier --check **/*.js"})
	remaining = packageScriptExpansionLimit
	resolved = resolveJestSequence(root, root, "npm test", nil, &remaining, "mocha")
	require.Contains(t, resolved.Reason, "mocha missing/*.test.js")
}

// Callers without the working directory keep rejecting patterns.
func TestStaticCommandsRejectPathnamePatterns(t *testing.T) {
	_, err := staticCommands("mocha *.test.js")
	require.ErrorContains(t, err, "dynamic shell syntax: mocha *.test.js")
	words, err := staticCommands("mocha '*.test.js'")
	require.NoError(t, err)
	require.Equal(t, [][]string{{"mocha", "*.test.js"}}, words)
	_, err = staticCommands("*.sh")
	require.Error(t, err)
}

// PowerShell passes patterns to native commands unexpanded on Windows.
func TestPowerShellPathnamePatternsStayUnresolved(t *testing.T) {
	root := writePathnameFixture(t, nil, "test/a.test.js")
	require.False(t, powershellArgvOnly("pwsh", "npx mocha test/*.test.js"))
	result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "npx mocha test/*.test.js", Shell: "pwsh"}, "javascript", "mocha")
	require.Contains(t, result.Reason, "Unsupported CI shell")
	require.True(t, powershellArgvOnly("pwsh", "npx mocha test/a.test.js"))
}
