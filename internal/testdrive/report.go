package testdrive

import (
	"errors"
	"fmt"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const reportFilename = "report.html"

type reportRuntime struct{ Framework, Tracer string }
type reportFact struct{ Label, Value string }
type reportCard struct {
	Title, Context string
	Tests          []intake.Test
	Coverages      []intake.CoverageFact
}
type reportModel struct {
	Headline, Summary string
	Facts             []reportFact
	Cards             []reportCard
}

func writeReport(repositoryRoot, sessionDirectory string, findings intake.Facts, commandFailed bool, runtime ...reportRuntime) (string, error) {
	path := filepath.Join(sessionDirectory, reportFilename)
	file, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create testdrive report: %w", err)
	}
	executeErr := testdriveReport.Execute(file, buildReport(repositoryRoot, findings, commandFailed, runtime...))
	if err := errors.Join(executeErr, file.Close()); err != nil {
		return "", fmt.Errorf("write testdrive report: %w", err)
	}
	return path, nil
}
func buildReport(_ string, findings intake.Facts, commandFailed bool, runtime ...reportRuntime) reportModel {
	info := reportRuntime{Framework: "Test command", Tracer: "Isolated installation"}
	if len(runtime) > 0 {
		info = runtime[0]
	}
	coverage := "Not reported"
	if findings.CoveredTestCount > 0 {
		coverage = fmt.Sprintf("%d / %d", findings.CoveredTestCount, findings.TestCount)
	}
	model := reportModel{Headline: "Test events received.", Summary: "No findings.", Facts: []reportFact{
		{"Test events", fmt.Sprint(findings.TestEventCount)},
		{"Tests with coverage", coverage},
		{info.Framework, passedFailed(!commandFailed)},
		{"Tracer", info.Tracer + " · isolated"},
	}}
	if findings.TestEventCount == 0 {
		model.Headline = "No test events received."
		model.Summary = "Check the instrumentation setup."
	}
	if findings.EmptyCoverageEntryCount > 0 {
		model.Cards = append(model.Cards, reportCard{
			Title:   "Tracer error: empty coverage entries",
			Context: fmt.Sprintf("%d coverage entries had an empty files list. Affected payloads were excluded from coverage counts. Inspect the captured traffic.", findings.EmptyCoverageEntryCount),
		})
	}
	if len(findings.FailedTests) > 0 {
		model.Cards = append(model.Cards, reportCard{Title: "Any tests failed?", Tests: findings.FailedTests})
	}
	if len(findings.FlakyTests) > 0 {
		model.Cards = append(model.Cards, reportCard{Title: "Any flaky tests?", Tests: findings.FlakyTests})
	}
	if len(findings.SlowTests) > 0 {
		model.Cards = append(model.Cards, reportCard{Title: "Any tests slower than the others?", Context: "Median test time · " + formatDuration(findings.TestDurationMedian), Tests: findings.SlowTests})
	}
	if len(findings.BroadCoverage) > 0 {
		model.Cards = append(model.Cards, reportCard{Title: "Any unusually broad test coverage?", Context: fmt.Sprintf("Median covered files · %d", findings.CoveredFilesMedian), Coverages: findings.BroadCoverage})
	}
	if len(model.Cards) > 0 && findings.TestEventCount > 0 {
		model.Summary = fmt.Sprintf("%d findings.", len(model.Cards))
	}
	if len(findings.ConfigurationErrors) > 0 {
		model.Summary = "Tracer configuration errors: " + strings.Join(findings.ConfigurationErrors, ", ") + ". Inspect the captured traffic and test output."
	}
	return model
}

var testdriveReport = template.Must(template.New("testdrive-report").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>DDTest report</title>
<style>body{font:16px system-ui;max-width:900px;margin:3rem auto;padding:1rem}article,section{margin:2rem 0}li{margin:.5rem 0}</style></head><body>
<h1>{{.Headline}}</h1><p>{{.Summary}}</p>
{{range .Cards}}<article class="problem-card"><h2>{{.Title}}</h2><p>{{.Context}}</p><ul>{{range .Tests}}<li>{{.Suite}} · {{.Name}}</li>{{end}}{{range .Coverages}}<li>{{.Name}} · {{.FileCount}} files</li>{{end}}</ul></article>{{end}}
<section><h2>Run details</h2><dl>{{range .Facts}}<dt>{{.Label}}</dt><dd>{{.Value}}</dd>{{end}}</dl></section>
<section><h2>Artifacts</h2><ul><li><a href="intake/">JSON traffic</a></li><li><a href="test-output.txt">Test output</a></li></ul></section></body></html>`))

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

func attemptTone(status string) string {
	if status == "pass" {
		return "good"
	}
	return "attention"
}

func findingDuration(test intake.Test) time.Duration {
	if test.Duration != 0 || len(test.Attempts) == 0 {
		return test.Duration
	}
	return test.Attempts[0].Duration
}

func passedFailed(passed bool) string {
	if passed {
		return "Passed"
	}
	return "Failed"
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

func plural(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
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
