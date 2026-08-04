package framework

import (
	"context"
	_ "embed"
	"encoding/json"
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
	binMochaPath         = "node_modules/.bin/mocha"
	mochaDiscoveryMarker = "__DDTEST_MOCHA_FILES__"
)

//go:embed scripts/mocha_adapter.js
var mochaAdapterScript string

type Mocha struct {
	executor        ext.CommandExecutor
	commandOverride []string
	platformEnv     map[string]string
}

func NewMocha() *Mocha {
	return &Mocha{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: loadCommandOverride(),
		platformEnv:     make(map[string]string),
	}
}

func (m *Mocha) SetPlatformEnv(platformEnv map[string]string) { m.platformEnv = platformEnv }
func (m *Mocha) GetPlatformEnv() map[string]string            { return m.platformEnv }
func (m *Mocha) Name() string                                 { return "mocha" }
func (m *Mocha) SupportsFullTestDiscovery() bool              { return false }

func (m *Mocha) SourceFileForSuite(suite string) (string, bool) {
	suite = strings.TrimSpace(suite)
	if suite == "" {
		return "", false
	}
	return suite, true
}

func (m *Mocha) HasUnskippableMarker(testFile string) bool {
	return utils.FileContainsAll(testFile, "@datadog", "unskippable")
}

func (m *Mocha) TestPattern() string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return custom
	}
	return filepath.ToSlash(filepath.Join("test", "**", "*.{js,cjs,mjs}"))
}

func (m *Mocha) DiscoverTests(context.Context, discovery.TestFileSet) ([]testoptimization.Test, error) {
	return nil, ErrFullTestDiscoveryUnsupported
}

func (m *Mocha) DiscoverTestFiles(ctx context.Context, testFiles discovery.TestFileSet) ([]string, error) {
	if settings.GetTestsExcludePattern() == "" {
		if testFiles.Empty() {
			return []string{}, nil
		}
		if testFiles.UseExplicitFiles() {
			return slices.Clone(testFiles.ExplicitFiles), nil
		}
	}

	command, baseArgs := m.getMochaCommand()
	cliArgs, err := mochaCLIArgs(command, baseArgs)
	if err != nil {
		return nil, err
	}
	request, err := json.Marshal(map[string]any{"mode": "discover", "cliArgs": cliArgs})
	if err != nil {
		return nil, fmt.Errorf("failed to encode Mocha discovery request: %w", err)
	}

	slog.Info("Discovering Mocha test files", "command", command, "args", baseArgs)
	output, err := m.executor.CombinedOutput(ctx, "node", []string{"--eval", mochaAdapterScript, string(request)}, m.discoveryEnv())
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return nil, fmt.Errorf("failed to discover Mocha test files: %w", err)
		}
		return nil, fmt.Errorf("failed to discover Mocha test files: %s: %w", message, err)
	}

	discoveredFiles, err := parseMochaDiscoveryOutput(output)
	if err != nil {
		return nil, err
	}
	if settings.GetTestsLocation() == "" && settings.GetTestsExcludePattern() == "" {
		return discoveredFiles, nil
	}
	return filterMochaTestFiles(discoveredFiles, testFiles)
}

func (m *Mocha) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	command, baseArgs := m.getMochaCommand()
	cliArgs, err := mochaCLIArgs(command, baseArgs)
	if err != nil {
		return err
	}
	request, err := json.Marshal(map[string]any{"mode": "run", "cliArgs": cliArgs, "files": testFiles})
	if err != nil {
		return fmt.Errorf("failed to encode Mocha run request: %w", err)
	}

	slog.Info("Running Mocha tests", "command", command, "args", baseArgs, "testFiles", testFiles)
	mergedEnv := make(map[string]string)
	maps.Copy(mergedEnv, m.platformEnv)
	maps.Copy(mergedEnv, envMap)
	return m.executor.Run(ctx, "node", []string{"--eval", mochaAdapterScript, string(request)}, mergedEnv)
}

func (m *Mocha) discoveryEnv() map[string]string {
	envMap := make(map[string]string, len(m.platformEnv)+1)
	maps.Copy(envMap, m.platformEnv)
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

func (m *Mocha) getMochaCommand() (string, []string) {
	if len(m.commandOverride) > 0 {
		return m.commandOverride[0], m.commandOverride[1:]
	}
	if info, err := os.Stat(binMochaPath); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
		return binMochaPath, nil
	}
	return "npx", []string{"mocha"}
}

func mochaCLIArgs(command string, baseArgs []string) ([]string, error) {
	if isMochaExecutable(command) {
		return slices.Clone(baseArgs), nil
	}
	for i, arg := range baseArgs {
		if isMochaExecutable(arg) {
			return slices.Clone(baseArgs[i+1:]), nil
		}
	}
	return nil, fmt.Errorf("Mocha command must invoke Mocha directly: %s %s", command, strings.Join(baseArgs, " "))
}

func isMochaExecutable(value string) bool {
	base := filepath.Base(value)
	return base == "mocha" || base == "mocha.js" || base == "_mocha"
}

func parseMochaDiscoveryOutput(output []byte) ([]string, error) {
	markerIndex := strings.LastIndex(string(output), mochaDiscoveryMarker)
	if markerIndex < 0 {
		return nil, fmt.Errorf("Mocha discovery output did not contain a file list")
	}
	encodedFiles := string(output[markerIndex+len(mochaDiscoveryMarker):])
	if lineEnd := strings.IndexByte(encodedFiles, '\n'); lineEnd >= 0 {
		encodedFiles = encodedFiles[:lineEnd]
	}
	var paths []string
	if err := json.Unmarshal([]byte(encodedFiles), &paths); err != nil {
		return nil, fmt.Errorf("failed to parse Mocha test file list: %w", err)
	}
	return normalizeMochaTestFiles(paths), nil
}

func normalizeMochaTestFiles(paths []string) []string {
	cwd, _ := os.Getwd()
	if resolvedCwd, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolvedCwd
	}
	files := make([]string, 0, len(paths))
	for _, candidate := range paths {
		testFile := strings.TrimSpace(candidate)
		if testFile == "" {
			continue
		}
		if filepath.IsAbs(testFile) && cwd != "" {
			pathForRel := testFile
			if resolvedPath, err := filepath.EvalSymlinks(testFile); err == nil {
				pathForRel = resolvedPath
			}
			relativePath, err := filepath.Rel(cwd, pathForRel)
			if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
				continue
			}
			testFile = relativePath
		}
		normalized := utils.NormalizePath(testFile)
		if normalized == "" {
			continue
		}
		if _, err := os.Stat(normalized); err != nil {
			continue
		}
		files = append(files, normalized)
	}
	slices.Sort(files)
	return slices.Compact(files)
}

func filterMochaTestFiles(testFiles []string, selectedTestFiles discovery.TestFileSet) ([]string, error) {
	if settings.GetTestsExcludePattern() != "" {
		selectedTestFiles.ExplicitFiles = nil
	}
	if settings.GetTestsLocation() == "" {
		selectedTestFiles.Pattern = ""
	}
	matcher, err := discovery.NewTestFileSetMatcher(selectedTestFiles, settings.GetTestsExcludePattern())
	if err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(testFiles))
	for _, testFile := range testFiles {
		normalized := utils.NormalizePath(testFile)
		if normalized != "" && matcher.MatchNormalizedPath(normalized) {
			filtered = append(filtered, normalized)
		}
	}
	slices.Sort(filtered)
	return slices.Compact(filtered), nil
}
