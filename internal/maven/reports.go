// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package maven

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"
)

type Test struct{ Module, Suite, Name, Status, Failure string }

var ErrNoTestCases = errors.New("native reports contain no test cases")

type testCase struct {
	Class    string    `xml:"classname,attr"`
	Name     string    `xml:"name,attr"`
	Skipped  *struct{} `xml:"skipped"`
	Failures []failure `xml:"failure"`
	Errors   []failure `xml:"error"`
}
type failure struct {
	Type    string `xml:"type,attr"`
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}
type testSuite struct {
	Name  string     `xml:"name,attr"`
	Cases []testCase `xml:"testcase"`
}

// ReadReports accepts Surefire/Failsafe and framework-produced JUnit XML. Reports
// must come from fresh declared reactor outputs, never from a saved fixture or
// the customer's previous build.
func ReadReports(root string, projects []Project) ([]Test, error) {
	byDirectory, testNG := map[string][]Test{}, map[string][]Test{}
	modules := map[string]bool{}
	for _, project := range projects {
		modules[filepath.ToSlash(project.Directory)] = true
	}
	reports := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".testoptimization" {
				return filepath.SkipDir
			}
			return nil
		}
		karate := strings.HasSuffix(entry.Name(), ".karate-json.txt")
		isTestNG := entry.Name() == "testng-results.xml"
		if !karate && !isTestNG && (!strings.HasPrefix(entry.Name(), "TEST-") || filepath.Ext(path) != ".xml") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		before, after, found := strings.Cut("/"+filepath.ToSlash(relative), "/target/")
		if !found {
			return nil
		}
		module := strings.Trim(before, "/")
		if module == "" {
			module = "."
		}
		if !modules[module] {
			return nil
		}
		// TestNG also writes junitreports/TEST-*.xml underneath Surefire's
		// directory. Its native testng-results.xml supplies those identities.
		if !strings.Contains(after, "/") || !slices.Contains([]string{"surefire-reports", "failsafe-reports", "scalatest-reports", "karate-reports"}, pathpkg.Dir(after)) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("native test report is not a regular file: %s", relative)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		reports++
		directory := filepath.Dir(path)
		if isTestNG {
			items, err := readTestNG(data, module)
			if err != nil {
				return fmt.Errorf("parse native TestNG report %s: %w", relative, err)
			}
			testNG[directory] = items
			return nil
		}
		if karate {
			items, err := readKarate(data, module)
			if err != nil {
				return fmt.Errorf("parse native Karate report %s: %w", relative, err)
			}
			byDirectory[directory] = append(byDirectory[directory], items...)
			return nil
		}
		var header struct{ XMLName xml.Name }
		if err := xml.Unmarshal(data, &header); err != nil {
			return fmt.Errorf("parse native report %s: %w", relative, err)
		}
		var suites []testSuite
		switch header.XMLName.Local {
		case "testsuite":
			var suite testSuite
			if err := xml.Unmarshal(data, &suite); err != nil {
				return err
			}
			suites = append(suites, suite)
		case "testsuites":
			var report struct {
				Suites []testSuite `xml:"testsuite"`
			}
			if err := xml.Unmarshal(data, &report); err != nil {
				return err
			}
			suites = report.Suites
		default:
			return fmt.Errorf("unsupported native report root %s in %s", header.XMLName.Local, relative)
		}
		for _, suite := range suites {
			for _, test := range suite.Cases {
				if test.Name == "" {
					return fmt.Errorf("native report %s contains a test with no name", relative)
				}
				class := test.Class
				if class == "" {
					class = suite.Name
				}
				item := Test{Module: module, Suite: class, Name: test.Name, Status: "passed"}
				if test.Skipped != nil {
					item.Status = "skipped"
				}
				for _, failure := range append(test.Failures, test.Errors...) {
					item.Status = "failed"
					// Stack frames can change when the agent wraps an invocation. Compare
					// exception type and assertion message; retain full output in HTML.
					message := failure.Message
					if message == "" {
						message, _, _ = strings.Cut(strings.TrimSpace(failure.Text), "\n")
					}
					item.Failure += failure.Type + ": " + message + "\n"
				}
				byDirectory[directory] = append(byDirectory[directory], item)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Older Surefire/TestNG combinations report only "(ClassName)" as every
	// method name. TestNG's own XML preserves methods. Require the same per-class
	// outcomes before replacing the duplicate JUnit report, never double count it
	// or silently discard results from a mixed-provider execution.
	for directory, items := range testNG {
		if junit := byDirectory[directory]; len(junit) > 0 && !maps.Equal(testNGOutcomeCounts(junit), testNGOutcomeCounts(items)) {
			return nil, fmt.Errorf("TestNG and JUnit native report outcomes disagree in %s", directory)
		}
		byDirectory[directory] = items
	}
	var tests []Test
	for _, directory := range slices.Sorted(maps.Keys(byDirectory)) {
		tests = append(tests, byDirectory[directory]...)
	}
	if err == nil && len(tests) == 0 {
		if reports > 0 {
			err = ErrNoTestCases
		} else {
			err = fmt.Errorf("no native test results under target; configure Surefire/Failsafe/JUnit XML or Karate JSON reporting and run the intended Maven profile")
		}
	}
	return tests, err
}

func testNGOutcomeCounts(tests []Test) map[[2]string]int {
	counts := map[[2]string]int{}
	for _, test := range tests {
		counts[[2]string{test.Suite, test.Status}]++
	}
	return counts
}

func readTestNG(data []byte, module string) ([]Test, error) {
	var report struct {
		XMLName xml.Name `xml:"testng-results"`
		Classes []struct {
			Name    string `xml:"name,attr"`
			Methods []struct {
				Name          string `xml:"name,attr"`
				Status        string `xml:"status,attr"`
				Configuration bool   `xml:"is-config,attr"`
				Exception     struct {
					Class   string `xml:"class,attr"`
					Message string `xml:"message"`
				} `xml:"exception"`
			} `xml:"test-method"`
		} `xml:"suite>test>class"`
	}
	if err := xml.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	var tests []Test
	for _, class := range report.Classes {
		for _, method := range class.Methods {
			if method.Configuration {
				if method.Status == "FAIL" {
					return nil, fmt.Errorf("TestNG configuration failed: %s.%s", class.Name, method.Name)
				}
				continue
			}
			// TestNG's success-percentage failures are tolerated by the build
			// (also used for quarantine); Surefire reports them as successful.
			status := map[string]string{"PASS": "passed", "FAIL": "failed", "SKIP": "skipped", "SUCCESS_PERCENTAGE_FAILURE": "passed"}[method.Status]
			if class.Name == "" || method.Name == "" || status == "" {
				return nil, fmt.Errorf("missing TestNG test identity or unsupported status %q", method.Status)
			}
			test := Test{Module: module, Suite: class.Name, Name: method.Name, Status: status}
			if status == "failed" {
				test.Failure = method.Exception.Class + ": " + strings.TrimSpace(method.Exception.Message)
			}
			tests = append(tests, test)
		}
	}
	return tests, nil
}

// Karate's JUnit test factory can leave Surefire's suite XML empty. Its native
// scenario JSON is authoritative; ignore reports of nested feature calls.
func readKarate(data []byte, module string) ([]Test, error) {
	var report struct {
		Name      string `json:"name"`
		Path      string `json:"relativePath"`
		CallDepth int    `json:"callDepth"`
		Scenarios []struct {
			Name   string `json:"name"`
			Failed bool   `json:"failed"`
			Steps  []struct {
				Result struct {
					Status string `json:"status"`
					Error  string `json:"errorMessage"`
				} `json:"result"`
			} `json:"stepResults"`
		} `json:"scenarioResults"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	if report.CallDepth > 0 {
		return nil, nil
	}
	if report.Name == "" || report.Path == "" {
		return nil, fmt.Errorf("missing Karate feature identity")
	}
	var tests []Test
	for _, scenario := range report.Scenarios {
		if scenario.Name == "" {
			return nil, fmt.Errorf("missing Karate scenario identity")
		}
		test := Test{Module: module, Suite: "[" + strings.TrimSuffix(report.Path, ".feature") + "] " + report.Name, Name: scenario.Name, Status: "passed"}
		// A skipped/disabled Karate scenario has no executed step results.
		if len(scenario.Steps) == 0 {
			test.Status = "skipped"
		}
		if scenario.Failed {
			test.Status = "failed"
		}
		for _, step := range scenario.Steps {
			if step.Result.Status == "failed" {
				test.Failure += step.Result.Error + "\n"
			}
		}
		tests = append(tests, test)
	}
	return tests, nil
}
