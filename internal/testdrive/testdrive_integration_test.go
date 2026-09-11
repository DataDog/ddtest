// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testdrive"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/tracer"
	"github.com/stretchr/testify/require"
)

const jestVersion = "30.5.1"

func TestInstrumentedJestFixture(t *testing.T) {
	if os.Getenv("DDTEST_RUN_NPM_INTEGRATION_TEST") == "" {
		t.Skip("set DDTEST_RUN_NPM_INTEGRATION_TEST=1 to run Jest with the pinned tracer")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	session, err := testdrive.NewSession(t.TempDir())
	require.NoError(t, err)
	ciInitPath, err := tracer.NewJavaScript().Install(ctx, session.Directory())
	require.NoError(t, err)

	jestDirectory := filepath.Join(session.Directory(), "jest")
	executor := &ext.DefaultCommandExecutor{}
	output, err := executor.CombinedOutput(ctx, "npm", []string{
		"install",
		"--prefix", jestDirectory,
		"--no-save",
		"--package-lock=false",
		"--no-audit",
		"--no-fund",
		"jest@" + jestVersion,
	}, nil)
	require.NoError(t, err, string(output))

	server, err := intake.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, server.Close())
	})

	fixtureDirectory, err := filepath.Abs(filepath.Join("testdata", "jest"))
	require.NoError(t, err)
	fixturePath := filepath.Join(fixtureDirectory, "one.test.js")
	jestPath := filepath.Join(jestDirectory, "node_modules", "jest", "bin", "jest.js")
	output, err = executor.CombinedOutput(ctx, "node", []string{
		jestPath,
		"--ci",
		"--config", "{}",
		"--runInBand",
		"--rootDir", fixtureDirectory,
		"--runTestsByPath", fixturePath,
	}, map[string]string{
		"NODE_OPTIONS":                                                "-r " + ciInitPath,
		constants.APIKeyEnvironmentVariable:                           "testdrive",
		constants.TestOptimizationEnabledEnvironmentVariable:          "true",
		constants.TestOptimizationAgentlessEnabledEnvironmentVariable: "true",
		constants.TestOptimizationAgentlessURLEnvironmentVariable:     server.URL(),
		constants.TestOptimizationTestSessionNameEnvironmentVariable:  "ddtest testdrive",
		"DD_CIVISIBILITY_GIT_UPLOAD_ENABLED":                          "false",
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED":                        "false",
		"DD_TRACE_STARTUP_LOGS":                                       "false",
		"DD_SERVICE":                                                  "ddtest-testdrive-fixture",
	})
	require.NoError(t, err, string(output))

	requests := server.Requests()
	require.NotEmpty(t, requests)
	var testCycleRequest *intake.RawRequest
	for i := range requests {
		if requests[i].Method == http.MethodPost && requests[i].Path == "/api/v2/citestcycle" {
			testCycleRequest = &requests[i]
			break
		}
	}
	require.NotNil(t, testCycleRequest, "observed requests: %v", requestPaths(requests))
	require.NotEmpty(t, testCycleRequest.Body)
	t.Logf("captured raw %s request: content-type=%q bytes=%d", testCycleRequest.Path, testCycleRequest.Header.Get("Content-Type"), len(testCycleRequest.Body))
}

func requestPaths(requests []intake.RawRequest) []string {
	paths := make([]string, len(requests))
	for i, request := range requests {
		paths[i] = request.Method + " " + request.Path
	}
	return paths
}
