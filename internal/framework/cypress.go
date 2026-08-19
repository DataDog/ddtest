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
	binCypressPath                  = "node_modules/.bin/cypress"
	cypressDiscoveryMarker          = "__DDTEST_CYPRESS_CONFIG__"
	cypressMissingDiscoverySpec     = "__ddtest_cypress_discovery_no_match__"
	cypressConfigImportToken        = "__DDTEST_CONFIG_IMPORT__"
	cypressDiscoveryConfigPathToken = "__DDTEST_DISCOVERY_CONFIG_PATH__"
	cypressDefaultE2EPattern        = "cypress/e2e/**/*.cy.{js,jsx,ts,tsx}"
)

var cypressConfigFilenames = []string{
	"cypress.config.js",
	"cypress.config.ts",
	"cypress.config.mjs",
	"cypress.config.cjs",
}

//go:embed scripts/cypress_discovery_config.ts
var cypressDiscoveryConfigScript string

type Cypress struct {
	executor        ext.CommandExecutor
	commandOverride []string
	platformEnv     map[string]string
}

type cypressDiscoveryConfig struct {
	ProjectRoot string   `json:"projectRoot"`
	TestingType string   `json:"testingType"`
	SpecFiles   []string `json:"specFiles"`
}

func NewCypress() *Cypress {
	return &Cypress{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: loadCommandOverride(),
		platformEnv:     make(map[string]string),
	}
}

func (c *Cypress) SetPlatformEnv(platformEnv map[string]string) { c.platformEnv = platformEnv }
func (c *Cypress) GetPlatformEnv() map[string]string            { return c.platformEnv }
func (c *Cypress) Name() string                                 { return "cypress" }
func (c *Cypress) SupportsFullTestDiscovery() bool              { return false }

func (c *Cypress) SourceFileForSuite(suite string) (string, bool) {
	suite = strings.TrimSpace(suite)
	if suite == "" {
		return "", false
	}
	command, baseArgs := c.getCypressCommand()
	cliArgs, err := cypressCLIArgs(command, baseArgs)
	if err != nil {
		return suite, true
	}
	projectRoot, err := cypressProjectRoot(cliArgs)
	if err != nil {
		return suite, true
	}
	cwd, err := os.Getwd()
	if err != nil || sameFilePath(projectRoot, cwd) {
		return utils.NormalizePath(suite), true
	}
	if resolvedCwd, resolveErr := filepath.EvalSymlinks(cwd); resolveErr == nil {
		cwd = resolvedCwd
	}
	relative, err := filepath.Rel(cwd, filepath.Join(projectRoot, filepath.FromSlash(suite)))
	if err != nil {
		return suite, true
	}
	return utils.NormalizePath(relative), true
}

func (c *Cypress) HasUnskippableMarker(testFile string) bool {
	return utils.FileContainsAll(testFile, "@datadog", "unskippable")
}

func (c *Cypress) TestPattern() string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return custom
	}
	return cypressDefaultE2EPattern
}

func (c *Cypress) DiscoverTests(context.Context, discovery.TestFileSet) ([]testoptimization.Test, error) {
	return nil, ErrFullTestDiscoveryUnsupported
}

func (c *Cypress) DiscoverTestFiles(ctx context.Context, selectedFiles discovery.TestFileSet) ([]string, error) {
	command, baseArgs := c.getCypressCommand()
	cliArgs, err := cypressCLIArgs(command, baseArgs)
	if err != nil {
		return nil, err
	}
	projectRoot, err := cypressProjectRoot(cliArgs)
	if err != nil {
		return nil, err
	}
	originalConfig, err := cypressConfigPath(projectRoot, cliArgs)
	if err != nil {
		return nil, err
	}
	discoveryConfigPath, err := prepareCypressDiscoveryConfig(projectRoot, originalConfig)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(discoveryConfigPath) }()

	args := cypressDiscoveryArgs(command, baseArgs, discoveryConfigPath)
	slog.Info("Discovering Cypress test files with command", "command", command, "args", args)
	output, err := c.executor.CombinedOutput(ctx, command, args, c.discoveryEnv())
	config, configErr := parseCypressDiscoveryOutput(output)
	if err != nil && configErr != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return nil, fmt.Errorf("failed to resolve Cypress configuration: %w", err)
		}
		return nil, fmt.Errorf("failed to resolve Cypress configuration: %s: %w", message, err)
	}
	if configErr != nil {
		return nil, configErr
	}
	if config.ProjectRoot == "" {
		config.ProjectRoot = projectRoot
	}
	if err := os.Remove(discoveryConfigPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed to remove temporary Cypress discovery config: %w", err)
	}
	discoveredFiles, err := discoverCypressSpecFiles(config)
	if err != nil {
		return nil, err
	}
	return filterCypressTestFiles(discoveredFiles, selectedFiles)
}

func (c *Cypress) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	if len(testFiles) == 0 {
		return nil
	}
	command, baseArgs := c.getCypressCommand()
	cliArgs, err := cypressCLIArgs(command, baseArgs)
	if err != nil {
		return err
	}
	projectTestFiles, err := cypressTestFilesRelativeToProject(cliArgs, testFiles)
	if err != nil {
		return err
	}
	args := cypressRunArgs(command, baseArgs, projectTestFiles)

	slog.Info("Running Cypress tests", "command", command, "args", args, "testFiles", testFiles)
	mergedEnv := make(map[string]string)
	maps.Copy(mergedEnv, c.platformEnv)
	maps.Copy(mergedEnv, envMap)
	return c.executor.Run(ctx, command, args, mergedEnv)
}

func (c *Cypress) discoveryEnv() map[string]string {
	envMap := make(map[string]string, len(c.platformEnv)+1)
	maps.Copy(envMap, c.platformEnv)
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

func (c *Cypress) getCypressCommand() (string, []string) {
	if len(c.commandOverride) > 0 {
		return c.commandOverride[0], c.commandOverride[1:]
	}
	if info, err := os.Stat(binCypressPath); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
		return binCypressPath, nil
	}
	return "npx", []string{"cypress"}
}

func cypressCLIArgs(command string, baseArgs []string) ([]string, error) {
	if isCypressExecutable(command) {
		return slices.Clone(baseArgs), nil
	}
	for i, arg := range baseArgs {
		if isCypressExecutable(arg) {
			return slices.Clone(baseArgs[i+1:]), nil
		}
	}
	return nil, fmt.Errorf("Cypress command must invoke Cypress directly: %s %s", command, strings.Join(baseArgs, " "))
}

func isCypressExecutable(value string) bool {
	base := filepath.Base(value)
	return base == "cypress" || base == "cypress.js"
}

func cypressDiscoveryArgs(command string, baseArgs []string, configPath string) []string {
	prefix, cliArgs := splitCypressCommand(command, baseArgs)
	args := append(prefix, "run")
	args = append(args, cypressConfigurationArgs(cliArgs)...)
	return append(args,
		"--config-file", configPath,
		"--spec", cypressMissingDiscoverySpec,
	)
}

func cypressRunArgs(command string, baseArgs, testFiles []string) []string {
	prefix, cliArgs := splitCypressCommand(command, baseArgs)
	args := append(prefix, "run")
	args = append(args, removeCypressOption(cliArgs, "--spec", "-s")...)
	return append(args, "--spec", strings.Join(testFiles, ","))
}

func cypressTestFilesRelativeToProject(cliArgs, testFiles []string) ([]string, error) {
	projectRoot, err := cypressProjectRoot(cliArgs)
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve working directory: %w", err)
	}
	if resolvedCwd, resolveErr := filepath.EvalSymlinks(cwd); resolveErr == nil {
		cwd = resolvedCwd
	}

	result := make([]string, 0, len(testFiles))
	for _, testFile := range testFiles {
		absolute := filepath.FromSlash(testFile)
		if !filepath.IsAbs(absolute) {
			absolute = filepath.Join(cwd, absolute)
		}
		if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
			absolute = resolved
		}
		relative, err := filepath.Rel(projectRoot, absolute)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve Cypress test file %q relative to project root: %w", testFile, err)
		}
		result = append(result, utils.NormalizePath(relative))
	}
	return result, nil
}

func splitCypressCommand(command string, baseArgs []string) ([]string, []string) {
	if isCypressExecutable(command) {
		return nil, removeCypressSubcommand(baseArgs)
	}
	for i, arg := range baseArgs {
		if isCypressExecutable(arg) {
			return slices.Clone(baseArgs[:i+1]), removeCypressSubcommand(baseArgs[i+1:])
		}
	}
	return slices.Clone(baseArgs), nil
}

func removeCypressSubcommand(args []string) []string {
	result := slices.Clone(args)
	for i, arg := range result {
		if arg == "run" || arg == "open" {
			return slices.Delete(result, i, i+1)
		}
	}
	return result
}

func cypressConfigurationArgs(cliArgs []string) []string {
	booleanOptions := map[string]bool{"--component": true, "--e2e": true}
	valueOptions := map[string]bool{
		"--config": true, "-c": true,
		"--env": true, "-e": true,
		"--project": true, "-P": true,
	}
	result := make([]string, 0, len(cliArgs))
	for i := 0; i < len(cliArgs); i++ {
		arg := cliArgs[i]
		if booleanOptions[arg] {
			result = append(result, arg)
			continue
		}
		if valueOptions[arg] && i+1 < len(cliArgs) {
			result = append(result, arg, cliArgs[i+1])
			i++
			continue
		}
		for option := range valueOptions {
			if strings.HasPrefix(arg, option+"=") {
				result = append(result, arg)
				break
			}
		}
	}
	return result
}

func removeCypressOption(args []string, options ...string) []string {
	result := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		matched := false
		for _, option := range options {
			if arg == option {
				matched = true
				if i+1 < len(args) {
					i++
				}
				break
			}
			if strings.HasPrefix(arg, option+"=") {
				matched = true
				break
			}
		}
		if !matched {
			result = append(result, arg)
		}
	}
	return result
}

func cypressProjectRoot(cliArgs []string) (string, error) {
	projectRoot := cypressOptionValue(cliArgs, "--project", "-P")
	if projectRoot == "" {
		projectRoot = "."
	}
	absolute, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("failed to resolve Cypress project root %q: %w", projectRoot, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		absolute = resolved
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("failed to access Cypress project root %q: %w", projectRoot, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("Cypress project root %q is not a directory", projectRoot)
	}
	return absolute, nil
}

func cypressConfigPath(projectRoot string, cliArgs []string) (string, error) {
	configured := cypressOptionValue(cliArgs, "--config-file", "-C")
	if configured == "false" {
		return "", nil
	}
	if configured != "" {
		if !filepath.IsAbs(configured) {
			configured = filepath.Join(projectRoot, configured)
		}
		absolute, err := filepath.Abs(configured)
		if err != nil {
			return "", fmt.Errorf("failed to resolve Cypress config file %q: %w", configured, err)
		}
		if _, err := os.Stat(absolute); err != nil {
			return "", fmt.Errorf("failed to access Cypress config file %q: %w", configured, err)
		}
		return absolute, nil
	}
	for _, filename := range cypressConfigFilenames {
		candidate := filepath.Join(projectRoot, filename)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", nil
}

func cypressOptionValue(args []string, options ...string) string {
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

func prepareCypressDiscoveryConfig(projectRoot, originalConfig string) (string, error) {
	configImport := "const originalConfig: Record<string, any> = {}"
	if originalConfig != "" {
		importPath, err := filepath.Rel(projectRoot, originalConfig)
		if err != nil {
			return "", fmt.Errorf("failed to resolve Cypress config import: %w", err)
		}
		importPath = "./" + filepath.ToSlash(importPath)
		encodedImportPath, err := json.Marshal(importPath)
		if err != nil {
			return "", fmt.Errorf("failed to encode Cypress config import: %w", err)
		}
		configImport = "import originalConfig from " + string(encodedImportPath)
	}

	configFile, err := os.CreateTemp(projectRoot, ".ddtest-cypress-config-*.ts")
	if err != nil {
		return "", fmt.Errorf("failed to create Cypress discovery config: %w", err)
	}
	configPath := configFile.Name()
	removeConfig := func() { _ = os.Remove(configPath) }
	script := strings.Replace(cypressDiscoveryConfigScript, cypressConfigImportToken, configImport, 1)
	encodedConfigPath, err := json.Marshal(configPath)
	if err != nil {
		_ = configFile.Close()
		removeConfig()
		return "", fmt.Errorf("failed to encode Cypress discovery config path: %w", err)
	}
	script = strings.Replace(script, cypressDiscoveryConfigPathToken, string(encodedConfigPath), 1)
	if _, err := configFile.WriteString(script); err != nil {
		_ = configFile.Close()
		removeConfig()
		return "", fmt.Errorf("failed to write Cypress discovery config: %w", err)
	}
	if err := configFile.Close(); err != nil {
		removeConfig()
		return "", fmt.Errorf("failed to close Cypress discovery config: %w", err)
	}
	return configPath, nil
}

func parseCypressDiscoveryOutput(output []byte) (cypressDiscoveryConfig, error) {
	markerIndex := strings.LastIndex(string(output), cypressDiscoveryMarker)
	if markerIndex < 0 {
		return cypressDiscoveryConfig{}, errors.New("Cypress discovery output did not contain resolved configuration")
	}
	encodedConfig := string(output[markerIndex+len(cypressDiscoveryMarker):])
	if lineEnd := strings.IndexByte(encodedConfig, '\n'); lineEnd >= 0 {
		encodedConfig = encodedConfig[:lineEnd]
	}
	var config cypressDiscoveryConfig
	if err := json.Unmarshal([]byte(encodedConfig), &config); err != nil {
		return cypressDiscoveryConfig{}, fmt.Errorf("failed to parse Cypress resolved configuration: %w", err)
	}
	if config.TestingType != "e2e" && config.TestingType != "component" {
		return cypressDiscoveryConfig{}, fmt.Errorf("Cypress returned unsupported testing type %q", config.TestingType)
	}
	return config, nil
}

func discoverCypressSpecFiles(config cypressDiscoveryConfig) ([]string, error) {
	projectRoot, err := filepath.Abs(config.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve Cypress project root %q: %w", config.ProjectRoot, err)
	}
	if resolvedRoot, resolveErr := filepath.EvalSymlinks(projectRoot); resolveErr == nil {
		projectRoot = resolvedRoot
	}
	cwd, _ := os.Getwd()
	if resolvedCwd, resolveErr := filepath.EvalSymlinks(cwd); resolveErr == nil {
		cwd = resolvedCwd
	}
	testFiles := make([]string, 0, len(config.SpecFiles))
	for _, specFile := range config.SpecFiles {
		filePath := filepath.FromSlash(specFile)
		if !filepath.IsAbs(filePath) {
			filePath = filepath.Join(projectRoot, filePath)
		}
		relativeToCwd, err := filepath.Rel(cwd, filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve Cypress spec file %q: %w", specFile, err)
		}
		testFiles = append(testFiles, utils.NormalizePath(relativeToCwd))
	}
	slices.Sort(testFiles)
	return slices.Compact(testFiles), nil
}

func filterCypressTestFiles(testFiles []string, selectedFiles discovery.TestFileSet) ([]string, error) {
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

func sameFilePath(left, right string) bool {
	leftResolved, leftErr := filepath.EvalSymlinks(left)
	rightResolved, rightErr := filepath.EvalSymlinks(right)
	if leftErr == nil {
		left = leftResolved
	}
	if rightErr == nil {
		right = rightResolved
	}
	return filepath.Clean(left) == filepath.Clean(right)
}
