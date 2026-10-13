// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/tinylib/msgp/msgp"
)

const (
	BrowserCoverageFinding = "browser-coverage-unavailable"
	BrowserCoverageTitle   = "No coverage for Vitest browser projects"
	BrowserCoverageContext = "dd-trace-js disables Test Impact Analysis coverage for runs containing Vitest browser projects. This also affects Node projects in the same run."
)

// Browser dependencies alone do not prove Browser Mode ran. Require both an
// observed Vitest browser event and an explicitly coverage-disabled session.
func (s *Server) browserCoverageStatus(tests []testReference) (unavailable, expectedMissing bool, err error) {
	browserSessions := make(map[uint64]bool)
	disabledSessions := make(map[uint64]bool)
	for _, request := range s.Requests() {
		if request.Method != http.MethodPost || request.Path != constants.TestCycleURLPath {
			continue
		}
		body, err := uncompressRequestBody(request)
		if err != nil {
			return false, false, err
		}
		payload, _, err := msgp.ReadMapStrIntfBytes(body, nil)
		if err != nil {
			return false, false, fmt.Errorf("read Vitest browser coverage status: %w", err)
		}
		events, _ := payload["events"].([]any)
		shared, _ := payload["metadata"].(map[string]any)
		for _, value := range events {
			event, _ := value.(map[string]any)
			kind := text(event["type"])
			switch kind {
			case "test", "test_suite_end", "test_module_end", "test_session_end":
			default:
				continue
			}
			content, _ := event["content"].(map[string]any)
			id := unsigned(content["test_session_id"])
			if id == 0 {
				continue
			}
			meta := make(map[string]any)
			for _, source := range []any{shared["*"], shared[kind], content["meta"]} {
				if tags, ok := source.(map[string]any); ok {
					for key, value := range tags {
						meta[key] = value
					}
				}
			}
			if !strings.EqualFold(text(meta["test.framework"]), "vitest") {
				continue
			}
			if text(meta["test.type"]) == "browser" {
				browserSessions[id] = true
			}
			coverage := meta["test.code_coverage.enabled"]
			if (kind == "test_session_end" || kind == "test_module_end") && (coverage == false || coverage == "false") {
				disabledSessions[id] = true
			}
		}
	}
	for id := range browserSessions {
		if disabledSessions[id] {
			unavailable = true
		}
	}
	expectedMissing = unavailable && len(tests) > 0
	for _, test := range tests {
		if !browserSessions[test.sessionID] || !disabledSessions[test.sessionID] {
			expectedMissing = false
		}
	}
	return unavailable, expectedMissing, nil
}
