// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

const reportFilename = "report.html"

type reportCard struct {
	Kind      string
	Title     string
	Count     int
	Context   string
	Tests     []reportTest
	Suites    []reportSuite
	Coverages []reportCoverage
}

type reportTest struct {
	Label         string
	Name          string
	Suite         string
	SourceFile    string
	Status        string
	Duration      string
	Attempts      []reportAttempt
	CoverageLevel string
	CoveredFiles  []string
	Source        reportSource
	DurationNanos int64
}

type reportAttempt struct {
	Number       int
	Status       string
	Duration     string
	Kind         string
	ErrorType    string
	ErrorMessage string
	ErrorStack   string
}

type reportCoverage struct {
	Name       string
	Level      string
	FileCount  int
	Files      []string
	SourceFile string
	Source     reportSource
}

type reportSource struct {
	Start int
	End   int
	Lines []reportSourceLine
	Error string
}

type reportSourceLine struct {
	Number int
	Code   template.HTML
}

type reportSuite struct {
	Key           string
	Name          string
	Status        string
	Duration      string
	TestCount     int
	CoveredCount  int
	ShowCoverage  bool
	CoveredFiles  []string
	Tests         []reportSuiteTest
	DurationNanos int64
}

type reportSuiteTest struct {
	TestIndex int
	Name      string
	Status    string
	Duration  string
}

type reportFact struct {
	Label string
	Value string
	Tone  string
}

type reportArtifact struct {
	Title string
	Href  string
}

type reportModel struct {
	NoTestEvents  bool
	CoverageLevel string
	Runtime       reportRuntime
	Summary       string
	Cards         []reportCard
	Facts         []reportFact
	Artifacts     []reportArtifact
	Suites        []reportSuite
	Tests         []reportTest
}

type reportRuntime struct{ Framework, Tracer, Command, Output, Error string }

type reportSourceFile struct {
	lines []string
	err   error
}

type reportSourceKey struct {
	file       string
	start, end int
}

type reportBuilder struct {
	repositoryRoot string
	sourceFiles    map[string]reportSourceFile
	sources        map[reportSourceKey]reportSource
}

func newReportBuilder(repositoryRoot string) *reportBuilder {
	return &reportBuilder{
		repositoryRoot: repositoryRoot,
		sourceFiles:    make(map[string]reportSourceFile),
		sources:        make(map[reportSourceKey]reportSource),
	}
}

func writeReport(repositoryRoot, sessionDirectory string, findings intake.Facts, commandFailed bool, runtime ...reportRuntime) (string, error) {
	path := filepath.Join(sessionDirectory, reportFilename)
	file, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create testdrive report: %w", err)
	}

	model := buildReport(repositoryRoot, findings, commandFailed, runtime...)
	executeErr := testdriveReport.Execute(file, model)
	closeErr := file.Close()
	if err := errors.Join(executeErr, closeErr); err != nil {
		return "", fmt.Errorf("write testdrive report: %w", err)
	}
	return path, nil
}

func buildReport(repositoryRoot string, findings intake.Facts, commandFailed bool, runtime ...reportRuntime) reportModel {
	builder := newReportBuilder(repositoryRoot)
	info := reportRuntime{Framework: "Test command", Tracer: "Not reported"}
	if len(runtime) > 0 {
		info = runtime[0]
	}
	status := passedFailed(!commandFailed)
	if findings.TestEventCount == 0 {
		status = "No test results received"
	}
	showTestCoverage := findings.CoverageLevel == "test"
	showSuiteCoverage := findings.CoverageLevel == "suite"
	suites := builder.reportSuites(findings.Tests, findings.SuiteCoverages, showSuiteCoverage)
	coverageLabel := "Tests with coverage"
	covered, total := findings.CoveredTestCount, findings.TestCount
	if showSuiteCoverage {
		coverageLabel = "Suites with coverage"
		covered, total = 0, len(suites)
		for _, suite := range suites {
			if suite.CoveredCount > 0 {
				covered++
			}
		}
	}
	coverageValue := fmt.Sprintf("%d / %d", covered, total)
	if covered == 0 {
		coverageValue = "Not reported"
	}
	model := reportModel{
		Summary:       "No findings.",
		NoTestEvents:  findings.TestEventCount == 0,
		Runtime:       info,
		CoverageLevel: findings.CoverageLevel,
		Facts: []reportFact{
			{Label: info.Framework, Value: status, Tone: factTone(!commandFailed && findings.TestEventCount > 0)},
			{Label: "Datadog library", Value: info.Tracer, Tone: "good"},
			{Label: coverageLabel, Value: coverageValue, Tone: factTone(total > 0 && covered == total)},
		},
		Artifacts: []reportArtifact{
			{Title: "JSON traffic", Href: "intake/"},
			{Title: "Test output", Href: testOutputFilename},
		},
		Tests: builder.reportTests(findings.Tests, showTestCoverage),
	}
	model.Suites = suites

	if findings.TestEventCount == 0 {
		model.Summary = "Check the instrumentation setup."
	}
	if findings.EmptyCoverageEntryCount > 0 {
		model.Cards = append(model.Cards, reportCard{
			Kind:    "error",
			Title:   "Tracer error: empty coverage entries",
			Count:   findings.EmptyCoverageEntryCount,
			Context: fmt.Sprintf("%d coverage entries had an empty files list. Affected payloads were excluded from coverage counts. Inspect the captured traffic.", findings.EmptyCoverageEntryCount),
		})
	}
	if len(findings.FailedTests) > 0 {
		model.Cards = append(model.Cards, reportCard{
			Kind: "failed", Title: "Failed tests", Context: "Inspect the errors and source behind each failure.", Count: len(findings.FailedTests),
			Tests: builder.reportTests(findings.FailedTests, showTestCoverage),
		})
	}
	if len(findings.FlakyTests) > 0 {
		model.Cards = append(model.Cards, reportCard{
			Kind: "flaky", Title: "Flaky tests", Context: "These tests both passed and failed across attempts.", Count: len(findings.FlakyTests),
			Tests: builder.reportTests(findings.FlakyTests, showTestCoverage),
		})
	}
	if len(findings.SlowTests) > 0 {
		model.Cards = append(model.Cards, reportCard{
			Kind: "slow", Title: "Slow tests", Count: len(findings.SlowTests),
			Context: "Median test time · " + formatDuration(findings.TestDurationMedian),
			Tests:   builder.reportTests(findings.SlowTests, showTestCoverage),
		})
	}
	if len(findings.SlowSuites) > 0 {
		byKey := make(map[string]reportSuite, len(model.Suites))
		for _, suite := range model.Suites {
			byKey[suite.Key] = suite
		}
		card := reportCard{Kind: "slow", Title: "Slow suites", Count: len(findings.SlowSuites), Context: "Median suite time · " + formatDuration(findings.SuiteDurationMedian)}
		for _, suite := range findings.SlowSuites {
			if row, ok := byKey[suite.Module+"\x00"+suite.Suite]; ok {
				card.Suites = append(card.Suites, row)
			}
		}
		model.Cards = append(model.Cards, card)
	}
	if len(findings.BroadCoverage) > 0 {
		model.Cards = append(model.Cards, reportCard{
			Kind: "coverage", Title: "Broad coverage", Count: len(findings.BroadCoverage),
			Context:   fmt.Sprintf("Median covered files · %d", findings.CoveredFilesMedian),
			Coverages: builder.reportCoverages(findings.BroadCoverage),
		})
	}
	count := len(findings.ConfigurationErrors) + findings.EmptyCoverageEntryCount
	for _, size := range []int{
		len(findings.FailedTests), len(findings.FlakyTests), len(findings.SlowTests), len(findings.SlowSuites), len(findings.BroadCoverage),
	} {
		count += size
	}
	if count > 0 {
		model.Summary = fmt.Sprintf("%d %s.", count, plural(count, "finding", "findings"))
	}
	if len(findings.ConfigurationErrors) > 0 {
		model.Summary += " Tracer configuration errors: " + strings.Join(findings.ConfigurationErrors, ", ") + ". Inspect the captured traffic and test output."
	}
	return model
}

func (builder *reportBuilder) reportTests(findings []intake.Test, showCoverage bool) []reportTest {
	tests := make([]reportTest, 0, len(findings))
	for _, finding := range findings {
		name := finding.Name
		if finding.Parameters != "" {
			name += " " + finding.Parameters
		}
		label := name
		if finding.Suite != "" {
			label = finding.Suite + " › " + label
		}
		if finding.Module != "" {
			label = finding.Module + " › " + label
		}
		status := testDisplayStatus(finding)
		suite := finding.Suite
		if finding.Module != "" {
			suite = finding.Module + " › " + suite
		}
		test := reportTest{
			Label: label, Name: name, Suite: suite,
			SourceFile: finding.SourceFile, Status: status,
			Duration:      formatDuration(findingDuration(finding)),
			DurationNanos: int64(findingDuration(finding)),
			Attempts:      make([]reportAttempt, 0, len(finding.Attempts)),
			Source:        builder.readSource(finding.SourceFile, finding.SourceStart, finding.SourceEnd),
		}
		if showCoverage && finding.CoverageLevel == "test" {
			test.CoverageLevel = finding.CoverageLevel
			test.CoveredFiles = finding.CoveredFiles
		}
		for attemptIndex, attempt := range finding.Attempts {
			kind := "Initial run"
			if attempt.Retry {
				kind = "Retry"
				if attempt.RetryReason != "" {
					kind += " · " + strings.ReplaceAll(attempt.RetryReason, "_", " ")
				}
			}
			test.Attempts = append(test.Attempts, reportAttempt{
				Number: attemptIndex + 1, Status: displayStatus(attempt.Status),
				Duration: formatDuration(attempt.Duration), Kind: kind,
				ErrorType: attempt.ErrorType, ErrorMessage: attempt.ErrorMessage, ErrorStack: attempt.ErrorStack,
			})
		}
		tests = append(tests, test)
	}
	return tests
}

func (builder *reportBuilder) reportCoverages(findings []intake.CoverageFact) []reportCoverage {
	coverages := make([]reportCoverage, 0, len(findings))
	for _, finding := range findings {
		files := slices.Clone(finding.Files)
		slices.Sort(files)
		coverage := reportCoverage{
			Name: finding.Name, Level: finding.Level, FileCount: finding.FileCount,
			Files: files, SourceFile: finding.SourceFile,
		}
		if finding.Level == "test" {
			coverage.Source = builder.readSource(finding.SourceFile, finding.SourceStart, finding.SourceEnd)
		}
		coverages = append(coverages, coverage)
	}
	return coverages
}

func (builder *reportBuilder) reportSuites(tests []intake.Test, coverages []intake.SuiteCoverage, showCoverage bool) []reportSuite {
	byName := make(map[string]*reportSuite)
	durations := make(map[string]time.Duration)
	for index, test := range tests {
		key := test.Module + "\x00" + test.Suite
		name := test.Suite
		if name == "" {
			name = "Unknown suite"
		}
		suite, found := byName[key]
		status := testDisplayStatus(test)
		if !found {
			if test.Module != "" {
				name = test.Module + " › " + name
			}
			suite = &reportSuite{Key: key, Name: name, Status: suiteStatus(status), ShowCoverage: showCoverage}
			byName[key] = suite
		} else if suiteStatusRank(status) > suiteStatusRank(suite.Status) {
			suite.Status = suiteStatus(status)
		}
		suite.TestCount++
		durations[key] += findingDuration(test)
		testName := test.Name
		if test.Parameters != "" {
			testName += " " + test.Parameters
		}
		suite.Tests = append(suite.Tests, reportSuiteTest{
			TestIndex: index, Name: testName, Status: status,
			Duration: formatDuration(findingDuration(test)),
		})
	}
	if showCoverage {
		for _, coverage := range coverages {
			key := coverage.Module + "\x00" + coverage.Suite
			if suite, found := byName[key]; found {
				files := slices.Clone(coverage.Files)
				slices.Sort(files)
				suite.CoveredCount = coverage.CoveredTests
				suite.CoveredFiles = files
			}
		}
	}

	suites := make([]reportSuite, 0, len(byName))
	for key, suite := range byName {
		suite.Duration = formatDuration(durations[key])
		suite.DurationNanos = int64(durations[key])
		suites = append(suites, *suite)
	}
	sort.Slice(suites, func(i, j int) bool { return suites[i].Name < suites[j].Name })
	return suites
}

func suiteStatus(status string) string {
	switch status {
	case "Fail":
		return "Failed"
	case "Pass":
		return "Passed"
	}
	return status
}

func suiteStatusRank(status string) int {
	switch suiteStatus(status) {
	case "Failed":
		return 4
	case "Flaky":
		return 3
	case "Passed", "Pass":
		return 2
	case "Skip":
		return 1
	default:
		return 0
	}
}

func testDisplayStatus(test intake.Test) string {
	status := test.Status
	sawPass := status == "pass"
	sawFailure := status == "fail"
	for _, attempt := range test.Attempts {
		sawPass = sawPass || attempt.Status == "pass"
		sawFailure = sawFailure || attempt.Status == "fail"
	}
	if sawPass && sawFailure {
		return "Flaky"
	}
	if status == "" && len(test.Attempts) > 0 {
		status = test.Attempts[len(test.Attempts)-1].Status
	}
	return displayStatus(status)
}

func findingDuration(test intake.Test) time.Duration {
	if test.Duration != 0 || len(test.Attempts) == 0 {
		return test.Duration
	}
	return test.Attempts[0].Duration
}

func readSource(repositoryRoot, sourceFile string, sourceStart, sourceEnd int) reportSource {
	return newReportBuilder(repositoryRoot).readSource(sourceFile, sourceStart, sourceEnd)
}

func (builder *reportBuilder) readSource(sourceFile string, sourceStart, sourceEnd int) reportSource {
	if sourceFile == "" {
		return reportSource{Error: "Source file not reported."}
	}
	if sourceStart < 1 {
		return reportSource{Error: "Source line not reported."}
	}
	key := reportSourceKey{file: sourceFile, start: sourceStart, end: sourceEnd}
	if source, cached := builder.sources[key]; cached {
		return source
	}
	path := sourceFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(builder.repositoryRoot, filepath.FromSlash(path))
	}
	file, cached := builder.sourceFiles[path]
	if !cached {
		contents, err := os.ReadFile(path)
		file.err = err
		if err == nil && len(contents) > 0 {
			text := strings.TrimSuffix(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n")
			file.lines = strings.Split(text, "\n")
		}
		builder.sourceFiles[path] = file
	}
	if file.err != nil {
		source := reportSource{Error: "Source could not be read: " + file.err.Error()}
		builder.sources[key] = source
		return source
	}
	lines := file.lines
	if sourceStart > len(lines) {
		source := reportSource{Error: fmt.Sprintf("Source line %d is outside %s.", sourceStart, sourceFile)}
		builder.sources[key] = source
		return source
	}
	if sourceEnd < sourceStart {
		sourceEnd = sourceStart + 4
	}
	sourceEnd = min(sourceEnd, len(lines))
	source := reportSource{Start: sourceStart, End: sourceEnd}
	inBlockComment := false
	for lineIndex := sourceStart - 1; lineIndex < sourceEnd; lineIndex++ {
		code := highlightJavaScriptLine(lines[lineIndex], &inBlockComment)
		if filepath.Ext(path) == ".py" || filepath.Ext(path) == ".rb" {
			code = template.HTML(template.HTMLEscapeString(lines[lineIndex]))
		}
		source.Lines = append(source.Lines, reportSourceLine{
			Number: lineIndex + 1,
			Code:   code,
		})
	}
	builder.sources[key] = source
	return source
}

func highlightJavaScriptLine(line string, inBlockComment *bool) template.HTML {
	var highlighted strings.Builder
	for index := 0; index < len(line); {
		if *inBlockComment {
			end := strings.Index(line[index:], "*/")
			if end < 0 {
				writeToken(&highlighted, "comment", line[index:])
				break
			}
			end += index + 2
			writeToken(&highlighted, "comment", line[index:end])
			*inBlockComment = false
			index = end
			continue
		}
		if strings.HasPrefix(line[index:], "//") {
			writeToken(&highlighted, "comment", line[index:])
			break
		}
		if strings.HasPrefix(line[index:], "/*") {
			end := strings.Index(line[index+2:], "*/")
			if end < 0 {
				writeToken(&highlighted, "comment", line[index:])
				*inBlockComment = true
				break
			}
			end += index + 4
			writeToken(&highlighted, "comment", line[index:end])
			index = end
			continue
		}
		if line[index] == '\'' || line[index] == '"' || line[index] == '`' {
			end := stringEnd(line, index)
			writeToken(&highlighted, "string", line[index:end])
			index = end
			continue
		}
		if isIdentifierStart(line[index]) {
			end := index + 1
			for end < len(line) && isIdentifierPart(line[end]) {
				end++
			}
			word := line[index:end]
			class := ""
			if javascriptKeywords[word] {
				class = "keyword"
			} else if javascriptLiterals[word] {
				class = "literal"
			}
			writeToken(&highlighted, class, word)
			index = end
			continue
		}
		if line[index] >= '0' && line[index] <= '9' {
			end := index + 1
			for end < len(line) && ((line[end] >= '0' && line[end] <= '9') || line[end] == '.') {
				end++
			}
			writeToken(&highlighted, "number", line[index:end])
			index = end
			continue
		}
		writeToken(&highlighted, "", line[index:index+1])
		index++
	}
	return template.HTML(highlighted.String())
}

func stringEnd(line string, start int) int {
	quote := line[start]
	escaped := false
	for index := start + 1; index < len(line); index++ {
		if escaped {
			escaped = false
			continue
		}
		if line[index] == '\\' {
			escaped = true
			continue
		}
		if line[index] == quote {
			return index + 1
		}
	}
	return len(line)
}

func writeToken(output *strings.Builder, class, value string) {
	escaped := template.HTMLEscapeString(value)
	if class == "" {
		output.WriteString(escaped)
		return
	}
	output.WriteString(`<span class="token-`)
	output.WriteString(class)
	output.WriteString(`">`)
	output.WriteString(escaped)
	output.WriteString(`</span>`)
}

func isIdentifierStart(character byte) bool {
	return character == '_' || character == '$' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
}

func isIdentifierPart(character byte) bool {
	return isIdentifierStart(character) || character >= '0' && character <= '9'
}

var javascriptKeywords = map[string]bool{
	"async": true, "await": true, "break": true, "case": true, "catch": true, "class": true,
	"const": true, "continue": true, "default": true, "delete": true, "do": true, "else": true,
	"export": true, "extends": true, "finally": true, "for": true, "from": true, "function": true,
	"if": true, "import": true, "in": true, "instanceof": true, "let": true, "new": true,
	"of": true, "return": true, "switch": true, "throw": true, "try": true, "typeof": true,
	"var": true, "void": true, "while": true, "with": true, "yield": true,
}

var javascriptLiterals = map[string]bool{
	"false": true, "null": true, "true": true, "undefined": true,
}

func passedFailed(passed bool) string {
	if passed {
		return "Passed"
	}
	return "Failed"
}

func factTone(good bool) string {
	if good {
		return "good"
	}
	return "attention"
}

// reportStatus provides consistent labels for the report's test and suite filters.
func reportStatus(status string) string {
	switch status {
	case "Pass", "Passed":
		return "Passed"
	case "Fail", "Failed":
		return "Failed"
	case "Skip", "Skipped":
		return "Skipped"
	case "Flaky":
		return "Flaky"
	default:
		return "Unknown"
	}
}

func displayStatus(status string) string {
	if status == "" {
		return "Unknown"
	}
	return strings.ToUpper(status[:1]) + status[1:]
}

func formatDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return duration.Round(time.Microsecond).String()
	}
	return duration.Round(time.Millisecond).String()
}

func fileURL(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return absoluteFileURL(absolutePath), nil
}

func absoluteFileURL(absolutePath string) string {
	slashPath := absolutePath
	// A backslash is a valid filename character in an absolute Unix path.
	if !strings.HasPrefix(absolutePath, "/") {
		slashPath = strings.ReplaceAll(absolutePath, `\`, "/")
	}
	if strings.HasPrefix(absolutePath, `\\`) {
		hostAndPath := strings.TrimPrefix(slashPath, "//")
		host, path, _ := strings.Cut(hostAndPath, "/")
		return (&url.URL{Scheme: "file", Host: host, Path: "/" + path}).String()
	}
	if len(slashPath) >= 2 && slashPath[1] == ':' {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

func terminalLink(target, label string) string {
	return "\x1b]8;;" + target + "\x1b\\" + label + "\x1b]8;;\x1b\\"
}

func plural(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

//go:embed report.html
var reportHTML string

var testdriveReport = template.Must(template.New("testdrive-report").Funcs(template.FuncMap{"plural": plural, "reportStatus": reportStatus}).Parse(reportHTML))
