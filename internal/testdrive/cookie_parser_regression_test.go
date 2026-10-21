// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

func TestCookieParserRecommendationUsesDiscoveredNVMMatrix(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	for target, source := range map[string]string{".github/workflows/ci.yml": "cookie-parser-ci.yml", "package.json": "cookie-parser-package.json"} {
		data, err := os.ReadFile(filepath.Join("../onboard/testdata", source))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(root, target), data, 0600))
	}
	// This uninstrumented fixture needs no network metadata: all test consumers
	// must first report missing instrumentation while retaining their NVM version.
	ci := onboard.CheckCIRuntimes(t.Context(), root, "mocha")
	require.Equal(t, "incompatible", ci.Status)
	recommendation, notes := recommendJavaScriptTracer(t.Context(), jestPreflight{Version: "9.2.2", Node: "24.14.1"}, &ci, func(_ context.Context, major string) (platform.JSSelection, error) {
		if major == "5" {
			return platform.JSSelection{Version: "5.131.1", Node: ">=18"}, nil
		}
		return platform.JSSelection{Version: "6.20.1", Node: ">=22"}, nil
	}, "mocha")
	require.Empty(t, notes)
	require.NotNil(t, recommendation)
	require.Equal(t, "5.131.1", recommendation.Selection.Version)
	require.Equal(t, []string{"18.14", "19.6", "20.12", "21.7", "22.0"}, recommendation.Compatible)
	require.Len(t, recommendation.Unsupported, 20)
	require.Empty(t, recommendation.Unresolved)
}
