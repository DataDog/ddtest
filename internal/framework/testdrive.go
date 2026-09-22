package framework

import (
	"fmt"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/kballard/go-shellquote"
	"strings"
)

func TestdriveCommand(_ string, runner Framework) (string, []string, error) {
	if command := strings.TrimSpace(settings.GetCommand()); command != "" {
		parts, err := shellquote.Split(command)
		if err != nil {
			return "", nil, fmt.Errorf("parse testdrive --command: %w", err)
		}
		if len(parts) == 0 {
			return "", nil, fmt.Errorf("testdrive --command is empty")
		}
		return parts[0], parts[1:], nil
	}
	if runner, ok := runner.(*Jest); ok {
		command, args := runner.getJestCommand()
		return command, args, nil
	}
	return "", nil, fmt.Errorf("unsupported testdrive framework: %s", runner.Name())
}
