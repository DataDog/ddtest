package framework

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	CloudGrid []string `json:"cloud_grid,omitempty"`
	Node      string   `json:"node"`
	Version   string   `json:"version"`
	Runner    string   `json:"runner"`
	Framework string   `json:"framework"`
	// JasmineCore is the jasmine-core version the Jasmine adapter loads.
	JasmineCore  string              `json:"jasmine_core,omitempty"`
	Files        []string            `json:"files,omitempty"`
	FileCount    int                 `json:"file_count,omitempty"`
	ServerPorts  []int               `json:"static_server_ports,omitempty"`
	Capabilities []map[string]string `json:"capabilities"`
	// GroupedSpecs reports specs such as [['setup.js', 'case.js']], which run in one worker.
	GroupedSpecs bool `json:"grouped_specs,omitempty"`
	// Multiremote reports object-form capabilities, which share the global specs.
	Multiremote bool `json:"multiremote,omitempty"`
}

// ErrWebdriverIOGroupedSpecs rejects planning that would split a grouped spec.
var ErrWebdriverIOGroupedSpecs = errors.New("WebdriverIO grouped specs run their files together in one worker; ddtest cannot distribute them separately. Flatten the groups or run this suite without ddtest planning")

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
	config := "wdio.conf.js"
	for _, extension := range []string{"js", "ts", "mjs", "cjs", "mts", "cts"} {
		candidate := "wdio.conf." + extension
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			config = candidate
			break
		}
	}
	return filepath.Join("node_modules", ".bin", "wdio"), []string{"run", config}
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
	env["NODE_OPTIONS"] += " --import " + strconv.Quote(utils.FileURL(observer))
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
	if config.GroupedSpecs {
		return nil, ErrWebdriverIOGroupedSpecs
	}
	return filterJavaScriptTestFiles(normalizeJavaScriptTestFiles(config.Files), selected)
}
func (w *WebdriverIO) RunTests(ctx context.Context, files []string, env map[string]string) error {
	command, args := w.Command()
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
	// Inherited options that already load dd-trace need no override; keep them.
	if _, found := options["NODE_OPTIONS"]; !found {
		options["NODE_OPTIONS"] = os.Getenv("NODE_OPTIONS")
	}
	options["NODE_OPTIONS"] += " --import " + strconv.Quote(utils.FileURL(observer))
	absolute := make([]string, len(files))
	for i, file := range files {
		absolute[i], err = filepath.Abs(file)
		if err != nil {
			return err
		}
	}
	selected, _ := json.Marshal(absolute)
	options["DDTEST_WDIO_FILES"] = string(selected)

	return w.executor.Run(ctx, command, args, options)
}
