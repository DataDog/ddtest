package framework

import (
	"context"
	"fmt"
	"strings"

	"github.com/DataDog/ddtest/internal/ext"
)

// Ruby full discovery uses datadog-ci's discovery mode. Without the gem,
// starting the framework could execute tests instead of collecting them.
func requireRubyDiscoveryLibrary(ctx context.Context, executor ext.CommandExecutor) error {
	// Inherit the project's environment without adding the instrumentation preload.
	output, err := executor.CombinedOutput(ctx, "bundle", []string{"info", "datadog-ci"}, nil)
	if err != nil {
		return fmt.Errorf("full test discovery requires datadog-ci in the project bundle: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
