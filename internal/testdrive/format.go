// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"net/url"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

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

func plural(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
