package runner

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/planner"
	"github.com/DataDog/ddtest/internal/runmetadata"
)

type runExecutionReport struct {
	Mode         string
	CINode       int
	LocalWorkers int
	TestFilesRun int
}

type runReport struct {
	RunInfo   runmetadata.RunInfo
	PlanInfo  planner.PlanInfo
	Execution runExecutionReport
	Duration  time.Duration
	Err       error
}

func printRunReport(w io.Writer, report runReport) {
	reportFprintln(w, "+++ DDTest: run report")
	reportFprintln(w)
	printRunInfoReport(w, report.RunInfo, report.PlanInfo)
	reportFprintln(w)
	printExecutionReport(w, report)
}

func printRunInfoReport(w io.Writer, runInfo runmetadata.RunInfo, planInfo planner.PlanInfo) {
	reportFprintln(w, "Run")
	reportFprintf(w, "  Service: %s\n", valueOrNotAvailable(runInfo.Service))
	reportFprintf(w, "  Repository: %s\n", valueOrNotAvailable(runInfo.Repository))
	reportFprintf(w, "  Commit: %s\n", valueOrNotAvailable(runInfo.Commit))
	reportFprintf(w, "  Branch: %s\n", valueOrNotAvailable(runInfo.Branch))
	reportFprintf(w, "  Platform: %s\n", formatPlatform(planInfo.Platform, planInfo.Framework))
	reportFprintf(w, "  OS tags: %s\n", formatTagList(planInfo.OSTags, constants.OSPlatform, constants.OSArchitecture, constants.OSVersion))
	reportFprintf(w, "  Runtime tags: %s\n", formatTagList(planInfo.RuntimeTags, constants.RuntimeName, constants.RuntimeVersion))
}

func printExecutionReport(w io.Writer, report runReport) {
	reportFprintln(w, "Execution")
	reportFprintf(w, "  Mode: %s\n", valueOrNotAvailable(report.Execution.Mode))
	if report.Execution.Mode == constants.RunModeCINode {
		reportFprintf(w, "  CI node: %d\n", report.Execution.CINode)
	}
	reportFprintf(w, "  Local workers: %s\n", formatCount(report.Execution.LocalWorkers))
	reportFprintf(w, "  Test files run: %s\n", formatCount(report.Execution.TestFilesRun))
	reportFprintf(w, "  Duration: %s\n", formatDuration(report.Duration))
	if report.Err == nil {
		reportFprintln(w, "  Result: passed")
		return
	}
	reportFprintln(w, "  Result: failed")
	reportFprintf(w, "  Error: %s\n", report.Err)
}

func reportFprintln(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}

func reportFprintf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func formatTagList(tags map[string]string, keys ...string) string {
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if value := tags[key]; value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	if len(parts) == 0 {
		return "not available"
	}
	return strings.Join(parts, ", ")
}

func formatPlatform(platformName, frameworkName string) string {
	switch {
	case platformName == "" && frameworkName == "":
		return "not available"
	case platformName == "":
		return frameworkName
	case frameworkName == "":
		return platformName
	default:
		return platformName + " / " + frameworkName
	}
}

func valueOrNotAvailable(value string) string {
	if value == "" {
		return "not available"
	}
	return value
}

func formatCount(count int) string {
	sign := ""
	if count < 0 {
		sign = "-"
		count = -count
	}

	value := strconv.Itoa(count)
	if len(value) <= 3 {
		return sign + value
	}

	prefixLength := len(value) % 3
	if prefixLength == 0 {
		prefixLength = 3
	}

	var builder strings.Builder
	builder.WriteString(sign)
	builder.WriteString(value[:prefixLength])
	for i := prefixLength; i < len(value); i += 3 {
		builder.WriteByte(',')
		builder.WriteString(value[i : i+3])
	}
	return builder.String()
}

func formatDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return duration.String()
	}
	return duration.Round(time.Millisecond).String()
}
