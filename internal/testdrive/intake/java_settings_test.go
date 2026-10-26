package intake

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"
)

func TestJavaSkippingDefaultsToTestsWithoutSourceFile(t *testing.T) {
	handler := scenarioHandler(Scenario{Feature: "skipping", Module: "app maven-surefire-plugin default-test", Suite: "example.Test", Test: "probe", TestLevel: "test"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, constants.SkippableTestsURLPath, bytes.NewBufferString(`{"data":{"attributes":{}}}`)))
	require.Equal(t, http.StatusOK, response.Code)
	for _, text := range []string{`"type":"test"`, `"name":"probe"`, `"test.bundle":"app maven-surefire-plugin default-test"`} {
		require.Contains(t, response.Body.String(), text)
	}
}

func TestJavaEFDReceivesNonemptyKnownBaselineWithoutProbe(t *testing.T) {
	handler := scenarioHandler(Scenario{Feature: "early-flake-detection", Module: "app", Test: "uniqueProbe", TestLevel: "test"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, constants.KnownTestsURLPath, bytes.NewBufferString(`{}`)))
	require.Contains(t, response.Body.String(), `"app":{"ddtest.known.Control":["knownControl"]}`)
	require.NotContains(t, response.Body.String(), "uniqueProbe")
}

func TestJavaCapabilityControlRetainsTagsWithoutEnablingFeatures(t *testing.T) {
	for _, collect := range []bool{false, true} {
		scenario := Scenario{CollectEvents: collect}
		server, err := StartScenario(t.TempDir(), scenario)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, server.Close()) })
		payload, err := msgp.AppendIntf(nil, map[string]any{"events": []any{
			map[string]any{"type": "test", "content": map[string]any{"span_id": uint64(1), "meta": map[string]any{
				"test.name": "probe", "test.suite": "Control", "test.status": "pass", "_dd.library_capabilities.auto_test_retries": "1",
			}}},
		}})
		require.NoError(t, err)
		response, err := testHTTPClient().Post(server.URL()+constants.TestCycleURLPath, "application/msgpack", bytes.NewReader(payload))
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.NoError(t, server.Close())
		facts, err := server.Facts()
		require.NoError(t, err)
		require.Len(t, facts.Tests, 1)
		if collect {
			require.Len(t, facts.Events, 1)
			require.Equal(t, "1", facts.Events[0].Tags["_dd.library_capabilities.auto_test_retries"])
		} else {
			require.Empty(t, facts.Events)
		}
		require.Empty(t, scenario.Feature)
	}
}
