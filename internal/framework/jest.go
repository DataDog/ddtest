package framework

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/jsconfig"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/utils"
)

const (
	binJestPath            = "node_modules/.bin/jest"
	nodeOptionsEnvVar      = "NODE_OPTIONS"
	ddTraceCIInitModule    = "dd-trace/ci/init"
	nodeRequireShortArg    = "-r"
	nodeRequireLongArg     = "--require"
	nodeRequireLongArgWith = nodeRequireLongArg + "="
)

var ErrFullTestDiscoveryUnsupported = errors.New("full test discovery is not supported")

var jestTestFileExtensions = []string{"js", "jsx", "ts", "tsx", "mjs", "mts", "cjs", "cts"}

type Jest struct {
	javaScriptDiscoveryState
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

func (j *Jest) DiscoverTestFilesNative(ctx context.Context, testFiles discovery.TestFileSet) ([]string, error) {
	j.nativeDiscoveryUsed = true
	if testFiles.Empty() {
		return []string{}, nil
	}
	if testFiles.UseExplicitFiles() {
		return slices.Clone(testFiles.ExplicitFiles), nil
	}
	if _, err := filterJestTestFiles(nil, testFiles); err != nil {
		return nil, err
	}

	command, baseArgs := j.getJestCommand()
	args := slices.Clone(baseArgs)
	args = append(args, "--listTests", "--json")

	slog.Info("Discovering Jest test files with command", "command", command, "args", args)
	output, err := j.executor.CombinedOutput(ctx, command, args, j.discoveryEnv())
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return nil, fmt.Errorf("failed to discover Jest test files: %w", err)
		}
		return nil, fmt.Errorf("failed to discover Jest test files: %s: %w", message, err)
	}

	discoveredFiles := parseJestListTestsOutput(output)
	if _, valid := parseJestJSONList(output); !valid && len(discoveredFiles) == 0 && strings.TrimSpace(string(output)) != "" {
		return nil, fmt.Errorf("native Jest discovery did not return a test file list: %s", strings.TrimSpace(string(output)))
	}
	if settings.GetTestsLocation() == "" && settings.GetTestsExcludePattern() == "" {
		return discoveredFiles, nil
	}

	return filterJestTestFiles(discoveredFiles, testFiles)
}

// DiscoverTestFilesFast is also used by differential tests: an unsupported
// configuration is an error here, so a fallback cannot disguise a mismatch.
func (j *Jest) DiscoverTestFilesFast(ctx context.Context, testFiles discovery.TestFileSet) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if settings.GetTestsLocation() != "" || testFiles.UseExplicitFiles() || testFiles.Pattern != "" && testFiles.Pattern != j.TestPattern() {
		return discoverJavaScriptTestFiles(ctx, testFiles, j.TestPattern())
	}
	command, args := j.getJestCommand()
	base := filepath.Base(command)
	if base == "npx" && len(args) > 0 && args[0] == "jest" {
		args = args[1:]
	} else if base != "jest" && base != "jest.js" && base != "jest.cmd" {
		return nil, fmt.Errorf("custom Jest launcher %q needs native discovery", command)
	}
	plan, err := jsconfig.CompileJest(ctx, jsconfig.JestOptions{Args: args, Env: j.platformEnv})
	if err != nil {
		return nil, err
	}
	files, err := plan.Discover(ctx)
	if err != nil {
		return nil, err
	}
	return filterJavaScriptTestFiles(files, discovery.TestFileSet{})
}

func (j *Jest) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	if len(testFiles) == 0 {
		return nil
	}
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

func (j *Jest) discoveryEnv() map[string]string {
	envMap := make(map[string]string, len(j.platformEnv)+1)
	maps.Copy(envMap, j.platformEnv)

	nodeOptions, ok := envMap[nodeOptionsEnvVar]
	if !ok {
		var found bool
		nodeOptions, found = os.LookupEnv(nodeOptionsEnvVar)
		if !found {
			return envMap
		}
	}

	envMap[nodeOptionsEnvVar] = stripNodeOptionsRequire(nodeOptions, ddTraceCIInitModule)
	return envMap
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

func stripNodeOptionsRequire(nodeOptions string, module string) string {
	fields := strings.Fields(nodeOptions)
	stripped := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		field := fields[i]

		if field == nodeRequireShortArg || field == nodeRequireLongArg {
			if i+1 < len(fields) && fields[i+1] == module {
				i++
				continue
			}
		}

		if strings.HasPrefix(field, nodeRequireShortArg) && strings.TrimPrefix(field, nodeRequireShortArg) == module {
			continue
		}

		if strings.HasPrefix(field, nodeRequireLongArgWith) && strings.TrimPrefix(field, nodeRequireLongArgWith) == module {
			continue
		}

		stripped = append(stripped, field)
	}

	return strings.Join(stripped, " ")
}

func parseJestListTestsOutput(output []byte) []string {
	if listed, ok := parseJestJSONList(output); ok {
		return normalizeJavaScriptTestFiles(listed)
	}
	cwd, _ := os.Getwd()
	if resolvedCwd, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolvedCwd
	}
	testFiles := make([]string, 0)
	for _, line := range strings.Split(string(output), "\n") {
		testFile := strings.TrimSpace(line)
		if testFile == "" {
			continue
		}

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
			continue
		}
		testFiles = append(testFiles, normalizedTestFile)
	}

	slices.Sort(testFiles)
	return slices.Compact(testFiles)
}

// Configs can write diagnostics around Jest's JSON list. Accept one complete
// array line (possibly prefixed by a log), and reject ambiguous output.
func parseJestJSONList(output []byte) ([]string, bool) {
	var result []string
	found := false
	for _, line := range strings.Split(string(output), "\n") {
		for start := strings.IndexByte(line, '['); start >= 0; start = strings.IndexByte(line, '[') {
			line = line[start:]
			var listed []string
			if json.Unmarshal([]byte(line), &listed) == nil {
				if found {
					return nil, false
				}
				result, found = listed, true
				break
			}
			line = line[1:]
		}
	}
	return result, found
}

func (j *Jest) DiscoverTestFiles(ctx context.Context, testFiles discovery.TestFileSet) ([]string, error) {
	j.nativeDiscoveryUsed = false
	if settings.GetForceFullTestDiscovery() {
		testFiles.ExplicitFiles = nil
		return j.DiscoverTestFilesNative(ctx, testFiles)
	}
	if settings.GetTestsLocation() != "" || testFiles.UseExplicitFiles() || testFiles.Pattern != "" && testFiles.Pattern != j.TestPattern() {
		return j.DiscoverTestFilesFast(ctx, testFiles)
	}
	files, err := j.DiscoverTestFilesFast(ctx, testFiles)
	if err == nil || ctx.Err() != nil {
		return files, err
	}
	slog.Warn("Jest config could not be resolved statically; using native discovery", "reason", err)
	testFiles.ExplicitFiles = nil
	return j.DiscoverTestFilesNative(ctx, testFiles)
}
