package bazelupload

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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
