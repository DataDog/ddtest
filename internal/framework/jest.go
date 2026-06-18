package framework

import (
	"context"
	"errors"
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
)

const binJestPath = "node_modules/.bin/jest"

var ErrFullTestDiscoveryUnsupported = errors.New("full test discovery is not supported")

var jestTestFileExtensions = []string{"js", "jsx", "ts", "tsx", "mjs", "cjs"}

type Jest struct {
	executor        ext.CommandExecutor
	commandOverride []string
	platformEnv     map[string]string
}

func NewJest() *Jest {
	return &Jest{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: loadCommandOverride(),
		platformEnv:     make(map[string]string),
	}
}

func (j *Jest) SetPlatformEnv(platformEnv map[string]string) {
	j.platformEnv = platformEnv
}

func (j *Jest) GetPlatformEnv() map[string]string {
	return j.platformEnv
}

func (j *Jest) Name() string {
	return "jest"
}

// We will not be discovering tests, but test suites.
// We'll be working outside of the Node.js process
func (j *Jest) SupportsFullTestDiscovery() bool {
	return false
}

func (j *Jest) TestPattern() string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return custom
	}
	return "{" +
		filepath.ToSlash(filepath.Join("**", "__tests__", "**", "*."+jestTestFileExtensionPattern())) + "," +
		filepath.ToSlash(filepath.Join("**", "*.{spec,test}."+jestTestFileExtensionPattern())) +
		"}"
}

func (j *Jest) TestExcludePattern() string {
	return ""
}

func (j *Jest) DiscoverTests(ctx context.Context, testFiles discovery.TestFileSet) ([]testoptimization.Test, error) {
	return nil, ErrFullTestDiscoveryUnsupported
}

func (j *Jest) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	command, baseArgs := j.getJestCommand()
	args := slices.Clone(baseArgs)
	args = append(args, "--runTestsByPath")
	args = append(args, testFiles...)

	slog.Info("Running tests with command", "command", command, "args", args)

	mergedEnv := make(map[string]string)
	maps.Copy(mergedEnv, j.platformEnv)
	maps.Copy(mergedEnv, envMap)
	return j.executor.Run(ctx, command, args, mergedEnv)
}

// Decide between user custom command, local jest binary and npx jest
func (j *Jest) getJestCommand() (string, []string) {
	if len(j.commandOverride) > 0 {
		return j.commandOverride[0], j.commandOverride[1:]
	}

	if info, err := os.Stat(binJestPath); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
		slog.Debug("Using local Jest binary")
		return binJestPath, []string{}
	}

	slog.Debug("Using npx jest for Jest commands")
	return "npx", []string{"jest"}
}

func jestTestFileExtensionPattern() string {
	return "{" + strings.Join(jestTestFileExtensions, ",") + "}"
}
