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
	nodeOptionsEnvVar       = "NODE_OPTIONS"
	ddTraceCIInitModule     = "dd-trace/ci/init"
	nodeOptionsDDTraceCIArg = "-r " + ddTraceCIInitModule
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

// GetPlatformEnv returns environment variables required for JS commands.
func (j *JavaScript) GetPlatformEnv() map[string]string {
	// Check if the NODE_OPTIONS is set in the env, and if so,
	// check if it contains the dd-trace init option
	// (the minimum required to start the libary)
	currentValue, exists := os.LookupEnv(nodeOptionsEnvVar)
	if exists && strings.Contains(currentValue, ddTraceCIInitModule) {
		return map[string]string{}
	}

	// If the NODE_OPTIONS contained something, prepend the dd-trace
	// init option at the beggining
	nodeOptions := nodeOptionsDDTraceCIArg
	if strings.TrimSpace(currentValue) != "" {
		nodeOptions = nodeOptionsDDTraceCIArg + " " + currentValue
	}

	// If NODE_OPTIONS is not set, just set it to '-r dd-trace/ci/init'
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
