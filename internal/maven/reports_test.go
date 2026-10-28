// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package maven

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadReportsReadsSurefireAndFailsafeResults(t *testing.T) {
	root := t.TempDir()
	projects := []Project{{Directory: ".", Artifact: "parent"}, {Directory: "service", Artifact: "service"}}
	writePOM(t, root, "service/target/surefire-reports/TEST-example.UnitTest.xml", `<testsuite name="example.UnitTest">
  <testcase classname="example.UnitTest" name="passes"/>
  <testcase classname="example.UnitTest" name="skipped"><skipped/></testcase>
  <testcase classname="example.UnitTest" name="fails"><failure type="org.opentest4j.AssertionFailedError" message="expected: 5">stack</failure></testcase>
  <testcase name="errors"><error type="java.lang.IllegalStateException">boom
	at example.UnitTest.errors(UnitTest.java:9)</error></testcase>
</testsuite>`)
	writePOM(t, root, "service/target/failsafe-reports/TEST-example.ServiceIT.xml", `<testsuites><testsuite name="example.ServiceIT"><testcase classname="example.ServiceIT" name="starts"/></testsuite></testsuites>`)
	// Other reports under target are not native Surefire results.
	writePOM(t, root, "service/target/surefire-reports/junitreports/TEST-example.UnitTest.xml", `<testsuite><testcase name="duplicate"/></testsuite>`)

	tests, err := ReadReports(root, projects)
	require.NoError(t, err)
	require.Equal(t, []Test{
		{Module: "service", Suite: "example.UnitTest", Name: "passes", Status: "passed"},
		{Module: "service", Suite: "example.UnitTest", Name: "skipped", Status: "skipped"},
		{Module: "service", Suite: "example.UnitTest", Name: "fails", Status: "failed", Failure: "org.opentest4j.AssertionFailedError: expected: 5\n"},
		{Module: "service", Suite: "example.UnitTest", Name: "errors", Status: "failed", Failure: "java.lang.IllegalStateException: boom\n"},
		{Module: "service", Suite: "example.ServiceIT", Name: "starts", Status: "passed"},
	}, tests)
}

func TestReadReportsDistinguishesMissingAndEmptyResults(t *testing.T) {
	root := t.TempDir()
	projects := []Project{{Directory: "."}}
	_, err := ReadReports(root, projects)
	require.ErrorIs(t, err, ErrNoReports)

	writePOM(t, root, "target/surefire-reports/TEST-example.EmptyTest.xml", `<testsuite name="example.EmptyTest"/>`)
	_, err = ReadReports(root, projects)
	require.ErrorIs(t, err, ErrNoTestCases)
}

func TestReadReportsRejectsUnusableReports(t *testing.T) {
	for name, contents := range map[string]string{
		"unnamed test": `<testsuite><testcase classname="example.Test"/></testsuite>`,
		"other format": `<testng-results/>`,
		"invalid XML":  `<testsuite>`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writePOM(t, root, "target/surefire-reports/TEST-example.Test.xml", contents)
			_, err := ReadReports(root, []Project{{Directory: "."}})
			require.Error(t, err)
		})
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "TEST-example.Test.xml")
	require.NoError(t, os.WriteFile(outside, []byte(`<testsuite><testcase name="t"/></testsuite>`), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "target/surefire-reports"), 0700))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "target/surefire-reports/TEST-example.Test.xml")))
	_, err := ReadReports(root, []Project{{Directory: "."}})
	require.ErrorContains(t, err, "not a regular file")
}

func TestIncompleteBuild(t *testing.T) {
	projects := []Project{{Directory: "core", Artifact: "core"}, {Directory: "api", Artifact: "api"}}
	failedCore := []Test{{Module: "core", Name: "fails", Status: "failed"}}
	testFailure := "[ERROR] Failed to execute goal org.apache.maven.plugins:maven-surefire-plugin:3.5.3:test (default-test) on project core: There are test failures.\n"
	for _, tc := range []struct {
		name       string
		exit       int
		tests      []Test
		output     string
		incomplete bool
	}{
		{"successful build", 0, []Test{{Module: "core", Status: "passed"}}, "", false},
		{"native test failure", 1, failedCore, testFailure, false},
		{"ANSI-colored test failure", 1, failedCore, "\x1b[1;31m" + testFailure + "\x1b[m", false},
		{"Failsafe verification failure", 1, failedCore, "[ERROR] Failed to execute goal org.apache.maven.plugins:maven-failsafe-plugin:3.5.3:verify (default) on project core: There are test failures.\n", false},
		{"compilation failure", 1, nil, "[ERROR] Failed to execute goal org.apache.maven.plugins:maven-compiler-plugin:3.13.0:compile (default-compile) on project api: Compilation failure\n", true},
		{"later module fails to compile", 1, failedCore, testFailure + "[ERROR] Failed to execute goal org.apache.maven.plugins:maven-compiler-plugin:3.13.0:compile (default-compile) on project api: Compilation failure\n", true},
		{"test failure without failed results", 1, []Test{{Module: "core", Status: "passed"}}, testFailure, true},
		{"failure without a reported goal", 1, failedCore, "[ERROR] BUILD FAILURE\n", true},
		{"unexpected exit code", 137, failedCore, testFailure, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.incomplete, IncompleteBuild(tc.exit, projects, tc.tests, tc.output))
		})
	}
	ambiguous := []Project{{Directory: "a", Artifact: "core"}, {Directory: "b", Artifact: "core"}}
	require.True(t, IncompleteBuild(1, ambiguous, []Test{{Module: "a", Status: "failed"}}, testFailure), "an artifactId two modules share cannot be attributed")
}
