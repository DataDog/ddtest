// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gradle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/maven"
	"github.com/stretchr/testify/require"
)

func TestReadReportsLabelsEachTestTask(t *testing.T) {
	root := t.TempDir()
	projects := []Project{{Path: ":", Directory: "."}, {Path: ":app", Directory: "app"}}
	writeFile(t, root, "app/build/test-results/test/TEST-example.AppTest.xml", `<testsuite name="example.AppTest">
  <testcase classname="example.AppTest" name="passes()"/>
  <testcase classname="example.AppTest" name="existingFailure()"><failure type="org.opentest4j.AssertionFailedError" message="expected: 5"/></testcase>
</testsuite>`)
	writeFile(t, root, "app/build/test-results/integrationTest/TEST-example.AppIT.xml", `<testsuite name="example.AppIT"><testcase classname="example.AppIT" name="starts()"/></testsuite>`)
	writeFile(t, root, "build/test-results/test/TEST-example.RootTest.xml", `<testsuite name="example.RootTest"><testcase classname="example.RootTest" name="works()"/></testsuite>`)
	// Gradle's binary results directory is not a report.
	writeFile(t, root, "app/build/test-results/test/binary/results.bin", "")
	tests, err := ReadReports(root, projects)
	require.NoError(t, err)
	require.Equal(t, []Test{
		{Test: maven.Test{Module: ".", Suite: "example.RootTest", Name: "works()", Status: "passed"}, Task: ":test"},
		{Test: maven.Test{Module: "app", Suite: "example.AppIT", Name: "starts()", Status: "passed"}, Task: ":app:integrationTest"},
		{Test: maven.Test{Module: "app", Suite: "example.AppTest", Name: "passes()", Status: "passed"}, Task: ":app:test"},
		{Test: maven.Test{Module: "app", Suite: "example.AppTest", Name: "existingFailure()", Status: "failed", Failure: "org.opentest4j.AssertionFailedError: expected: 5\n"}, Task: ":app:test"},
	}, tests)
}

func TestReadReportsDistinguishesMissingAndEmptyResults(t *testing.T) {
	root := t.TempDir()
	projects := []Project{{Path: ":", Directory: "."}}
	_, err := ReadReports(root, projects)
	require.ErrorIs(t, err, ErrNoReports)
	writeFile(t, root, "build/test-results/test/TEST-example.EmptyTest.xml", `<testsuite name="example.EmptyTest"/>`)
	_, err = ReadReports(root, projects)
	require.ErrorIs(t, err, ErrNoTestCases)
	outside := filepath.Join(t.TempDir(), "TEST-example.Test.xml")
	require.NoError(t, os.WriteFile(outside, []byte(`<testsuite><testcase name="t"/></testsuite>`), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "build/test-results/test/TEST-example.Linked.xml")))
	_, err = ReadReports(root, projects)
	require.ErrorContains(t, err, "not a regular file")
}

func TestIncompleteBuild(t *testing.T) {
	failedApp := []Test{{Test: maven.Test{Status: "failed"}, Task: ":app:test"}}
	testFailure := func(task string) string {
		return "* What went wrong:\nExecution failed for task '" + task + "'.\n> There were failing tests. See the report at: file:///w/index.html\n"
	}
	for _, tc := range []struct {
		name       string
		exit       int
		tests      []Test
		output     string
		incomplete bool
	}{
		{"successful build", 0, nil, "BUILD SUCCESSFUL", false},
		{"native test failure", 1, failedApp, "FAILURE: Build failed with an exception.\n\n" + testFailure(":app:test"), false},
		{"several test failures with --continue", 1, append(failedApp, Test{Test: maven.Test{Status: "failed"}, Task: ":lib:test"}), "FAILURE: Build completed with 2 failures.\n\n1: Task failed with an exception.\n-----------\n" + testFailure(":app:test") + "\n2: Task failed with an exception.\n-----------\n" + testFailure(":lib:test"), false},
		{"compilation failure", 1, nil, "* What went wrong:\nExecution failed for task ':app:compileJava'.\n> Compilation failed; see the compiler error output for details.\n", true},
		{"test failure and a later compilation failure", 1, failedApp, testFailure(":app:test") + "\n* What went wrong:\nExecution failed for task ':lib:compileJava'.\n> Compilation failed\n", true},
		{"configuration failure", 1, nil, "* What went wrong:\nA problem occurred evaluating project ':app'.\n> Could not find method foo()\n", true},
		{"test task failure without failed results", 1, []Test{{Test: maven.Test{Status: "passed"}, Task: ":app:test"}}, testFailure(":app:test"), true},
		{"failure without a reported cause", 1, failedApp, "BUILD FAILED in 3s\n", true},
		{"unexpected exit code", 137, failedApp, testFailure(":app:test"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.incomplete, IncompleteBuild(tc.exit, tc.tests, tc.output))
		})
	}
}

func TestTaskPath(t *testing.T) {
	require.Equal(t, ":test", TaskPath(":", "test"))
	require.Equal(t, ":app:test", TaskPath(":app", "test"))
	require.Equal(t, ":services:api:integrationTest", TaskPath(":services:api", "integrationTest"))
}
