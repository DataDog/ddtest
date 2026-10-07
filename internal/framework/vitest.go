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

const ddTraceRegisterPath = "dd-trace/register.js"

//go:embed scripts/vitest.mjs
var vitestScript string

//go:embed scripts/vitest_modern.mjs
var vitestModernScript string

//go:embed scripts/vitest_legacy.mjs
var vitestLegacyScript string

var vitestTestFileExtensions = []string{"js", "jsx", "ts", "tsx", "mjs", "mts", "cjs", "cts"}

type Vitest struct {
	executor      ext.CommandExecutor
	configFile    string
	customCommand string
	platformEnv   map[string]string
}

func NewVitest() *Vitest {
	return &Vitest{
		executor:      &ext.DefaultCommandExecutor{},
		configFile:    settings.GetVitestConfig(),
		customCommand: settings.GetCommand(),
		platformEnv:   make(map[string]string),
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
	return filepath.ToSlash(filepath.Join("**", "*.{test,spec}."+utils.JoinGlobPatterns(vitestTestFileExtensions)))
}

func (v *Vitest) DiscoverTests(ctx context.Context, testFiles discovery.TestFileSet) ([]testoptimization.Test, error) {
	return nil, ErrFullTestDiscoveryUnsupported
}

// DiscoverTestFiles uses the same config-aware Node adapter as execution.
// A discovery failure is returned to the caller, never replaced with a broader glob.
func (v *Vitest) DiscoverTestFiles(ctx context.Context, testFiles discovery.TestFileSet) ([]string, error) {
	if err := v.validateCommand(); err != nil {
		return nil, err
	}
	// With an exclude pattern, explicit files are generic glob candidates;
	// Vitest's configuration must remain authoritative before excluding files.
	if settings.GetTestsExcludePattern() == "" {
		if testFiles.Empty() {
			return []string{}, nil
		}
		if testFiles.UseExplicitFiles() {
			return slices.Clone(testFiles.ExplicitFiles), nil
		}
	}

	dir, err := prepareVitestAdapter(vitestRequest{Config: v.configFile, Discover: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	output, err := v.executor.CombinedOutput(ctx, "node", []string{filepath.Join(dir, "vitest.mjs")}, v.discoveryEnv())
	if err != nil {
		return nil, fmt.Errorf("failed to discover Vitest test files: %s: %w", strings.TrimSpace(string(output)), err)
	}
	if message := strings.TrimSpace(string(output)); message != "" {
		slog.Debug("Vitest discovery output", "output", message)
	}
	// Keep the result separate from configuration/plugin logs on stdout.
	contents, err := os.ReadFile(filepath.Join(dir, "files.json"))
	if err != nil {
		return nil, fmt.Errorf("failed to read Vitest test file list: %w", err)
	}
	var files []string
	if err := json.Unmarshal(contents, &files); err != nil {
		return nil, fmt.Errorf("failed to parse Vitest test file list: %w", err)
	}
	files = normalizeJavaScriptTestFiles(files)
	if settings.GetTestsLocation() == "" && settings.GetTestsExcludePattern() == "" {
		return files, nil
	}
	return filterJavaScriptTestFiles(files, testFiles)
}

func (v *Vitest) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	if err := v.validateCommand(); err != nil {
		return err
	}
	if len(testFiles) == 0 {
		return nil
	}
	dir, err := prepareVitestAdapter(vitestRequest{Config: v.configFile, Files: testFiles})
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	env := make(map[string]string)
	maps.Copy(env, v.platformEnv)
	maps.Copy(env, envMap)
	slog.Info("Running assigned Vitest files with Node API", "config", v.configFile, "files", testFiles)
	return v.executor.Run(ctx, "node", []string{filepath.Join(dir, "vitest.mjs")}, env)
}

func (v *Vitest) validateCommand() error {
	if strings.TrimSpace(v.customCommand) != "" {
		return fmt.Errorf("Vitest uses DDTest's Node API adapter and does not support --command; move options to vitest.config.ts or select a config with --vitest-config (see docs/running.md#vitest-integration)")
	}
	return nil
}

// Command identifies the adapter's runtime. RunTests supplies its temporary script.
func (v *Vitest) Command() (string, []string) {
	return "node", nil
}

type vitestRequest struct {
	Config   string   `json:"config,omitempty"`
	Discover bool     `json:"discover,omitempty"`
	Files    []string `json:"files,omitempty"`
}

// prepareVitestAdapter keeps the entrypoint, version adapters, and request
// together. The caller owns the directory and removes it after Node exits.
func prepareVitestAdapter(request vitestRequest) (string, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to encode Vitest request: %w", err)
	}
	dir, err := os.MkdirTemp("", "ddtest-vitest-*")
	if err != nil {
		return "", fmt.Errorf("failed to create Vitest adapter directory: %w", err)
	}
	for name, contents := range map[string]string{
		"vitest.mjs":        vitestScript,
		"vitest_modern.mjs": vitestModernScript,
		"vitest_legacy.mjs": vitestLegacyScript,
		"request.json":      string(encoded),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("failed to write Vitest adapter file %q: %w", name, err)
		}
	}
	return dir, nil
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

func stripNodeOptionsImport(nodeOptions string, module string) string {
	return utils.NodeOptionsWithoutImport(nodeOptions, module)
}
