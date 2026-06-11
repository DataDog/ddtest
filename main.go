package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/DataDog/ddtest/internal/cmd"
)

func main() {
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

	if err := cmd.Execute(); err != nil {
		slog.Error("FAILURE", "error", err)
		os.Exit(1)
	}
}
