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
	"time"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

const reportFilename = "report.html"

type reportCard struct {
	Title   string
	Answer  string
	Summary string
	Tone    string
	Items   []string
}

type reportModel struct {
	Headline string
	Summary  string
	Cards    []reportCard
}

func writeReport(sessionDirectory string, findings intake.Findings, commandFailed bool) (string, error) {
	path := filepath.Join(sessionDirectory, reportFilename)
	file, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create testdrive report: %w", err)
	}

	model := buildReport(findings, commandFailed)
	executeErr := testdriveReport.Execute(file, model)
	closeErr := file.Close()
	if err := errors.Join(executeErr, closeErr); err != nil {
		return "", fmt.Errorf("write testdrive report: %w", err)
	}
	return path, nil
}

func buildReport(findings intake.Findings, commandFailed bool) reportModel {
	headline := "Test Optimization is working."
	summary := fmt.Sprintf("%d tests analyzed · %d with coverage", findings.TestCount, findings.CoveredTestCount)
	if findings.TestEventCount == 0 {
		headline = "No test events arrived."
		summary = "The test command ran, but instrumentation needs another look."
	}

	failed := reportCard{Title: "Any tests failed?", Answer: "No", Summary: "All observed tests finished successfully.", Tone: "good"}
	if len(findings.FailedTests) > 0 {
		failed.Answer = fmt.Sprintf("Yes · %d", len(findings.FailedTests))
		failed.Summary = "These tests were still failing after retries."
		failed.Tone = "attention"
		failed.Items = testFindingItems(findings.FailedTests, false)
	} else if commandFailed {
		failed.Answer = "Unknown"
		failed.Summary = "The test command failed before a final failing test event was captured."
		failed.Tone = "attention"
	}

	retried := reportCard{Title: "Any tests passed on retry?", Answer: "No", Summary: "No failure recovered on a retry.", Tone: "good"}
	if len(findings.PassedOnRetry) > 0 {
		retried.Answer = fmt.Sprintf("Yes · %d", len(findings.PassedOnRetry))
		retried.Summary = "These tests failed first, then passed."
		retried.Tone = "attention"
		retried.Items = testFindingItems(findings.PassedOnRetry, false)
	}

	slow := reportCard{Title: "Any tests slower than the others?", Answer: "No", Summary: "No clear duration outliers found.", Tone: "good"}
	if len(findings.SlowTests) > 0 {
		slow.Answer = fmt.Sprintf("Yes · %d", len(findings.SlowTests))
		slow.Summary = "These tests took at least twice the median test time."
		slow.Tone = "attention"
		slow.Items = testFindingItems(findings.SlowTests, true)
	}

	broad := reportCard{Title: "Any tests covering an unusual number of files?", Answer: "No", Summary: "No clear coverage outliers found.", Tone: "good"}
	if len(findings.BroadCoverage) > 0 {
		broad.Answer = fmt.Sprintf("Yes · %d", len(findings.BroadCoverage))
		broad.Summary = "These tests or suites covered at least twice the median file count."
		broad.Tone = "attention"
		broad.Items = coverageFindingItems(findings.BroadCoverage)
	}

	return reportModel{Headline: headline, Summary: summary, Cards: []reportCard{failed, retried, slow, broad}}
}

func testFindingItems(findings []intake.TestFinding, showDuration bool) []string {
	items := make([]string, 0, min(len(findings), 5))
	for _, finding := range findings[:min(len(findings), 5)] {
		label := finding.Name
		if finding.Suite != "" {
			label = finding.Suite + " › " + finding.Name
		}
		if showDuration {
			label += " · " + formatDuration(finding.Duration)
		}
		items = append(items, label)
	}
	return items
}

func coverageFindingItems(findings []intake.CoverageFinding) []string {
	items := make([]string, 0, min(len(findings), 5))
	for _, finding := range findings[:min(len(findings), 5)] {
		items = append(items, fmt.Sprintf("%s · %d files (%s level)", finding.Name, finding.FileCount, finding.Level))
	}
	return items
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
    :root { color-scheme: dark; --ink: #f8f6ff; --muted: #bbb3d0; --violet: #a878ff; --pink: #ef8cdb; }
    * { box-sizing: border-box; }
    body {
      margin: 0; min-height: 100vh; color: var(--ink);
      font: 16px/1.5 ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: #0b0712;
      overflow-x: hidden;
    }
    body::before {
      content: ""; position: fixed; inset: -35%; z-index: -2;
      background: radial-gradient(circle at 28% 38%, #7135c9 0, transparent 30%),
                  radial-gradient(circle at 72% 25%, #bd3c9f 0, transparent 26%),
                  radial-gradient(circle at 58% 78%, #36257e 0, transparent 31%);
      filter: blur(45px); animation: breathe 9s ease-in-out infinite alternate;
    }
    body::after {
      content: "✦  ·  ✧  ·  ✦  ·  ✧"; position: fixed; top: 7%; right: 5%; z-index: -1;
      color: #d9b8ff; opacity: .38; letter-spacing: 1.8rem; transform: rotate(-8deg);
    }
    @keyframes breathe { to { transform: scale(1.08) translate3d(2%, -1%, 0); filter: blur(58px); } }
    main { width: min(1040px, calc(100% - 36px)); margin: 0 auto; padding: 80px 0 64px; }
    .eyebrow { margin: 0 0 14px; color: #d7c5ff; font-weight: 750; letter-spacing: .14em; text-transform: uppercase; font-size: .78rem; }
    h1 { margin: 0; max-width: 850px; font-size: clamp(2.5rem, 7vw, 5.2rem); line-height: .98; letter-spacing: -.055em; }
    .gradient { background: linear-gradient(105deg, #fff 15%, #d2b2ff 52%, #ff9ce6); -webkit-background-clip: text; color: transparent; }
    .summary { margin: 22px 0 46px; color: var(--muted); font-size: 1.08rem; }
    .grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
    .card {
      min-height: 230px; padding: 26px; border: 1px solid rgba(255,255,255,.1); border-radius: 24px;
      background: linear-gradient(145deg, rgba(255,255,255,.09), rgba(255,255,255,.035));
      box-shadow: 0 24px 70px rgba(0,0,0,.28); backdrop-filter: blur(16px);
    }
    .question { margin: 0; color: var(--muted); font-size: .96rem; }
    .answer { margin: 13px 0 8px; font-size: 2.1rem; font-weight: 850; letter-spacing: -.035em; }
    .good .answer { color: #a9f7d0; }
    .attention .answer { color: #ffc3ef; }
    .detail { margin: 0; color: #d7d0e5; }
    ul { margin: 18px 0 0; padding: 0; list-style: none; color: #f0eaff; font-size: .9rem; }
    li { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; margin-top: 7px; }
    li::before { content: "✦"; color: var(--violet); margin-right: 9px; }
    footer { margin-top: 32px; color: #8f87a1; font-size: .85rem; }
    @media (max-width: 720px) { main { padding-top: 52px; } .grid { grid-template-columns: 1fr; } .card { min-height: 0; } }
    @media (prefers-reduced-motion: reduce) { body::before { animation: none; } }
  </style>
</head>
<body>
  <main>
    <p class="eyebrow">ddtest · local testdrive</p>
    <h1 class="gradient">{{.Headline}}</h1>
    <p class="summary">{{.Summary}}</p>
    <section class="grid">
      {{range .Cards}}<article class="card {{.Tone}}">
        <p class="question">{{.Title}}</p>
        <p class="answer">{{.Answer}}</p>
        <p class="detail">{{.Summary}}</p>
        {{if .Items}}<ul>{{range .Items}}<li title="{{.}}">{{.}}</li>{{end}}</ul>{{end}}
      </article>{{end}}
    </section>
    <footer>Generated locally. Your test data stays in this testdrive session.</footer>
  </main>
</body>
</html>
`))
