// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

func TestTracerRecommendationMatchesFrameworkLocalNodeAndCIMatrix(t *testing.T) {
	resolve := func(_ context.Context, major string) (platform.JSSelection, error) {
		return map[string]platform.JSSelection{
			"5": {Version: "5.128.0", Node: ">=18"},
			"6": {Version: "6.17.0", Node: ">=22"},
		}[major], nil
	}
	for _, tc := range []struct {
		name, jest, node, runner string
		nodes                    []string
		want                     string
	}{
		{"i18next", "28.1.3", "24.14.1", "jest-circus/runner", []string{"14.x", "16.x", "18.x", "20.x", "20.x"}, "5.128.0"},
		{"newest breaks tie", "28.1.3", "24.14.1", "jest-circus/runner", []string{"22.x", "24.x"}, "6.17.0"},
		{"framework limits selection", "27.5.0", "24.14.1", "jest-circus/runner", []string{"24.x"}, "5.128.0"},
		{"local Node limits selection", "28.1.3", "20.10.0", "jest-circus/runner", nil, "5.128.0"},
		{"unsupported CI", "28.1.3", "24.14.1", "jest-circus/runner", []string{"14.x"}, ""},
		{"unsupported runner", "28.1.3", "24.14.1", "jest-jasmine2", []string{"24.x"}, ""},
		{"unresolved CI", "28.1.3", "24.14.1", "jest-circus/runner", []string{"lts/*"}, "6.17.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ci := &onboard.RuntimeCheck{}
			for _, node := range tc.nodes {
				ci.Jobs = append(ci.Jobs, onboard.RuntimeFinding{Node: node})
			}
			check := jestPreflight{Version: tc.jest, Node: tc.node, Projects: []jestProject{{Runner: tc.runner}}}
			got, notes := recommendJavaScriptTracer(t.Context(), check, ci, resolve, "jest")
			if tc.want == "" {
				require.Nil(t, got)
				require.NotEmpty(t, notes)
				return
			}
			require.Empty(t, notes)
			require.Equal(t, tc.want, got.Selection.Version)
			if tc.name == "i18next" {
				require.Equal(t, []string{"18.x", "20.x"}, got.Compatible)
				require.Equal(t, []string{"14.x", "16.x"}, got.Unsupported)
			}
			if tc.name == "unresolved CI" {
				require.Empty(t, got.Compatible)
				require.Equal(t, []string{"lts/*"}, got.Unresolved)
			}
		})
	}
}

func TestTracerRecommendationReportsMetadataFailure(t *testing.T) {
	got, notes := recommendJavaScriptTracer(t.Context(), jestPreflight{}, nil, func(context.Context, string) (platform.JSSelection, error) {
		return platform.JSSelection{}, errors.New("registry unavailable")
	}, "jest")
	require.Nil(t, got)
	require.Len(t, notes, 3)
	require.Contains(t, notes[0], "registry unavailable")
}

func TestVitestRecommendationCoversOlderCIWithoutChangingSelection(t *testing.T) {
	for _, excluded := range []bool{false, true} {
		drive := preparedTestdrive(t)
		drive.framework = framework.NewVitest()
		drive.resolveJSTracer = func(_ context.Context, major string) (platform.JSSelection, error) {
			if major == "5" {
				return platform.JSSelection{Version: "5.129.0", Node: ">=18"}, nil
			}
			return platform.JSSelection{Version: "6.18.0", Node: ">=22"}, nil
		}
		ci := &onboard.RuntimeCheck{Status: "incompatible", Jobs: []onboard.RuntimeFinding{{Node: "20.x"}, {Node: "22.x"}}}
		if excluded {
			ci.Status = "compatible"
			ci.Jobs[0].Status = "excluded"
			ci.Jobs[0].Code = "excluded_runtime"
		}
		result := validationResult{CIRuntime: ci, Selection: &platform.JSSelection{Version: "6.18.0", Source: "project"}, Preflight: &jestPreflight{Node: "24.14.1", Version: "4.0.1", VitestProjects: []vitestProject{{Pool: "forks"}}, Verdict: verdict{Status: "compatible"}}}
		var output bytes.Buffer
		drive.recommendTracer(t.Context(), &output, &result)
		require.NotNil(t, result.Preflight.Recommendation)
		require.Equal(t, "5.129.0", result.Preflight.Recommendation.Selection.Version)
		require.Equal(t, []string{"20.x", "22.x"}, result.Preflight.Recommendation.Compatible)
		require.Equal(t, "6.18.0", result.Selection.Version)
		require.Equal(t, "project", result.Selection.Source)
		require.Contains(t, output.String(), "before excluding runtimes")
	}
}

func TestVitestRecommendationRespectsFeaturePrerequisitesAndUnknownRuntimes(t *testing.T) {
	check := jestPreflight{Node: "24.14.1", Version: "4.0.1", VitestProjects: []vitestProject{{Pool: "forks"}}}
	ci := &onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{Node: "20"}, {Node: "22"}, {Node: "lts/*"}}}
	got, _ := recommendJavaScriptTracer(t.Context(), check, ci, func(_ context.Context, major string) (platform.JSSelection, error) {
		if major == "5" {
			return platform.JSSelection{Version: "5.119.0", Node: ">=18"}, nil
		}
		return platform.JSSelection{Version: "6.18.0", Node: ">=22"}, nil
	}, "vitest")
	require.Equal(t, "6.18.0", got.Selection.Version, "older tracer lacks Vitest skipping")
	require.Equal(t, []string{"20"}, got.Unsupported)
	require.Equal(t, []string{"lts/*"}, got.Unresolved)
}
