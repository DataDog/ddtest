package runner

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/planner"
	"github.com/DataDog/ddtest/internal/runmetadata"
)

func TestPrintRunReport_Passed(t *testing.T) {
	var output strings.Builder

	printRunReport(&output, runReport{
		RunInfo: runmetadata.RunInfo{
			Service:    "checkout-api",
			Repository: "https://github.com/acme/checkout.git",
			Commit:     "9f3a1c7d2b4e",
			Branch:     "feature/split-report",
		},
		PlanInfo: planner.PlanInfo{
			Platform:  "ruby",
			Framework: "rspec",
			OSTags: map[string]string{
				"os.platform":     "linux",
				"os.architecture": "amd64",
				"os.version":      "6.8.0",
			},
			RuntimeTags: map[string]string{
				"runtime.name":    "ruby",
				"runtime.version": "3.3.4",
			},
		},
		Execution: runExecutionReport{
			Mode:         constants.RunModeCINode,
			CINode:       2,
			LocalWorkers: 2,
			TestFilesRun: 87,
		},
		Duration: 238 * time.Second,
	})

	expected := `+++ DDTest: run report

Run
  Service: checkout-api
  Repository: https://github.com/acme/checkout.git
  Commit: 9f3a1c7d2b4e
  Branch: feature/split-report
  Platform: ruby / rspec
  OS tags: os.platform=linux, os.architecture=amd64, os.version=6.8.0
  Runtime tags: runtime.name=ruby, runtime.version=3.3.4

Execution
  Mode: CI node
  CI node: 2
  Local workers: 2
  Test files run: 87
  Duration: 3m58s
  Result: passed
`
	if output.String() != expected {
		t.Errorf("unexpected run report:\n%s", output.String())
	}
}

func TestPrintRunReport_Failed(t *testing.T) {
	var output strings.Builder

	printRunReport(&output, runReport{
		Execution: runExecutionReport{
			Mode:         runModeSequential,
			LocalWorkers: 1,
			TestFilesRun: 2,
		},
		Err: errors.New("rspec exited with status 1"),
	})

	report := output.String()
	if !strings.Contains(report, "  Result: failed") ||
		!strings.Contains(report, "  Error: rspec exited with status 1") {
		t.Errorf("expected failed run report, got:\n%s", report)
	}
}

func TestFormatPlatform(t *testing.T) {
	tests := []struct {
		name      string
		platform  string
		framework string
		want      string
	}{
		{name: "none", want: "not available"},
		{name: "platform only", platform: "ruby", want: "ruby"},
		{name: "framework only", framework: "rspec", want: "rspec"},
		{name: "both", platform: "python", framework: "pytest", want: "python / pytest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatPlatform(tt.platform, tt.framework); got != tt.want {
				t.Fatalf("formatPlatform() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatCount(t *testing.T) {
	tests := []struct {
		count int
		want  string
	}{
		{count: 0, want: "0"},
		{count: 999, want: "999"},
		{count: 1000, want: "1,000"},
		{count: 1234567, want: "1,234,567"},
		{count: -1234567, want: "-1,234,567"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatCount(tt.count); got != tt.want {
				t.Fatalf("formatCount(%d) = %q, want %q", tt.count, got, tt.want)
			}
		})
	}
}
