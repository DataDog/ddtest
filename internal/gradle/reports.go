// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gradle

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/maven"
)

// Test is one test case of a Gradle test task's native JUnit XML report.
type Test struct {
	// Module of the embedded test is the project directory.
	maven.Test
	// Task is the test task's path, as :app:test, which dd-java-agent reports
	// as test.module.
	Task string
}

// ErrNoReports reports a build that wrote no native report for any project.
var ErrNoReports = errors.New("no native TEST-*.xml reports under the projects' build/test-results; run the Gradle test tasks the CI step runs, with Gradle's default JUnit XML reports enabled")

// ErrNoTestCases reports native reports that contain no test case.
var ErrNoTestCases = errors.New("native reports contain no test cases")

// ResultsDirectory is where a project's test tasks write their reports, one
// directory per task, with Gradle's default build directory.
var ResultsDirectory = filepath.Join("build", "test-results")

// TaskPath is the path of a project's task, as :app:test or, in the root
// project, :test.
func TaskPath(project, task string) string {
	return strings.TrimSuffix(project, ":") + ":" + task
}

// ReadReports reads the native reports of every project's test tasks under
// root. Call it only on fresh build output: reports from an earlier build are
// read as well.
func ReadReports(root string, projects []Project) ([]Test, error) {
	var tests []Test
	reports := 0
	for _, project := range projects {
		directory := filepath.Join(project.Directory, ResultsDirectory)
		entries, err := os.ReadDir(filepath.Join(root, directory))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			paths, err := filepath.Glob(filepath.Join(root, directory, entry.Name(), "TEST-*.xml"))
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
				read, err := maven.ReadReport(path, project.Directory)
				if err != nil {
					return nil, err
				}
				reports++
				for _, test := range read {
					tests = append(tests, Test{Test: test, Task: TaskPath(project.Path, entry.Name())})
				}
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

// failedTask is the task a Gradle failure names, as in
// Execution failed for task ':app:test'.
var failedTask = regexp.MustCompile(`^Execution failed for task '([^']+)'\.$`)

// IncompleteBuild reports whether a Gradle build stopped for a reason other
// than native test failures: configuration, compilation or another task's
// failure leaves tasks unexecuted. It is false only when every failure is a
// test task that failed with failing tests and whose native results contain a
// failure.
func IncompleteBuild(exitCode int, tests []Test, output string) bool {
	if exitCode == 0 {
		return false
	}
	if exitCode != 1 {
		return true
	}
	failed := map[string]bool{}
	for _, test := range tests {
		failed[test.Task] = failed[test.Task] || test.Status == "failed"
	}
	lines := strings.Split(output, "\n")
	failures := 0
	for i, line := range lines {
		if strings.TrimSpace(line) != "* What went wrong:" {
			continue
		}
		failures++
		// The failure names a test task, then why it failed.
		if i+2 >= len(lines) {
			return true
		}
		match := failedTask.FindStringSubmatch(strings.TrimSpace(lines[i+1]))
		if match == nil || !strings.HasPrefix(strings.TrimSpace(lines[i+2]), "> There were failing tests") || !failed[match[1]] {
			return true
		}
	}
	return failures == 0
}
