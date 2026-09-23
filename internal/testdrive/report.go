// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
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
	Title     string
	Count     int
	Context   string
	Tests     []reportTest
	Coverages []reportCoverage
}

type reportTest struct {
	Label         string
	Name          string
	Suite         string
	SourceFile    string
	Status        string
	Tone          string
	Duration      string
	Attempts      []reportAttempt
	CoverageLevel string
	CoveredFiles  []string
	Source        reportSource
}

type reportAttempt struct {
	Number       int
	Status       string
	Tone         string
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
	Name         string
	Status       string
	Tone         string
	Duration     string
	TestCount    int
	CoveredCount int
	ShowCoverage bool
	CoveredFiles []string
	Tests        []reportSuiteTest
}

type reportSuiteTest struct {
	Name     string
	Status   string
	Tone     string
	Duration string
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
	Headline  string
	Summary   string
	Cards     []reportCard
	Facts     []reportFact
	Artifacts []reportArtifact
	Suites    []reportSuite
	Tests     []reportTest
}

type reportRuntime struct{ Framework, Tracer string }

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
	info := reportRuntime{Framework: "Test command", Tracer: "Isolated installation"}
	if len(runtime) > 0 {
		info = runtime[0]
	}
	showTestCoverage := findings.CoverageLevel == "test"
	showSuiteCoverage := findings.CoverageLevel == "suite"
	model := reportModel{
		Headline: "Test events received.",
		Summary:  "No findings.",
		Facts: []reportFact{
			{Label: "Test events", Value: fmt.Sprintf("%d", findings.TestEventCount), Tone: factTone(findings.TestEventCount > 0)},
			{Label: "Tests with coverage", Value: fmt.Sprintf("%d / %d", findings.CoveredTestCount, findings.TestCount), Tone: factTone(findings.TestCount > 0 && findings.CoveredTestCount == findings.TestCount)},
			{Label: info.Framework, Value: passedFailed(!commandFailed), Tone: factTone(!commandFailed)},
			{Label: "Tracer", Value: info.Tracer + " · isolated", Tone: "good"},
		},
		Artifacts: []reportArtifact{
			{Title: "JSON traffic", Href: "intake/"},
			{Title: "Test output", Href: testOutputFilename},
		},
		Tests: reportTests(repositoryRoot, findings.Tests, showTestCoverage),
	}
	model.Suites = reportSuites(findings.Tests, showSuiteCoverage)
	if findings.CoveredTestCount == 0 {
		model.Facts[1].Value = "Not reported"
	}

	if findings.TestEventCount == 0 {
		model.Headline = "No test events received."
		model.Summary = "Check the instrumentation setup."
	}
	if findings.EmptyCoverageEntryCount > 0 {
		model.Cards = append(model.Cards, reportCard{
			Title:   "Tracer error: empty coverage entries",
			Count:   findings.EmptyCoverageEntryCount,
			Context: fmt.Sprintf("%d coverage entries had an empty files list. Affected payloads were excluded from coverage counts. Inspect the captured traffic.", findings.EmptyCoverageEntryCount),
		})
	}
	if len(findings.FailedTests) > 0 {
		model.Cards = append(model.Cards, reportCard{
			Title: "Any tests failed?", Count: len(findings.FailedTests),
			Tests: reportTests(repositoryRoot, findings.FailedTests, showTestCoverage),
		})
	}
	if len(findings.FlakyTests) > 0 {
		model.Cards = append(model.Cards, reportCard{
			Title: "Any flaky tests?", Count: len(findings.FlakyTests),
			Tests: reportTests(repositoryRoot, findings.FlakyTests, showTestCoverage),
		})
	}
	if len(findings.SlowTests) > 0 {
		model.Cards = append(model.Cards, reportCard{
			Title: "Any tests slower than the others?", Count: len(findings.SlowTests),
			Context: "Median test time · " + formatDuration(findings.TestDurationMedian),
			Tests:   reportTests(repositoryRoot, findings.SlowTests, showTestCoverage),
		})
	}
	if len(findings.BroadCoverage) > 0 {
		model.Cards = append(model.Cards, reportCard{
			Title: "Any unusually broad test coverage?", Count: len(findings.BroadCoverage),
			Context:   fmt.Sprintf("Median covered files · %d", findings.CoveredFilesMedian),
			Coverages: reportCoverages(repositoryRoot, findings.BroadCoverage),
		})
	}
	if len(model.Cards) > 0 && findings.TestEventCount > 0 {
		model.Summary = fmt.Sprintf("%d findings.", len(model.Cards))
	}
	if len(findings.ConfigurationErrors) > 0 {
		model.Summary = "Tracer configuration errors: " + strings.Join(findings.ConfigurationErrors, ", ") + ". Inspect the captured traffic and test output."
	}
	return model
}

func reportTests(repositoryRoot string, findings []intake.Test, showCoverage bool) []reportTest {
	tests := make([]reportTest, 0, len(findings))
	for _, finding := range findings {
		label := finding.Name
		if finding.Suite != "" {
			label = finding.Suite + " › " + finding.Name
		}
		status, tone := testDisplayStatus(finding)
		test := reportTest{
			Label: label, Name: finding.Name, Suite: finding.Suite,
			SourceFile: finding.SourceFile, Status: status, Tone: tone,
			Duration: formatDuration(findingDuration(finding)),
			Attempts: make([]reportAttempt, 0, len(finding.Attempts)),
			Source:   readSource(repositoryRoot, finding.SourceFile, finding.SourceStart, finding.SourceEnd),
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
				Number: attemptIndex + 1, Status: displayStatus(attempt.Status), Tone: attemptTone(attempt.Status),
				Duration: formatDuration(attempt.Duration), Kind: kind,
				ErrorType: attempt.ErrorType, ErrorMessage: attempt.ErrorMessage, ErrorStack: attempt.ErrorStack,
			})
		}
		tests = append(tests, test)
	}
	return tests
}

func reportCoverages(repositoryRoot string, findings []intake.CoverageFact) []reportCoverage {
	coverages := make([]reportCoverage, 0, len(findings))
	for _, finding := range findings {
		files := slices.Clone(finding.Files)
		slices.Sort(files)
		coverage := reportCoverage{
			Name: finding.Name, Level: finding.Level, FileCount: finding.FileCount,
			Files: files, SourceFile: finding.SourceFile,
		}
		if finding.Level == "test" {
			coverage.Source = readSource(repositoryRoot, finding.SourceFile, finding.SourceStart, finding.SourceEnd)
		}
		coverages = append(coverages, coverage)
	}
	return coverages
}

func reportSuites(tests []intake.Test, showCoverage bool) []reportSuite {
	byName := make(map[string]*reportSuite)
	durations := make(map[string]time.Duration)
	for _, test := range tests {
		name := test.Suite
		if name == "" {
			name = "Unknown suite"
		}
		suite, found := byName[name]
		if !found {
			suite = &reportSuite{Name: name, Status: "Passed", Tone: "good", ShowCoverage: showCoverage}
			byName[name] = suite
		}
		status, tone := testDisplayStatus(test)
		if status == "Fail" {
			suite.Status = "Failed"
			suite.Tone = "attention"
		} else if status == "Flaky" && suite.Status != "Failed" {
			suite.Status = "Flaky"
			suite.Tone = "attention"
		}
		suite.TestCount++
		durations[name] += findingDuration(test)
		if showCoverage && test.CoverageLevel == "suite" {
			suite.CoveredCount++
			suite.CoveredFiles = appendUniqueStrings(suite.CoveredFiles, test.CoveredFiles...)
		}
		suite.Tests = append(suite.Tests, reportSuiteTest{
			Name: test.Name, Status: status, Tone: tone,
			Duration: formatDuration(findingDuration(test)),
		})
	}

	suites := make([]reportSuite, 0, len(byName))
	for _, suite := range byName {
		suite.Duration = formatDuration(durations[suite.Name])
		suites = append(suites, *suite)
	}
	sort.Slice(suites, func(i, j int) bool { return suites[i].Name < suites[j].Name })
	return suites
}

func testDisplayStatus(test intake.Test) (string, string) {
	status := test.Status
	sawPass := status == "pass"
	sawFailure := status == "fail"
	for _, attempt := range test.Attempts {
		sawPass = sawPass || attempt.Status == "pass"
		sawFailure = sawFailure || attempt.Status == "fail"
	}
	if sawPass && sawFailure {
		return "Flaky", "attention"
	}
	if status == "" && len(test.Attempts) > 0 {
		status = test.Attempts[len(test.Attempts)-1].Status
	}
	return displayStatus(status), attemptTone(status)
}

func findingDuration(test intake.Test) time.Duration {
	if test.Duration != 0 || len(test.Attempts) == 0 {
		return test.Duration
	}
	return test.Attempts[0].Duration
}

func appendUniqueStrings(values []string, additions ...string) []string {
	for _, addition := range additions {
		if slices.Contains(values, addition) {
			continue
		}
		values = append(values, addition)
	}
	slices.Sort(values)
	return values
}

func readSource(repositoryRoot, sourceFile string, sourceStart, sourceEnd int) reportSource {
	if sourceFile == "" {
		return reportSource{Error: "Source file not reported."}
	}
	if sourceStart < 1 {
		return reportSource{Error: "Source line not reported."}
	}
	path := sourceFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(repositoryRoot, filepath.FromSlash(path))
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return reportSource{Error: "Source could not be read: " + err.Error()}
	}
	lines := strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n")
	if sourceStart > len(lines) {
		return reportSource{Error: fmt.Sprintf("Source line %d is outside %s.", sourceStart, sourceFile)}
	}
	if sourceEnd < sourceStart {
		if filepath.Ext(path) == ".py" || filepath.Ext(path) == ".rb" {
			sourceEnd = min(sourceStart+11, len(lines))
		} else {
			sourceEnd = inferJavaScriptTestEnd(lines, sourceStart)
		}
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
	return source
}

func inferJavaScriptTestEnd(lines []string, sourceStart int) int {
	parentheses := 0
	sawParenthesis := false
	inBlockComment := false
	var quote byte
	escaped := false
	for lineIndex := sourceStart - 1; lineIndex < len(lines); lineIndex++ {
		line := lines[lineIndex]
		for characterIndex := 0; characterIndex < len(line); characterIndex++ {
			character := line[characterIndex]
			if inBlockComment {
				if character == '*' && characterIndex+1 < len(line) && line[characterIndex+1] == '/' {
					inBlockComment = false
					characterIndex++
				}
				continue
			}
			if quote != 0 {
				if escaped {
					escaped = false
					continue
				}
				if character == '\\' {
					escaped = true
					continue
				}
				if character == quote {
					quote = 0
				}
				continue
			}
			if character == '/' && characterIndex+1 < len(line) {
				switch line[characterIndex+1] {
				case '/':
					characterIndex = len(line)
					continue
				case '*':
					inBlockComment = true
					characterIndex++
					continue
				}
			}
			if character == '\'' || character == '"' || character == '`' {
				quote = character
				continue
			}
			switch character {
			case '(':
				parentheses++
				sawParenthesis = true
			case ')':
				parentheses--
				if sawParenthesis && parentheses == 0 && onlyStatementEnd(line[characterIndex+1:]) {
					return lineIndex + 1
				}
			}
		}
	}
	return min(sourceStart+19, len(lines))
}

func onlyStatementEnd(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || value == ";" || strings.HasPrefix(value, "//")
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

func displayStatus(status string) string {
	if status == "" {
		return "Unknown"
	}
	return strings.ToUpper(status[:1]) + status[1:]
}

func attemptTone(status string) string {
	if status == "pass" {
		return "good"
	}
	return "attention"
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
	slashPath := strings.ReplaceAll(absolutePath, `\`, "/")
	if strings.HasPrefix(slashPath, "//") {
		hostAndPath := strings.TrimPrefix(slashPath, "//")
		host, path, _ := strings.Cut(hostAndPath, "/")
		return (&url.URL{Scheme: "file", Host: host, Path: "/" + path}).String()
	}
	if len(slashPath) >= 2 && slashPath[1] == ':' {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

func terminalLink(target string) string {
	return "\x1b]8;;" + target + "\x1b\\" + target + "\x1b]8;;\x1b\\"
}

func plural(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

var testdriveReport = template.Must(template.New("testdrive-report").Funcs(template.FuncMap{"plural": plural}).Parse(`
{{define "attempts"}}
  <div class="attempts">
    {{range .Attempts}}<article class="attempt">
      <div class="attempt-head">
        <span class="status {{.Tone}}">{{.Status}}</span>
        <span>Run {{.Number}} · {{.Kind}}</span>
        <span>{{.Duration}}</span>
      </div>
      {{if .ErrorMessage}}<p class="error-message">{{if .ErrorType}}{{.ErrorType}}: {{end}}{{.ErrorMessage}}</p>{{end}}
      {{if .ErrorStack}}<details class="stack"><summary>Stack trace</summary><pre>{{.ErrorStack}}</pre></details>{{end}}
    </article>{{end}}
  </div>
{{end}}
{{define "source"}}
  {{if .Lines}}<div class="source-block">
    <div class="source-heading">Source · lines {{.Start}}–{{.End}}</div>
    <pre class="source"><code>{{range .Lines}}<span class="source-line"><span class="line-number">{{.Number}}</span><span class="line-code">{{.Code}}</span></span>{{end}}</code></pre>
  </div>{{else if .Error}}<p class="source-error">{{.Error}}</p>{{end}}
{{end}}
{{define "covered-files"}}
  {{if .}}<div class="covered-files-wrap">
    <ul class="covered-files" data-paginated data-page-size="50">{{range .}}<li class="page-item">{{.}}</li>{{end}}</ul>
    <div class="pager"><button type="button" data-prev>Previous</button><span data-page></span><button type="button" data-next>Next</button></div>
  </div>{{end}}
{{end}}
{{define "test-detail"}}
  <div class="expanded">
    {{if .SourceFile}}<p class="source-path">{{.SourceFile}}</p>{{end}}
    {{template "attempts" .}}
    {{if .CoverageLevel}}<div class="coverage"><strong>Coverage · {{.CoverageLevel}} level · {{len .CoveredFiles}} {{plural (len .CoveredFiles) "file" "files"}}</strong>{{template "covered-files" .CoveredFiles}}</div>{{end}}
    {{template "source" .Source}}
  </div>
{{end}}
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>ddtest · Test Optimization report</title>
  <style>
    :root { color-scheme: light; --ink: #17151c; --muted: #68656f; --line: #dedce2; --surface: #fff; --background: #f6f6f7; --accent: #632ca6; --failure: #b42318; }
    * { box-sizing: border-box; }
    [hidden] { display: none !important; }
    body { margin: 0; color: var(--ink); background: var(--background); font: 15px/1.45 ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    button { font: inherit; }
    main { width: min(1040px, calc(100% - 32px)); margin: 0 auto; padding: 40px 0 64px; }
    .eyebrow { margin: 0 0 8px; color: var(--muted); font-size: .76rem; font-weight: 700; letter-spacing: .08em; text-transform: uppercase; }
    h1 { margin: 0; font-size: clamp(1.7rem, 3vw, 2.15rem); line-height: 1.15; letter-spacing: -.03em; }
    .summary { margin: 8px 0 28px; color: var(--muted); }
    .tabs { display: flex; gap: 24px; border-bottom: 1px solid var(--line); margin-bottom: 24px; }
    .tab { margin-bottom: -1px; padding: 11px 1px; border: 0; border-bottom: 2px solid transparent; color: var(--muted); background: none; cursor: pointer; }
    .tab.active { border-bottom-color: var(--accent); color: var(--ink); font-weight: 700; }
    .count { color: var(--muted); font-size: .82rem; }
    .section-title { margin: 0 0 12px; font-size: 1rem; }
    .problems { display: grid; gap: 12px; margin-bottom: 28px; }
    .problem-card, .run, .result-row { border: 1px solid var(--line); border-radius: 10px; background: var(--surface); }
    .problem-header { display: flex; justify-content: space-between; gap: 16px; align-items: baseline; padding: 16px 18px; border-bottom: 1px solid var(--line); }
    .problem-header h2 { margin: 0; font-size: 1rem; }
    .problem-summary { display: flex; gap: 14px; align-items: baseline; }
    .problem-context { color: var(--muted); font-size: .82rem; }
    .problem-count { color: var(--failure); font-weight: 700; }
    details.result { border-top: 1px solid #ecebef; }
    details.result:first-child { border-top: 0; }
    details.result > summary, details.result-row > summary { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; gap: 18px; align-items: center; padding: 13px 18px; cursor: pointer; list-style: none; }
    summary::-webkit-details-marker { display: none; }
    details.result > summary::after, details.result-row > summary::after { content: "+"; color: var(--muted); font-size: 1.05rem; }
    details[open].result > summary::after, details[open].result-row > summary::after { content: "−"; }
    .result-name { min-width: 0; font-weight: 650; overflow-wrap: anywhere; }
    .result-meta { color: var(--muted); font-size: .84rem; white-space: nowrap; }
    .expanded { padding: 4px 18px 18px; border-top: 1px solid #ecebef; }
    .source-path { margin: 14px 0; color: var(--muted); font: .8rem ui-monospace, SFMono-Regular, Menlo, monospace; }
    .attempts { display: grid; gap: 8px; }
    .attempt { padding: 12px 14px; border: 1px solid #e9e7ec; border-radius: 8px; background: #fafafa; }
    .attempt-head { display: flex; flex-wrap: wrap; gap: 7px 14px; color: var(--muted); font-size: .84rem; }
    .status { color: var(--ink); font-weight: 700; }
    .status.attention { color: var(--failure); }
    .error-message { margin: 10px 0 0; color: var(--failure); white-space: pre-wrap; font: .8rem/1.5 ui-monospace, SFMono-Regular, Menlo, monospace; }
    .stack { margin-top: 8px; }
    .stack summary { color: var(--muted); cursor: pointer; font-size: .8rem; }
    pre { overflow: auto; margin: 8px 0 0; padding: 14px; border-radius: 7px; background: #f2f2f3; color: #2d2a32; font: .78rem/1.55 ui-monospace, SFMono-Regular, Menlo, monospace; white-space: pre; }
    .coverage { margin-top: 14px; font-size: .82rem; }
    .covered-files { margin: 8px 0 0; padding: 0; border: 1px solid var(--line); border-radius: 7px; list-style: none; }
    .covered-files li { padding: 6px 9px; border-top: 1px solid var(--line); color: var(--muted); font: .74rem ui-monospace, SFMono-Regular, Menlo, monospace; overflow-wrap: anywhere; }
    .covered-files li:first-child { border-top: 0; }
    .covered-files-wrap > .pager { margin-top: 8px; }
    .source-block { margin-top: 16px; }
    .source-heading { color: var(--muted); font-size: .8rem; font-weight: 650; }
    pre.source { padding: 10px 0; }
    .source-line { display: grid; grid-template-columns: 46px minmax(max-content, 1fr); min-height: 1.55em; }
    .line-number { padding-right: 12px; color: #96929c; text-align: right; user-select: none; }
    .line-code { padding-right: 14px; }
    .token-keyword { color: #592b8c; font-weight: 650; }
    .token-string { color: #77540a; }
    .token-comment { color: #77737c; font-style: italic; }
    .token-number, .token-literal { color: #315f7d; }
    .source-error { color: var(--muted); font-size: .8rem; }
    .run { margin-top: 28px; padding: 17px 18px; }
    .facts { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); margin: 0; }
    .fact { padding: 0 16px; border-left: 1px solid var(--line); }
    .fact:first-child { padding-left: 0; border-left: 0; }
    .fact dt { color: var(--muted); font-size: .78rem; }
    .fact dd { margin: 4px 0 0; font-weight: 700; }
    .fact.attention dd { color: var(--failure); }
    .artifacts { display: flex; gap: 18px; margin-top: 17px; padding-top: 14px; border-top: 1px solid var(--line); }
    .artifact { color: var(--accent); font-size: .86rem; font-weight: 650; text-decoration: none; }
    .artifact:hover { text-decoration: underline; }
    .results { display: grid; gap: 8px; }
    details.result-row > summary { grid-template-columns: minmax(0, 1fr) auto auto auto auto; }
    .suite-detail { padding: 4px 18px 18px; border-top: 1px solid #ecebef; }
    .suite-tests { width: 100%; margin-top: 12px; border-collapse: collapse; font-size: .84rem; }
    .suite-tests th, .suite-tests td { padding: 8px 0; border-bottom: 1px solid #ecebef; text-align: left; }
    .suite-tests th { color: var(--muted); font-weight: 500; }
    .suite-tests th:last-child, .suite-tests td:last-child { text-align: right; }
    .pager { display: flex; justify-content: flex-end; gap: 12px; align-items: center; margin-top: 14px; color: var(--muted); font-size: .82rem; }
    .pager button { padding: 6px 10px; border: 1px solid var(--line); border-radius: 6px; background: var(--surface); cursor: pointer; }
    .pager button:disabled { cursor: default; opacity: .45; }
    .empty { color: var(--muted); }
    @media (max-width: 720px) { .facts { grid-template-columns: repeat(2, 1fr); gap: 16px 0; } .fact:nth-child(3) { padding-left: 0; border-left: 0; } details.result-row > summary { grid-template-columns: minmax(0, 1fr) auto auto auto; } .result-meta.secondary { display: none; } }
  </style>
</head>
<body>
  <main>
    <p class="eyebrow">ddtest · local testdrive</p>
    <h1>{{.Headline}}</h1>
    <p class="summary">{{.Summary}}</p>

    <nav class="tabs" aria-label="Report sections">
      <button class="tab active" type="button" data-tab="overview">Overview</button>
      <button class="tab" type="button" data-tab="suites">Suites <span class="count">{{len .Suites}}</span></button>
      <button class="tab" type="button" data-tab="tests">Tests <span class="count">{{len .Tests}}</span></button>
    </nav>

    <section id="overview" class="panel">
      {{if .Cards}}<div class="problems">
        {{range .Cards}}<article class="problem-card">
          <header class="problem-header"><h2>{{.Title}}</h2><div class="problem-summary">{{if .Context}}<span class="problem-context">{{.Context}}</span>{{end}}<span class="problem-count">{{.Count}}</span></div></header>
          <div class="problem-list">
            {{range .Tests}}<details class="result">
              <summary><span class="result-name">{{.Label}}</span><span class="result-meta">{{.Status}} · {{.Duration}}</span></summary>
              {{template "test-detail" .}}
            </details>{{end}}
            {{range .Coverages}}<details class="result">
              <summary><span class="result-name">{{.Name}}</span><span class="result-meta">{{.FileCount}} {{plural .FileCount "file" "files"}} · {{.Level}} level</span></summary>
              <div class="expanded">
                {{if .SourceFile}}<p class="source-path">{{.SourceFile}}</p>{{end}}
                {{template "covered-files" .Files}}
                {{template "source" .Source}}
              </div>
            </details>{{end}}
          </div>
        </article>{{end}}
      </div>{{end}}

      <section class="run" aria-label="Run details">
        <h2 class="section-title">Run details</h2>
        <dl class="facts">{{range .Facts}}<div class="fact {{.Tone}}"><dt>{{.Label}}</dt><dd>{{.Value}}</dd></div>{{end}}</dl>
        <div class="artifacts">{{range .Artifacts}}<a class="artifact" href="{{.Href}}">{{.Title}} ↗</a>{{end}}</div>
      </section>
    </section>

    <section id="suites" class="panel" hidden>
      <h2 class="section-title">All test suites</h2>
      {{if .Suites}}<div class="results" data-paginated data-page-size="20">
        {{range .Suites}}<details class="result-row page-item">
          <summary>
            <span class="result-name">{{.Name}}</span>
            <span class="result-meta secondary">{{.TestCount}} {{plural .TestCount "test" "tests"}}{{if .ShowCoverage}} · {{.CoveredCount}} with coverage{{end}}</span>
            <span class="result-meta status {{.Tone}}">{{.Status}}</span>
            <span class="result-meta">{{.Duration}}</span>
          </summary>
          <div class="suite-detail">
            {{if .CoveredFiles}}<div class="coverage"><strong>Covered {{plural (len .CoveredFiles) "file" "files"}} · {{len .CoveredFiles}}</strong>{{template "covered-files" .CoveredFiles}}</div>{{end}}
            <table class="suite-tests"><thead><tr><th>Test</th><th>Status</th><th>Time</th></tr></thead><tbody>
              {{range .Tests}}<tr><td>{{.Name}}</td><td class="status {{.Tone}}">{{.Status}}</td><td>{{.Duration}}</td></tr>{{end}}
            </tbody></table>
          </div>
        </details>{{end}}
      </div><div class="pager"><button type="button" data-prev>Previous</button><span data-page></span><button type="button" data-next>Next</button></div>
      {{else}}<p class="empty">No suites received.</p>{{end}}
    </section>

    <section id="tests" class="panel" hidden>
      <h2 class="section-title">All tests</h2>
      {{if .Tests}}<div class="results" data-paginated data-page-size="20">
        {{range .Tests}}<details class="result-row page-item">
          <summary>
            <span class="result-name">{{.Label}}</span>
            <span class="result-meta secondary">{{len .Attempts}} {{plural (len .Attempts) "run" "runs"}}</span>
            <span class="result-meta status {{.Tone}}">{{.Status}}</span>
            <span class="result-meta">{{.Duration}}</span>
          </summary>
          {{template "test-detail" .}}
        </details>{{end}}
      </div><div class="pager"><button type="button" data-prev>Previous</button><span data-page></span><button type="button" data-next>Next</button></div>
      {{else}}<p class="empty">No tests received.</p>{{end}}
    </section>
  </main>
  <script>
    document.querySelectorAll('.tab').forEach(function (tab) {
      tab.addEventListener('click', function () {
        document.querySelectorAll('.tab').forEach(function (item) { item.classList.toggle('active', item === tab); });
        document.querySelectorAll('.panel').forEach(function (panel) { panel.hidden = panel.id !== tab.dataset.tab; });
      });
    });
    document.querySelectorAll('[data-paginated]').forEach(function (list) {
      var items = Array.from(list.children).filter(function (item) { return item.classList.contains('page-item'); });
      var pageSize = Number(list.dataset.pageSize) || 20;
      var pageCount = Math.max(1, Math.ceil(items.length / pageSize));
      var page = 0;
      var pager = list.nextElementSibling;
      function render() {
        items.forEach(function (item, index) { item.hidden = index < page * pageSize || index >= (page + 1) * pageSize; });
        pager.querySelector('[data-page]').textContent = 'Page ' + (page + 1) + ' of ' + pageCount;
        pager.querySelector('[data-prev]').disabled = page === 0;
        pager.querySelector('[data-next]').disabled = page === pageCount - 1;
        pager.hidden = pageCount === 1;
      }
      pager.querySelector('[data-prev]').addEventListener('click', function () { if (page > 0) { page--; render(); } });
      pager.querySelector('[data-next]').addEventListener('click', function () { if (page + 1 < pageCount) { page++; render(); } });
      render();
    });
  </script>
</body>
</html>
`))
