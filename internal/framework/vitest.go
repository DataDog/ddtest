package framework

import (
	"context"
	_ "embed"
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
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/utils"
)

const (
	binVitestPath           = "node_modules/.bin/vitest"
	ddTraceRegisterPath     = "dd-trace/register.js"
	vitestV1DiscoveryMarker = "__DDTEST_VITEST_FILES__"
)

//go:embed scripts/vitest_v1_discovery.mjs
var vitestV1DiscoveryScript string

var vitestTestFileExtensions = []string{"js", "jsx", "ts", "tsx", "mjs", "mts", "cjs", "cts"}

type Vitest struct {
	executor        ext.CommandExecutor
	commandOverride []string
	platformEnv     map[string]string
}

func NewVitest() *Vitest {
	return &Vitest{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: loadCommandOverride(),
		platformEnv:     make(map[string]string),
	}
}

func (v *Vitest) SetPlatformEnv(platformEnv map[string]string) {
	v.platformEnv = platformEnv
}

func (v *Vitest) GetPlatformEnv() map[string]string {
	return v.platformEnv
}

func (v *Vitest) Name() string {
	return "vitest"
}

// Vitest is planned and skipped at suite (test file) level. Native full test
// discovery is unnecessary for that mode.
func (v *Vitest) SupportsFullTestDiscovery() bool {
	return false
}

func (v *Vitest) SourceFileForSuite(suite string) (string, bool) {
	suite = strings.TrimSpace(suite)
	if suite == "" {
		return "", false
	}
	return suite, true
}

func (v *Vitest) HasUnskippableMarker(testFile string) bool {
	return utils.FileContainsAll(testFile, "@datadog", "unskippable")
}

func (v *Vitest) TestPattern() string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return custom
	}
	return filepath.ToSlash(filepath.Join("**", "*.{test,spec}.{"+strings.Join(vitestTestFileExtensions, ",")+"}"))
}

func (v *Vitest) DiscoverTests(ctx context.Context, testFiles discovery.TestFileSet) ([]testoptimization.Test, error) {
	return nil, ErrFullTestDiscoveryUnsupported
}

// DiscoverTestFiles uses Vitest's config-aware file listing when available,
// then falls back to the Vitest 1.6 API and finally DDTest's filesystem glob.
func (v *Vitest) DiscoverTestFiles(ctx context.Context, testFiles discovery.TestFileSet) ([]string, error) {
	// With an exclude pattern, ExplicitFiles contains candidates from DDTest's generic
	// glob. Vitest discovery must remain authoritative before applying the exclude.
	if settings.GetTestsExcludePattern() == "" {
		if testFiles.Empty() {
			return []string{}, nil
		}
		if testFiles.UseExplicitFiles() {
			return slices.Clone(testFiles.ExplicitFiles), nil
		}
	}

	command, baseArgs := v.getVitestCommand()
	args := vitestArgsForSubcommand(baseArgs, "list")
	args = append(args, "--filesOnly", "--json")

	slog.Info("Discovering Vitest test files with command", "command", command, "args", args)
	output, err := v.executor.CombinedOutput(ctx, command, args, v.discoveryEnv())
	if err != nil {
		if supportsVitestV1DiscoveryFallback(output) {
			return v.discoverVitestV1TestFiles(ctx, command, baseArgs, testFiles)
		}
		message := strings.TrimSpace(string(output))
		if message == "" {
			return nil, fmt.Errorf("failed to discover Vitest test files: %w", err)
		}
		return nil, fmt.Errorf("failed to discover Vitest test files: %s: %w", message, err)
	}

	discoveredFiles, err := parseVitestListFilesOutput(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Vitest test file list: %w", err)
	}
	if settings.GetTestsLocation() == "" && settings.GetTestsExcludePattern() == "" {
		return discoveredFiles, nil
	}

	return filterVitestTestFiles(discoveredFiles, testFiles)
}

// discoverVitestV1TestFiles uses the project's vitest/node API to load its
// configuration and list test files without executing them.
func (v *Vitest) discoverVitestV1TestFiles(ctx context.Context, command string, baseArgs []string, testFiles discovery.TestFileSet) ([]string, error) {
	cliArgs, err := json.Marshal(vitestCLIArgs(command, baseArgs))
	if err == nil {
		slog.Info("Vitest does not support list --filesOnly; using the Vitest 1.6 config-aware discovery API")
		output, discoveryErr := v.executor.CombinedOutput(ctx, "node", []string{
			"--input-type=module",
			"--eval",
			vitestV1DiscoveryScript,
			string(cliArgs),
		}, v.discoveryEnv())
		if discoveryErr == nil {
			discoveredFiles, parseErr := parseVitestV1DiscoveryOutput(output)
			if parseErr == nil {
				if settings.GetTestsLocation() == "" && settings.GetTestsExcludePattern() == "" {
					return discoveredFiles, nil
				}
				return filterVitestTestFiles(discoveredFiles, testFiles)
			}
			err = parseErr
		} else {
			message := strings.TrimSpace(string(output))
			if message == "" {
				err = discoveryErr
			} else {
				err = fmt.Errorf("%s: %w", message, discoveryErr)
			}
		}
	}

	slog.Warn("Vitest 1.6 config-aware discovery failed; using ddtest glob discovery", "error", err)
	return discovery.DiscoverTestFiles(testFiles.Pattern, settings.GetTestsExcludePattern())
}

func (v *Vitest) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	command, baseArgs := v.getVitestCommand()
	args := vitestArgsForSubcommand(baseArgs, "run")
	args = append(args, testFiles...)

	slog.Info("Running tests with command", "command", command, "args", args)

	mergedEnv := make(map[string]string)
	maps.Copy(mergedEnv, v.platformEnv)
	maps.Copy(mergedEnv, envMap)
	return v.executor.Run(ctx, command, args, mergedEnv)
}

func (v *Vitest) discoveryEnv() map[string]string {
	envMap := make(map[string]string, len(v.platformEnv)+1)
	maps.Copy(envMap, v.platformEnv)

	nodeOptions, ok := envMap[nodeOptionsEnvVar]
	if !ok {
		var found bool
		nodeOptions, found = os.LookupEnv(nodeOptionsEnvVar)
		if !found {
			return envMap
		}
	}

	nodeOptions = stripNodeOptionsRequire(nodeOptions, ddTraceCIInitModule)
	envMap[nodeOptionsEnvVar] = stripNodeOptionsImport(nodeOptions, ddTraceRegisterPath)
	return envMap
}

// Decide between a user custom command, the local Vitest binary and npx.
func (v *Vitest) getVitestCommand() (string, []string) {
	if len(v.commandOverride) > 0 {
		return v.commandOverride[0], v.commandOverride[1:]
	}

	if info, err := os.Stat(binVitestPath); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
		slog.Debug("Using local Vitest binary")
		return binVitestPath, nil
	}

	slog.Debug("Using npx vitest for Vitest commands")
	return "npx", []string{"vitest"}
}

func vitestArgsForSubcommand(baseArgs []string, subcommand string) []string {
	args := slices.Clone(baseArgs)
	for i, arg := range args {
		switch arg {
		case "run", "watch", "dev", "list":
			args[i] = subcommand
			return args
		}
	}

	for i, arg := range args {
		base := filepath.Base(arg)
		if base == "vitest" || base == "vitest.mjs" {
			args = slices.Insert(args, i+1, subcommand)
			return args
		}
	}

	return append([]string{subcommand}, args...)
}

func vitestCLIArgs(command string, baseArgs []string) []string {
	if isVitestExecutable(command) {
		return append([]string{"vitest"}, baseArgs...)
	}

	for i, arg := range baseArgs {
		if isVitestExecutable(arg) {
			return append([]string{"vitest"}, baseArgs[i+1:]...)
		}
	}

	return []string{"vitest"}
}

func isVitestExecutable(value string) bool {
	base := filepath.Base(value)
	return base == "vitest" || base == "vitest.mjs"
}

func supportsVitestV1DiscoveryFallback(output []byte) bool {
	message := strings.ToLower(string(output))
	return strings.Contains(message, "unknown option") &&
		(strings.Contains(message, "filesonly") || strings.Contains(message, "files-only"))
}

func filterVitestTestFiles(testFiles []string, selectedTestFiles discovery.TestFileSet) ([]string, error) {
	if settings.GetTestsExcludePattern() != "" {
		selectedTestFiles.ExplicitFiles = nil
	}
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
		if normalizedTestFile != "" && testFileMatcher.MatchNormalizedPath(normalizedTestFile) {
			filteredFiles = append(filteredFiles, normalizedTestFile)
		}
	}

	slices.Sort(filteredFiles)
	return slices.Compact(filteredFiles), nil
}

func parseVitestListFilesOutput(output []byte) ([]string, error) {
	var listedFiles []struct {
		File string `json:"file"`
	}
	if err := json.Unmarshal(output, &listedFiles); err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(listedFiles))
	for _, listedFile := range listedFiles {
		paths = append(paths, listedFile.File)
	}
	return normalizeVitestTestFiles(paths), nil
}

func parseVitestV1DiscoveryOutput(output []byte) ([]string, error) {
	markerIndex := strings.LastIndex(string(output), vitestV1DiscoveryMarker)
	if markerIndex < 0 {
		return nil, errors.New("Vitest 1.6 discovery output did not contain a file list")
	}

	encodedFiles := string(output[markerIndex+len(vitestV1DiscoveryMarker):])
	if lineEnd := strings.IndexByte(encodedFiles, '\n'); lineEnd >= 0 {
		encodedFiles = encodedFiles[:lineEnd]
	}
	var testFiles []string
	if err := json.Unmarshal([]byte(encodedFiles), &testFiles); err != nil {
		return nil, fmt.Errorf("failed to parse Vitest 1.6 discovery output: %w", err)
	}
	return normalizeVitestTestFiles(testFiles), nil
}

func normalizeVitestTestFiles(paths []string) []string {
	cwd, _ := os.Getwd()
	if resolvedCwd, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolvedCwd
	}

	testFiles := make([]string, 0)
	for _, path := range paths {
		testFile := strings.TrimSpace(path)
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

func stripNodeOptionsImport(nodeOptions string, module string) string {
	fields := strings.Fields(nodeOptions)
	stripped := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if field == "--import" {
			if i+1 < len(fields) && fields[i+1] == module {
				i++
				continue
			}
		}
		if strings.HasPrefix(field, "--import=") && strings.TrimPrefix(field, "--import=") == module {
			continue
		}
		stripped = append(stripped, field)
	}
	return strings.Join(stripped, " ")
}
