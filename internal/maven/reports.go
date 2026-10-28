// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package maven

import (
	"cmp"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Test is one test case from a module's native Surefire or Failsafe report.
type Test struct {
	// Module is the project directory relative to the inspected root.
	Module, Suite, Name, Status string
	// Failure lists each failure or error as "type: message" lines.
	Failure string
}

// ErrNoTestCases reports native reports that contain no test case, as when
// every selected test was skipped before it started.
var ErrNoTestCases = errors.New("native reports contain no test cases")

// ErrNoReports reports a build that wrote no native report for any module.
var ErrNoReports = errors.New("no native TEST-*.xml reports under the modules' target/surefire-reports or target/failsafe-reports; configure Surefire or Failsafe XML reporting and run the intended Maven profile")

type reportCase struct {
	Class    string          `xml:"classname,attr"`
	Name     string          `xml:"name,attr"`
	Skipped  *struct{}       `xml:"skipped"`
	Failures []reportFailure `xml:"failure"`
	Errors   []reportFailure `xml:"error"`
}

type reportFailure struct {
	Type    string `xml:"type,attr"`
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

type reportSuite struct {
	Name  string       `xml:"name,attr"`
	Cases []reportCase `xml:"testcase"`
}

// reportDirectories are where Surefire and Failsafe write one TEST-*.xml per class.
var reportDirectories = []string{filepath.Join("target", "surefire-reports"), filepath.Join("target", "failsafe-reports")}

// ReadReports reads the native reports of every project under root. Call it
// only on fresh build output: reports from an earlier build are read as well.
func ReadReports(root string, projects []Project) ([]Test, error) {
	var tests []Test
	reports := 0
	for _, project := range projects {
		for _, directory := range reportDirectories {
			paths, err := filepath.Glob(filepath.Join(root, project.Directory, directory, "TEST-*.xml"))
			if err != nil {
				return nil, err
			}
			slices.Sort(paths)
			for _, path := range paths {
				info, err := os.Lstat(path)
				if err != nil {
					return nil, err
				}
				if !info.Mode().IsRegular() {
					return nil, fmt.Errorf("native test report is not a regular file: %s", path)
				}
				read, err := readReport(path, project.Directory)
				if err != nil {
					return nil, err
				}
				reports++
				tests = append(tests, read...)
			}
		}
	}
	switch {
	case reports == 0:
		return nil, ErrNoReports
	case len(tests) == 0:
		return nil, ErrNoTestCases
	}
	return tests, nil
}

func readReport(path, module string) ([]Test, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root struct{ XMLName xml.Name }
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse native report %s: %w", path, err)
	}
	var suites []reportSuite
	switch root.XMLName.Local {
	case "testsuite":
		var suite reportSuite
		if err := xml.Unmarshal(data, &suite); err != nil {
			return nil, fmt.Errorf("parse native report %s: %w", path, err)
		}
		suites = []reportSuite{suite}
	case "testsuites":
		var report struct {
			Suites []reportSuite `xml:"testsuite"`
		}
		if err := xml.Unmarshal(data, &report); err != nil {
			return nil, fmt.Errorf("parse native report %s: %w", path, err)
		}
		suites = report.Suites
	default:
		return nil, fmt.Errorf("unsupported native report root %q in %s", root.XMLName.Local, path)
	}
	var tests []Test
	for _, suite := range suites {
		for _, testCase := range suite.Cases {
			if testCase.Name == "" {
				return nil, fmt.Errorf("native report %s contains a test without a name", path)
			}
			test := Test{Module: module, Suite: cmp.Or(testCase.Class, suite.Name), Name: testCase.Name, Status: "passed"}
			if testCase.Skipped != nil {
				test.Status = "skipped"
			}
			for _, failure := range append(testCase.Failures, testCase.Errors...) {
				test.Status = "failed"
				// Stack frames can change when the agent wraps an invocation;
				// compare the exception type and message only.
				message := failure.Message
				if message == "" {
					message, _, _ = strings.Cut(strings.TrimSpace(failure.Text), "\n")
				}
				test.Failure += failure.Type + ": " + message + "\n"
			}
			tests = append(tests, test)
		}
	}
	return tests, nil
}

var failedGoal = regexp.MustCompile(`^\[ERROR\] Failed to execute goal ([^ ]+) \([^)]*\) on project ([^:]+): (.*)$`)

// testGoals are the plugin goals whose failure a native test failure explains.
var testGoals = []string{"org.apache.maven.plugins:maven-surefire-plugin:test", "org.apache.maven.plugins:maven-failsafe-plugin:verify"}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// IncompleteBuild reports whether a Maven build stopped for a reason other than
// native test failures: compilation, setup or a later module's failure leaves
// part of the reactor unexecuted, even when earlier modules wrote reports. It
// is false only when every failed goal is a test goal of a module whose native
// results contain a failure.
func IncompleteBuild(exitCode int, projects []Project, tests []Test, output string) bool {
	if exitCode != 0 && exitCode != 1 {
		return true
	}
	failedModules := map[string]bool{}
	for _, test := range tests {
		failedModules[test.Module] = failedModules[test.Module] || test.Status == "failed"
	}
	goals := 0
	for _, line := range strings.Split(ansiEscape.ReplaceAllString(output, ""), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[ERROR] Failed to execute goal") {
			continue
		}
		match := failedGoal.FindStringSubmatch(line)
		if match == nil || !strings.HasPrefix(match[3], "There are test failures") {
			return true
		}
		// The goal is group:artifact:version:goal; the version varies.
		parts := strings.Split(match[1], ":")
		if len(parts) != 4 || !slices.Contains(testGoals, parts[0]+":"+parts[1]+":"+parts[3]) {
			return true
		}
		// Maven names the failed project by artifactId.
		index := slices.IndexFunc(projects, func(project Project) bool { return project.Artifact == match[2] })
		if index < 0 || slices.ContainsFunc(projects[index+1:], func(project Project) bool { return project.Artifact == match[2] }) || !failedModules[projects[index].Directory] {
			return true
		}
		goals++
	}
	return exitCode != 0 && goals == 0
}
