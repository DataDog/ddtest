package intake

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/stretchr/testify/require"
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
