// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

// jestTest is one framework-normalized test result used by paired validation.
type jestTest struct {
	WebdriverIOCapability string              `json:"webdriverio_capability,omitempty"`
	WebdriverIOTestID     string              `json:"webdriverio_test_id,omitempty"`
	WebdriverIOBrowser    string              `json:"webdriverio_browser,omitempty"`
	CucumberAttempts      []string            `json:"-"`
	WebdriverIOAttempts   []string            `json:"-"`
	CucumberLine          int                 `json:"-"`
	CypressAttempts       []string            `json:"-"`
	Playwright            *playwrightIdentity `json:"playwright,omitempty"`
	// NativeID is the runner's own unique example identity, reported in the
	// tracer's parameters metadata as scoped_id.
	NativeID string `json:"native_id,omitempty"`
	// telemetryClass qualifies telemetryName with its test class, which the
	// tracer reports as the suite.
	telemetryClass string
	// probe marks the controlled probe when its native name is not probeName.
	probe         bool
	telemetryName *string
	File          string `json:"file"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	Failure       string `json:"failure,omitempty"`
}

type nativeTest = jestTest

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// Object addresses differ between processes. Only addresses inside a
// recognizable object representation are replaced: Ruby's #<Foo:0x000001a2>
// (and #<Foo:0x000001a2 @ivar=...>), Python's <foo.Bar object at 0x7f3a>
// and Java's com.acme.Widget@1b6d3586, an array's [Ljava.lang.String;@1b6d3586
// or [I@1b6d3586, or a lambda's Test$$Lambda$1352/0x000000980176c488@4f3b5c60,
// whose number and hidden class address differ too. Bare hexadecimal values
// are data and stay, so differing values still differ.
var (
	inspectedObjectAddress = regexp.MustCompile(`(#<[A-Z][A-Za-z0-9_]*(?:::[A-Z][A-Za-z0-9_]*)*:)0x[0-9a-fA-F]{6,}`)
	reprObjectAddress      = regexp.MustCompile(`(<[A-Za-z_][A-Za-z0-9_.]* object at )0x[0-9a-fA-F]{6,}(>)`)
	lambdaObject           = regexp.MustCompile(`\$\$Lambda(?:\$[0-9]+)?(?:/0x[0-9a-f]+)?@[0-9a-f]{1,8}\b`)
	objectHash             = regexp.MustCompile(`(\b[A-Za-z_$][A-Za-z0-9_$]*(?:\.[A-Za-z_$][A-Za-z0-9_$]*)+|\[+L[A-Za-z_$][A-Za-z0-9_$.]*;|\[+[ZBCSIJFD])@[0-9a-f]{1,8}\b`)
)

// withoutObjectAddresses replaces object addresses in a failure message,
// so the same failure compares equal across runs.
func withoutObjectAddresses(message string) string {
	message = inspectedObjectAddress.ReplaceAllString(message, "${1}0x…")
	message = reprObjectAddress.ReplaceAllString(message, "${1}0x…${2}")
	message = lambdaObject.ReplaceAllString(message, "$$$$Lambda@…")
	return objectHash.ReplaceAllString(message, "$1@…")
}

func failureSignature(message string) string {
	var lines []string
	for _, line := range strings.Split(withoutObjectAddresses(ansiEscape.ReplaceAllString(message, "")), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "at ") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func commandDiagnostic(output []byte) string {
	text := []rune(failureSignature(string(output)))
	const limit = diagnosticLimit
	if len(text) > limit {
		const prefix = "[truncated] …"
		return prefix + string(text[len(text)-(limit-len([]rune(prefix))):])
	}
	return string(text)
}

func executedTests(run validationRun) int {
	count := 0
	for _, test := range run.Tests {
		if test.Status == "passed" || test.Status == "failed" || (run.framework == "cucumber" && (test.Status == "pending" || test.Status == "undefined")) {
			count++
		}
	}
	return count
}

func outcomeKeys(run validationRun) []string {
	keys := []string{fmt.Sprintf("exit=%d", run.ExitCode), "result=" + run.ResultError}
	keys = append(keys, run.SuiteErrors...)
	keys = append(keys, run.SuiteFailures...)
	for _, test := range run.Tests {
		scoped := ""
		if test.NativeID != "" {
			scoped = " [scoped_id: " + test.NativeID + "]"
		}
		keys = append(keys, repositoryTestFile(run, test.File)+" › "+test.Name+scoped+cucumberLocation(test)+playwrightLabel(test)+webdriverIOOutcome(test)+": "+test.Status+"\n"+test.Failure+playwrightOutcome(test)+cypressOutcome(test)+cucumberOutcome(test)+webdriverIOAttemptOutcome(test))
	}
	for i := range keys {
		keys[i] = normalizeOwnedTemporaryPath(run, keys[i])
	}
	slices.Sort(keys)
	return keys
}

// compareRuns compares an uninstrumented and a reporting-only run of the same
// suite, then matches the reporting run's telemetry with telemetry.
func compareRuns(baseline, instrumented validationRun, telemetry func(validationRun) verdict) verdict {
	if reason := pairTimeout(baseline, instrumented); reason != "" {
		return verdict{Status: "inconclusive", Code: "command_timeout", Reason: reason}
	}
	if baseline.ResultError != "" {
		return verdict{Status: "inconclusive", Reason: "The uninstrumented run did not produce usable results: " + reportText(baseline.ResultError) + "."}
	}
	if executedTests(baseline) == 0 {
		return verdict{Status: "inconclusive", Reason: "The uninstrumented run did not produce results for any executed tests; check project setup using the recorded baseline command."}
	}
	if baseline.Facts.TestEventCount > 0 {
		return verdict{Status: "inconclusive", Reason: "The baseline emitted test telemetry despite instrumentation being disabled; a clean comparison was not established."}
	}
	left, right := outcomeKeys(baseline), outcomeKeys(instrumented)
	if !slices.Equal(left, right) {
		differences := []string{}
		counts := make(map[string]int)
		for _, key := range left {
			counts[key]++
		}
		for _, key := range right {
			counts[key]--
		}
		for key, count := range counts {
			if count == 0 {
				continue
			}
			label, _, _ := strings.Cut(key, "\n")
			side := "Without instrumentation only"
			if count < 0 {
				side = "With instrumentation only"
			}
			differences = append(differences, side+": "+label)
		}
		slices.Sort(differences)
		return verdict{Status: "suspected regression", Reason: "Uninstrumented and reporting-only results differ.", Differences: differences}
	}
	if telemetry := telemetry(instrumented); telemetry.Status != "" {
		return telemetry
	}
	return verdict{Status: "compatible", Reason: "No observed behavioral regression: test identities, outcomes, failure details, and exit code match. Existing test failures are acceptable; reporting telemetry was verified."}
}

func verifyNativeTelemetry(run validationRun, includeSkipped bool) verdict {
	if run.framework == "webdriverio" {
		return verifyWebdriverIOTelemetry(run)
	}
	if run.framework == "cucumber" {
		return verifyCucumberTelemetry(run)
	}
	if run.framework == "playwright" {
		return verifyPlaywrightTelemetry(run)
	}
	if run.framework == "cypress" {
		return verifyCypressTelemetry(run)
	}
	if run.Facts.TestEventCount == 0 {
		return verdict{Status: "inconclusive", Reason: "The instrumented run sent no test events; matching test outcomes do not prove instrumentation worked."}
	}
	if len(run.Facts.ConfigurationErrors) > 0 {
		return verdict{Status: "inconclusive", Reason: "The tracer reported configuration errors: " + strings.Join(run.Facts.ConfigurationErrors, ", ")}
	}
	type identity struct{ file, name, status string }
	expected := make(map[identity]int)
	scopedExamples := make(map[string]bool)
	classes := make(map[string][]string)
	for _, test := range run.Tests {
		status := map[string]string{"passed": "pass", "failed": "fail"}[test.Status]
		if includeSkipped && test.Status == "skipped" {
			status = "skip"
		}
		if status != "" {
			name := test.Name
			if test.telemetryName != nil {
				name = *test.telemetryName
			}
			if test.telemetryClass != "" {
				key := test.File + "\x00" + name
				if !slices.Contains(classes[key], test.telemetryClass) {
					classes[key] = append(classes[key], test.telemetryClass)
				}
				name = test.telemetryClass + "\x00" + name
			}
			if test.NativeID != "" {
				name = "scoped_id:" + test.NativeID
				scopedExamples[test.File+"\x00"+name] = true
			}
			expected[identity{test.File, name, status}]++
		}
	}
	observed := make(map[identity]int)
	var differences []string
	for _, test := range run.Facts.Tests {
		// The tracer reports source files relative to the Git checkout, and
		// suite names relative to the test process directory.
		file, base := test.SourceFile, run.checkoutRoot
		if file == "" {
			file, base = test.Suite, run.root
		}
		if base == "" {
			base = run.root
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(base, file)
		}
		file = filepath.Clean(file)
		name := test.Name
		if candidates := classes[file+"\x00"+name]; len(candidates) > 0 {
			var matches []string
			for _, class := range candidates {
				if test.Suite == class || strings.HasPrefix(test.Suite, class+" at ") {
					matches = append(matches, class)
				}
			}
			// Missing or ambiguous class evidence must not match by method alone.
			if len(matches) == 1 {
				name = matches[0] + "\x00" + name
			}
		}
		if len(scopedExamples) > 0 {
			var parameters struct {
				Metadata struct {
					ScopedID string `json:"scoped_id"`
				}
			}
			if json.Unmarshal([]byte(test.Parameters), &parameters) == nil {
				id := "scoped_id:" + parameters.Metadata.ScopedID
				if scopedExamples[file+"\x00"+id] {
					name = id
				}
			}
		}
		observe := func(attempt intake.TestRun) {
			if attempt.Status == "pass" || attempt.Status == "fail" || includeSkipped && attempt.Status == "skip" {
				observed[identity{file, name, attempt.Status}]++
			}
		}
		// A span sent twice is one execution; counting it again could hide a
		// missing test that shares its name, such as another outline example.
		spans := map[uint64]int{}
		attempts := make([]intake.TestRun, 0, len(test.Attempts))
		for _, attempt := range test.Attempts {
			if attempt.SpanID != 0 {
				spans[attempt.SpanID]++
				if spans[attempt.SpanID] > 1 {
					continue
				}
			}
			attempts = append(attempts, attempt)
		}
		for _, count := range spans {
			if count > 1 {
				differences = append(differences, fmt.Sprintf("Duplicate telemetry: file %q, test %q, one span reported %d times", file, test.Name, count))
			}
		}
		var final *intake.TestRun
		for i, attempt := range attempts {
			if attempt.Retry && attempt.RetryReason != "external" && attempt.RetryReason != "" {
				return verdict{Status: "inconclusive", Code: "unexpected_retry", Reason: "Unexpected tracer-controlled retry telemetry in the reporting-only run."}
			}
			if !attempt.Retry && final != nil {
				observe(*final)
			}
			final = &attempts[i]
		}
		if final != nil {
			observe(*final)
		}
	}
	for key, count := range expected {
		if count > observed[key] {
			differences = append(differences, fmt.Sprintf("Missing telemetry: file %q, test %q, status %q (count %d)", key.file, key.name, key.status, count-observed[key]))
		}
	}
	for key, count := range observed {
		if count > expected[key] {
			differences = append(differences, fmt.Sprintf("Unexpected telemetry: file %q, test %q, status %q (count %d)", key.file, key.name, key.status, count-expected[key]))
		}
	}
	if len(differences) > 0 {
		slices.Sort(differences)
		return verdict{Status: "inconclusive", Reason: "Reported telemetry does not match the executed tests.", Differences: differences}
	}
	return verdict{}
}

// Only the temporary directory created for this run is interchangeable. Keep
// file suffixes and arbitrary customer paths/names intact, including failures.
// repositoryTestFile shows a native test file relative to the checkout the run
// used, so differences name repository paths, not a temporary copy.
func repositoryTestFile(run validationRun, file string) string {
	if run.root == "" || !filepath.IsAbs(file) {
		return file
	}
	if relative, err := filepath.Rel(run.root, file); err == nil && filepath.IsLocal(relative) {
		return filepath.ToSlash(relative)
	}
	return file
}

func normalizeOwnedTemporaryPath(run validationRun, value string) string {
	if run.temporaryRoot == "" {
		return value
	}
	return strings.ReplaceAll(value, run.temporaryRoot+string(filepath.Separator), "<ddtest-tmp>/")
}
