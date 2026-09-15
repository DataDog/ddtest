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
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/tracer"
)

const reportFilename = "report.html"

type reportCard struct {
	Title     string
	Answer    string
	Summary   string
	Tests     []reportTest
	Coverages []reportCoverage
}

type reportTest struct {
	Label       string
	SourceFile  string
	SourceCode  string
	SourceError string
	Attempts    []reportAttempt
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
	Name        string
	Level       string
	FileCount   int
	Files       []string
	SourceFile  string
	SourceCode  string
	SourceError string
}

type reportFact struct {
	Label  string
	Value  string
	Detail string
	Tone   string
}

type reportArtifact struct {
	Title  string
	Detail string
	Href   string
}

type reportModel struct {
	Headline  string
	Summary   string
	Cards     []reportCard
	Facts     []reportFact
	Artifacts []reportArtifact
}

func writeReport(repositoryRoot, sessionDirectory string, findings intake.Findings, commandFailed bool) (string, error) {
	path := filepath.Join(sessionDirectory, reportFilename)
	file, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create testdrive report: %w", err)
	}

	model := buildReport(repositoryRoot, findings, commandFailed)
	executeErr := testdriveReport.Execute(file, model)
	closeErr := file.Close()
	if err := errors.Join(executeErr, closeErr); err != nil {
		return "", fmt.Errorf("write testdrive report: %w", err)
	}
	return path, nil
}

func buildReport(repositoryRoot string, findings intake.Findings, commandFailed bool) reportModel {
	model := reportModel{
		Headline: "Test Optimization is working.",
		Summary:  "No problems stood out in this run.",
		Facts: []reportFact{
			{Label: "Test events", Value: fmt.Sprintf("%d received", findings.TestEventCount), Detail: "The tracer sent test events to the local intake.", Tone: factTone(findings.TestEventCount > 0)},
			{Label: "Coverage", Value: fmt.Sprintf("%d of %d tests", findings.CoveredTestCount, findings.TestCount), Detail: "Tests associated with a coverage payload.", Tone: factTone(findings.TestCount > 0 && findings.CoveredTestCount == findings.TestCount)},
			{Label: "Jest", Value: passedFailed(!commandFailed), Detail: "Result from the Jest command exit status.", Tone: factTone(!commandFailed)},
			{Label: "Tracer", Value: "dd-trace@" + tracer.JavaScriptVersion, Detail: "Installed only inside this isolated testdrive session.", Tone: "good"},
		},
		Artifacts: []reportArtifact{
			{Title: "JSON traffic", Detail: "Every decoded request received by the local intake", Href: "intake/"},
			{Title: "Jest output", Detail: "The complete output from the test command", Href: testOutputFilename},
		},
	}

	if findings.TestEventCount == 0 {
		model.Headline = "No test events arrived."
		model.Summary = "The test command ran, but instrumentation needs another look."
	}

	if len(findings.FailedTests) > 0 {
		model.Cards = append(model.Cards, reportCard{
			Title:   "Any tests failed?",
			Answer:  fmt.Sprintf("Yes · %d", len(findings.FailedTests)),
			Summary: "Still failing after all retries.",
			Tests:   reportTests(repositoryRoot, findings.FailedTests),
		})
	}

	if len(findings.PassedOnRetry) > 0 {
		model.Cards = append(model.Cards, reportCard{
			Title:   "Any flaky tests?",
			Answer:  fmt.Sprintf("Yes · %d", len(findings.PassedOnRetry)),
			Summary: "Failed first, then passed on retry.",
			Tests:   reportTests(repositoryRoot, findings.PassedOnRetry),
		})
	}

	if len(findings.SlowTests) > 0 {
		model.Cards = append(model.Cards, reportCard{
			Title:   "Any tests slower than the others?",
			Answer:  fmt.Sprintf("Yes · %d", len(findings.SlowTests)),
			Summary: "At least twice the median test time.",
			Tests:   reportTests(repositoryRoot, findings.SlowTests),
		})
	}

	if len(findings.BroadCoverage) > 0 {
		model.Cards = append(model.Cards, reportCard{
			Title:     "Any unusually broad test coverage?",
			Answer:    fmt.Sprintf("Yes · %d", len(findings.BroadCoverage)),
			Summary:   "At least twice the median file count.",
			Coverages: reportCoverages(repositoryRoot, findings.BroadCoverage),
		})
	}

	if len(model.Cards) > 0 && findings.TestEventCount > 0 {
		model.Summary = fmt.Sprintf("%d things worth a closer look.", len(model.Cards))
	}
	return model
}

func reportTests(repositoryRoot string, findings []intake.TestFinding) []reportTest {
	tests := make([]reportTest, 0, len(findings))
	for _, finding := range findings {
		label := finding.Name
		if finding.Suite != "" {
			label = finding.Suite + " › " + finding.Name
		}
		sourceCode, sourceError := readSource(repositoryRoot, finding.SourceFile)
		test := reportTest{
			Label:       label,
			SourceFile:  finding.SourceFile,
			SourceCode:  sourceCode,
			SourceError: sourceError,
			Attempts:    make([]reportAttempt, 0, len(finding.Attempts)),
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
				Number:       attemptIndex + 1,
				Status:       displayStatus(attempt.Status),
				Tone:         attemptTone(attempt.Status),
				Duration:     formatDuration(attempt.Duration),
				Kind:         kind,
				ErrorType:    attempt.ErrorType,
				ErrorMessage: attempt.ErrorMessage,
				ErrorStack:   attempt.ErrorStack,
			})
		}
		tests = append(tests, test)
	}
	return tests
}

func reportCoverages(repositoryRoot string, findings []intake.CoverageFinding) []reportCoverage {
	coverages := make([]reportCoverage, 0, len(findings))
	for _, finding := range findings {
		sourceCode, sourceError := readSource(repositoryRoot, finding.SourceFile)
		coverages = append(coverages, reportCoverage{
			Name:        finding.Name,
			Level:       finding.Level,
			FileCount:   finding.FileCount,
			Files:       finding.Files,
			SourceFile:  finding.SourceFile,
			SourceCode:  sourceCode,
			SourceError: sourceError,
		})
	}
	return coverages
}

func readSource(repositoryRoot, sourceFile string) (string, string) {
	if sourceFile == "" {
		return "", "The tracer did not report a source file."
	}
	path := sourceFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(repositoryRoot, filepath.FromSlash(path))
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", "Source could not be read: " + err.Error()
	}
	return string(contents), ""
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
	return (&url.URL{Scheme: "file", Path: absolutePath}).String(), nil
}

func terminalLink(target string) string {
	return "\x1b]8;;" + target + "\x1b\\" + target + "\x1b]8;;\x1b\\"
}

var testdriveReport = template.Must(template.New("testdrive-report").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>ddtest · Test Optimization report</title>
  <style>
    :root { color-scheme: dark; --ink: #f8f6ff; --muted: #bbb3d0; --violet: #a878ff; --mint: #a9f7d0; }
    * { box-sizing: border-box; }
    body { margin: 0; min-height: 100vh; color: var(--ink); font: 16px/1.5 ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #0b0712; overflow-x: hidden; }
    body::before { content: ""; position: fixed; inset: -35%; z-index: -2; background: radial-gradient(circle at 28% 38%, #7135c9 0, transparent 30%), radial-gradient(circle at 72% 25%, #bd3c9f 0, transparent 26%), radial-gradient(circle at 58% 78%, #36257e 0, transparent 31%); filter: blur(45px); animation: breathe 9s ease-in-out infinite alternate; }
    body::after { content: "✦  ·  ✧  ·  ✦  ·  ✧"; position: fixed; top: 7%; right: 5%; z-index: -1; color: #d9b8ff; opacity: .38; letter-spacing: 1.8rem; transform: rotate(-8deg); }
    @keyframes breathe { to { transform: scale(1.08) translate3d(2%, -1%, 0); filter: blur(58px); } }
    main { width: min(1080px, calc(100% - 36px)); margin: 0 auto; padding: 76px 0 64px; }
    .eyebrow, .section-label { margin: 0 0 14px; color: #d7c5ff; font-weight: 750; letter-spacing: .14em; text-transform: uppercase; font-size: .78rem; }
    h1 { margin: 0; max-width: 900px; font-size: clamp(2.5rem, 7vw, 5.2rem); line-height: .98; letter-spacing: -.055em; }
    .gradient { background: linear-gradient(105deg, #fff 15%, #d2b2ff 52%, #ff9ce6); -webkit-background-clip: text; color: transparent; }
    .summary { margin: 22px 0 46px; color: var(--muted); font-size: 1.08rem; }
    .issues { display: grid; gap: 16px; margin-bottom: 56px; }
    details.card { border: 1px solid rgba(255,255,255,.12); border-radius: 24px; background: linear-gradient(145deg, rgba(255,255,255,.10), rgba(255,255,255,.04)); box-shadow: 0 24px 70px rgba(0,0,0,.28); backdrop-filter: blur(16px); overflow: hidden; }
    details.card[open] { border-color: rgba(214,164,255,.38); }
    summary { position: relative; display: grid; grid-template-columns: 1fr auto; gap: 10px 28px; padding: 26px 70px 26px 28px; cursor: pointer; list-style: none; }
    summary::-webkit-details-marker { display: none; }
    summary::after { content: "↘"; position: absolute; right: 28px; top: 50%; transform: translateY(-50%); color: var(--violet); font-size: 1.35rem; transition: transform .2s ease; }
    details[open] summary::after { transform: translateY(-50%) rotate(180deg); }
    .question { margin: 0; color: var(--muted); font-size: .96rem; }
    .answer { grid-row: span 2; align-self: center; margin: 0; color: #ffc3ef; font-size: 2rem; font-weight: 850; letter-spacing: -.035em; }
    .card-summary { margin: 2px 0 0; color: #e5dfef; }
    .expanded { padding: 0 28px 30px; border-top: 1px solid rgba(255,255,255,.08); }
    .test-detail { padding: 28px 0 6px; }
    .test-detail + .test-detail { border-top: 1px solid rgba(255,255,255,.08); }
    h3 { margin: 0; font-size: 1.2rem; letter-spacing: -.015em; }
    .source-path { margin: 4px 0 18px; color: #a99fbb; font: .84rem ui-monospace, SFMono-Regular, Menlo, monospace; }
    .attempts { display: grid; gap: 10px; }
    .attempt { padding: 16px 18px; border-radius: 16px; background: rgba(7,4,14,.42); }
    .attempt-head { display: flex; flex-wrap: wrap; gap: 8px 14px; align-items: baseline; }
    .attempt-status { font-weight: 800; }
    .attempt-status.good { color: var(--mint); }
    .attempt-status.attention { color: #ffc3ef; }
    .attempt-kind, .attempt-duration { color: var(--muted); font-size: .88rem; }
    .error-message { white-space: pre-wrap; margin: 14px 0 0; color: #ffd6f1; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .82rem; }
    .error-stack { margin-top: 10px; }
    .error-stack summary { display: block; padding: 0; color: #ad9dc4; font-size: .8rem; }
    .error-stack summary::after { content: none; }
    pre { margin: 10px 0 0; max-height: 520px; overflow: auto; padding: 18px; border: 1px solid rgba(255,255,255,.08); border-radius: 14px; background: #090610; color: #e9e1f5; font: .78rem/1.55 ui-monospace, SFMono-Regular, Menlo, monospace; white-space: pre; }
    .source-block { margin-top: 20px; }
    .source-title, .covered-title { color: #cfc4df; font-size: .84rem; font-weight: 750; }
    .source-error { margin: 8px 0 0; color: #e8a8cf; font-size: .84rem; }
    .covered-files { display: flex; flex-wrap: wrap; gap: 7px; margin: 10px 0 0; padding: 0; list-style: none; }
    .covered-files li { padding: 6px 9px; border-radius: 9px; background: rgba(168,120,255,.12); color: #d9cef0; font: .76rem ui-monospace, SFMono-Regular, Menlo, monospace; }
    .setup { margin-top: 10px; }
    .facts { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; }
    .fact { min-height: 150px; padding: 20px; border: 1px solid rgba(255,255,255,.09); border-radius: 18px; background: rgba(255,255,255,.045); }
    .fact-label { margin: 0; color: var(--muted); font-size: .82rem; }
    .fact-value { margin: 9px 0 7px; font-size: 1.2rem; font-weight: 820; }
    .fact.good .fact-value { color: var(--mint); }
    .fact.attention .fact-value { color: #ffc3ef; }
    .fact-detail { margin: 0; color: #a99fbb; font-size: .78rem; }
    .artifacts { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; margin-top: 12px; }
    .artifact { position: relative; display: block; min-height: 118px; padding: 20px 54px 20px 20px; border: 1px solid rgba(168,120,255,.28); border-radius: 18px; color: var(--ink); background: linear-gradient(135deg, rgba(115,60,199,.22), rgba(239,140,219,.08)); text-decoration: none; transition: transform .18s ease, border-color .18s ease; }
    .artifact:hover { transform: translateY(-2px); border-color: rgba(239,140,219,.58); }
    .artifact::after { content: "↗"; position: absolute; right: 21px; top: 20px; color: #d9b8ff; font-size: 1.2rem; }
    .artifact-title { display: block; font-weight: 820; }
    .artifact-detail { display: block; margin-top: 7px; color: #b8aec8; font-size: .8rem; }
    footer { margin-top: 32px; color: #8f87a1; font-size: .85rem; }
    @media (max-width: 820px) { .facts { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
    @media (max-width: 560px) { main { padding-top: 52px; } summary { grid-template-columns: 1fr; } .answer { grid-row: auto; } .facts, .artifacts { grid-template-columns: 1fr; } }
    @media (prefers-reduced-motion: reduce) { body::before { animation: none; } * { transition: none !important; } }
  </style>
</head>
<body>
  <main>
    <p class="eyebrow">ddtest · local testdrive</p>
    <h1 class="gradient">{{.Headline}}</h1>
    <p class="summary">{{.Summary}}</p>

    {{if .Cards}}<section class="issues" aria-label="Test suite findings">
      {{range .Cards}}<details class="card">
        <summary>
          <p class="question">{{.Title}}</p>
          <p class="answer">{{.Answer}}</p>
          <p class="card-summary">{{.Summary}}</p>
        </summary>
        <div class="expanded">
          {{range .Tests}}<section class="test-detail">
            <h3>{{.Label}}</h3>
            {{if .SourceFile}}<p class="source-path">{{.SourceFile}}</p>{{end}}
            <div class="attempts">
              {{range .Attempts}}<article class="attempt">
                <div class="attempt-head">
                  <span class="attempt-status {{.Tone}}">{{.Status}}</span>
                  <span class="attempt-kind">Run {{.Number}} · {{.Kind}}</span>
                  <span class="attempt-duration">{{.Duration}}</span>
                </div>
                {{if .ErrorMessage}}<p class="error-message">{{if .ErrorType}}{{.ErrorType}}: {{end}}{{.ErrorMessage}}</p>{{end}}
                {{if .ErrorStack}}<details class="error-stack"><summary>Full error stack</summary><pre>{{.ErrorStack}}</pre></details>{{end}}
              </article>{{end}}
            </div>
            <div class="source-block">
              <span class="source-title">Source code</span>
              {{if .SourceCode}}<pre>{{.SourceCode}}</pre>{{else}}<p class="source-error">{{.SourceError}}</p>{{end}}
            </div>
          </section>{{end}}
          {{range .Coverages}}<section class="test-detail">
            <h3>{{.Name}}</h3>
            <p class="source-path">{{.FileCount}} covered files · {{.Level}}-level coverage{{if .SourceFile}} · {{.SourceFile}}{{end}}</p>
            {{if .Files}}<span class="covered-title">Covered files</span><ul class="covered-files">{{range .Files}}<li>{{.}}</li>{{end}}</ul>{{end}}
            <div class="source-block">
              <span class="source-title">Source code</span>
              {{if .SourceCode}}<pre>{{.SourceCode}}</pre>{{else}}<p class="source-error">{{.SourceError}}</p>{{end}}
            </div>
          </section>{{end}}
        </div>
      </details>{{end}}
    </section>{{end}}

    <section class="setup" aria-label="Testdrive setup proof">
      <p class="section-label">What testdrive proved</p>
      <div class="facts">{{range .Facts}}<article class="fact {{.Tone}}">
        <p class="fact-label">{{.Label}}</p>
        <p class="fact-value">{{.Value}}</p>
        <p class="fact-detail">{{.Detail}}</p>
      </article>{{end}}</div>
      <div class="artifacts">{{range .Artifacts}}<a class="artifact" href="{{.Href}}">
        <span class="artifact-title">{{.Title}}</span>
        <span class="artifact-detail">{{.Detail}}</span>
      </a>{{end}}</div>
    </section>
    <footer>Generated locally. Your test data stays in this testdrive session.</footer>
  </main>
</body>
</html>
`))
