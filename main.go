package main

import (
	"log/slog"
	"os"

	"github.com/DataDog/datadog-test-runner/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		slog.Error("FAILURE", "error", err)
		os.Exit(1)
	}
}
