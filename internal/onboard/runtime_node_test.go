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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const ltsManifestFixture = `[
 {"version":"22.4.1","stable":true,"lts":"Jod"},
 {"version":"26.0.0","stable":true},
 {"version":"24.3.0","stable":true,"lts":"Krypton"},
 {"version":"22.8.0","stable":true,"lts":"Jod"},
 {"version":"28.0.0","stable":false,"lts":"Future"}
]`

func TestLTSMajorResolution(t *testing.T) {
	for _, tc := range []struct{ alias, version string }{
		{"lts/*", "24"}, {"lts/-0", "24"}, {"lts/-1", "22"}, {"lts/Jod", "22"},
		{"lts/krypton", "24"}, {"lts/-2", ""}, {"lts/unknown", ""}, {"lts/-", ""}, {"lts/--1", ""},
	} {
		t.Run(tc.alias, func(t *testing.T) {
			version, err := resolveLTSMajor([]byte(ltsManifestFixture), tc.alias)
			if tc.version == "" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.version, version)
			}
		})
	}
	for _, data := range []string{"invalid json", "[]", `[{"stable":true,"lts":"Bad","version":"24.x"}]`} {
		_, err := resolveLTSMajor([]byte(data), "lts/*")
		require.Error(t, err)
	}
}

func TestCILTSResolutionPreservesAliasProvenanceAndUnknownPatch(t *testing.T) {
	for _, tc := range []struct{ requirement, status string }{
		{">=22", "compatible"}, {">=26", "incompatible"}, {">=24.2.0", "inconclusive"},
	} {
		t.Run(tc.requirement, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: runtimeTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, nodeVersionsManifest, request.URL.String())
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(ltsManifestFixture))}, nil
			})}
			workflow := strings.Replace(fmt.Sprintf(runtimeWorkflow, ""), "node-version: ${{ matrix.node-version }}", "node-version: lts/*", 1)
			result := checkCIRuntimes(t.Context(), newJestRepository(t, workflow), setupNodeResolver(client), func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.16.0", Node: tc.requirement}, nil
			})
			require.Equal(t, tc.status, result.Status)
			require.Equal(t, 1, calls)
			require.Len(t, result.Jobs, 4)
			for _, job := range result.Jobs {
				require.Equal(t, "lts/*", job.Node)
				require.Equal(t, "24", job.NodeResolution.Version)
				require.Equal(t, nodeVersionsManifest, job.NodeResolution.Source)
				require.False(t, job.NodeResolution.ResolvedAt.IsZero())
			}
		})
	}
}

func TestUnavailableLTSMetadataStaysInconclusive(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: runtimeTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("metadata unavailable")
	})}
	workflow := strings.Replace(fmt.Sprintf(runtimeWorkflow, ""), "node-version: ${{ matrix.node-version }}", "node-version: lts/*", 1)
	result := checkCIRuntimes(t.Context(), newJestRepository(t, workflow), setupNodeResolver(client), func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.16.0", Node: ">=22"}, nil
	})
	require.Equal(t, "inconclusive", result.Status)
	require.Equal(t, 1, calls)
	for _, job := range result.Jobs {
		require.Nil(t, job.NodeResolution)
		require.Contains(t, job.Reason, "metadata unavailable")
	}
}
