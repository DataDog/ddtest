package testdrive

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
)

// Poetry keys cached environments by project path. Resolve the prepared
// environment before entering a copy, and prevent probes from creating one.
func pythonPoetryEnvironment(ctx context.Context, command, directory string, env map[string]string, timeout time.Duration) (map[string]string, error) {
	prefix := []string{"--no-interaction", "--no-ansi"}
	if directory != "" {
		prefix = append(prefix, "--directory", directory)
	}
	executor := &ext.DefaultCommandExecutor{Timeout: timeout}
	stdout, stderr, err := executor.Output(ctx, command, append(prefix, "env", "info", "--path"), env)
	selected := strings.TrimSpace(string(stdout))
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, fmt.Errorf("inspect Poetry environment: %w", err)
		}
		// Explicitly configured system environments need no virtualenv pin.
		configured, _, configErr := executor.Output(ctx, command, append(prefix, "config", "virtualenvs.create"), env)
		if configErr != nil || strings.TrimSpace(string(configured)) != "false" {
			return nil, fmt.Errorf("prepare the Poetry environment with the project's test dependencies before running testdrive: %w: %s", err, strings.TrimSpace(string(stderr)))
		}
		selected = ""
	} else if info, statErr := os.Stat(selected); !filepath.IsAbs(selected) || statErr != nil || !info.IsDir() {
		return nil, fmt.Errorf("poetry selected an unavailable environment: %q", selected)
	}
	pinned := maps.Clone(env)
	if pinned == nil {
		pinned = make(map[string]string)
	}
	if selected != "" {
		pinned["VIRTUAL_ENV"] = selected
	}
	pinned["POETRY_VIRTUALENVS_CREATE"] = "false"
	return pinned, nil
}
