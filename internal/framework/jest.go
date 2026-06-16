package framework

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

const binJestPath = "node_modules/.bin/jest"

var (
	jestTestFileExtensions = []string{"js", "jsx", "ts", "tsx", "mjs", "cjs"}
	jestExcludedDirs       = map[string]struct{}{
		"node_modules": {},
		".git":         {},
		"dist":         {},
		"build":        {},
		"coverage":     {},
		".next":        {},
	}
)

type Jest struct {
	executor        ext.CommandExecutor
	commandOverride []string
	platformEnv     map[string]string
}

func NewJest() *Jest {
	return &Jest{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: loadCommandOverride(),
		platformEnv:     make(map[string]string),
	}
}

func (j *Jest) SetPlatformEnv(platformEnv map[string]string) {
	j.platformEnv = platformEnv
}

func (j *Jest) GetPlatformEnv() map[string]string {
	return j.platformEnv
}

func (j *Jest) Name() string {
	return "jest"
}

// Makes it a FullTestDiscoverySupporter.
// We will not be discovering tests, but test suites.
// We'll be working outside of the Node.js process
func (j *Jest) SupportsFullTestDiscovery() bool {
	return false
}

func (j *Jest) DiscoverTests(ctx context.Context) ([]testoptimization.Test, error) {
	return nil, ErrFullTestDiscoveryUnsupported
}

func (j *Jest) DiscoverTestFiles() ([]string, error) {
	var patterns []string
	if custom := settings.GetTestsLocation(); custom != "" {
		patterns = []string{custom}
	} else {
		patterns = defaultJestTestPatterns()
	}

	// Simple glob search, taking into consideration
	// excluded Jest test files
	filesByPath := make(map[string]struct{})
	for _, pattern := range patterns {
		matches, err := globTestFiles(pattern)
		if err != nil {
			return nil, err
		}

		for _, match := range matches {
			if isJestExcludedPath(match) {
				continue
			}
			filesByPath[filepath.ToSlash(match)] = struct{}{}
		}
	}

	testFiles := make([]string, 0, len(filesByPath))
	for testFile := range filesByPath {
		testFiles = append(testFiles, testFile)
	}
	slices.Sort(testFiles)

	slog.Debug("Discovered Jest test files", "count", len(testFiles))
	return testFiles, nil
}

func (j *Jest) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	command, baseArgs := j.getJestCommand()
	args := slices.Clone(baseArgs)
	args = append(args, "--runTestsByPath")
	args = append(args, testFiles...)

	slog.Info("Running tests with command", "command", command, "args", args)

	mergedEnv := make(map[string]string)
	maps.Copy(mergedEnv, j.platformEnv)
	maps.Copy(mergedEnv, envMap)
	return j.executor.Run(ctx, command, args, mergedEnv)
}

// Decide between user custom command, local jest binary and npx jest
func (j *Jest) getJestCommand() (string, []string) {
	if len(j.commandOverride) > 0 {
		return j.commandOverride[0], j.commandOverride[1:]
	}

	if info, err := os.Stat(binJestPath); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
		slog.Debug("Using local Jest binary")
		return binJestPath, []string{}
	}

	slog.Debug("Using npx jest for Jest commands")
	return "npx", []string{"jest"}
}

func defaultJestTestPatterns() []string {
	patterns := make([]string, 0, len(jestTestFileExtensions)*3)
	for _, extension := range jestTestFileExtensions {
		patterns = append(patterns,
			filepath.Join("**", "__tests__", "**", "*."+extension),
			filepath.Join("**", "*.spec."+extension),
			filepath.Join("**", "*.test."+extension),
		)
	}
	return patterns
}

func isJestExcludedPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if _, ok := jestExcludedDirs[part]; ok {
			return true
		}
	}
	return false
}
