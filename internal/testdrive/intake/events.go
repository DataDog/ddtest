// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"fmt"
	"net/http"

	"github.com/tinylib/msgp/msgp"
)

const testCyclePath = "/api/v2/citestcycle"

type testReference struct {
	sessionID uint64
	suiteID   uint64
	spanID    uint64
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
	for _, request := range s.Requests() {
		if request.Method != http.MethodPost || request.Path != testCyclePath {
			continue
		}

		requestTests, err := readTests(request.Body)
		if err != nil {
			return nil, fmt.Errorf("recognize test events in %s: %w", testCyclePath, err)
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
	fieldCount, rest, err := msgp.ReadMapHeaderBytes(payload)
	if err != nil {
		return testReference{}, nil, err
	}

	var reference testReference
	for range fieldCount {
		var field string
		field, rest, err = msgp.ReadStringBytes(rest)
		if err != nil {
			return testReference{}, nil, err
		}

		switch field {
		case "test_session_id":
			reference.sessionID, rest, err = msgp.ReadUint64Bytes(rest)
		case "test_suite_id":
			reference.suiteID, rest, err = msgp.ReadUint64Bytes(rest)
		case "span_id":
			reference.spanID, rest, err = msgp.ReadUint64Bytes(rest)
		default:
			rest, err = msgp.Skip(rest)
		}
		if err != nil {
			return testReference{}, nil, err
		}
	}
	return reference, rest, nil
}
