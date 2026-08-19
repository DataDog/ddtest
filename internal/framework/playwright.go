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
	playwrightDefaultPattern   = "**/*.@(spec|test).?(c|m)[jt]s?(x)"
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
	discovered, err := normalizePlaywrightTestFiles(discoveryResult.Files)
	if err != nil {
		return nil, err
	}
	return filterPlaywrightTestFiles(discovered, selectedFiles)
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

var playwrightValueOptions = map[string]bool{
	"--browser": true, "-c": true, "--config": true, "--global-timeout": true,
	"-g": true, "--grep": true, "-G": true, "--grep-invert": true,
	"--last-failed-file": true, "--max-failures": true, "--output": true,
	"--repeat-each": true, "--reporter": true, "--retries": true, "--shard": true,
	"--test-list": true, "--test-list-invert": true, "--timeout": true,
	"--trace": true, "--tsconfig": true, "--ui-host": true, "--ui-port": true,
	"--update-source-method": true, "-j": true, "--workers": true, "--run-agents": true,
}

var playwrightVariadicValueOptions = map[string]bool{
	"--project": true,
}

var playwrightOptionalValueOptions = map[string]bool{
	"--only-changed": true, "-u": true, "--update-snapshots": true,
}

func playwrightDiscoveryArgs(command string, baseArgs []string, reporterPath string) []string {
	prefix, cliArgs := splitPlaywrightCommand(command, baseArgs)
	filtered, tail := filterPlaywrightArgs(cliArgs, false)
	args := append(prefix, "test")
	args = append(args, filtered...)
	args = append(args, "--list", "--reporter="+reporterPath)
	return append(args, tail...)
}

func playwrightRunArgs(command string, baseArgs, testFiles []string) []string {
	prefix, cliArgs := splitPlaywrightCommand(command, baseArgs)
	filtered, tail := filterPlaywrightArgs(cliArgs, true)
	args := append(prefix, "test")
	for _, testFile := range testFiles {
		args = append(args, playwrightExactFileFilter(testFile))
	}
	// --project has a variadic value in Playwright's CLI. Keep file filters
	// before all preserved options so a trailing space-form --project value
	// cannot consume them as additional project names.
	args = append(args, filtered...)
	return append(args, tail...)
}

// filterPlaywrightArgs removes options that conflict with DDTest's discovery or
// sharding. For execution, it also removes the user's original positional file
// filters so they cannot re-add files that were assigned to another worker.
func filterPlaywrightArgs(args []string, running bool) ([]string, []string) {
	result := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return result, slices.Clone(args[i:])
		}
		name := arg
		if before, _, ok := strings.Cut(arg, "="); ok {
			name = before
		}
		remove := name == "--shard" || name == "--list" || name == "--ui" || name == "--ui-host" || name == "--ui-port" ||
			(!running && (name == "--reporter" || name == "--debug"))

		if playwrightValueOptions[name] {
			if !remove {
				result = append(result, arg)
			}
			if arg == name && i+1 < len(args) {
				i++
				if !remove {
					result = append(result, args[i])
				}
			}
			continue
		}
		if playwrightVariadicValueOptions[name] {
			if !remove {
				result = append(result, arg)
			}
			if arg == name {
				for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					i++
					if !remove {
						result = append(result, args[i])
					}
				}
			}
			continue
		}
		if playwrightOptionalValueOptions[name] {
			if !remove {
				result = append(result, arg)
			}
			if arg == name && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				if !remove {
					result = append(result, args[i])
				}
			}
			continue
		}
		if running && !strings.HasPrefix(arg, "-") {
			continue
		}
		if !remove {
			result = append(result, arg)
		}
	}
	return result, nil
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
	text := string(output)
	markerIndex := strings.LastIndex(text, playwrightDiscoveryMarker)
	if markerIndex < 0 {
		return playwrightDiscoveryResult{}, errors.New("Playwright discovery output did not contain a test file list")
	}
	encoded := text[markerIndex+len(playwrightDiscoveryMarker):]
	if lineEnd := strings.IndexByte(encoded, '\n'); lineEnd >= 0 {
		encoded = encoded[:lineEnd]
	}
	var result playwrightDiscoveryResult
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		return playwrightDiscoveryResult{}, fmt.Errorf("failed to parse Playwright test file list: %w", err)
	}
	return result, nil
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

func normalizePlaywrightTestFiles(testFiles []string) ([]string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve working directory: %w", err)
	}
	if resolvedCwd, resolveErr := filepath.EvalSymlinks(cwd); resolveErr == nil {
		cwd = resolvedCwd
	}
	result := make([]string, 0, len(testFiles))
	for _, testFile := range testFiles {
		path := filepath.FromSlash(testFile)
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		relative, err := filepath.Rel(cwd, path)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve Playwright test file %q: %w", testFile, err)
		}
		// macOS commonly exposes the same temporary directory through /var and
		// /private/var. Canonicalize only when the original path appears outside
		// the working directory so ordinary spec-directory symlinks stay intact.
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			if resolvedPath, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
				if resolvedRelative, relErr := filepath.Rel(cwd, resolvedPath); relErr == nil &&
					resolvedRelative != ".." && !strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
					relative = resolvedRelative
				}
			}
		}
		result = append(result, utils.NormalizePath(relative))
	}
	slices.Sort(result)
	return slices.Compact(result), nil
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
