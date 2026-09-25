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
