// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"
)

func TestDiscoveryRejectsWorkflowExpressionsLeftInCommands(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - run: jest --config '${{ secrets.JEST_CONFIG }}'
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Unresolved, 1)
	require.Contains(t, scope.Unresolved[0], "unresolved CI workflow expression requires review: ${{ secrets.JEST_CONFIG }}")
	_, err = JestValidationCommand(root)
	require.ErrorContains(t, err, "unresolved CI workflow expression")
}

func TestDiscoveryInterpolatesMatrixWorkingDirectories(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    strategy:
      matrix: {package: [packages/a, packages/b]}
    steps:
      - run: npx jest
        working-directory: ${{ matrix.package }}
`)
	for _, name := range []string{"a", "b"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", name), 0755))
	}
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	var directories []string
	for _, command := range scope.Commands {
		directories = append(directories, command.Directory)
	}
	require.ElementsMatch(t, []string{"packages/a", "packages/b"}, directories)
}

func TestDiscoveryUsesPowerShellDefaultOnWindowsRunners(t *testing.T) {
	root := newJestRepository(t, `jobs:
  windows:
    runs-on: windows-latest
    steps:
      - run: jest --config .\jest.config.js
  matrix:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix: {os: [ubuntu-latest, windows-latest]}
    steps:
      - run: jest --config matrix.js
  explicit:
    runs-on: [self-hosted, windows]
    steps:
      - run: jest --config bash.js
        shell: bash
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	var commands []string
	for _, command := range scope.Commands {
		if len(command.UnvalidatedSetup) == 0 {
			commands = append(commands, command.Command)
		}
	}
	require.Contains(t, commands, "jest --config matrix.js", "the Ubuntu row keeps Bash")
	require.Contains(t, commands, "jest --config bash.js", "an explicit shell overrides the runner default")
	require.NotContains(t, commands, "jest --config .jest.config.js")
	unresolved := strings.Join(scope.Unresolved, "\n")
	require.Contains(t, unresolved, "/ windows")
	require.Contains(t, unresolved, "Unsupported CI shell: pwsh")
	require.Contains(t, unresolved, `os: "windows-latest"`)
}

func TestPrintfFormatThatWritesVariablesIsSetup(t *testing.T) {
	for command, assigns := range map[string]bool{
		`printf '%n' PATH`:      true,
		`printf -- '%5n' PATH`:  true,
		`printf -v PATH x`:      true,
		`printf '%%n\n'`:        false,
		`printf '%s\n' "$PATH"`: false,
		`printf -- '%s' value`:  false,
	} {
		file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
		require.NoError(t, err)
		call := file.Stmts[0].Cmd.(*syntax.CallExpr)
		require.Equal(t, !assigns, outputOnlyCall(call), command)
		words, err := shellquote.Split(command)
		require.NoError(t, err)
		require.Equal(t, assigns, printfAssigns(words[1:]), command)
	}
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - run: printf '%n' PATH && jest
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "Shell variable assignment requires review")
}

func TestXargsDependencyRemovalIsRetainedAsSetup(t *testing.T) {
	require.True(t, mutatesCIFiles("xargs -n1 npm rm --silent --save-dev"))
	require.True(t, mutatesCIFiles("xargs -I {} npm uninstall {}"))
	require.False(t, mutatesCIFiles("xargs -n 1 echo"))
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - run: printf '%s\n' react | xargs -n1 npm rm --silent --save-dev
      - run: npm test
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Contains(t, strings.Join(scope.Commands[0].UnvalidatedSetup, "\n"), "dependency changes require separate validation")
}

func TestJestConfigurationOutputIsNotATestCommand(t *testing.T) {
	root := t.TempDir()
	for command, matched := range map[string]bool{
		"jest --showConfig":     false,
		"jest --show-config":    false,
		"jest --version=true":   false,
		"jest --help=true":      false,
		"jest --version=false":  true,
		"jest -- --showConfig":  true,
		"jest --config ci.json": true,
	} {
		require.Equal(t, matched, resolveJestCommand(root, command, nil).Matched, command)
	}
}

func TestJestValidationCommandKeepsCIEnvironment(t *testing.T) {
	root := newJestRepository(t, `jobs:
  esm:
    steps:
      - run: jest
        env: {NODE_OPTIONS: --experimental-vm-modules, TZ: UTC}
`)
	selected, err := JestValidationCommand(root)
	require.NoError(t, err)
	require.Equal(t, "NODE_OPTIONS=--experimental-vm-modules TZ=UTC "+shellquote.Join(filepath.Join(root, "node_modules/.bin/jest")), selected)

	root = newJestRepository(t, `jobs:
  utc:
    env: {TZ: UTC}
    steps:
      - run: jest
  local:
    env: {TZ: Europe/Madrid}
    steps:
      - run: jest
`)
	_, err = JestValidationCommand(root)
	require.ErrorContains(t, err, "multiple Jest CI commands found")
}

// Stub runners record which package's binary ran, where, and with what flags.
func writeRecordingJest(t *testing.T, directory, label, output string) {
	t.Helper()
	bin := filepath.Join(directory, "node_modules", ".bin")
	require.NoError(t, os.MkdirAll(bin, 0755))
	script := "#!/bin/sh\nprintf '%s\\n' \"" + label + ":$(pwd -P):$*\" >> " + shellquote.Join(output) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "jest"), []byte(script), 0755))
}

func runForwarded(t *testing.T, root, selected string) {
	t.Helper()
	words, err := shellquote.Split(selected)
	require.NoError(t, err)
	cmd := exec.CommandContext(t.Context(), words[0], append(words[1:], "--listTests", "--runTestsByPath", "probe file.js")...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
}

func TestForwardingFollowsDirectoryChangesIntoPackageScripts(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	output := filepath.Join(t.TempDir(), "runs")
	app := filepath.Join(root, "app")
	require.NoError(t, os.MkdirAll(app, 0755))
	writeScripts(t, root, map[string]string{"test": "jest --config root.js"})
	writeScripts(t, app, map[string]string{"test": "jest --config app.js"})
	writeRecordingJest(t, root, "root", output)
	writeRecordingJest(t, app, "app", output)

	selected, err := ForwardJestCommand(root, "cd app && npm test")
	require.NoError(t, err)
	runForwarded(t, root, selected)
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, "app:"+app+":--config app.js --listTests --runTestsByPath probe file.js\n", string(data))
}

func TestForwardingKeepsDirectoryChangesInsideExpandedScripts(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	output := filepath.Join(t.TempDir(), "runs")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "app"), 0755))
	writeScripts(t, root, map[string]string{"test": "npm run unit && tsc", "unit": "cd app && jest"})
	writeRecordingJest(t, root, "jest", output)
	tsc := "#!/bin/sh\nprintf '%s\\n' \"tsc:$(pwd -P)\" >> " + shellquote.Join(output) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "node_modules", ".bin", "tsc"), []byte(tsc), 0755))

	selected, err := ForwardJestCommand(root, "npm test")
	require.NoError(t, err)
	runForwarded(t, root, selected)
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, "jest:"+filepath.Join(root, "app")+":--listTests --runTestsByPath probe file.js\ntsc:"+root+"\n", string(data))
}

func TestForwardingExpandsScriptsWhoseTextWouldSwallowOptions(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	output := filepath.Join(t.TempDir(), "runs")
	writeRecordingJest(t, root, "jest", output)
	writeScripts(t, root, map[string]string{"test": "jest # unit tests"})
	selected, err := ForwardJestCommand(root, "npm test")
	require.NoError(t, err)
	require.NotEqual(t, "npm test --", selected, "npm would append the options to the comment")
	runForwarded(t, root, selected)
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, "jest:"+root+":--listTests --runTestsByPath probe file.js\n", string(data))

	for _, script := range []string{"jest;", "npm run unit", "jest --ci"} {
		scripts := map[string]string{"test": script, "unit": "jest; echo done"}
		writeScripts(t, root, scripts)
		selected, err := ForwardJestCommand(root, "yarn test")
		if script == "jest --ci" {
			require.NoError(t, err)
			require.Equal(t, "yarn test", selected, "a plain runner script keeps the native shortcut")
			continue
		}
		require.NotEqual(t, "yarn test", selected, script)
		require.Error(t, err, script)
	}
}
