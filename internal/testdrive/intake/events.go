// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
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

// Event retains the identity and feature tags needed to verify a scenario,
// including suite events when the tracer skips an entire Jest file.
type Event struct {
	Type string            `json:"type"`
	Tags map[string]string `json:"tags"`
}

// collectEventFacts reads only effective version tags and known capability
// states for ordinary runs. Only explicit probe collection copies full tags.
func (s *Server) collectEventFacts(facts *Facts) error {
	versions := []struct {
		key    string
		seen   map[string]struct{}
		output *[]string
	}{
		{"runtime.version", make(map[string]struct{}), &facts.RuntimeVersions},
		{"test.framework_version", make(map[string]struct{}), &facts.FrameworkVersions},
		{"library_version", make(map[string]struct{}), &facts.TracerVersions},
	}
	for _, request := range s.Requests() {
		if request.Method != http.MethodPost || request.Path != constants.TestCycleURLPath {
			continue
		}
		body, err := uncompressRequestBody(request)
		if err != nil {
			return err
		}
		payload, _, err := msgp.ReadMapStrIntfBytes(body, nil)
		if err != nil {
			return err
		}
		values, _ := payload["events"].([]any)
		shared, _ := payload["metadata"].(map[string]any)
		for _, value := range values {
			event, _ := value.(map[string]any)
			content, _ := event["content"].(map[string]any)
			metadata, _ := content["meta"].(map[string]any)
			eventType := text(event["type"])
			common, _ := shared["*"].(map[string]any)
			typed, _ := shared[eventType].(map[string]any)
			sources := []map[string]any{common, typed, metadata}
			for _, field := range versions {
				if version, _ := effectiveStringTag(field.key, sources); version != "" {
					field.seen[version] = struct{}{}
				}
			}
			if eventType == "test" {
				for _, name := range []string{"auto_test_retries", "early_flake_detection", "test_impact_analysis", "test_management.quarantine", "test_management.disable", "test_management.attempt_to_fix"} {
					value, advertised := effectiveStringTag("_dd.library_capabilities."+name, sources)
					if !advertised {
						continue
					}
					observed := "unknown"
					if version, err := strconv.Atoi(value); err == nil && version >= 0 {
						observed = "available"
						if version == 0 {
							observed = "unavailable"
						}
					}
					if facts.LibraryCapabilities == nil {
						facts.LibraryCapabilities = make(map[string]string)
					}
					state := facts.LibraryCapabilities[name]
					if state == "" || state == "unknown" {
						facts.LibraryCapabilities[name] = observed
					} else if observed != "unknown" && state != observed {
						facts.LibraryCapabilities[name] = "mixed"
					}
				}
			}
			if s.collectEvents {
				tags := make(map[string]string)
				for _, source := range sources {
					for key, value := range source {
						if v, ok := value.(string); ok {
							tags[key] = v
						}
					}
				}
				facts.Events = append(facts.Events, Event{Type: eventType, Tags: tags})
			}
		}
	}
	for _, field := range versions {
		*field.output = slices.Sorted(maps.Keys(field.seen))
	}
	return nil
}

// Match event-tag precedence without copying unrelated customer metadata.
// A non-string value does not replace an inherited string tag.
func effectiveStringTag(key string, sources []map[string]any) (string, bool) {
	for i := len(sources) - 1; i >= 0; i-- {
		if value, ok := sources[i][key].(string); ok {
			return value, true
		}
	}
	return "", false
}
