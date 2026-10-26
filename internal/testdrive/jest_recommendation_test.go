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
			"5.128.0": {Version: "5.128.0", Node: ">=18"},
			"6.17.0":  {Version: "6.17.0", Node: ">=22"},
		}[major], nil
	}
	for _, tc := range []struct {
		name, jest, node, runner string
		nodes                    []string
		want                     string
	}{
		{"older CI nodes", "28.1.3", "24.14.1", "jest-circus/runner", []string{"14.x", "16.x", "18.x", "20.x", "20.x"}, "5.128.0"},
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
			got, notes := recommendJavaScriptTracer(t.Context(), check, ci, resolve, recommendationTags, "jest")
			if tc.want == "" {
				require.Nil(t, got)
				require.NotEmpty(t, notes)
				return
			}
			require.Empty(t, notes)
			require.Equal(t, tc.want, got.Selection.Version)
			if tc.name == "older CI nodes" {
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
	}, recommendationTags, "jest")
	require.Nil(t, got)
	require.Len(t, notes, 3)
	require.Contains(t, notes[0], "registry unavailable")
}

func recommendationTags(context.Context) (map[string]string, error) {
	return map[string]string{"latest": "6.17.0", "latest-node18": "5.128.0"}, nil
}

func TestMochaRecommendationRejectsObsoleteTracerEvenWithLargerNodeCoverage(t *testing.T) {
	releases := map[string]platform.JSSelection{
		"0.36.6":  {Version: "0.36.6", Node: ">=8"},
		"5.132.0": {Version: "5.132.0", Node: ">=18"},
		"6.20.0":  {Version: "6.20.0", Node: ">=22"},
	}
	resolve := func(_ context.Context, selector string) (platform.JSSelection, error) { return releases[selector], nil }
	tags := func(context.Context) (map[string]string, error) {
		return map[string]string{"latest": "6.20.0", "latest-node18": "5.132.0", "latest-node8": "0.36.6"}, nil
	}
	ci := &onboard.RuntimeCheck{}
	for _, node := range []string{"8.17", "10.24", "12.22", "18.14", "20.12", "22.0"} {
		ci.Jobs = append(ci.Jobs, onboard.RuntimeFinding{Node: node})
	}
	got, notes := recommendJavaScriptTracer(t.Context(), jestPreflight{Version: "9.2.2", Node: "24.19.0"}, ci, resolve, tags, "mocha")
	require.Empty(t, notes)
	require.NotNil(t, got)
	require.Equal(t, "5.132.0", got.Selection.Version)
	for _, version := range []string{"0.36.6", "4.55.0", "7.0.0"} {
		check := checkMochaSupport(jestPreflight{Version: "9.2.2", Node: "24.19.0"}, platform.JSSelection{Version: version, Node: ">=8"})
		require.Equal(t, "unverified_tracer_major", check.Code)
		require.NotEqual(t, "compatible", check.Status)
	}
}

func TestTracerRecommendationNamesTheCoordinatorValidationCommand(t *testing.T) {
	run := &Testdrive{framework: &framework.Jest{}, resolveJSTags: recommendationTags, resolveJSTracer: func(_ context.Context, major string) (platform.JSSelection, error) {
		return map[string]platform.JSSelection{"5.128.0": {Version: "5.128.0", Node: ">=18"}, "6.17.0": {Version: "6.17.0", Node: ">=22"}}[major], nil
	}}
	result := &validationResult{
		Preflight: &jestPreflight{Version: "28.1.3", Node: "24.14.1", Projects: []jestProject{{Runner: "jest-circus/runner"}}, Verdict: verdict{Status: onboard.StatusCompatible}},
		Selection: &platform.JSSelection{Requested: "latest", Version: "6.17.0"},
		CIRuntime: &onboard.RuntimeCheck{Status: "incompatible", Jobs: []onboard.RuntimeFinding{{Node: "20.x"}}},
	}
	var output bytes.Buffer
	run.recommendTracer(t.Context(), &output, result)
	require.Contains(t, output.String(), "Use js-tracer-version: 5.128.0 in CI, then run full validation through the agent workflow: ")
	require.Contains(t, output.String(), "testdrive --framework jest --all --agent --tracer-version 5.128.0 --yes")
}
