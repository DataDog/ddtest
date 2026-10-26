package main

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/DataDog/ddtest/internal/cmd"
)

var executeCommand = cmd.Execute

func main() {
	os.Exit(run(executeCommand))
}

func run(execute func() error) int {
	// Configure log level. Set DDTEST_LOG_LEVEL=debug (or DD_LOG_LEVEL=debug) to
	// enable debug output, which includes sample skippable/discovered test IDs.
	logLevel := slog.LevelInfo
	if strings.ToLower(os.Getenv("DDTEST_LOG_LEVEL")) == "debug" ||
		strings.ToLower(os.Getenv("DD_LOG_LEVEL")) == "debug" {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	// it doesn't make sense to use ddtest without test optimization mode,
	// so we just enable it
	_ = os.Setenv("DD_CIVISIBILITY_ENABLED", "1")

	// this is weird, I know: I am trying to get rid of annoying appsec telemetry
	// warning that is polluting the logs when Datadog Agent is not available
	_ = os.Setenv("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", "0")
	// Keep testdrive's ddtestProcessSettings in sync: reports must not
	// present these process defaults as the user's ambient settings.

	if err := execute(); err != nil {
		// Cobra has already printed the error.
		var coded interface{ ExitCode() int }
		if errors.As(err, &coded) && coded.ExitCode() > 0 {
			return coded.ExitCode()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				return 128 + int(status.Signal())
			}
			if code := exitErr.ExitCode(); code > 0 {
				return code
			}
		}
		return 1
	}
	return 0
}
