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

// TestEventCount returns the number of test events observed by the intake.
func (s *Server) TestEventCount() (int, error) {
	count := 0
	for _, request := range s.Requests() {
		if request.Method != http.MethodPost || request.Path != testCyclePath {
			continue
		}

		requestCount, err := countTestEvents(request.Body)
		if err != nil {
			return 0, fmt.Errorf("recognize test events in %s: %w", testCyclePath, err)
		}
		count += requestCount
	}
	return count, nil
}

func countTestEvents(payload []byte) (int, error) {
	fieldCount, rest, err := msgp.ReadMapHeaderBytes(payload)
	if err != nil {
		return 0, err
	}

	count := 0
	for range fieldCount {
		var field string
		field, rest, err = msgp.ReadStringBytes(rest)
		if err != nil {
			return 0, err
		}

		if field != "events" {
			rest, err = msgp.Skip(rest)
			if err != nil {
				return 0, err
			}
			continue
		}

		var eventCount int
		eventCount, rest, err = countTestsInEventArray(rest)
		if err != nil {
			return 0, err
		}
		count += eventCount
	}
	return count, nil
}

func countTestsInEventArray(payload []byte) (int, []byte, error) {
	eventCount, rest, err := msgp.ReadArrayHeaderBytes(payload)
	if err != nil {
		return 0, nil, err
	}

	count := 0
	for range eventCount {
		fieldCount, remaining, readErr := msgp.ReadMapHeaderBytes(rest)
		if readErr != nil {
			return 0, nil, readErr
		}
		rest = remaining

		eventType := ""
		for range fieldCount {
			var field string
			field, rest, readErr = msgp.ReadStringBytes(rest)
			if readErr != nil {
				return 0, nil, readErr
			}

			if field == "type" {
				eventType, rest, readErr = msgp.ReadStringBytes(rest)
			} else {
				rest, readErr = msgp.Skip(rest)
			}
			if readErr != nil {
				return 0, nil, readErr
			}
		}
		if eventType == "test" {
			count++
		}
	}
	return count, rest, nil
}
