package framework

import (
	"log/slog"
	"strings"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/kballard/go-shellquote"
)

func loadCommandOverride() []string {
	command := strings.TrimSpace(settings.GetCommand())
	if command == "" {
		return nil
	}

	parts, err := shellquote.Split(command)
	if err != nil {
		slog.Warn("Command contains invalid quoting; falling back to whitespace parsing.", "error", err)
		parts = strings.Fields(command)
	}
	if len(parts) == 0 {
		return nil
	}

	return parts
}

// runnerCommandOverride removes arguments replaced by discovery and optimized runs.
func runnerCommandOverride(parts []string) []string {
	// Check for -- separator and remove it along with everything after it
	for i, part := range parts {
		if part == "--" {
			slog.Warn("Command contains '--' separator which causes ddtest-added flags to be misinterpreted. The '--' separator and anything after it will be removed. ddtest will automatically provide test files and flags.", "original_command", strings.Join(parts, " "))
			return parts[:i:i]
		}
	}

	return parts
}
