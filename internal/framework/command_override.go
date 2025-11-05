package framework

import (
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

	return parts
}
