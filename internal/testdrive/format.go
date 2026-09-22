package testdrive

import (
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"strings"
	"time"
)

func testDisplayStatus(test intake.TestFinding) (string, string) {
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

func findingDuration(test intake.TestFinding) time.Duration {
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
