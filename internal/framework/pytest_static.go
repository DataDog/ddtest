package framework

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/kballard/go-shellquote"
)

//go:embed scripts/pytest_static_discovery.py
var pytestStaticDiscoveryScript string

// tryStaticDiscovery never returns a partial inventory. Any uncertainty falls back
// to the tracer-backed collector, so an incomplete inventory cannot skip a file.
// Static discovery results deliberately do not populate the discovery cache;
// results produced by the fallback collector still use the normal cache.
func (p *PyTest) tryStaticDiscovery(ctx context.Context, testFiles discovery.TestFileSet, env map[string]string) ([]testoptimization.Test, bool) {
	tests, err := p.discoverTestsStatically(ctx, testFiles, env)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("Pytest static discovery unavailable; falling back to pytest collection", "reason", err)
		}
		return nil, false
	}
	slog.Info("Pytest static discovery succeeded", "count", len(tests))
	return tests, true
}

func (p *PyTest) discoverTestsStatically(ctx context.Context, testFiles discovery.TestFileSet, env map[string]string) ([]testoptimization.Test, error) {
	command, args := p.Command()
	interpreter, options, err := staticPytestCommand(command, withFrameworkFiles(command, args, "pytest", nil))
	if err != nil {
		return nil, err
	}
	files, err := p.DiscoverTestFiles(ctx, testFiles)
	if err != nil {
		return nil, err
	}
	input, err := json.Marshal(struct {
		Files   []string `json:"files"`
		Options []string `json:"options"`
	}{files, options})
	if err != nil {
		return nil, err
	}
	// Use a manifest rather than argv to accommodate large test selections.
	manifest, err := os.CreateTemp("", "ddtest-pytest-static-*.json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(manifest.Name()) }()
	_, writeErr := manifest.Write(input)
	closeErr := manifest.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}

	// Isolated mode prevents importing project modules through the working
	// directory or PYTHONPATH. The helper only uses the Python standard library.
	output, err := p.executor.CombinedOutput(ctx, interpreter, []string{"-I", "-c", pytestStaticDiscoveryScript, manifest.Name()}, env)
	if err != nil {
		return nil, fmt.Errorf("static discovery helper failed: %w", err)
	}
	var result struct {
		Tests  []testoptimization.Test `json:"tests"`
		Reason string                  `json:"reason"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("invalid static discovery response: %w", err)
	}
	if result.Reason != "" {
		return nil, fmt.Errorf("%s", result.Reason)
	}
	if result.Tests == nil {
		return nil, fmt.Errorf("static discovery helper returned no inventory")
	}
	return result.Tests, nil
}

func staticPytestCommand(command string, args []string) (string, []string, error) {
	if isPythonExecutable(command) && len(args) >= 2 && args[0] == "-m" && args[1] == "pytest" {
		return command, args[2:], nil
	}
	if name := filepath.Base(command); name != "pytest" && name != "py.test" {
		return "", nil, fmt.Errorf("unsupported pytest command (expected pytest or python -m pytest)")
	}
	// A console script may belong to a different virtualenv than PATH's python.
	// Read its shebang rather than guessing which interpreter to use.
	path, err := exec.LookPath(command)
	if err != nil {
		return "", nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = file.Close() }()
	line, err := bufio.NewReader(file).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "#!") {
		return "", nil, fmt.Errorf("pytest console script has no supported Python shebang")
	}
	parts, err := shellquote.Split(strings.TrimSpace(strings.TrimPrefix(line, "#!")))
	if err == nil && len(parts) == 2 && parts[0] == "/usr/bin/env" {
		parts = parts[1:]
	}
	if err != nil || len(parts) != 1 || !isPythonExecutable(parts[0]) {
		return "", nil, fmt.Errorf("pytest console script has no supported Python shebang")
	}
	return parts[0], args, nil
}

func isPythonExecutable(command string) bool {
	name := strings.TrimSuffix(filepath.Base(command), ".exe")
	if name == "python" {
		return true
	}
	version, ok := strings.CutPrefix(name, "python")
	return ok && version != "" && strings.ContainsAny(version, "0123456789") && strings.Trim(version, "0123456789.") == ""
}
