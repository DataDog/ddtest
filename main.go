package main

import (
	"log/slog"
	"os"

	"github.com/DataDog/ddtest/internal/cmd"
	"github.com/DataDog/ddtest/internal/git"
)

func main() {
	if err := git.CheckAvailable(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}

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
