// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"

	"github.com/tinylib/msgp/msgp"
)

const testCoveragePath = "/api/v2/citestcov"

// CoveredTestCount returns the number of observed tests with matching coverage.
func (s *Server) CoveredTestCount() (int, error) {
	tests, err := s.testReferences()
	if err != nil {
		return 0, err
	}

	coverages, err := s.coverageReferences()
	if err != nil {
		return 0, err
	}
	coveredTests := make(map[uint64]struct{}, len(coverages))
	coveredSuites := make(map[suiteReference]struct{}, len(coverages))
	for _, coverage := range coverages {
		if coverage.spanID != 0 {
			coveredTests[coverage.spanID] = struct{}{}
			continue
		}
		coveredSuites[suiteReference{sessionID: coverage.sessionID, suiteID: coverage.suiteID}] = struct{}{}
	}

	count := 0
	for _, test := range tests {
		_, testCovered := coveredTests[test.spanID]
		_, suiteCovered := coveredSuites[suiteReference{sessionID: test.sessionID, suiteID: test.suiteID}]
		if testCovered || suiteCovered {
			count++
		}
	}
	return count, nil
}

func (s *Server) coverageReferences() ([]testReference, error) {
	var coverages []testReference
	for _, request := range s.Requests() {
		if request.Method != http.MethodPost || request.Path != testCoveragePath {
			continue
		}

		requestCoverages, err := readMultipartCoverage(request)
		if err != nil {
			return nil, fmt.Errorf("recognize coverage in %s: %w", testCoveragePath, err)
		}
		coverages = append(coverages, requestCoverages...)
	}
	return coverages, nil
}

func readMultipartCoverage(request RawRequest) ([]testReference, error) {
	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}
	if mediaType != "multipart/form-data" {
		return nil, fmt.Errorf("unexpected content type %q", mediaType)
	}

	reader := multipart.NewReader(bytes.NewReader(request.Body), params["boundary"])
	var coverages []testReference
	for {
		part, partErr := reader.NextPart()
		if partErr == io.EOF {
			return coverages, nil
		}
		if partErr != nil {
			return nil, partErr
		}

		partBody, readErr := io.ReadAll(part)
		_ = part.Close()
		if readErr != nil {
			return nil, readErr
		}
		partMediaType, _, parseErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if parseErr != nil || partMediaType != "application/msgpack" {
			continue
		}

		partCoverages, decodeErr := readCoverageEntries(partBody)
		if decodeErr != nil {
			return nil, decodeErr
		}
		coverages = append(coverages, partCoverages...)
	}
}

func readCoverageEntries(payload []byte) ([]testReference, error) {
	fieldCount, rest, err := msgp.ReadMapHeaderBytes(payload)
	if err != nil {
		return nil, err
	}

	var coverages []testReference
	for range fieldCount {
		var field string
		field, rest, err = msgp.ReadStringBytes(rest)
		if err != nil {
			return nil, err
		}
		if field != "coverages" {
			rest, err = msgp.Skip(rest)
			if err != nil {
				return nil, err
			}
			continue
		}

		var entryCount uint32
		entryCount, rest, err = msgp.ReadArrayHeaderBytes(rest)
		if err != nil {
			return nil, err
		}
		for range entryCount {
			var coverage testReference
			coverage, rest, err = readTestReference(rest)
			if err != nil {
				return nil, err
			}
			coverages = append(coverages, coverage)
		}
	}
	return coverages, nil
}
