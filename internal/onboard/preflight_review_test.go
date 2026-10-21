// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigurationRejectsUnappliedPrerequisites(t *testing.T) {
	for _, script := range []string{"pnpm add -D jest@30 && jest", "sed -i 's/jest-circus/jest-jasmine2/' jest.config.js && jest", "tsc && jest"} {
		t.Run(script, func(t *testing.T) {
			root := t.TempDir()
			writeScripts(t, root, map[string]string{"test": script})
			_, err := JestConfigurationCommand(root, "npm test")
			require.Error(t, err)
		})
	}
}

func TestConfigurationSelectionPreservesCIEnvironment(t *testing.T) {
	workflow := `env: {GLOBAL: workflow, NODE_ENV: development}
jobs:
  tests:
    env: {JOB: job}
    steps:
      - run: npm test
        env: {NODE_ENV: production}
`
	root := newJestRepository(t, workflow)
	entry, err := JestConfigurationCommand(root, "")
	require.NoError(t, err)
	require.Equal(t, "production", entry.Environment["NODE_ENV"])
	require.Equal(t, "workflow", entry.Environment["GLOBAL"])
	require.Equal(t, "job", entry.Environment["JOB"])
	// Equal commands with different configuration inputs are different checks.
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/second.yml"), []byte(strings.Replace(workflow, "production", "test", 1)), 0600))
	_, err = JestConfigurationCommand(root, "")
	require.Error(t, err)
}

func TestCIRuntimeChangesNeverReuseSetupNode(t *testing.T) {
	for _, command := range []string{"nvm use", "nvm install 22", "nvm install --lts", "n 16", "fnm use 16", "fnm exec 16 jest"} {
		t.Run(command, func(t *testing.T) {
			workflow := strings.Replace(fmt.Sprintf(runtimeWorkflow, ""), "      - run: npm run test", "      - run: "+command+"\n      - run: npm run test", 1)
			result := checkCIRuntimes(t.Context(), newJestRepository(t, workflow), nil, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.17.0", Node: ">=22"}, nil
			})
			require.Equal(t, StatusInconclusive, result.Status)
			found := false
			for _, job := range result.Jobs {
				if job.Command == "npm run test" {
					found = true
					require.Empty(t, job.Node)
					require.Nil(t, job.NodeResolution)
					require.Equal(t, CodeRuntimeUnknown, job.Code)
				}
			}
			require.True(t, found)
		})
	}
}

func TestCIExplicitNodeExecutableIsUnresolved(t *testing.T) {
	for _, node := range []string{"/opt/node20/bin/node", "./runtime/node"} {
		workflow := strings.Replace(fmt.Sprintf(runtimeWorkflow, ""), "npm run test", node+" node_modules/jest/bin/jest.js", 1)
		result := checkCIRuntimes(t.Context(), newJestRepository(t, workflow), nil, func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "6.17.0", Node: ">=22"}, nil
		})
		require.Equal(t, StatusInconclusive, result.Status)
		for _, job := range result.Jobs {
			require.Equal(t, CodeRuntimeUnknown, job.Code)
			require.Empty(t, job.Node)
			require.Nil(t, job.NodeResolution)
		}
	}
}

func TestTracerMetadataFailuresAreCachedAcrossMatrix(t *testing.T) {
	calls := 0
	result := checkCIRuntimes(t.Context(), newJestRepository(t, fmt.Sprintf(runtimeWorkflow, "")), nil, func(context.Context, string, string) (tracerRequirement, error) {
		calls++
		return tracerRequirement{}, errors.New("registry unavailable")
	})
	require.Equal(t, 1, calls)
	require.Equal(t, StatusInconclusive, result.Status)
	for _, job := range result.Jobs {
		require.Equal(t, CodeTracerMetadata, job.Code)
	}
}

func TestSetupNodeVersionFiles(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct{ name, data, want string }{
		{".nvmrc", "v20.18.0\n", "20.18.0"}, {".node-version", "22.x\n", "22.x"},
		{"package.json", `{"engines":{"node":"^20"}}`, "^20"},
		{"package.json", `{"volta":{"node":"22.1.0"},"engines":{"node":"^20"}}`, "22.1.0"},
	} {
		require.NoError(t, os.WriteFile(filepath.Join(root, tc.name), []byte(tc.data), 0600))
		got, err := setupNodeVersion(root, runtimeStep{With: map[string]string{"node-version-file": tc.name}}, nil)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
	for _, file := range []string{"missing", "../outside", "package.json"} {
		if file == "package.json" {
			require.NoError(t, os.WriteFile(filepath.Join(root, file), []byte(`{}`), 0600))
		}
		_, err := setupNodeVersion(root, runtimeStep{With: map[string]string{"node-version-file": file}}, nil)
		require.Error(t, err)
		require.NotContains(t, err.Error(), root)
	}
	outside := filepath.Join(t.TempDir(), "version")
	require.NoError(t, os.WriteFile(outside, []byte("22"), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escaped")))
	_, err := setupNodeVersion(root, runtimeStep{With: map[string]string{"node-version-file": "escaped"}}, nil)
	require.Error(t, err)
}

func TestSetupNodeSemverSelectorsAndPlatforms(t *testing.T) {
	const manifest = `[
 {"version":"18.20.0","stable":true,"lts":"Hydrogen","files":[{"platform":"linux","arch":"x64"}]},
 {"version":"20.18.1","stable":true,"lts":"Iron","files":[{"platform":"linux","arch":"x64"},{"platform":"darwin","arch":"arm64"}]},
 {"version":"20.18.2","stable":true,"lts":"Iron","files":[{"platform":"linux","arch":"x64"}]},
 {"version":"22.4.0","stable":true,"lts":"Jod","files":[{"platform":"linux","arch":"x64"}]}
 ]`
	for _, tc := range []struct{ selector, platform, want string }{
		{"20.x", "linux-x64", "20.18.2"}, {">=18", "linux-x64", "22.4.0"}, {"^20", "linux-x64", "20.18.2"}, {"*", "linux-x64", "22.4.0"},
		{"lts/*", "linux-x64", "22.4.0"}, {"lts/-1", "linux-x64", "20.18.2"}, {"lts/iron", "linux-x64", "20.18.2"},
		{"20.x", "osx-arm64-tar", "20.18.1"},
	} {
		t.Run(tc.selector+tc.platform, func(t *testing.T) {
			got, err := resolveManifestNode([]byte(manifest), tc.selector, tc.platform)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	for _, selector := range []string{"^30", "garbage", "lts/-4", "lts/unknown"} {
		_, err := resolveManifestNode([]byte(manifest), selector, "linux-x64")
		require.Error(t, err)
	}
}

func TestUnknownReplacementSetupClearsPriorRuntime(t *testing.T) {
	for _, replacement := range []string{
		"      - uses: actions/setup-node@v4\n        with: {node-version-file: missing.nvmrc}\n",
		"      - uses: actions/setup-node@v4\n        with: {node-version: '${{ inputs.runtime }}'}\n",
	} {
		workflow := strings.Replace(fmt.Sprintf(runtimeWorkflow, ""), "      - run: npm run test", replacement+"      - run: npm run test", 1)
		result := checkCIRuntimes(t.Context(), newJestRepository(t, workflow), nil, func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "6.17.0", Node: ">=22"}, nil
		})
		require.Equal(t, StatusInconclusive, result.Status)
		for _, job := range result.Jobs {
			require.Equal(t, CodeNodeResolution, job.Code)
			require.Empty(t, job.Node)
			require.Nil(t, job.NodeResolution)
		}
	}
}

func TestConfigurationInspectionUsesGitHubCIDefaultAndExplicitOverrides(t *testing.T) {
	for _, key := range []string{"CI", "GITHUB_ACTIONS"} {
		for _, tc := range []struct{ workflow, job, step, command, want string }{
			{"", "", "", "npm test", "true"},
			{"env: {CI: 'false'}\n", "", "", "npm test", "false"},
			{"env: {CI: 'false'}\n", "    env: {CI: job}\n", "", "npm test", "job"},
			{"", "", "        env: {CI: step}\n", "npm test", "step"},
			{"", "", "        env: {CI: step}\n", "CI=command jest", "command"},
		} {
			workflow := tc.workflow + "jobs:\n  tests:\n" + tc.job + "    steps:\n      - run: " + tc.command + "\n" + tc.step
			workflow = strings.ReplaceAll(workflow, "CI", key)
			root := newJestRepository(t, workflow)
			entry, err := JestConfigurationCommand(root, "")
			require.NoError(t, err)
			require.Equal(t, tc.want, entry.Environment[key])
		}
	}

}
func TestCIBareNodeExecutableNamesUseSetupRuntime(t *testing.T) {
	for _, name := range []string{"node", "node.exe"} {
		workflow := strings.Replace(fmt.Sprintf(runtimeWorkflow, ""), "npm run test", name+" node_modules/jest/bin/jest.js", 1)
		result := checkCIRuntimes(t.Context(), newJestRepository(t, workflow), nil, func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "5.132.0", Node: ">=18"}, nil
		})
		require.Equal(t, StatusCompatible, result.Status)
		for _, finding := range result.Jobs {
			require.Equal(t, StatusCompatible, finding.Status)
			require.NotEmpty(t, finding.Node)
			require.Empty(t, finding.Code)
		}
	}
}

func TestSkippedNonJavaScriptActionCannotExcludeJest(t *testing.T) {
	for _, language := range []string{"python", "ruby", "all"} {
		workflow := `jobs:
  valid:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '22.0.0'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.17.0'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
  skipped:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '22.0.0'}
      - uses: datadog/test-visibility-github-action@v3
        if: false
        with: {languages: ` + language + `, js-tracer-version: '6.17.0'}
      - run: npm test
`
		result := checkCIRuntimes(t.Context(), newJestRepository(t, workflow), nil, func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "6.17.0", Node: ">=22"}, nil
		})
		expectedStatus, expectedCode := StatusIncompatible, CodeMissingInstrumentation
		if language == "all" {
			expectedStatus, expectedCode = StatusCompatible, CodeExcludedRuntime
		}
		require.Equal(t, expectedStatus, result.Status)
		for _, finding := range result.Jobs {
			if finding.Job == "skipped" {
				require.Equal(t, expectedCode, finding.Code)
			}
		}
	}
}
