package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	binCucumberPath        = "node_modules/.bin/cucumber-js"
	cucumberPublishEnabled = "CUCUMBER_PUBLISH_ENABLED"
	cucumberRedactedValue  = "[REDACTED]"
)

var cucumberValueOptions = map[string]bool{
	"--config":            true,
	"-c":                  true,
	"--format":            true,
	"-f":                  true,
	"--format-options":    true,
	"--i18n-keywords":     true,
	"--import":            true,
	"-i":                  true,
	"--language":          true,
	"--loader":            true,
	"-l":                  true,
	"--name":              true,
	"-n":                  true,
	"--order":             true,
	"--parallel":          true,
	"--plugin":            true,
	"--plugin-options":    true,
	"--profile":           true,
	"-p":                  true,
	"--publish-token":     true,
	"--require":           true,
	"-r":                  true,
	"--require-module":    true,
	"--retry":             true,
	"--retry-tag-filter":  true,
	"--shard":             true,
	"--snippet-interface": true,
	"--snippet-syntax":    true,
	"--tags":              true,
	"-t":                  true,
	"--world-parameters":  true,
}

type Cucumber struct {
	executor        ext.CommandExecutor
	commandOverride []string
	platformEnv     map[string]string
}

type cucumberEnvelope struct {
	Pickle *struct {
		ID  string `json:"id"`
		URI string `json:"uri"`
	} `json:"pickle"`
	TestCase *struct {
		PickleID string `json:"pickleId"`
	} `json:"testCase"`
}

func NewCucumber() *Cucumber {
	return &Cucumber{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: loadCommandOverride(),
		platformEnv:     make(map[string]string),
	}
}

func (c *Cucumber) SetPlatformEnv(platformEnv map[string]string) { c.platformEnv = platformEnv }
func (c *Cucumber) GetPlatformEnv() map[string]string            { return c.platformEnv }
func (c *Cucumber) Name() string                                 { return "cucumber" }
func (c *Cucumber) SupportsFullTestDiscovery() bool              { return false }

func (c *Cucumber) SourceFileForSuite(suite string) (string, bool) {
	suite = utils.NormalizePath(strings.TrimSpace(suite))
	if suite == "" {
		return "", false
	}
	return suite, true
}

func (c *Cucumber) HasUnskippableMarker(testFile string) bool {
	return utils.FileContainsAll(testFile, "@datadog", "unskippable")
}

func (c *Cucumber) TestPattern() string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return custom
	}
	return filepath.ToSlash(filepath.Join("features", "**", "*.{feature,feature.md}"))
}

func (c *Cucumber) DiscoverTests(context.Context, discovery.TestFileSet) ([]testoptimization.Test, error) {
	return nil, ErrFullTestDiscoveryUnsupported
}

// DiscoverTestFiles asks Cucumber to build a dry-run test plan and writes its
// Messages stream to a temporary file. TestCase envelopes identify the pickles
// that survived profile, tag, name and path filtering; their Pickle envelopes
// carry the feature file URI.
func (c *Cucumber) DiscoverTestFiles(ctx context.Context, selectedFiles discovery.TestFileSet) ([]string, error) {
	command, baseArgs := c.getCucumberCommand()
	if _, err := cucumberCLIArgs(command, baseArgs); err != nil {
		return nil, err
	}

	messageFile, err := os.CreateTemp(".", ".ddtest-cucumber-discovery-*.ndjson")
	if err != nil {
		return nil, fmt.Errorf("failed to create Cucumber discovery output: %w", err)
	}
	messagePath := messageFile.Name()
	if err := messageFile.Close(); err != nil {
		_ = os.Remove(messagePath)
		return nil, fmt.Errorf("failed to close Cucumber discovery output: %w", err)
	}
	defer func() { _ = os.Remove(messagePath) }()

	args := slices.Clone(baseArgs)
	args = append(args,
		"--dry-run",
		"--parallel", "0",
		"--format", "message:"+filepath.Base(messagePath),
	)
	slog.Info("Discovering Cucumber test files with command", "command", command, "args", redactCucumberArgs(args))
	output, err := c.executor.CombinedOutput(ctx, command, args, c.discoveryEnv())
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return nil, fmt.Errorf("failed to discover Cucumber test files: %w", err)
		}
		return nil, fmt.Errorf("failed to discover Cucumber test files: %s: %w", message, err)
	}

	discoveredFiles, err := parseCucumberMessages(messagePath)
	if err != nil {
		return nil, err
	}
	if settings.GetTestsLocation() == "" && settings.GetTestsExcludePattern() == "" {
		return discoveredFiles, nil
	}
	return filterJavaScriptTestFiles(discoveredFiles, selectedFiles)
}

func (c *Cucumber) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	if len(testFiles) == 0 {
		return nil
	}
	command, baseArgs := c.getCucumberCommand()
	cliArgs, err := cucumberCLIArgs(command, baseArgs)
	if err != nil {
		return err
	}
	args := cucumberArgsWithoutPaths(baseArgs, cliArgs)
	args = append(args, testFiles...)

	slog.Info("Running Cucumber tests with command", "command", command, "args", redactCucumberArgs(args))
	mergedEnv := make(map[string]string)
	maps.Copy(mergedEnv, c.platformEnv)
	maps.Copy(mergedEnv, envMap)
	return c.executor.Run(ctx, command, args, mergedEnv)
}

func (c *Cucumber) discoveryEnv() map[string]string {
	envMap := make(map[string]string, len(c.platformEnv)+2)
	maps.Copy(envMap, c.platformEnv)
	nodeOptions, ok := envMap[nodeOptionsEnvVar]
	if !ok {
		nodeOptions, _ = os.LookupEnv(nodeOptionsEnvVar)
	}
	envMap[nodeOptionsEnvVar] = stripNodeOptionsRequire(nodeOptions, ddTraceCIInitModule)
	envMap[cucumberPublishEnabled] = "false"
	return envMap
}

func (c *Cucumber) getCucumberCommand() (string, []string) {
	if len(c.commandOverride) > 0 {
		return c.commandOverride[0], c.commandOverride[1:]
	}
	if info, err := os.Stat(binCucumberPath); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
		return binCucumberPath, nil
	}
	return "npx", []string{"cucumber-js"}
}

func cucumberCLIArgs(command string, baseArgs []string) ([]string, error) {
	if isCucumberExecutable(command) {
		return slices.Clone(baseArgs), nil
	}
	for i, arg := range baseArgs {
		if isCucumberExecutable(arg) {
			return slices.Clone(baseArgs[i+1:]), nil
		}
	}
	return nil, fmt.Errorf("Cucumber command must invoke cucumber-js directly: %s %s", command, strings.Join(baseArgs, " "))
}

func isCucumberExecutable(value string) bool {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(value, `\`, "/")))
	return base == "cucumber-js" || base == "cucumber-js.js" ||
		base == "cucumber-js.cmd" || base == "cucumber-js.ps1"
}

func cucumberArgsWithoutPaths(baseArgs, cliArgs []string) []string {
	prefixLength := len(baseArgs) - len(cliArgs)
	args := slices.Clone(baseArgs[:prefixLength])
	afterSeparator := false
	for i := 0; i < len(cliArgs); i++ {
		arg := cliArgs[i]
		if afterSeparator {
			continue
		}
		if arg == "--" {
			afterSeparator = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			args = append(args, arg)
			if !strings.Contains(arg, "=") && cucumberValueOptions[arg] && i+1 < len(cliArgs) {
				i++
				args = append(args, cliArgs[i])
			}
		}
	}
	return args
}

func redactCucumberArgs(args []string) []string {
	redacted := slices.Clone(args)
	for i, arg := range redacted {
		switch {
		case arg == "--publish-token" && i+1 < len(redacted):
			redacted[i+1] = cucumberRedactedValue
		case strings.HasPrefix(arg, "--publish-token="):
			redacted[i] = "--publish-token=" + cucumberRedactedValue
		}
	}
	return redacted
}

func parseCucumberMessages(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read Cucumber discovery output: %w", err)
	}
	defer func() { _ = file.Close() }()

	pickleURIs := make(map[string]string)
	selectedPickles := make(map[string]struct{})
	decoder := json.NewDecoder(file)
	for {
		var envelope cucumberEnvelope
		if err := decoder.Decode(&envelope); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to parse Cucumber discovery output: %w", err)
		}
		if envelope.Pickle != nil && envelope.Pickle.ID != "" && envelope.Pickle.URI != "" {
			pickleURIs[envelope.Pickle.ID] = envelope.Pickle.URI
		}
		if envelope.TestCase != nil && envelope.TestCase.PickleID != "" {
			selectedPickles[envelope.TestCase.PickleID] = struct{}{}
		}
	}
	files := make([]string, 0, len(selectedPickles))
	for pickleID := range selectedPickles {
		files = append(files, pickleURIs[pickleID])
	}
	return normalizeJavaScriptTestFiles(files), nil
}
