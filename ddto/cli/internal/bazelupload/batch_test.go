package bazelupload

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// A shared request takes up to batchSources compatible test sources, and the
// coverage sources between them do not close it.
func TestBatchesSpanOtherSources(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "CODEOWNERS", "* @team\n")
	var sources []Source
	for i := range batchSources + 1 {
		test, coverage := fmt.Sprintf("test-%02d.json", i), fmt.Sprintf("coverage-%02d.json", i)
		writeFixture(t, directory, test, inputPayload)
		writeFixture(t, directory, coverage, `[]`)
		coverageSource := testSource(coverage, coverage)
		coverageSource.Kind = Coverage
		sources = append(sources, testSource(test, test), coverageSource)
	}
	var mu sync.Mutex
	requests := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mu.Lock()
		requests[request.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	report := runFixture(t, directory, testPlan(sources...), Options{Batch: true, AgentlessURL: server.URL, APIKey: "local-test"})
	if report.Files.Succeeded != len(sources) || report.SharedRequests != 1 ||
		requests["/api/v2/citestcycle"] != 2 || requests["/api/v2/citestcov"] != batchSources+1 {
		t.Fatalf("requests %v, shared %d, files %+v", requests, report.SharedRequests, report.Files)
	}
}

// Compatible sources share a request although sources of another batch key
// lie between them.
func TestBatchesGroupInterleavedKeys(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "CODEOWNERS", "* @team\n")
	var sources []Source
	for i := range 16 {
		name := fmt.Sprintf("test-%02d.json", i)
		group := map[bool]string{false: "a", true: "b"}[i%2 == 1]
		writeFixture(t, directory, name, strings.Replace(inputPayload, `"metadata": {"*": {`,
			`"metadata": {"test": {"fixture.group":"`+group+`"}, "*": {`, 1))
		sources = append(sources, testSource(name, name))
	}
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	report := runFixture(t, directory, testPlan(sources...), Options{Batch: true, AgentlessURL: server.URL, APIKey: "local-test"})
	if report.Files.Succeeded != len(sources) || report.SharedRequests != 2 || requests != 2 {
		t.Fatalf("%d requests, %d shared, files %+v", requests, report.SharedRequests, report.Files)
	}
}

// Each source of a batch records the shared request, as the rules uploader's
// Python batches do, so that a failed batch still shows every source reached
// delivery; the report lists and counts the request once.
func TestEveryBatchSourceRecordsItsRequest(t *testing.T) {
	accepted, rejected := true, false
	for _, test := range []struct {
		name      string
		status    int
		dryRun    bool
		succeeded *bool
		attempts  int
	}{
		{"accepted", http.StatusAccepted, false, &accepted, 1},
		{"rejected without replay", http.StatusForbidden, false, &rejected, 1},
		{"dry run", http.StatusAccepted, true, nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "CODEOWNERS", "* @team\n")
			var sources []Source
			var ids []string
			for i := range 3 {
				name := fmt.Sprintf("test-%d.json", i)
				writeFixture(t, directory, name, inputPayload)
				sources = append(sources, testSource(name, name))
				ids = append(ids, name)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(test.status) }))
			defer server.Close()

			report := runFixture(t, directory, testPlan(sources...),
				Options{Batch: true, DryRun: test.dryRun, AgentlessURL: server.URL, APIKey: "local-test"})
			if len(report.BatchRequests) != 1 {
				t.Fatalf("batch requests: %+v", report.BatchRequests)
			}
			request := report.BatchRequests[0]
			// Sources join a batch as they finish preparing, in any order.
			if request.ID != "batch-1" || strings.Join(slices.Sorted(slices.Values(request.Sources)), ",") != strings.Join(ids, ",") ||
				request.Attempts != test.attempts || (request.Succeeded == nil) != (test.succeeded == nil) ||
				request.Succeeded != nil && *request.Succeeded != *test.succeeded {
				t.Fatalf("batch request: %+v", request)
			}
			for _, source := range report.Sources {
				// The request's bytes count once, on its first source.
				if source.Requests.Planned != 1 || source.Requests.Attempted != test.attempts ||
					strings.Join(source.RequestIDs, ",") != "batch-1" || (source.ID == request.Sources[0]) != (source.PreparedBytes > 0) {
					t.Fatalf("source %s: %+v", source.ID, source)
				}
			}
			succeeded, failed := 0, 0
			if test.succeeded != nil && *test.succeeded {
				succeeded = 1
			} else if test.succeeded != nil {
				failed = 1
			}
			if report.Requests != (RequestCounts{Planned: 1, Attempted: test.attempts, Succeeded: succeeded, Failed: failed}) ||
				report.SharedRequests != 1 {
				t.Fatalf("report requests %+v, shared %d", report.Requests, report.SharedRequests)
			}
		})
	}
}

// With batching, every test request is listed and named on its sources,
// including the per-source requests after a definitive batch rejection, so
// the totals count each request once; without batching, none is.
func TestBatchModeListsEveryTestRequest(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "CODEOWNERS", "* @team\n")
	var sources []Source
	for i := range 3 {
		name := fmt.Sprintf("test-%d.json", i)
		writeFixture(t, directory, name, inputPayload)
		sources = append(sources, testSource(name, name))
	}
	var mu sync.Mutex
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		posts++
		first := posts == 1
		mu.Unlock()
		if first {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	report := runFixture(t, directory, testPlan(sources...), Options{Batch: true, KeepPayloads: true, AgentlessURL: server.URL, APIKey: "local-test"})
	if len(report.BatchRequests) != 4 || report.Requests != (RequestCounts{Planned: 4, Attempted: 4, Succeeded: 3, Failed: 1}) {
		t.Fatalf("requests %+v, listed %+v", report.Requests, report.BatchRequests)
	}
	for _, source := range report.Sources {
		if len(source.RequestIDs) != 2 || source.RequestIDs[0] != "batch-1" || source.Requests.Planned != 2 ||
			source.Requests.Failed != 1 || source.Requests.Succeeded != 1 || source.Status != statusSucceeded {
			t.Fatalf("source %s: %+v", source.ID, source)
		}
	}

	for i := range sources {
		writeFixture(t, directory, sources[i].Path, inputPayload)
	}
	plain := runFixture(t, directory, testPlan(sources...), Options{AgentlessURL: server.URL, APIKey: "local-test"})
	if len(plain.BatchRequests) != 0 || plain.Requests.Planned != 3 {
		t.Fatalf("without batching: requests %+v, listed %+v", plain.Requests, plain.BatchRequests)
	}
	for _, source := range plain.Sources {
		if len(source.RequestIDs) != 0 {
			t.Fatalf("source %s without batching: %+v", source.ID, source)
		}
	}
}
