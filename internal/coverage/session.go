// Package coverage coordinates coverage files owned by independent JavaScript test processes.
package coverage

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/DataDog/ddtest/internal/ext"
)

const WorkerDirectoryEnv = "DDTEST_WORKER_COVERAGE_DIRECTORY"
const HelperEnv = "DDTEST_COVERAGE_HELPER"
const NYCEnv = "DDTEST_COVERAGE_NYC"

//go:embed scripts/collect.cjs
var helperScript string

//go:embed scripts/cypress.cjs
var cypressScript string

type Session struct {
	Directory   string
	helper      string
	cypressHook string
	nyc         string
}

// New creates a fresh namespace; existing coverage and concurrent runs are never cleaned.
func New(ctx context.Context, output string) (*Session, error) {
	output, err := filepath.Abs(output)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(output, "ddtest-")
	if err != nil {
		return nil, err
	}
	s := &Session{Directory: directory, helper: filepath.Join(directory, "coverage.cjs"), cypressHook: filepath.Join(directory, "cypress.cjs")}
	for file, data := range map[string]string{s.helper: helperScript, s.cypressHook: cypressScript} {
		if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
			return nil, err
		}
	}
	executor := &ext.DefaultCommandExecutor{}
	outputPath := filepath.Join(directory, "nyc-path.txt")
	if result, err := executor.CombinedOutput(ctx, "node", []string{s.helper, "resolve", outputPath}, nil); err != nil {
		return nil, fmt.Errorf("coverage setup failed: %s: %w", result, err)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, err
	}
	s.nyc = string(data)
	return s, nil
}

// WorkerEnv is called only for nonempty batches and is safe for concurrent workers.
func (s *Session) WorkerEnv(env map[string]string, node, worker int) error {
	directory := filepath.Join(s.Directory, fmt.Sprintf("node-%d-worker-%d", node, worker))
	if err := os.Mkdir(directory, 0o755); err != nil {
		return fmt.Errorf("create worker coverage directory: %w", err)
	}
	env[WorkerDirectoryEnv] = directory
	env[HelperEnv] = s.helper
	env[NYCEnv] = s.nyc
	env["DDTEST_COVERAGE_CYPRESS_HOOK"] = s.cypressHook
	return nil
}

func (s *Session) Merge(ctx context.Context) error {
	executor := &ext.DefaultCommandExecutor{}
	if err := executor.Run(ctx, "node", []string{s.helper, "merge", s.Directory, s.nyc}, nil); err != nil {
		return fmt.Errorf("merge worker coverage in %s: %w", s.Directory, err)
	}
	return nil
}

func NodeRequire(path string) string { return "--require " + strconv.Quote(path) }
