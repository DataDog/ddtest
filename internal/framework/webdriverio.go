package framework

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/utils"
)

//go:embed scripts/webdriverio_observer.mjs
var webdriverIOObserver string

//go:embed scripts/webdriverio_reporter.mjs
var webdriverIOReporter string

// WebdriverIOConfig is the effective runner selection, without credentials or service options.
type WebdriverIOConfig struct {
	Version      string              `json:"version"`
	Runner       string              `json:"runner"`
	Framework    string              `json:"framework"`
	Files        []string            `json:"files,omitempty"`
	FileCount    int                 `json:"file_count,omitempty"`
	ServerPorts  []int               `json:"static_server_ports,omitempty"`
	Capabilities []map[string]string `json:"capabilities"`
}

type WebdriverIO struct {
	executor        ext.CommandExecutor
	platform        PlatformEnvironment
	commandOverride []string
}

func NewWebdriverIO(p PlatformEnvironment) *WebdriverIO {
	return &WebdriverIO{executor: &ext.DefaultCommandExecutor{}, platform: p, commandOverride: loadCommandOverride()}
}
func (w *WebdriverIO) Name() string                    { return "webdriverio" }
func (w *WebdriverIO) Platform() PlatformEnvironment   { return w.platform }
func (w *WebdriverIO) SupportsFullTestDiscovery() bool { return false }
func (w *WebdriverIO) HasUnskippableMarker(file string) bool {
	return utils.FileContainsAll(file, "@datadog", "unskippable")
}
func (w *WebdriverIO) SourceFileForSuite(suite string) (string, bool) {
	return suite, strings.TrimSpace(suite) != ""
}
func (w *WebdriverIO) TestPattern() string {
	if value := settings.GetTestsLocation(); value != "" {
		return value
	}
	return "**/*.{spec,e2e}.{js,cjs,mjs,ts}"
}
func (w *WebdriverIO) Command() (string, []string) {
	if len(w.commandOverride) > 0 {
		return w.commandOverride[0], w.commandOverride[1:]
	}
	return filepath.Join("node_modules", ".bin", "wdio"), []string{"run", "wdio.conf.js"}
}
func (w *WebdriverIO) DiscoverTests(context.Context, discovery.TestFileSet) ([]testoptimization.Test, error) {
	return nil, ErrFullTestDiscoveryUnsupported
}

// PrepareWebdriverIOObserver adds a native reporter through the public config API.
// The same observer is used for file discovery and baseline/instrumented validation.
func PrepareWebdriverIOObserver(directory string) (string, error) {
	for name, source := range map[string]string{"webdriverio_observer.mjs": webdriverIOObserver, "webdriverio_reporter.mjs": webdriverIOReporter} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(source), 0600); err != nil {
			return "", err
		}
	}
	return filepath.Join(directory, "webdriverio_observer.mjs"), nil
}
func (w *WebdriverIO) DiscoverTestFiles(ctx context.Context, selected discovery.TestFileSet) ([]string, error) {
	directory, err := os.MkdirTemp("", "ddtest-wdio-discovery-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	observer, err := PrepareWebdriverIOObserver(directory)
	if err != nil {
		return nil, err
	}
	env, err := w.platform.DiscoveryEnv(ctx, FileDiscovery, RuntimeOptions{})
	if err != nil {
		return nil, err
	}
	if env == nil {
		env = map[string]string{}
	}
	env["NODE_OPTIONS"] += " --import " + strconv.Quote(observer)
	env["DDTEST_WDIO_INSPECT"] = filepath.Join(directory, "config.json")
	command, args := w.Command()
	data, err := w.executor.CombinedOutput(ctx, command, args, env)
	if err != nil {
		return nil, fmt.Errorf("inspect WebdriverIO: %w: %s", err, strings.TrimSpace(string(data)))
	}
	body, err := os.ReadFile(env["DDTEST_WDIO_INSPECT"])
	if err != nil {
		return nil, err
	}
	var config WebdriverIOConfig
	if err = json.Unmarshal(body, &config); err != nil {
		return nil, err
	}
	return filterJavaScriptTestFiles(normalizeJavaScriptTestFiles(config.Files), selected)
}
func (w *WebdriverIO) RunTests(ctx context.Context, files []string, env map[string]string) error {
	command, args := w.Command()
	args = slices.Clone(args)
	if len(files) == 0 {
		return nil
	}
	directory, err := os.MkdirTemp("", "ddtest-wdio-run-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	observer, err := PrepareWebdriverIOObserver(directory)
	if err != nil {
		return err
	}
	options, err := w.platform.RunEnv(RuntimeOptions{Env: env, ESM: true})
	if err != nil {
		return err
	}
	options["NODE_OPTIONS"] += " --import " + strconv.Quote(observer)
	selected, _ := json.Marshal(files)
	options["DDTEST_WDIO_FILES"] = string(selected)

	return w.executor.Run(ctx, command, args, options)
}
