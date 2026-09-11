// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testdrive"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/tracer"
	"github.com/stretchr/testify/require"
)

const (
	jestVersion    = "30.5.1"
	fixtureTimeout = 5 * time.Minute
)

type instrumentedJestFixture struct {
	session          *testdrive.Session
	server           *intake.Server
	ciInitPath       string
	jestPath         string
	fixtureDirectory string
	fixturePath      string
	outputPath       string
	sessionName      string
	requests         []intake.RawRequest
	testEventCount   int
	coveredTestCount int
}

func TestInstrumentedJestFixture(t *testing.T) {
	requireNPMIntegration(t)

	ctx, cancel := context.WithTimeout(t.Context(), fixtureTimeout)
	defer cancel()

	fixture, err := prepareInstrumentedJestFixture(ctx, t.TempDir(), "ddtest testdrive")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, fixture.server.Close())
	})

	require.NoError(t, fixture.run(ctx))
	assertFixtureResult(t, fixture)
}

func TestInstrumentedJestFixturesAreIsolated(t *testing.T) {
	requireNPMIntegration(t)

	ctx, cancel := context.WithTimeout(t.Context(), fixtureTimeout)
	defer cancel()
	repositoryRoot := t.TempDir()

	first, err := prepareInstrumentedJestFixture(ctx, repositoryRoot, "first testdrive")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, first.server.Close())
	})
	second, err := prepareInstrumentedJestFixture(ctx, repositoryRoot, "second testdrive")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, second.server.Close())
	})

	require.NotEqual(t, first.session.Directory(), second.session.Directory())
	require.Equal(t, filepath.Dir(first.session.Directory()), filepath.Dir(second.session.Directory()))
	require.NotEqual(t, first.server.URL(), second.server.URL())
	require.NotEqual(t, first.ciInitPath, second.ciInitPath)
	require.NotEqual(t, first.outputPath, second.outputPath)

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, fixture := range []*instrumentedJestFixture{first, second} {
		go func() {
			<-start
			results <- fixture.run(ctx)
		}()
	}
	close(start)

	firstError := <-results
	secondError := <-results
	require.NoError(t, firstError)
	require.NoError(t, secondError)
	assertFixtureResult(t, first)
	assertFixtureResult(t, second)
}

func requireNPMIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("DDTEST_RUN_NPM_INTEGRATION_TEST") == "" {
		t.Skip("set DDTEST_RUN_NPM_INTEGRATION_TEST=1 to run Jest with the pinned tracer")
	}
}

func prepareInstrumentedJestFixture(ctx context.Context, repositoryRoot, sessionName string) (*instrumentedJestFixture, error) {
	session, err := testdrive.NewSession(repositoryRoot)
	if err != nil {
		return nil, err
	}
	ciInitPath, err := tracer.NewJavaScript().Install(ctx, session.Directory())
	if err != nil {
		return nil, err
	}

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
	if err != nil {
		return nil, commandError("install Jest", output, err)
	}

	server, err := intake.Start(session.Directory())
	if err != nil {
		return nil, err
	}
	fixtureDirectory, err := filepath.Abs(filepath.Join("testdata", "jest"))
	if err != nil {
		_ = server.Close()
		return nil, err
	}

	return &instrumentedJestFixture{
		session:          session,
		server:           server,
		ciInitPath:       ciInitPath,
		jestPath:         filepath.Join(jestDirectory, "node_modules", "jest", "bin", "jest.js"),
		fixtureDirectory: fixtureDirectory,
		fixturePath:      filepath.Join(fixtureDirectory, "one.test.js"),
		outputPath:       filepath.Join(session.Directory(), "jest-output.txt"),
		sessionName:      sessionName,
	}, nil
}

func (f *instrumentedJestFixture) run(ctx context.Context) error {
	executor := &ext.DefaultCommandExecutor{}
	output, err := executor.CombinedOutput(ctx, "node", []string{
		f.jestPath,
		"--ci",
		"--config", "{}",
		"--runInBand",
		"--rootDir", f.fixtureDirectory,
		"--runTestsByPath", f.fixturePath,
	}, map[string]string{
		"NODE_OPTIONS":                                                "-r " + f.ciInitPath,
		constants.APIKeyEnvironmentVariable:                           "testdrive",
		constants.TestOptimizationEnabledEnvironmentVariable:          "true",
		constants.TestOptimizationAgentlessEnabledEnvironmentVariable: "true",
		constants.TestOptimizationAgentlessURLEnvironmentVariable:     f.server.URL(),
		constants.TestOptimizationTestSessionNameEnvironmentVariable:  f.sessionName,
		"DD_CIVISIBILITY_GIT_UPLOAD_ENABLED":                          "false",
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED":                        "false",
		"DD_TRACE_STARTUP_LOGS":                                       "false",
		"DD_SERVICE":                                                  "ddtest-testdrive-fixture",
	})
	if writeErr := os.WriteFile(f.outputPath, output, 0644); writeErr != nil {
		return fmt.Errorf("write Jest output: %w", writeErr)
	}
	if err != nil {
		return commandError("run Jest", output, err)
	}

	testEventCount, err := f.server.TestEventCount()
	if err != nil {
		return err
	}
	coveredTestCount, err := f.server.CoveredTestCount()
	if err != nil {
		return err
	}
	f.requests = f.server.Requests()
	f.testEventCount = testEventCount
	f.coveredTestCount = coveredTestCount
	return nil
}

func assertFixtureResult(t *testing.T, fixture *instrumentedJestFixture) {
	t.Helper()

	require.Equal(t, 1, fixture.testEventCount)
	require.Equal(t, 1, fixture.coveredTestCount)
	resolvedSessionDirectory, err := filepath.EvalSymlinks(fixture.session.Directory())
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(fixture.ciInitPath, resolvedSessionDirectory+string(filepath.Separator)))
	require.True(t, strings.HasPrefix(fixture.outputPath, fixture.session.Directory()+string(filepath.Separator)))
	require.FileExists(t, fixture.ciInitPath)
	require.FileExists(t, fixture.jestPath)
	require.FileExists(t, fixture.outputPath)
	intakeFiles, err := os.ReadDir(filepath.Join(fixture.session.Directory(), "intake"))
	require.NoError(t, err)
	require.NotEmpty(t, intakeFiles)
	storedEvents := false
	storedCoverage := false
	for _, intakeFile := range intakeFiles {
		require.Equal(t, ".json", filepath.Ext(intakeFile.Name()))
		contents, readErr := os.ReadFile(filepath.Join(fixture.session.Directory(), "intake", intakeFile.Name()))
		require.NoError(t, readErr)
		require.True(t, json.Valid(contents))
		storedEvents = storedEvents || strings.HasSuffix(intakeFile.Name(), "-citestcycle.json")
		storedCoverage = storedCoverage || strings.HasSuffix(intakeFile.Name(), "-citestcov.json")
	}
	require.True(t, storedEvents)
	require.True(t, storedCoverage)

	for _, path := range []string{"/api/v2/citestcycle", "/api/v2/citestcov"} {
		request := findRequest(fixture.requests, path)
		require.NotNil(t, request, "observed requests: %v", requestPaths(fixture.requests))
		require.NotEmpty(t, request.Body)
		if path == "/api/v2/citestcycle" {
			require.True(t, bytes.Contains(request.Body, []byte(fixture.sessionName)))
		}
	}
	for _, request := range fixture.requests {
		t.Logf("%s captured raw %s %s: content-type=%q bytes=%d", fixture.sessionName, request.Method, request.Path, request.Header.Get("Content-Type"), len(request.Body))
	}
}

func findRequest(requests []intake.RawRequest, path string) *intake.RawRequest {
	for i := range requests {
		if requests[i].Method == http.MethodPost && requests[i].Path == path {
			return &requests[i]
		}
	}
	return nil
}

func requestPaths(requests []intake.RawRequest) []string {
	paths := make([]string, len(requests))
	for i, request := range requests {
		paths[i] = request.Method + " " + request.Path
	}
	return paths
}

func commandError(action string, output []byte, err error) error {
	diagnostic := strings.TrimSpace(string(output))
	if diagnostic == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %s: %w", action, diagnostic, err)
}
