package testdrive

import (
	"testing"

	"github.com/DataDog/ddtest/internal/maven"
	"github.com/stretchr/testify/require"
)

func TestMavenBuildFailuresNeedNativeAttribution(t *testing.T) {
	projects := []maven.Project{{Directory: "first", Artifact: "first"}, {Directory: "second", Artifact: "second", Name: "first"}}
	tests := []maven.Test{{Module: "first", Name: "fails", Status: "failed"}}
	testFailure := "[ERROR] Failed to execute goal org.apache.maven.plugins:maven-surefire-plugin:3.5.3:test (default-test) on project first: There are test failures."
	compileFailure := "[ERROR] Failed to execute goal org.apache.maven.plugins:maven-compiler-plugin:3.13.0:testCompile (default-testCompile) on project second: Compilation failure"
	for _, tc := range []struct {
		name, output string
		exit         int
		complete     bool
	}{
		{"successful command", "", 0, true},
		{"reported test failure", testFailure, 1, true},
		{"failsafe failure", "[ERROR] Failed to execute goal org.apache.maven.plugins:maven-failsafe-plugin:3.5.3:verify (default) on project first: There are test failures.", 1, true},
		{"failure ignored by Maven", testFailure, 0, true},
		{"compile failure", compileFailure, 1, false},
		{"fail-never compile failure", compileFailure, 0, false},
		{"tests and later compilation", testFailure + "\n" + compileFailure, 1, false},
		{"missing native failure", "[ERROR] Failed to execute goal org.apache.maven.plugins:maven-surefire-plugin:3.5.3:test (default-test) on project second: There are test failures.", 1, false},
		{"crashed fork", "[ERROR] Failed to execute goal org.apache.maven.plugins:maven-surefire-plugin:3.5.3:test (default-test) on project first: The forked VM terminated without properly saying goodbye", 1, false},
		{"unknown failing process", "", 1, false},
		{"interrupted process", testFailure, 2, false},
		{"dependency failure ignored", "[ERROR] Failed to execute goal on project second: Could not resolve dependencies", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason := incompleteMavenBuild(tc.exit, projects, tests, tc.output)
			if tc.complete {
				require.Empty(t, reason)
			} else {
				require.Contains(t, reason, "incomplete")
			}
		})
	}
}
