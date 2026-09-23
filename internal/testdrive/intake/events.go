// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/ddtest/internal/constants"
)

type testReference struct {
	sessionID    uint64
	suiteID      uint64
	spanID       uint64
	module       string
	parameters   string
	name         string
	suite        string
	sourceFile   string
	sourceStart  int
	sourceEnd    int
	status       string
	finalStatus  string
	duration     time.Duration
	isRetry      bool
	retryReason  string
	errorType    string
	errorMessage string
	errorStack   string
}

type suiteReference struct {
	sessionID uint64
	suiteID   uint64
}

// TestEventCount returns the number of test events observed by the intake.
func (s *Server) TestEventCount() (int, error) {
	tests, err := s.testReferences()
	if err != nil {
		return 0, err
	}
	return len(tests), nil
}

func (s *Server) testReferences() ([]testReference, error) {
	var tests []testReference
	for requestIndex, request := range s.Requests() {
		if request.Method != http.MethodPost || request.Path != constants.TestCycleURLPath {
			continue
		}

		body, err := uncompressRequestBody(request)
		if err != nil {
			return nil, fmt.Errorf("uncompress request %d to %s: %w", requestIndex+1, constants.TestCycleURLPath, err)
		}
		requestTests, err := readTests(body)
		if err != nil {
			return nil, fmt.Errorf(
				"recognize test events in request %d to %s (content type %q, content encoding %q): %w",
				requestIndex+1,
				constants.TestCycleURLPath,
				request.Header.Get("Content-Type"),
				request.Header.Get("Content-Encoding"),
				err,
			)
		}
		tests = append(tests, requestTests...)
	}
	return tests, nil
}

func readTests(payload []byte) ([]testReference, error) {
	fieldCount, rest, err := msgp.ReadMapHeaderBytes(payload)
	if err != nil {
		return nil, err
	}

	var tests []testReference
	for range fieldCount {
		var field string
		field, rest, err = msgp.ReadStringBytes(rest)
		if err != nil {
			return nil, err
		}

		if field != "events" {
			rest, err = msgp.Skip(rest)
			if err != nil {
				return nil, err
			}
			continue
		}

		var requestTests []testReference
		requestTests, rest, err = readTestsInEventArray(rest)
		if err != nil {
			return nil, err
		}
		tests = append(tests, requestTests...)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("unexpected trailing MessagePack bytes")
	}
	return tests, nil
}

func readTestsInEventArray(payload []byte) ([]testReference, []byte, error) {
	eventCount, rest, err := msgp.ReadArrayHeaderBytes(payload)
	if err != nil {
		return nil, nil, err
	}

	var tests []testReference
	for range eventCount {
		var eventType string
		var reference testReference
		fieldCount, remaining, readErr := msgp.ReadMapHeaderBytes(rest)
		if readErr != nil {
			return nil, nil, readErr
		}
		rest = remaining

		for range fieldCount {
			var field string
			field, rest, readErr = msgp.ReadStringBytes(rest)
			if readErr != nil {
				return nil, nil, readErr
			}

			switch field {
			case "type":
				eventType, rest, readErr = msgp.ReadStringBytes(rest)
			case "content":
				reference, rest, readErr = readTestReference(rest)
			default:
				rest, readErr = msgp.Skip(rest)
			}
			if readErr != nil {
				return nil, nil, readErr
			}
		}
		if eventType == "test" {
			tests = append(tests, reference)
		}
	}
	return tests, rest, nil
}

func readTestReference(payload []byte) (testReference, []byte, error) {
	content, rest, err := msgp.ReadMapStrIntfBytes(payload, nil)
	if err != nil {
		return testReference{}, nil, err
	}

	reference := testReference{
		sessionID: unsigned(content["test_session_id"]),
		suiteID:   unsigned(content["test_suite_id"]),
		spanID:    unsigned(content["span_id"]),
		duration:  time.Duration(integer(content["duration"])),
	}
	if metadata, ok := content["meta"].(map[string]any); ok {
		reference.module = text(metadata["test.module"])
		reference.parameters = text(metadata["test.parameters"])
		reference.name = text(metadata["test.name"])
		reference.suite = text(metadata["test.suite"])
		reference.sourceFile = text(metadata["test.source.file"])
		reference.status = strings.ToLower(text(metadata["test.status"]))
		reference.finalStatus = strings.ToLower(text(metadata["test.final_status"]))
		if reference.status == "" {
			reference.status = reference.finalStatus
		}
		reference.isRetry = truthy(metadata["test.is_retry"])
		reference.retryReason = text(metadata["test.retry_reason"])
		reference.errorType = text(metadata["error.type"])
		reference.errorMessage = text(metadata["error.message"])
		reference.errorStack = text(metadata["error.stack"])
	}
	if metrics, ok := content["metrics"].(map[string]any); ok {
		reference.isRetry = reference.isRetry || truthy(metrics["test.is_retry"])
		reference.sourceStart = int(integer(metrics["test.source.start"]))
		reference.sourceEnd = int(integer(metrics["test.source.end"]))
	}
	return reference, rest, nil
}

func unsigned(value any) uint64 {
	switch value := value.(type) {
	case uint64:
		return value
	case int64:
		if value > 0 {
			return uint64(value)
		}
	}
	return 0
}

func integer(value any) int64 {
	switch value := value.(type) {
	case int64:
		return value
	case uint64:
		return int64(value)
	case float64:
		return int64(value)
	case float32:
		return int64(value)
	}
	return 0
}

func text(value any) string {
	valueText, _ := value.(string)
	return valueText
}

func truthy(value any) bool {
	switch value := value.(type) {
	case bool:
		return value
	case string:
		parsed, err := strconv.ParseBool(value)
		return err == nil && parsed
	default:
		return integer(value) != 0
	}
}
