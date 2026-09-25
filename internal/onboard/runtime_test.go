// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const runtimeWorkflow = `jobs:
  tests:
    strategy:
      matrix:
        node-version: [20.20.1, 22.22.1, 24.14.0, 25.8.1]
    steps:
      - uses: actions/setup-node@v3
        with:
          node-version: ${{ matrix.node-version }}
      - uses: datadog/test-visibility-github-action@v3
        %s
        with:
          languages: js
      - run: npm run test
        env: {NODE_OPTIONS: "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"}
`

func TestCIRuntimesCatchLuxonRegressionAndRespectExclusion(t *testing.T) {
	for _, tc := range []struct{ name, guard, status string }{
		{"unconditional instrumentation", "", "incompatible"},
		{"preserve Node 20 tests", "if: ${{ !startsWith(matrix.node-version, '20.') }}", "compatible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workflow := fmt.Sprintf(runtimeWorkflow, tc.guard)
			root := newJestRepository(t, workflow)
			calls := 0
			result := checkCIRuntimes(t.Context(), root, nil, func(_ context.Context, action, version string) (tracerRequirement, error) {
				calls++
				require.Equal(t, githubAction+"@v3", action)
				require.Empty(t, version)
				return tracerRequirement{Version: "6.16.0", Node: ">=22"}, nil
			})
			require.Equal(t, tc.status, result.Status)
			require.Len(t, result.Jobs, 4)
			require.Equal(t, 1, calls)
			require.Equal(t, "20.20.1", result.Jobs[0].Node)
			if tc.status == "incompatible" {
				require.Equal(t, "incompatible", result.Jobs[0].Status)
				require.Equal(t, "dd-trace@6.16.0", result.Jobs[0].Tracer)
				require.Equal(t, ">=22", result.Jobs[0].Requirement)
			} else {
				require.Equal(t, "excluded", result.Jobs[0].Status)
			}
			data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
			require.NoError(t, err)
			require.Equal(t, workflow, string(data))
		})
	}
}

func TestCIRuntimesNeverGuessUnknownConfigurations(t *testing.T) {
	original := fmt.Sprintf(runtimeWorkflow, "")
	for _, tc := range []struct{ name, old, new, reason string }{
		{"dynamic matrix", "node-version: [20.20.1, 22.22.1, 24.14.0, 25.8.1]", "${{ fromJSON(needs.build.outputs.matrix) }}", "dynamic CI matrix"},
		{"dynamic version", "node-version: ${{ matrix.node-version }}", "node-version: ${{ inputs.node }}", "cannot statically resolve"},
		{"version file", "node-version: ${{ matrix.node-version }}", "node-version-file: .nvmrc", "missing or dynamic"},
		{"lts alias", "node-version: ${{ matrix.node-version }}", "node-version: lts/*", "missing or dynamic"},
		{"unknown condition", "with:\n          languages: js", "if: ${{ github.event_name == 'push' }}\n        with:\n          languages: js", "cannot statically resolve CI condition"},
		{"no action", "uses: datadog/test-visibility-github-action@v3", "uses: actions/checkout@v3", "No Datadog JavaScript action"},
		{"other language", "languages: js", "languages: python", "No Datadog JavaScript action"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := checkCIRuntimes(t.Context(), newJestRepository(t, strings.Replace(original, tc.old, tc.new, 1)), nil, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.16.0", Node: ">=22"}, nil
			})
			require.Equal(t, "inconclusive", result.Status)
			require.Contains(t, result.Jobs[0].Reason, tc.reason)
		})
	}
	result := checkCIRuntimes(t.Context(), newJestRepository(t, original), nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{}, fmt.Errorf("metadata unavailable")
	})
	require.Equal(t, "inconclusive", result.Status)
	require.Contains(t, result.Jobs[0].Reason, "metadata unavailable")
	result = checkCIRuntimes(t.Context(), t.TempDir(), nil, nil)
	require.Equal(t, "not applicable", result.Status)
}

func TestCIRuntimesUseSelectedTracerAndEveryJob(t *testing.T) {
	workflow := `jobs:
  old:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '20'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: 5.99.0}
      - run: npm test
        env: {NODE_OPTIONS: "-r ${{ env.DD_TRACE_PACKAGE }}"}
  new:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '22'}
      - uses: datadog/test-visibility-github-action@v4
        with: {languages: js}
      - run: npm test
        env: {NODE_OPTIONS: "-r ${{ env.DD_TRACE_PACKAGE }}"}
`
	result := checkCIRuntimes(t.Context(), newJestRepository(t, workflow), nil, func(_ context.Context, action, version string) (tracerRequirement, error) {
		if version == "5.99.0" {
			return tracerRequirement{Version: version, Node: ">=18"}, nil
		}
		require.Equal(t, githubAction+"@v4", action)
		return tracerRequirement{Version: "7.0.0", Node: ">=24"}, nil
	})
	require.Equal(t, "incompatible", result.Status)
	require.Len(t, result.Jobs, 2)
	require.Equal(t, "incompatible", result.Jobs[0].Status)
	require.Equal(t, "compatible", result.Jobs[1].Status)
}

func TestNodeRequirements(t *testing.T) {
	for _, tc := range []struct{ node, engine, status string }{
		{"20.20.1", ">=22", "incompatible"}, {"20", ">=22", "incompatible"}, {"20.x", ">=22", "incompatible"},
		{"22", ">=22", "compatible"}, {"v22.0.0", ">=22", "compatible"}, {"24.1.2", ">=22", "compatible"},
		{"22", ">=24", "incompatible"}, {"22", ">=22.2.0", "inconclusive"}, {"22.1", ">=22.2.0", "incompatible"},
		{"22.2", ">=22.2.0", "compatible"}, {"lts/*", ">=22", "inconclusive"}, {"", ">=22", "inconclusive"},
		{"22", ">=18 <23", "inconclusive"}, {"22", "", "inconclusive"}, {"22", ">=22 || >=24", "inconclusive"},
		{"22.x.3", ">=22", "inconclusive"},
	} {
		t.Run(tc.node+"/"+tc.engine, func(t *testing.T) {
			status, _ := CompareNodeRequirement(tc.node, tc.engine)
			require.Equal(t, tc.status, status)
		})
	}
}

func TestRuntimeMatrixAndConditions(t *testing.T) {
	rows, err := runtimeMatrix(map[string]any{"node": []any{"20", "22"}, "os": []any{"linux", "windows"}})
	require.NoError(t, err)
	require.Len(t, rows, 4)
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", true}, {"false", false}, {"matrix.node == '22'", true}, {"${{ matrix.node != '20' }}", true},
		{"${{ startsWith(matrix.node, '2') }}", true}, {"${{ !startsWith(matrix.node, '2') }}", false},
	} {
		actual, err := runtimeCondition(tc.value, map[string]any{"node": "22"})
		require.NoError(t, err)
		require.Equal(t, tc.want, actual)
	}
	_, err = runtimeCondition("matrix.missing != '20'", map[string]any{"node": "22"})
	require.Error(t, err)
	_, err = runtimeMatrix(map[string]any{"node": []any{"${{ inputs.node }}"}})
	require.Error(t, err)
	_, err = runtimeMatrix(map[string]any{"node": make([]any, 257)})
	require.Error(t, err)
}

type runtimeTransport func(*http.Request) (*http.Response, error)

func (f runtimeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRuntimeMetadataUsesActionRefDefaultAndExplicitOverride(t *testing.T) {
	for _, explicit := range []string{"", "5.99.0"} {
		t.Run(explicit, func(t *testing.T) {
			var addresses []string
			client := &http.Client{Transport: runtimeTransport(func(r *http.Request) (*http.Response, error) {
				addresses = append(addresses, r.URL.String())
				body := `{"version":"6.16.0","engines":{"node":">=22"}}`
				if r.URL.Host == "raw.githubusercontent.com" {
					body = "inputs:\n  js-tracer-version:\n    default: '6.16.0'\n"
				}
				if strings.HasSuffix(r.URL.Path, "/5.99.0") {
					body = `{"version":"5.99.0","engines":{"node":">=18"}}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			requirement, err := resolveRequirement(t.Context(), client, githubAction+"@abc123", explicit)
			require.NoError(t, err)
			if explicit == "" {
				require.Equal(t, []string{"https://raw.githubusercontent.com/DataDog/test-visibility-github-action/abc123/action.yml", "https://registry.npmjs.org/dd-trace/6.16.0"}, addresses)
				require.Equal(t, ">=22", requirement.Node)
			} else {
				require.Equal(t, []string{"https://registry.npmjs.org/dd-trace/5.99.0"}, addresses)
				require.Equal(t, ">=18", requirement.Node)
			}
		})
	}
}

func TestRuntimeMetadataErrorsAreNotCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"not found", "", 404}, {"malformed", "not json", 200}, {"missing engines", `{"version":"6.16.0"}`, 200}, {"oversized", strings.Repeat("x", (1<<20)+1), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: runtimeTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			_, err := resolveRequirement(t.Context(), client, githubAction+"@v3", "6.16.0")
			require.Error(t, err)
		})
	}
}

func TestExplicitEmptyTracerInputOverridesActionDefault(t *testing.T) {
	workflow := strings.Replace(fmt.Sprintf(runtimeWorkflow, ""), "languages: js", "languages: js\n          js-tracer-version: ''", 1)
	result := checkCIRuntimes(t.Context(), newJestRepository(t, workflow), nil, func(_ context.Context, _ string, version string) (tracerRequirement, error) {
		require.Equal(t, "latest", version)
		return tracerRequirement{Version: "6.16.0", Node: ">=22", Requested: version}, nil
	})
	require.Equal(t, "incompatible", result.Status)
	require.Equal(t, "latest", result.Jobs[0].TracerRequested)
	require.True(t, result.Jobs[0].TracerFloating)
}
