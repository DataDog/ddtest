package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/utils"
)

const (
	binJestPath = "node_modules/.bin/jest"
)

var ErrFullTestDiscoveryUnsupported = errors.New("full test discovery is not supported")

var jestTestFileExtensions = []string{"js", "jsx", "ts", "tsx", "mjs", "cjs"}

type Jest struct {
	executor        ext.CommandExecutor
	commandOverride []string
	platform        PlatformEnvironment
}

func NewJest(p PlatformEnvironment) *Jest {
	return &Jest{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: loadCommandOverride(),
		platform:        p,
	}
}

func (j *Jest) Platform() PlatformEnvironment { return j.platform }

func (j *Jest) Name() string {
	return "jest"
}

// We will not be discovering tests, but test suites.
// We'll be working outside of the Node.js process
func (j *Jest) SupportsFullTestDiscovery() bool {
	return false
}

func (j *Jest) SourceFileForSuite(suite string) (string, bool) {
	suite = strings.TrimSpace(suite)
	if suite == "" {
		return "", false
	}
	return suite, true
}

func (j *Jest) HasUnskippableMarker(testFile string) bool {
	return utils.FileContainsAll(testFile, "@datadog", "unskippable")
}

func (j *Jest) TestPattern() string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return custom
	}
	return utils.JoinGlobPatterns([]string{
		filepath.ToSlash(filepath.Join("**", "__tests__", "**", "*."+jestTestFileExtensionPattern())),
		filepath.ToSlash(filepath.Join("**", "*.{spec,test}."+jestTestFileExtensionPattern())),
	})
}

func (j *Jest) DiscoverTests(ctx context.Context, testFiles discovery.TestFileSet) ([]testoptimization.Test, error) {
	return nil, ErrFullTestDiscoveryUnsupported
}

func (j *Jest) DiscoverTestFiles(ctx context.Context, testFiles discovery.TestFileSet) ([]string, error) {
	if testFiles.Empty() {
		return []string{}, nil
	}
	if testFiles.UseExplicitFiles() {
		return slices.Clone(testFiles.ExplicitFiles), nil
	}

	envMap, err := j.platform.DiscoveryEnv(ctx, FileDiscovery, RuntimeOptions{})
	if err != nil {
		return nil, err
	}

	command, baseArgs := j.Command()
	args := slices.Clone(baseArgs)
	args = withFrameworkOptions(command, args, "jest", "--listTests", "--json")

	slog.Info("Discovering Jest test files with command", "command", command, "args", args)
	output, err := j.executor.CombinedOutput(ctx, command, args, envMap)
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return nil, fmt.Errorf("failed to discover Jest test files: %w", err)
		}
		return nil, fmt.Errorf("failed to discover Jest test files: %s: %w", message, err)
	}

	discoveredFiles, err := parseJestListTestsOutput(output)
	if err != nil {
		return nil, fmt.Errorf("failed to discover Jest test files: %w", err)
	}
	if settings.GetTestsLocation() == "" && settings.GetTestsExcludePattern() == "" {
		return discoveredFiles, nil
	}

	return filterJestTestFiles(discoveredFiles, testFiles)
}

func (j *Jest) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	command, baseArgs := j.Command()
	args := slices.Clone(baseArgs)
	args = withFrameworkOptions(command, args, "jest", "--runTestsByPath")
	args = withFrameworkFiles(command, args, "jest", testFiles)

	slog.Info("Running tests with command", "command", command, "args", args)

	mergedEnv, err := j.platform.RunEnv(RuntimeOptions{Env: envMap})
	if err != nil {
		return err
	}
	return j.executor.Run(ctx, command, args, mergedEnv)
}

// Decide between user custom command, local jest binary and npx jest
func (j *Jest) Command() (string, []string) {
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

func jestTestFileExtensionPattern() string {
	return utils.JoinGlobPatterns(jestTestFileExtensions)
}

func filterJestTestFiles(testFiles []string, selectedTestFiles discovery.TestFileSet) ([]string, error) {
	if settings.GetTestsLocation() == "" {
		selectedTestFiles.Pattern = ""
	}

	testFileMatcher, err := discovery.NewTestFileSetMatcher(selectedTestFiles, settings.GetTestsExcludePattern())
	if err != nil {
		return nil, err
	}

	filteredFiles := make([]string, 0, len(testFiles))
	for _, testFile := range testFiles {
		normalizedTestFile := utils.NormalizePath(testFile)
		if normalizedTestFile == "" {
			continue
		}
		if testFileMatcher.MatchNormalizedPath(normalizedTestFile) {
			filteredFiles = append(filteredFiles, normalizedTestFile)
		}
	}

	slices.Sort(filteredFiles)
	return slices.Compact(filteredFiles), nil
}

// Jest's --listTests --json writes an array of absolute paths. Preloads and
// package managers may log before or after it, even without a newline. Accept
// exactly one such array; missing or ambiguous output must not become an empty
// successful plan.
func parseJestListTestsOutput(output []byte) ([]string, error) {
	var paths []string
	found := false
	for len(output) > 0 {
		start := bytes.IndexByte(output, '[')
		if start < 0 {
			break
		}
		output = output[start:]
		decoder := json.NewDecoder(bytes.NewReader(output))
		var candidate []string
		if err := decoder.Decode(&candidate); err != nil {
			output = output[1:]
			continue
		}
		output = output[decoder.InputOffset():]
		if slices.ContainsFunc(candidate, func(path string) bool { return !filepath.IsAbs(path) }) {
			continue
		}
		if found {
			return nil, errors.New("ambiguous Jest JSON test list")
		}
		paths, found = candidate, true
	}
	if !found {
		return nil, errors.New("missing Jest JSON test list")
	}

	cwd, _ := os.Getwd()
	if resolvedCwd, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolvedCwd
	}
	testFiles := make([]string, 0)
	for _, testFile := range paths {
		if filepath.IsAbs(testFile) && cwd != "" {
			pathForRel := testFile
			if resolvedPath, err := filepath.EvalSymlinks(testFile); err == nil {
				pathForRel = resolvedPath
			}
			relativePath, err := filepath.Rel(cwd, pathForRel)
			if err != nil || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) || relativePath == ".." {
				continue
			}
			testFile = relativePath
		}

		normalizedTestFile := utils.NormalizePath(testFile)
		if normalizedTestFile == "" {
			continue
		}
		if _, err := os.Stat(normalizedTestFile); err != nil {
			return nil, fmt.Errorf("invalid Jest test file %q: %w", testFile, err)
		}
		testFiles = append(testFiles, normalizedTestFile)
	}

	slices.Sort(testFiles)
	return slices.Compact(testFiles), nil
}
