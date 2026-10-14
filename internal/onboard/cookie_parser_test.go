// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func cookieParserRepository(t *testing.T) string {
	t.Helper()
	workflow, err := os.ReadFile("testdata/cookie-parser-ci.yml")
	require.NoError(t, err)
	root := newJestRepository(t, string(workflow))
	manifest, err := os.ReadFile("testdata/cookie-parser-package.json")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), manifest, 0600))
	return root
}

func TestCookieParserScopeAndNVMRuntime(t *testing.T) {
	root := cookieParserRepository(t)
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	for _, entry := range scope.Commands {
		require.Empty(t, entry.UnvalidatedSetup)
		require.Len(t, entry.Locations, 25)
	}
	result := checkCIRuntimes(t.Context(), root, nil, nil, "mocha")
	require.Equal(t, "incompatible", result.Status, result.Jobs)
	require.Len(t, result.Jobs, 25)
	for _, job := range result.Jobs {
		require.Equal(t, "missing_instrumentation", job.Code, job)
		require.NotEmpty(t, job.Node, job)
	}
}

func TestCookieParserOnboardedCI(t *testing.T) {
	for _, style := range []string{"matrix-metadata", "job-environment", "inherited-options"} {
		t.Run(style, func(t *testing.T) {
			root := cookieParserRepository(t)
			path := filepath.Join(root, ".github/workflows/test.yml")
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			workflow := string(data)
			condition := `contains(fromJSON(env.DD_INSTRUMENTED_NODE), matrix.node-version)`
			if style == "job-environment" {
				workflow = strings.Replace(workflow, "    runs-on: ubuntu-latest", "    runs-on: ubuntu-latest\n    env:\n      DD_INSTRUMENTED_NODE: '[\"18.14\",\"19.6\",\"20.12\",\"21.7\",\"22.0\"]'", 1)
			} else {
				condition = "matrix.dd-instrument == true"
				for _, version := range []string{"18.14", "19.6", "20.12", "21.7", "22.0"} {
					workflow = strings.Replace(workflow, `node-version: "`+version+`"`, `node-version: "`+version+`"`+"\n          dd-instrument: true", 1)
				}
			}
			workflow = strings.Replace(workflow, "    - name: Run tests\n      shell: bash", "    - uses: datadog/test-visibility-github-action@v3\n      if: "+condition+"\n      with: {languages: js, js-tracer-version: '5.131.1'}\n    - name: Run tests\n      shell: bash\n      env:\n        NODE_OPTIONS: ${{ "+condition+" && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}\n        DD_CIVISIBILITY_ENABLED: ${{ "+condition+" && 'true' || 'false' }}", 1)
			if style == "inherited-options" {
				workflow = strings.Replace(workflow, "    runs-on: ubuntu-latest", "    runs-on: ubuntu-latest\n    env: {NODE_OPTIONS: '--max-old-space-size=2048'}", 1)
				workflow = strings.Replace(workflow, "NODE_OPTIONS: ${{ "+condition, "NODE_OPTIONS: ${{ env.NODE_OPTIONS }} ${{ "+condition, 1)
			}
			require.NoError(t, os.WriteFile(path, []byte(workflow), 0600))
			scope, err := DiscoverValidationScope(root, "mocha")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "5.131.1", Node: ">=18"}, nil
			}, "mocha")
			// The fallback npm test branch has no NYC wrapper. Runtime selection
			// and exclusions still work, but its CI coverage setup needs review.
			require.Equal(t, "inconclusive", result.Status, result.Jobs)
			inconclusive, excluded := 0, 0
			for _, job := range result.Jobs {
				if job.Status == "inconclusive" {
					require.Contains(t, job.Reason, "NYC")
					require.Contains(t, job.Reason, "npm test")
					inconclusive++
				}
				if job.Status == "excluded" {
					excluded++
				}
			}
			require.Equal(t, 5, inconclusive)
			require.Equal(t, 20, excluded)
		})
	}
}

func TestCookieParserSetupRejectsOpaqueCommands(t *testing.T) {
	root := cookieParserRepository(t)
	for _, command := range []string{`nvm install --default 22 && node hidden-tests.js`, `echo "$(npm test)"`, `if npm -ps ls nyc | grep -q nyc; then npm test; else node hidden-tests.js; fi`, `nvm install 22; nvm use 18`, `sed -i s/foo/bar/ test.js`} {
		require.Empty(t, nvmRuntime(command))
	}
	for _, shell := range []string{"bash -c {0}", "bash -o custom {0}", "bash pipefail {0}", "python {0}"} {
		require.False(t, supportedCIShell(shell))
	}
	for _, command := range []string{`echo "$(npm test)"`, `nvm install 22; nvm use 18`, `if npm -ps ls nyc | grep -q nyc; then npm test; else node hidden-tests.js; fi`} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "mocha")
		require.True(t, result.Matched || result.Reason != "")
		require.False(t, result.Review)
	}
	_, err := interpolateMatrix(`npm install ${{ matrix.package }}`, map[string]any{"package": "x; npm test"})
	require.Error(t, err)
}

func TestMatrixDiagnosticsRetainCausesAndScope(t *testing.T) {
	result := groupMatrixDiagnostics([]string{
		"ci.yml / test map[node:18] / step 2 (opaque): custom setup needs review",
		"ci.yml / test map[node:20] / step 2 (opaque): custom setup needs review",
		"ci.yml / test map[node:22] / step 2 (opaque): custom setup needs review",
		"ci.yml / test map[node:22] / step 3 (other): different cause",
	})
	require.Len(t, result, 2)
	require.Contains(t, result[0], "custom setup needs review")
	require.Contains(t, result[0], "affects 3 matrix entries")
	require.Contains(t, result[1], "different cause")
}
