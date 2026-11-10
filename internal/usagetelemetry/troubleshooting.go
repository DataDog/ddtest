// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package usagetelemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	maxTroubleshootingBytes = 64 * 1024
	maxLocalReportBytes     = 32 * 1024 * 1024
	maxDiagnosticEntries    = 32
	troubleshootingEvent    = "ddtest.troubleshooting"
)

// TroubleshootingContext stays local. ReportPath is never part of the payload.
type TroubleshootingContext struct {
	ReportPath string
	Framework  string
	Tracer     string
	Runtime    string
}

type troubleshootingBundle struct {
	event
	Diagnostics troubleshootingDiagnostics `json:"diagnostics"`
}

type troubleshootingDiagnostics struct {
	ReportStatus           string                   `json:"report_status"`
	DDTestCommit           string                   `json:"ddtest_commit,omitempty"`
	Error                  *TroubleshootingError    `json:"error,omitempty"`
	Framework              string                   `json:"framework,omitempty"`
	TracerVersion          string                   `json:"tracer_version,omitempty"`
	RuntimeVersion         string                   `json:"runtime_version,omitempty"`
	CheckOnly              bool                     `json:"check_only"`
	AllConfigurations      bool                     `json:"all_configurations"`
	StoppedBeforeExecution bool                     `json:"stopped_before_execution"`
	ConfigurationCount     int                      `json:"configuration_count"`
	Features               []troubleshootingFeature `json:"features,omitempty"`
	Runs                   []troubleshootingRun     `json:"runs,omitempty"`
	Truncated              bool                     `json:"truncated"`
}

type troubleshootingFeature struct {
	Name         string           `json:"name"`
	Status       string           `json:"status"`
	Code         string           `json:"code,omitempty"`
	ObservedCode string           `json:"observed_code,omitempty"`
	Evidence     *FeatureEvidence `json:"evidence,omitempty"`
}

type troubleshootingRun struct {
	Role              string       `json:"role"`
	Instrumented      bool         `json:"instrumented"`
	ExitCode          *int         `json:"exit_code"`
	TimedOut          bool         `json:"timed_out"`
	ResultError       bool         `json:"result_error"`
	Passed            int          `json:"passed"`
	Failed            int          `json:"failed"`
	Skipped           int          `json:"skipped"`
	Other             int          `json:"other"`
	SuiteErrorCount   int          `json:"suite_error_count"`
	TestEventCount    int          `json:"test_event_count"`
	RuntimeVersions   []string     `json:"runtime_versions,omitempty"`
	FrameworkVersions []string     `json:"framework_versions,omitempty"`
	TracerVersions    []string     `json:"tracer_versions,omitempty"`
	TracerState       *TracerState `json:"tracer_state,omitempty"`
}

// This projection deliberately cannot retain arbitrary fields from the private
// report. Even the selected fields are normalized before sending the event.
type localTroubleshootingReport struct {
	SchemaVersion          int                      `json:"schema_version"`
	Framework              string                   `json:"framework"`
	Tracer                 string                   `json:"tracer"`
	CheckOnly              bool                     `json:"check_only"`
	All                    bool                     `json:"all"`
	StoppedBeforeExecution bool                     `json:"stopped_before_execution"`
	Configurations         []struct{}               `json:"configurations"`
	Features               []troubleshootingFeature `json:"features"`
	Runs                   []struct {
		Role         string `json:"role"`
		Instrumented bool   `json:"instrumented"`
		ExitCode     *int   `json:"exit_code"`
		Timeout      string `json:"timeout"`
		ResultError  string `json:"result_error"`
		TestCounts   struct {
			Passed  int `json:"passed"`
			Failed  int `json:"failed"`
			Skipped int `json:"skipped"`
			Other   int `json:"other"`
		} `json:"test_counts"`
		SuiteErrorCount   int          `json:"suite_error_count"`
		TestEventCount    int          `json:"test_event_count"`
		Runtimes          []string     `json:"runtimes"`
		FrameworkVersions []string     `json:"framework_versions"`
		TracerVersions    []string     `json:"tracer_versions"`
		TracerState       *TracerState `json:"tracer_state"`
	} `json:"runs"`
}

// SetTroubleshootingReport remembers only a local report location, not its
// contents. It does not send a request.
func SetTroubleshootingReport(output io.Writer, path string) {
	if observer, ok := output.(interface{ SetTroubleshootingReport(string) }); ok {
		observer.SetTroubleshootingReport(path)
	}
}

func (a *Attempt) SetTroubleshootingReport(path string) {
	if a != nil && !a.finished {
		a.troubleshootingContext.ReportPath = path
	}
}

// SetTroubleshootingContext retains only context used by the final projection.
// It never sends data or reads rendered command output.
func SetTroubleshootingContext(output io.Writer, info TroubleshootingContext) {
	if observer, ok := output.(interface{ SetTroubleshootingContext(TroubleshootingContext) }); ok {
		observer.SetTroubleshootingContext(info)
	}
}

func (a *Attempt) SetTroubleshootingContext(info TroubleshootingContext) {
	if a == nil || a.finished {
		return
	}
	if info.ReportPath != "" {
		a.troubleshootingContext.ReportPath = info.ReportPath
	}
	if info.Framework != "" {
		a.troubleshootingContext.Framework = info.Framework
	}
	if info.Tracer != "" {
		a.troubleshootingContext.Tracer = info.Tracer
	}
	if info.Runtime != "" {
		a.troubleshootingContext.Runtime = info.Runtime
	}
}

// troubleshootingPayload builds at most one bounded event at command finish.
// No temporary bundle, arbitrary attachment or user-supplied JSON is uploaded.
func (a *Attempt) troubleshootingPayload() []byte {
	if a == nil || disabled() || a.finished || a.cancelled() || a.event.Mode == "report" ||
		!slices.Contains([]string{"failed", "blocked", "unsupported"}, a.event.Outcome) ||
		a.event.Phase == "options" || a.event.Phase == "consent" || a.event.Code == "invalid_options" {
		return nil
	}
	info := a.troubleshootingContext

	details := troubleshootingDetails(info)
	details.DDTestCommit = diagnosticBuildCommit()
	details.Error = a.troubleshootingError.sanitized()
	// The command mode is known even when no current report could be written.
	details.CheckOnly = details.CheckOnly || a.event.Mode == "configuration_check"
	e := a.event
	e.Message, e.EventName, e.Date = troubleshootingEvent, troubleshootingEvent, time.Now().UTC()
	e.Version = diagnosticVersion(e.Version)
	e.ExitCode, e.DurationMS = nil, nil
	data, err := marshalTroubleshooting(troubleshootingBundle{event: e, Diagnostics: details})
	// Keep large matrices within the event limit and declare truncation explicitly.
	for err == nil && len(data) > maxTroubleshootingBytes {
		details.Truncated = true
		if len(details.Features) > 1 {
			details.Features = details.Features[:len(details.Features)-1]
		} else if len(details.Runs) > 0 {
			details.Runs = details.Runs[:len(details.Runs)-1]
		} else {
			return nil
		}
		data, err = marshalTroubleshooting(troubleshootingBundle{event: e, Diagnostics: details})
	}
	if err != nil || a.cancelled() || validateTroubleshooting(data) != nil {
		return nil
	}
	return data
}

func (a *Attempt) cancelled() bool { return a.ctx != nil && a.ctx.Err() != nil }

func troubleshootingDetails(info TroubleshootingContext) troubleshootingDiagnostics {
	d := troubleshootingDiagnostics{ReportStatus: "not_available", Framework: info.Framework, TracerVersion: info.Tracer, RuntimeVersion: info.Runtime}
	if info.ReportPath == "" {
		return d.sanitized()
	}
	data, err := readTroubleshootingFile(info.ReportPath, maxLocalReportBytes)
	if err != nil {
		d.ReportStatus = "unreadable_or_too_large"
		return d.sanitized()
	}
	var report localTroubleshootingReport
	if json.Unmarshal(data, &report) != nil || report.SchemaVersion != 1 {
		d.ReportStatus = "invalid"
		return d.sanitized()
	}
	d.ReportStatus, d.Framework = "included", report.Framework
	if version := diagnosticTracerVersion(report.Tracer); (version != "" && version != "unknown") || d.TracerVersion == "" {
		d.TracerVersion = version
	}
	d.CheckOnly, d.AllConfigurations, d.StoppedBeforeExecution = report.CheckOnly, report.All, report.StoppedBeforeExecution
	d.ConfigurationCount = len(report.Configurations)
	d.Features = report.Features
	for _, run := range report.Runs {
		d.Runs = append(d.Runs, troubleshootingRun{
			Role: run.Role, Instrumented: run.Instrumented, ExitCode: run.ExitCode,
			TimedOut: run.Timeout != "", ResultError: run.ResultError != "",
			Passed: run.TestCounts.Passed, Failed: run.TestCounts.Failed, Skipped: run.TestCounts.Skipped, Other: run.TestCounts.Other,
			SuiteErrorCount: run.SuiteErrorCount, TestEventCount: run.TestEventCount, RuntimeVersions: run.Runtimes,
			FrameworkVersions: run.FrameworkVersions, TracerVersions: run.TracerVersions, TracerState: run.TracerState,
		})
	}
	return d.sanitized()
}

func (d troubleshootingDiagnostics) sanitized() troubleshootingDiagnostics {
	d.ReportStatus = diagnosticEnum(d.ReportStatus, "not_available", "unreadable_or_too_large", "invalid", "included")
	if !commitPattern.MatchString(d.DDTestCommit) {
		d.DDTestCommit = ""
	}
	d.Error = d.Error.sanitized()
	d.Framework = diagnosticEnum(d.Framework, "", "jest", "vitest", "mocha", "jasmine", "playwright", "cypress", "webdriverio", "cucumber", "rspec", "minitest", "pytest", "junit4", "junit5", "testng", "cucumber-java", "go")
	d.TracerVersion, d.RuntimeVersion = diagnosticTracerVersion(d.TracerVersion), diagnosticVersion(d.RuntimeVersion)
	d.ConfigurationCount = diagnosticCount(d.ConfigurationCount)
	d.Truncated = d.Truncated || len(d.Features) > maxDiagnosticEntries || len(d.Runs) > maxDiagnosticEntries
	features, runs := d.Features, d.Runs
	d.Features, d.Runs = nil, nil
	for _, feature := range features[:min(len(features), maxDiagnosticEntries)] {
		feature.Name = diagnosticEnum(feature.Name, "auto-retries", "early-flake-detection", "skipping", "quarantine", "disabled", "attempt-to-fix", "all")
		feature.Status = diagnosticEnum(feature.Status, "passed", "failed", "not exercised", "not supported", "inconclusive", "known limitation", "unvalidated")
		feature.Code = diagnosticEnum(feature.Code, "", "tracer_control_not_applied", "tracer_limitation", "tracer_feature_not_supported")
		feature.ObservedCode = diagnosticEnum(feature.ObservedCode, "", "tracer_control_not_applied", "tracer_limitation", "tracer_feature_not_supported")
		if feature.Evidence != nil {
			d.Truncated = d.Truncated || len(feature.Evidence.Counts) > maxFeatureCounts || len(feature.Evidence.Flags) > maxFeatureFlags
			feature.Evidence = feature.Evidence.sanitized()
		}
		d.Features = append(d.Features, feature)
	}
	for _, run := range runs[:min(len(runs), maxDiagnosticEntries)] {
		run.Role = diagnosticEnum(run.Role, "baseline", "reporting-only", "repeat", "probe-control", "probe-feature")
		if run.ExitCode != nil && (*run.ExitCode < -1 || *run.ExitCode > 255) {
			run.ExitCode = nil
		}
		run.Passed, run.Failed, run.Skipped, run.Other = diagnosticCount(run.Passed), diagnosticCount(run.Failed), diagnosticCount(run.Skipped), diagnosticCount(run.Other)
		run.SuiteErrorCount, run.TestEventCount = diagnosticCount(run.SuiteErrorCount), diagnosticCount(run.TestEventCount)
		versions := run.RuntimeVersions
		run.RuntimeVersions = nil
		d.Truncated = d.Truncated || len(versions) > 8
		for _, version := range versions[:min(len(versions), 8)] {
			run.RuntimeVersions = append(run.RuntimeVersions, diagnosticVersion(version))
		}
		d.Truncated = d.Truncated || len(run.FrameworkVersions) > 8 || len(run.TracerVersions) > 8 || (run.TracerState != nil && len(run.TracerState.Capabilities) > 6)
		run.FrameworkVersions, run.TracerVersions = diagnosticVersions(run.FrameworkVersions), diagnosticVersions(run.TracerVersions)
		run.TracerState = run.TracerState.sanitized()
		d.Runs = append(d.Runs, run)
	}
	return d
}

var diagnosticVersionPattern = regexp.MustCompile(`^v?([0-9]{1,4}(?:\.[0-9]{1,4}){0,3})(?:[-+][a-zA-Z0-9.-]+)?$`)

func diagnosticTracerVersion(value string) string {
	for _, prefix := range []string{"dd-trace@", "ddtrace@", "datadog-ci@", "dd-java-agent@"} {
		if version, ok := strings.CutPrefix(value, prefix); ok {
			return diagnosticVersion(version)
		}
	}
	return diagnosticVersion(value)
}

// Retain numeric version components only, never custom refs, URLs or suffixes.
func diagnosticVersion(value string) string {
	if value == "" || value == "dev" {
		return value
	}
	if len(value) <= 80 {
		if match := diagnosticVersionPattern.FindStringSubmatch(value); match != nil {
			return match[1]
		}
	}
	return "unknown"
}

func diagnosticEnum(value string, allowed ...string) string {
	if slices.Contains(allowed, value) {
		return value
	}
	return "unknown"
}

func diagnosticCount(value int) int { return max(0, min(value, 1_000_000_000)) }

func marshalTroubleshooting(bundle troubleshootingBundle) ([]byte, error) {
	data, err := json.MarshalIndent(bundle, "", "  ")
	return append(data, '\n'), err
}

func readTroubleshootingFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("diagnostic JSON must be a regular file within the size limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open diagnostic JSON")
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() > limit {
		return nil, fmt.Errorf("diagnostic JSON must be a regular file within the size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, fmt.Errorf("cannot read diagnostic JSON within the size limit")
	}
	return data, nil
}

// validateTroubleshooting checks the final bytes against the fixed schema and
// normalized values before network delivery. No report bytes are sent directly.
func validateTroubleshooting(data []byte) error {
	var bundle troubleshootingBundle
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return fmt.Errorf("not an allowlisted DDTest troubleshooting bundle")
	}
	metadata := &Attempt{}
	metadata.ObserveResult("failed", bundle.Phase, bundle.Code)
	if bundle.SchemaVersion != 1 || bundle.Service != "ddtest-cli" || bundle.Source != "ddtest" ||
		bundle.EventName != troubleshootingEvent || bundle.Message != troubleshootingEvent ||
		len(bundle.AttemptID) != 26 || strings.Trim(bundle.AttemptID, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") != "" ||
		!slices.Contains([]string{"onboard", "testdrive"}, bundle.Command) ||
		!slices.Contains([]string{"discovery", "apply", "instructions", "validation", "configuration_check"}, bundle.Mode) ||
		!slices.Contains([]string{"failed", "blocked", "unsupported"}, bundle.Outcome) ||
		bundle.Phase != metadata.event.Phase || bundle.Code != metadata.event.Code ||
		bundle.Version != diagnosticVersion(bundle.Version) ||
		bundle.OS != diagnosticEnum(bundle.OS, "darwin", "linux", "windows", "freebsd", "openbsd", "netbsd", "dragonfly", "solaris", "illumos", "aix", "android", "ios", "plan9", "js", "wasip1") ||
		bundle.Arch != diagnosticEnum(bundle.Arch, "amd64", "arm64", "386", "arm", "ppc64", "ppc64le", "riscv64", "s390x", "mips", "mipsle", "mips64", "mips64le", "wasm", "loong64") ||
		bundle.Date.IsZero() || bundle.ExitCode != nil || bundle.DurationMS != nil ||
		!reflect.DeepEqual(bundle.Diagnostics, bundle.Diagnostics.sanitized()) {
		return fmt.Errorf("diagnostic JSON contains unsupported values")
	}
	canonical, err := marshalTroubleshooting(bundle)
	if err != nil || !bytes.Equal(data, canonical) {
		return fmt.Errorf("diagnostic JSON must use the generated format without extra or duplicate fields")
	}
	return nil
}
