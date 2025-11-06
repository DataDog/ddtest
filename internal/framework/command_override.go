package framework

import (
	"log/slog"
	"strings"

	"github.com/DataDog/ddtest/internal/settings"
)

func loadCommandOverride() []string {
	command := strings.TrimSpace(settings.GetCommand())
	if command == "" {
		return nil
	}

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil
	}

	// Check for -- separator and remove it along with everything after it
	for i, part := range parts {
		if part == "--" {
			slog.Warn("Command contains '--' separator which causes ddtest-added flags to be misinterpreted. The '--' separator and anything after it will be removed. ddtest will automatically provide test files and flags.", "original_command", command)
			return parts[:i]
		}
	}

	return parts
}
