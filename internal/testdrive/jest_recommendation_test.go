// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"errors"
	"testing"

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
