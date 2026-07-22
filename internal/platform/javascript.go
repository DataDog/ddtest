package platform

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
)

//go:embed scripts/javascript_env.js
var javascriptEnvScript string

const (
	nodeOptionsEnvVar             = "NODE_OPTIONS"
	ddTraceCIInitModule           = "dd-trace/ci/init"     // For Jest and Vitest.
	ddTraceRegisterModule         = "dd-trace/register.js" // For Vitest.
	nodeOptionsDDTraceCIArg       = "-r " + ddTraceCIInitModule
	nodeOptionsDDTraceRegisterArg = "--import " + ddTraceRegisterModule
)

type JavaScript struct {
	executor ext.CommandExecutor
}

func NewJavaScript() *JavaScript {
	return &JavaScript{
		executor: &ext.DefaultCommandExecutor{},
	}
}

func (j *JavaScript) Name() string {
	return "javascript"
}

func (j *JavaScript) TestSkippingLevel() settings.TestSkippingLevel {
	return settings.TestSkippingLevelSuite
}

// GetPlatformEnv returns environment variables required for JS commands.
func (j *JavaScript) GetPlatformEnv() map[string]string {
	// Jest needs CI initialization; Vitest additionally needs the module registration hook.
	// Add only missing Datadog preloads and preserve existing NODE_OPTIONS.
	currentValue, _ := os.LookupEnv(nodeOptionsEnvVar)
	requiredOptions := make([]string, 0, 2)
	if settings.GetFramework() == "vitest" && !strings.Contains(currentValue, ddTraceRegisterModule) {
		requiredOptions = append(requiredOptions, nodeOptionsDDTraceRegisterArg)
	}
	if !strings.Contains(currentValue, ddTraceCIInitModule) {
		requiredOptions = append(requiredOptions, nodeOptionsDDTraceCIArg)
	}
	if len(requiredOptions) == 0 {
		return map[string]string{}
	}

	// Keep user-provided options after the required Datadog preloads.
	if strings.TrimSpace(currentValue) != "" {
		requiredOptions = append(requiredOptions, currentValue)
	}
	nodeOptions := strings.Join(requiredOptions, " ")

	slog.Debug("Setting NODE_OPTIONS to auto-instrument with dd-trace-js", "nodeOptions", nodeOptions)
	return map[string]string{
		nodeOptionsEnvVar: nodeOptions,
	}
}

func (j *JavaScript) CreateTagsMap() (map[string]string, error) {
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
	if err := j.executor.Run(context.Background(), "node", []string{"-e", javascriptEnvScript, tempFile}, nil); err != nil {
		return nil, fmt.Errorf("failed to execute JavaScript script: %w", err)
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

func (j *JavaScript) DetectFramework() (framework.Framework, error) {
	frameworkName := settings.GetFramework()
	platformEnv := j.GetPlatformEnv()

	var fw framework.Framework
	switch frameworkName {
	case "jest":
		fw = framework.NewJest()
	case "vitest":
		fw = framework.NewVitest()
	default:
		return nil, fmt.Errorf("framework '%s' is not supported by platform 'javascript'", frameworkName)
	}

	fw.SetPlatformEnv(platformEnv)
	return fw, nil
}

// Confirm that Node.js is installed by running 'node --version'
// and confirm that the dd-trace package is resolvable
func (j *JavaScript) SanityCheck() error {
	if output, err := j.executor.CombinedOutput(context.Background(), "node", []string{"--version"}, nil); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return fmt.Errorf("node --version command failed: %w", err)
		}
		return fmt.Errorf("node --version command failed: %s", message)
	}

	output, err := j.executor.CombinedOutput(context.Background(), "node", []string{"-e", fmt.Sprintf("require.resolve(%q)", ddTraceCIInitModule)}, nil)
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return fmt.Errorf("failed to resolve %s: %w", ddTraceCIInitModule, err)
		}
		return fmt.Errorf("failed to resolve %s: %s", ddTraceCIInitModule, message)
	}

	return nil
}
