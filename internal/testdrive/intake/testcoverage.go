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

	"github.com/DataDog/ddtest/internal/constants"
)

type coverageReference struct {
	testReference
	fileCount int
	files     []string
}

// CoveredTestCount returns the number of observed tests with matching coverage.
// Payloads containing empty coverage entries are excluded; EmptyCoverageEntryCount
// reports these tracer errors separately.
func (s *Server) CoveredTestCount() (int, error) {
	tests, err := s.testReferences()
	if err != nil {
		return 0, err
	}

	coverages, _, err := s.coverageReferences()
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

// EmptyCoverageEntryCount returns the number of entries with an empty files list.
// These indicate a tracer error. Their entire MessagePack payload is excluded
// from coverage counts, while the captured request remains available for diagnosis.
func (s *Server) EmptyCoverageEntryCount() (int, error) {
	_, emptyEntries, err := s.coverageReferences()
	return emptyEntries, err
}

func (s *Server) coverageReferences() ([]coverageReference, int, error) {
	var coverages []coverageReference
	emptyEntries := 0
	for _, request := range s.Requests() {
		if request.Method != http.MethodPost || request.Path != constants.TestCoverageURLPath {
			continue
		}

		requestCoverages, requestEmptyEntries, err := readMultipartCoverage(request)
		if err != nil {
			return nil, 0, fmt.Errorf("recognize coverage in %s: %w", constants.TestCoverageURLPath, err)
		}
		coverages = append(coverages, requestCoverages...)
		emptyEntries += requestEmptyEntries
	}
	return coverages, emptyEntries, nil
}

func readMultipartCoverage(request RawRequest) ([]coverageReference, int, error) {
	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil {
		return nil, 0, err
	}
	if mediaType != "multipart/form-data" {
		return nil, 0, fmt.Errorf("unexpected content type %q", mediaType)
	}

	body, err := uncompressRequestBody(request)
	if err != nil {
		return nil, 0, err
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var coverages []coverageReference
	emptyEntries := 0
	for {
		part, partErr := reader.NextPart()
		if partErr == io.EOF {
			return coverages, emptyEntries, nil
		}
		if partErr != nil {
			return nil, 0, partErr
		}

		partBody, readErr := io.ReadAll(part)
		_ = part.Close()
		if readErr != nil {
			return nil, 0, readErr
		}
		partMediaType, _, parseErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if parseErr != nil || (partMediaType != "application/msgpack" && partMediaType != "application/x-msgpack") {
			continue
		}

		partCoverages, decodeErr := readCoverageEntries(partBody)
		if decodeErr != nil {
			return nil, 0, decodeErr
		}
		partEmptyEntries := 0
		for _, coverage := range partCoverages {
			if coverage.fileCount == 0 {
				partEmptyEntries++
			}
		}
		// One empty entry invalidates this payload, but remains a tracked tracer error.
		emptyEntries += partEmptyEntries
		if partEmptyEntries == 0 {
			coverages = append(coverages, partCoverages...)
		}
	}
}

func readCoverageEntries(payload []byte) ([]coverageReference, error) {
	fieldCount, rest, err := msgp.ReadMapHeaderBytes(payload)
	if err != nil {
		return nil, err
	}

	var coverages []coverageReference
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
			var coverage coverageReference
			coverage, rest, err = readCoverageReference(rest)
			if err != nil {
				return nil, err
			}
			coverages = append(coverages, coverage)
		}
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("unexpected trailing MessagePack bytes")
	}
	return coverages, nil
}

func readCoverageReference(payload []byte) (coverageReference, []byte, error) {
	content, rest, err := msgp.ReadMapStrIntfBytes(payload, nil)
	if err != nil {
		return coverageReference{}, nil, err
	}

	encodedFiles, _ := content["files"].([]any)
	files := make([]string, 0, len(encodedFiles))
	for _, encodedFile := range encodedFiles {
		file, _ := encodedFile.(map[string]any)
		if filename := text(file["filename"]); filename != "" {
			files = append(files, filename)
		}
	}
	return coverageReference{
		testReference: testReference{
			sessionID: unsigned(content["test_session_id"]),
			suiteID:   unsigned(content["test_suite_id"]),
			spanID:    unsigned(content["span_id"]),
		},
		fileCount: len(encodedFiles),
		files:     files,
	}, rest, nil
}
