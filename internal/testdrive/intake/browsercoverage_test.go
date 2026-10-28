// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"net/http"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"
)

func TestBrowserCoverageLimitationRequiresSessionEvidence(t *testing.T) {
	event := func(kind string, session uint64, meta map[string]any) map[string]any {
		return map[string]any{"type": kind, "content": map[string]any{"test_session_id": session, "meta": meta}}
	}
	test := event("test", 1, map[string]any{"test.name": "Node test", "test.status": "pass"})
	browser := event("test_suite_end", 1, map[string]any{"test.framework": "vitest", "test.type": "browser"})
	disabled := event("test_session_end", 1, map[string]any{"test.framework": "vitest", "test.code_coverage.enabled": "false"})
	for _, tc := range []struct {
		name                        string
		events                      []any
		browserInfo, missingWarning bool
	}{
		{"mixed Node and browser run", []any{test, browser, disabled}, true, false},
		{"browser suite fails before tests", []any{browser, disabled}, true, false},
		{"no browser evidence", []any{test, disabled}, false, true},
		{"coverage status unknown", []any{test, browser}, false, true},
		{"browser coverage enabled", []any{test, browser, event("test_session_end", 1, map[string]any{"test.framework": "vitest", "test.code_coverage.enabled": "true"})}, false, true},
		{"unrelated session disables coverage", []any{test, browser, event("test_session_end", 2, map[string]any{"test.framework": "vitest", "test.code_coverage.enabled": false})}, false, true},
		{"other browser framework", []any{test, event("test_suite_end", 1, map[string]any{"test.framework": "playwright", "test.type": "browser"}), disabled}, false, true},
		{"unrelated Node session still needs a warning", []any{test, browser, disabled, event("test", 2, map[string]any{"test.name": "other Node run", "test.status": "pass"})}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Separate intake requests and event order must not affect classification.
			server := &Server{}
			for _, e := range tc.events {
				payload, err := msgp.AppendIntf(nil, map[string]any{"events": []any{e}})
				require.NoError(t, err)
				server.requests = append([]RawRequest{{Method: http.MethodPost, Path: constants.TestCycleURLPath, Body: payload}}, server.requests...)
			}
			facts, err := server.Facts()
			require.NoError(t, err)
			require.Equal(t, tc.browserInfo, facts.BrowserCoverageUnavailable)
			require.Equal(t, tc.missingWarning, facts.MissingCoverage)
		})
	}
}

func TestBrowserCoverageRecognizesSharedFrameworkMetadata(t *testing.T) {
	payload, err := msgp.AppendIntf(nil, map[string]any{
		"metadata": map[string]any{"*": map[string]any{"test.framework": "vitest"}},
		"events": []any{
			map[string]any{"type": "test_suite_end", "content": map[string]any{"test_session_id": uint64(1), "meta": map[string]any{"test.type": "browser"}}},
			map[string]any{"type": "test_session_end", "content": map[string]any{"test_session_id": uint64(1), "meta": map[string]any{"test.code_coverage.enabled": false}}},
		},
	})
	require.NoError(t, err)
	server := &Server{requests: []RawRequest{{Method: http.MethodPost, Path: constants.TestCycleURLPath, Body: payload}}}
	facts, err := server.Facts()
	require.NoError(t, err)
	require.True(t, facts.BrowserCoverageUnavailable)
	advice := Troubleshoot(t.TempDir(), "Vitest", facts, CoverageSetup{})
	require.Len(t, advice, 1)
	require.Equal(t, BrowserCoverageFinding, advice[0].Finding)
	require.Contains(t, advice[0].Text, "--vitest-config")
	require.NotContains(t, advice[0].Text, "--command")
}
