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
	"regexp"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/utils"
)

const (
	binPlaywrightPath          = "node_modules/.bin/playwright"
	playwrightDiscoveryMarker  = "__DDTEST_PLAYWRIGHT_FILES__"
	playwrightErrorMarker      = "__DDTEST_PLAYWRIGHT_ERROR__"
	playwrightDefaultPattern   = "**/*.{spec,test}.{js,jsx,ts,tsx,mjs,mts,cjs,cts}"
	playwrightReporterFileMode = 0600
)

//go:embed scripts/playwright_discovery_reporter.cjs
var playwrightDiscoveryReporterScript string

type Playwright struct {
	executor        ext.CommandExecutor
	commandOverride []string
	platformEnv     map[string]string
	discoveryRoot   string
}

type playwrightDiscoveryResult struct {
	RootDir string   `json:"rootDir"`
	Files   []string `json:"files"`
}

type playwrightDiscoveryError struct {
	Message string `json:"message"`
}

func NewPlaywright() *Playwright {
	return &Playwright{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: loadCommandOverride(),
		platformEnv:     make(map[string]string),
	}
}

func (p *Playwright) SetPlatformEnv(platformEnv map[string]string) { p.platformEnv = platformEnv }
func (p *Playwright) GetPlatformEnv() map[string]string            { return p.platformEnv }
func (p *Playwright) Name() string                                 { return "playwright" }
func (p *Playwright) SupportsFullTestDiscovery() bool              { return false }

func (p *Playwright) SourceFileForSuite(suite string) (string, bool) {
	suite = strings.TrimSpace(suite)
	if suite == "" {
		return "", false
	}
	command, baseArgs := p.getPlaywrightCommand()
	cliArgs, err := playwrightCLIArgs(command, baseArgs)
	if err != nil {
		return utils.NormalizePath(suite), true
	}
	rootDir := p.discoveryRoot
	if rootDir == "" {
		rootDir, err = playwrightConfigRoot(cliArgs)
		if err != nil {
			return utils.NormalizePath(suite), true
		}
	}
	cwd, err := os.Getwd()
	if err != nil || sameFilePath(rootDir, cwd) {
		return utils.NormalizePath(suite), true
	}
	if resolvedCwd, resolveErr := filepath.EvalSymlinks(cwd); resolveErr == nil {
		cwd = resolvedCwd
	}
	relative, err := filepath.Rel(cwd, filepath.Join(rootDir, filepath.FromSlash(suite)))
	if err != nil {
		return utils.NormalizePath(suite), true
	}
	return utils.NormalizePath(relative), true
}

func (p *Playwright) HasUnskippableMarker(testFile string) bool {
	return utils.FileContainsAll(testFile, "@datadog", "unskippable")
}

func (p *Playwright) TestPattern() string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return custom
	}
	return playwrightDefaultPattern
}

func (p *Playwright) DiscoverTests(context.Context, discovery.TestFileSet) ([]testoptimization.Test, error) {
	return nil, ErrFullTestDiscoveryUnsupported
}

func (p *Playwright) DiscoverTestFiles(ctx context.Context, selectedFiles discovery.TestFileSet) ([]string, error) {
	command, baseArgs := p.getPlaywrightCommand()
	if _, err := playwrightCLIArgs(command, baseArgs); err != nil {
		return nil, err
	}
	reporterPath, err := preparePlaywrightDiscoveryReporter()
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(reporterPath) }()

	args := playwrightDiscoveryArgs(command, baseArgs, reporterPath)
	slog.Info("Discovering Playwright test files with command", "command", command, "args", args)
	output, commandErr := p.executor.CombinedOutput(ctx, command, args, p.discoveryEnv())
	discoveryResult, parseErr := parsePlaywrightDiscoveryOutput(output)
	if commandErr != nil && (!isPlaywrightNoTestsExit(output, commandErr) || len(discoveryResult.Files) > 0) {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return nil, fmt.Errorf("failed to list Playwright tests: %w", commandErr)
		}
		return nil, fmt.Errorf("failed to list Playwright tests: %s: %w", message, commandErr)
	}
	if commandErr != nil && parseErr != nil {
		// Playwright 1.18 reports the no-tests error before onBegin, so the
		// reporter cannot emit a file payload. A verified no-tests exit still
		// represents a successful empty discovery.
		if strings.Contains(string(output), playwrightDiscoveryMarker) {
			return nil, parseErr
		}
		return filterPlaywrightTestFiles(nil, selectedFiles)
	}
	if parseErr != nil {
		return nil, parseErr
	}
	p.discoveryRoot = discoveryResult.RootDir
	for i := range discoveryResult.Files {
		discoveryResult.Files[i] = utils.NormalizePath(discoveryResult.Files[i])
	}
	slices.Sort(discoveryResult.Files)
	return filterPlaywrightTestFiles(slices.Compact(discoveryResult.Files), selectedFiles)
}

func (p *Playwright) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	if len(testFiles) == 0 {
		return nil
	}
	command, baseArgs := p.getPlaywrightCommand()
	if _, err := playwrightCLIArgs(command, baseArgs); err != nil {
		return err
	}
	args := playwrightRunArgs(command, baseArgs, testFiles)
	slog.Info("Running Playwright tests", "command", command, "args", args, "testFiles", testFiles)
	mergedEnv := make(map[string]string)
	maps.Copy(mergedEnv, p.platformEnv)
	maps.Copy(mergedEnv, envMap)
	return p.executor.Run(ctx, command, args, mergedEnv)
}

func (p *Playwright) discoveryEnv() map[string]string {
	envMap := make(map[string]string, len(p.platformEnv)+1)
	maps.Copy(envMap, p.platformEnv)
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

func (p *Playwright) getPlaywrightCommand() (string, []string) {
	if len(p.commandOverride) > 0 {
		return p.commandOverride[0], p.commandOverride[1:]
	}
	if info, err := os.Stat(binPlaywrightPath); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
		return binPlaywrightPath, nil
	}
	return "npx", []string{"playwright"}
}

func isPlaywrightExecutable(value string) bool {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(value, `\`, "/")))
	return base == "playwright" || base == "playwright.js" ||
		base == "playwright.cmd" || base == "playwright.ps1"
}

func playwrightCLIArgs(command string, baseArgs []string) ([]string, error) {
	var cliArgs []string
	if isPlaywrightExecutable(command) {
		cliArgs = slices.Clone(baseArgs)
	} else {
		for i, arg := range baseArgs {
			if isPlaywrightExecutable(arg) {
				cliArgs = slices.Clone(baseArgs[i+1:])
				break
			}
		}
		if cliArgs == nil {
			return nil, fmt.Errorf("Playwright command must invoke Playwright directly: %s %s", command, strings.Join(baseArgs, " "))
		}
	}
	if len(cliArgs) > 0 && cliArgs[0] == "test" {
		return cliArgs[1:], nil
	}
	if len(cliArgs) > 0 && !strings.HasPrefix(cliArgs[0], "-") {
		return nil, fmt.Errorf("Playwright command must invoke the test subcommand, got %q", cliArgs[0])
	}
	return cliArgs, nil
}

func splitPlaywrightCommand(command string, baseArgs []string) ([]string, []string) {
	if isPlaywrightExecutable(command) {
		cliArgs, _ := playwrightCLIArgs(command, baseArgs)
		return nil, cliArgs
	}
	for i, arg := range baseArgs {
		if isPlaywrightExecutable(arg) {
			cliArgs, _ := playwrightCLIArgs(command, baseArgs)
			return slices.Clone(baseArgs[:i+1]), cliArgs
		}
	}
	return slices.Clone(baseArgs), nil
}

type playwrightOptionArity uint8

const (
	playwrightNoValue playwrightOptionArity = iota
	playwrightSingleValue
	playwrightOptionalValue
	playwrightVariadicValue
)

var playwrightOptionArities = map[string]playwrightOptionArity{
	"--browser": playwrightSingleValue, "-c": playwrightSingleValue, "--config": playwrightSingleValue,
	"--global-timeout": playwrightSingleValue, "-g": playwrightSingleValue, "--grep": playwrightSingleValue,
	"-G": playwrightSingleValue, "--grep-invert": playwrightSingleValue, "--last-failed-file": playwrightSingleValue,
	"--max-failures": playwrightSingleValue, "--output": playwrightSingleValue, "--repeat-each": playwrightSingleValue,
	"--reporter": playwrightSingleValue, "--retries": playwrightSingleValue, "--shard": playwrightSingleValue,
	"--test-list": playwrightSingleValue, "--test-list-invert": playwrightSingleValue, "--timeout": playwrightSingleValue,
	"--trace": playwrightSingleValue, "--tsconfig": playwrightSingleValue, "--ui-host": playwrightSingleValue,
	"--ui-port": playwrightSingleValue, "--update-source-method": playwrightSingleValue, "-j": playwrightSingleValue,
	"--workers": playwrightSingleValue, "--run-agents": playwrightSingleValue,

	"--only-changed": playwrightOptionalValue, "-u": playwrightOptionalValue, "--update-snapshots": playwrightOptionalValue,

	"--project": playwrightVariadicValue,
}

var playwrightRunOverrides = []string{"--shard", "--list", "--ui", "--ui-host", "--ui-port"}

var playwrightDiscoveryOverrides = append(slices.Clone(playwrightRunOverrides), "--reporter", "--debug")

func playwrightDiscoveryArgs(command string, baseArgs []string, reporterPath string) []string {
	prefix, cliArgs := splitPlaywrightCommand(command, baseArgs)
	args := append(prefix, "test")
	args = append(args, filterPlaywrightArgs(cliArgs, playwrightDiscoveryOverrides, false)...)
	args = append(args, "--list", "--reporter="+reporterPath)
	return args
}

func playwrightRunArgs(command string, baseArgs, testFiles []string) []string {
	prefix, cliArgs := splitPlaywrightCommand(command, baseArgs)
	args := append(prefix, "test")
	for _, testFile := range testFiles {
		args = append(args, playwrightExactFileFilter(testFile))
	}
	// --project has a variadic value in Playwright's CLI. Keep file filters
	// before all preserved options so a trailing space-form --project value
	// cannot consume them as additional project names.
	return append(args, filterPlaywrightArgs(cliArgs, playwrightRunOverrides, true)...)
}

func filterPlaywrightArgs(args, overridden []string, removeFiles bool) []string {
	result := make([]string, 0, len(args))
	for i := 0; i < len(args); {
		arg := args[i]
		name, _, _ := strings.Cut(arg, "=")
		end := playwrightArgumentEnd(args, i)
		if !slices.Contains(overridden, name) && (!removeFiles || strings.HasPrefix(arg, "-")) {
			result = append(result, args[i:end]...)
		}
		i = end
	}
	return result
}

func playwrightArgumentEnd(args []string, index int) int {
	name, _, hasInlineValue := strings.Cut(args[index], "=")
	end := index + 1
	if hasInlineValue {
		return end
	}

	switch playwrightOptionArities[name] {
	case playwrightSingleValue:
		if end < len(args) {
			end++
		}
	case playwrightOptionalValue:
		if end < len(args) && !strings.HasPrefix(args[end], "-") {
			end++
		}
	case playwrightVariadicValue:
		for end < len(args) && !strings.HasPrefix(args[end], "-") {
			end++
		}
	}
	return end
}

func playwrightExactFileFilter(testFile string) string {
	path := filepath.FromSlash(testFile)
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	normalized := filepath.ToSlash(path)
	escaped := regexp.QuoteMeta(normalized)
	escaped = strings.ReplaceAll(escaped, "/", `[/\\]`)
	return `^` + escaped + `$`
}

func preparePlaywrightDiscoveryReporter() (string, error) {
	file, err := os.CreateTemp("", ".ddtest-playwright-reporter-*.cjs")
	if err != nil {
		return "", fmt.Errorf("failed to create Playwright discovery reporter: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := file.Chmod(playwrightReporterFileMode); err != nil {
		_ = file.Close()
		cleanup()
		return "", fmt.Errorf("failed to set Playwright discovery reporter permissions: %w", err)
	}
	if _, err := file.WriteString(playwrightDiscoveryReporterScript); err != nil {
		_ = file.Close()
		cleanup()
		return "", fmt.Errorf("failed to write Playwright discovery reporter: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", fmt.Errorf("failed to close Playwright discovery reporter: %w", err)
	}
	return path, nil
}

func parsePlaywrightDiscoveryOutput(output []byte) (playwrightDiscoveryResult, error) {
	var parseErr error
	lines := strings.Split(string(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		encoded, ok := strings.CutPrefix(lines[i], playwrightDiscoveryMarker)
		if !ok {
			continue
		}
		var result playwrightDiscoveryResult
		if err := json.Unmarshal([]byte(encoded), &result); err != nil {
			parseErr = err
			continue
		}
		return result, nil
	}
	if parseErr != nil {
		return playwrightDiscoveryResult{}, fmt.Errorf("failed to parse Playwright test file list: %w", parseErr)
	}
	return playwrightDiscoveryResult{}, errors.New("Playwright discovery output did not contain a test file list")
}

type exitCoder interface {
	ExitCode() int
}

func isPlaywrightNoTestsExit(output []byte, commandErr error) bool {
	var exitErr exitCoder
	if !errors.As(commandErr, &exitErr) || exitErr.ExitCode() != 1 {
		return false
	}
	errors, err := parsePlaywrightDiscoveryErrors(output)
	return err == nil && len(errors) == 1 && isPlaywrightNoTestsMessage(errors[0].Message)
}

func parsePlaywrightDiscoveryErrors(output []byte) ([]playwrightDiscoveryError, error) {
	var result []playwrightDiscoveryError
	for _, line := range strings.Split(string(output), "\n") {
		markerIndex := strings.Index(line, playwrightErrorMarker)
		if markerIndex < 0 {
			continue
		}
		var discoveryErr playwrightDiscoveryError
		if err := json.Unmarshal([]byte(line[markerIndex+len(playwrightErrorMarker):]), &discoveryErr); err != nil {
			return nil, fmt.Errorf("failed to parse Playwright discovery error: %w", err)
		}
		result = append(result, discoveryErr)
	}
	return result, nil
}

func isPlaywrightNoTestsMessage(message string) bool {
	trimmed := strings.TrimSpace(message)
	if strings.EqualFold(trimmed, "=================\n no tests found.\n=================") {
		return true
	}
	firstLine, _, _ := strings.Cut(trimmed, "\n")
	firstLine = strings.TrimSpace(strings.TrimPrefix(firstLine, "Error:"))
	return strings.TrimSuffix(firstLine, ".") == "No tests found"
}

func filterPlaywrightTestFiles(testFiles []string, selectedFiles discovery.TestFileSet) ([]string, error) {
	if settings.GetTestsExcludePattern() != "" {
		selectedFiles.ExplicitFiles = nil
	}
	if settings.GetTestsLocation() == "" {
		selectedFiles.Pattern = ""
	}
	matcher, err := discovery.NewTestFileSetMatcher(selectedFiles, settings.GetTestsExcludePattern())
	if err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(testFiles))
	for _, testFile := range testFiles {
		if matcher.MatchNormalizedPath(testFile) {
			filtered = append(filtered, testFile)
		}
	}
	return filtered, nil
}

func playwrightConfigRoot(cliArgs []string) (string, error) {
	configured := playwrightOptionValue(cliArgs, "--config", "-c")
	root := "."
	if configured != "" {
		root = configured
		if info, err := os.Stat(root); err == nil && !info.IsDir() {
			root = filepath.Dir(root)
		} else if filepath.Ext(root) != "" {
			root = filepath.Dir(root)
		}
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("failed to resolve Playwright config root %q: %w", root, err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	return absolute, nil
}

func playwrightOptionValue(args []string, options ...string) string {
	value := ""
	for i := 0; i < len(args); i++ {
		for _, option := range options {
			if args[i] == option && i+1 < len(args) {
				value = args[i+1]
				i++
				break
			}
			if candidate, ok := strings.CutPrefix(args[i], option+"="); ok {
				value = candidate
				break
			}
		}
	}
	return value
}
