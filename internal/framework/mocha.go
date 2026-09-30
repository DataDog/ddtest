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
	mochaRequestEnvVar   = "DDTEST_MOCHA_REQUEST"
)

//go:embed scripts/mocha_adapter.js
var mochaAdapterScript string

type Mocha struct {
	executor        ext.CommandExecutor
	commandOverride []string
	platform        Platform
}

func NewMocha(p Platform) *Mocha {
	return &Mocha{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: loadCommandOverride(),
		platform:        p,
	}
}

func (m *Mocha) Platform() Platform { return m.platform }

func (m *Mocha) Name() string                    { return "mocha" }
func (m *Mocha) SupportsFullTestDiscovery() bool { return false }

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

	command, baseArgs := m.Command()
	cliArgs, err := mochaCLIArgs(command, baseArgs)
	if err != nil {
		return nil, err
	}
	requestBody := map[string]any{"mode": "discover", "cliArgs": cliArgs}
	if settings.GetTestsLocation() != "" {
		requestBody["spec"] = []string{testFiles.Pattern}
	}
	request, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to encode Mocha discovery request: %w", err)
	}
	adapterPath, adapterEnv, err := prepareMochaAdapter(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(adapterPath) }()
	adapterEnv, err = m.platform.DiscoveryEnv(ctx, FileDiscovery, RuntimeOptions{Framework: m.Name(), Env: adapterEnv, PreloadFiles: []string{adapterPath}})
	if err != nil {
		return nil, err
	}

	slog.Info("Discovering Mocha test files", "command", command, "args", baseArgs)
	output, err := m.executor.CombinedOutput(ctx, command, baseArgs, adapterEnv)
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
	return filterJavaScriptTestFiles(discoveredFiles, testFiles)
}

func (m *Mocha) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	command, baseArgs := m.Command()
	cliArgs, err := mochaCLIArgs(command, baseArgs)
	if err != nil {
		return err
	}
	request, err := json.Marshal(map[string]any{"mode": "run", "cliArgs": cliArgs, "files": testFiles})
	if err != nil {
		return fmt.Errorf("failed to encode Mocha run request: %w", err)
	}

	slog.Info("Running Mocha tests", "command", command, "args", baseArgs, "testFiles", testFiles)
	adapterPath, adapterEnv, err := prepareMochaAdapter(request)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(adapterPath) }()
	maps.Copy(adapterEnv, envMap)
	adapterEnv[mochaRequestEnvVar] = string(request)
	adapterEnv, err = m.platform.RunEnv(RuntimeOptions{Framework: m.Name(), Env: adapterEnv, PreloadFiles: []string{adapterPath}})
	if err != nil {
		return err
	}

	return m.executor.Run(ctx, command, baseArgs, adapterEnv)
}

func prepareMochaAdapter(request []byte) (string, map[string]string, error) {
	adapterFile, err := os.CreateTemp("", "ddtest-mocha-adapter-*.js")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create Mocha adapter: %w", err)
	}
	adapterPath := adapterFile.Name()
	removeAdapter := func() { _ = os.Remove(adapterPath) }
	if _, err := adapterFile.WriteString(mochaAdapterScript); err != nil {
		_ = adapterFile.Close()
		removeAdapter()
		return "", nil, fmt.Errorf("failed to write Mocha adapter: %w", err)
	}
	if err := adapterFile.Close(); err != nil {
		removeAdapter()
		return "", nil, fmt.Errorf("failed to close Mocha adapter: %w", err)
	}

	adapterEnv := map[string]string{mochaRequestEnvVar: string(request)}
	return adapterPath, adapterEnv, nil
}

func (m *Mocha) Command() (string, []string) {
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
	return normalizeJavaScriptTestFiles(paths), nil
}
