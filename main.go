package main

import (
	"log/slog"
	"os"

	"github.com/DataDog/ddtest/internal/cmd"
)

func main() {
	// Configure slog with Info level
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	if err := cmd.Execute(); err != nil {
		slog.Error("FAILURE", "error", err)
		os.Exit(1)
	}
}
