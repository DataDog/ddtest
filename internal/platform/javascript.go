package platform

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

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/kballard/go-shellquote"
)

//go:embed scripts/javascript_env.js
var javascriptEnvScript string

const (
	nodeOptionsEnvVar       = "NODE_OPTIONS"
	ddTraceCIInitModule     = "dd-trace/ci/init"     // For Jest and Vitest.
	ddTraceRegisterModule   = "dd-trace/register.js" // For Vitest.
	nodeOptionsDDTraceCIArg = "-r " + ddTraceCIInitModule
	nodeImportArg           = "--import" // For Vitest.
)

type JavaScript struct {
	executor commandExecutor
}

func NewJavaScript() *JavaScript {
	return &JavaScript{
		executor: &ext.DefaultCommandExecutor{},
	}
}

func (j *JavaScript) Name() string {
	return "javascript"
}

func (j *JavaScript) Detect(repositoryRoot string) (bool, error) {
	return detectAnyFile(repositoryRoot, "package.json")
}

func (j *JavaScript) DetectFramework() (framework.Framework, error) {
	root := "."
	hint := settings.GetFramework()
	candidates := []framework.Framework{framework.NewJest(), framework.NewMocha(), framework.NewCypress(), framework.NewPlaywright(), framework.NewCucumber(), framework.NewVitest()}
	if hint == "" {
		manifest, found, err := readPackageManifest(root)
		if err != nil {
			return nil, err
		}
		if !found {
			candidates = nil
		} else {
			candidates = slices.DeleteFunc(candidates, func(f framework.Framework) bool {
				names := []string{f.Name()}
				switch f.Name() {
				case "playwright":
					names = append(names, "@playwright/test")
				case "cucumber":
					names = append(names, "@cucumber/cucumber", "cucumber-js")
				}
				return !manifest.usesAny(names...)
			})
		}
	}
	fw, err := selectFramework(j.Name(), hint, candidates)
	if err != nil {
		return nil, err
	}
	env := j.GetPlatformEnv()
	if fw.Name() == "vitest" {
		env = addNodeImport(env, ddTraceRegisterModule)
	}
	fw.SetPlatformEnv(env)
	return fw, nil
}

func (j *JavaScript) TestSkippingLevel() settings.TestSkippingLevel {
	return settings.TestSkippingLevelSuite
}

// GetPlatformEnv returns environment variables required for JS commands.
func (j *JavaScript) GetPlatformEnv() map[string]string {
	// Jest and Vitest need CI initialization.
	// Add the preload only when missing and preserve existing NODE_OPTIONS.
	currentValue, _ := os.LookupEnv(nodeOptionsEnvVar)
	if strings.Contains(currentValue, ddTraceCIInitModule) {
		return map[string]string{}
	}

	// Keep user-provided options after the required Datadog preloads.
	nodeOptions := nodeOptionsDDTraceCIArg
	if strings.TrimSpace(currentValue) != "" {
		nodeOptions += " " + currentValue
	}

	slog.Debug("Setting NODE_OPTIONS to auto-instrument with dd-trace-js", "nodeOptions", nodeOptions)
	return map[string]string{
		nodeOptionsEnvVar: nodeOptions,
	}
}

func addNodeImport(platformEnv map[string]string, module string) map[string]string {
	nodeOptions, ok := platformEnv[nodeOptionsEnvVar]
	if !ok {
		nodeOptions, _ = os.LookupEnv(nodeOptionsEnvVar)
	}
	if strings.Contains(nodeOptions, module) {
		return platformEnv
	}

	importOption := nodeImportArg + " " + module
	if strings.TrimSpace(nodeOptions) != "" {
		importOption += " " + nodeOptions
	}
	platformEnv[nodeOptionsEnvVar] = importOption
	return platformEnv
}

func (j *JavaScript) CreateTagsMap(ctx context.Context) (map[string]string, error) {
	tags := make(map[string]string)
	tags["language"] = j.Name()

	// Create plan directory if it doesn't exist
	if err := os.MkdirAll(constants.PlanDirectory, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plan directory: %w", err)
	}

	// Create a temporary file for the JavaScript script output
	tempFile := constants.JavaScriptEnvOutputPath
	defer func() { _ = os.Remove(tempFile) }()

	// Execute the embedded JavaScript script to get runtime tags
	if output, err := j.executor.CombinedOutput(ctx, "node", []string{"-e", javascriptEnvScript, tempFile}, nil); err != nil {
		return nil, runtimeTagProbeError("failed to execute JavaScript script", output, err)
	}

	// Read the JSON output from the temp file
	fileContent, err := os.ReadFile(tempFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read JavaScript script output file: %w", err)
	}

	// Parse the JSON output.
	// The extracted tags from the node process are:
	// "os.platform", "os.architecture", "os.version", "runtime.name" & "runtime.version"
	var javascriptTags map[string]string
	if err := json.Unmarshal(fileContent, &javascriptTags); err != nil {
		return nil, fmt.Errorf("failed to parse runtime tags JSON: %w, tried to parse: %s", err, string(fileContent))
	}

	// Merge the tags from the JavaScript output
	maps.Copy(tags, javascriptTags)

	return tags, nil
}

// Confirm that Node.js is installed by running 'node --version'
// and confirm that the dd-trace package is resolvable
func (j *JavaScript) SanityCheck(ctx context.Context) error {
	if output, err := j.executor.CombinedOutput(ctx, "node", []string{"--version"}, nil); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return fmt.Errorf("node --version command failed: %w", err)
		}
		return fmt.Errorf("node --version command failed: %s", message)
	}

	path, err := j.DetectTracer(ctx, TracerOptions{})
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("dd-trace is not installed")
	}
	return nil
}

type packageManifest struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func readPackageManifest(repositoryRoot string) (packageManifest, bool, error) {
	path := filepath.Join(repositoryRoot, "package.json")
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return packageManifest{}, false, nil
	}
	if err != nil {
		return packageManifest{}, false, fmt.Errorf("read %s: %w", path, err)
	}

	var manifest packageManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return packageManifest{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return manifest, true, nil
}

func (m packageManifest) usesAny(names ...string) bool {
	for _, name := range names {
		if _, ok := m.Dependencies[name]; ok {
			return true
		}
		if _, ok := m.DevDependencies[name]; ok {
			return true
		}
	}

	for _, script := range m.Scripts {
		if isDirectJavaScriptCommand(script, names...) {
			return true
		}
	}
	return false
}

// isDirectJavaScriptCommand deliberately accepts only a single direct runner
// invocation. For shell expressions and wrappers, detection relies on declared
// dependencies rather than guessing which words represent an executable.
func isDirectJavaScriptCommand(script string, names ...string) bool {
	if strings.ContainsAny(script, "\r\n;&|<>`$()#") {
		return false
	}
	args, err := shellquote.Split(script)
	if err != nil || len(args) == 0 || slices.Contains(args, "--") {
		return false
	}
	command := strings.TrimPrefix(args[0], "./")
	command = strings.TrimPrefix(command, "node_modules/.bin/")
	for _, name := range names {
		if command == name {
			return true
		}
	}
	return false
}

// DetectTracer resolves the project's CI preload using Node's module resolution.
func (j *JavaScript) DetectTracer(ctx context.Context, _ TracerOptions) (string, error) {
	path, err := tracerProbe(ctx, j.executor, "node", []string{"-e", resolveJavaScriptModule, ddTraceCIInitModule}, map[string]string{"NODE_OPTIONS": ""})
	if err != nil {
		return "", fmt.Errorf("failed to resolve %s: %w", ddTraceCIInitModule, err)
	}
	return path, nil
}

const resolveJavaScriptModule = "process.stdout.write(require.resolve(process.argv[1]))"

// InstallTracer reuses the project preload or installs an isolated fallback.
func (j *JavaScript) InstallTracer(ctx context.Context, options TracerOptions) (TracerInstallation, error) {
	sessionDirectory := options.Directory
	version := options.Version
	if version == "" {
		version = "latest"
	}
	if ref, ok := strings.CutPrefix(version, "git:"); ok {
		if ref == "" {
			return TracerInstallation{}, fmt.Errorf("tracer git ref must not be empty")
		}
		version = "git+https://github.com/DataDog/dd-trace-js.git#" + ref
	}
	cleanEnvironment := map[string]string{"NODE_OPTIONS": "", "NPM_CONFIG_GLOBAL": "false", "npm_config_global": "false"}
	path, err := j.DetectTracer(ctx, options)
	if err == nil && path != "" {
		return TracerInstallation{Path: path, Project: true}, nil
	}
	packageName := "dd-trace@" + version
	installArgs := []string{
		"install",
		"--prefix", sessionDirectory,
		"--global=false",
		"--no-save",
		"--package-lock=false",
		"--no-audit",
		"--no-fund",
		packageName,
	}
	if output, err := j.executor.CombinedOutput(ctx, "npm", installArgs, cleanEnvironment); err != nil {
		return TracerInstallation{}, runtimeTagProbeError("install "+packageName, output, err)
	}

	ciInitModule := filepath.Join(sessionDirectory, "node_modules", "dd-trace", "ci", "init")
	output, stderr, err := j.executor.Output(ctx, "node", []string{"-e", resolveJavaScriptModule, ciInitModule}, cleanEnvironment)
	if err != nil {
		return TracerInstallation{}, runtimeTagProbeError("resolve dd-trace/ci/init", stderr, err)
	}

	ciInitPath := strings.TrimSpace(string(output))
	if !filepath.IsAbs(ciInitPath) {
		return TracerInstallation{}, fmt.Errorf("resolve dd-trace/ci/init: node returned non-absolute path %q", ciInitPath)
	}
	return TracerInstallation{Path: ciInitPath}, nil
}
