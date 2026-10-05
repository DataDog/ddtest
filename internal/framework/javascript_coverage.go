package framework

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/coverage"
	"github.com/DataDog/ddtest/internal/ext"
)

// runJavaScriptTests applies coverage coordination only to execution, never discovery.
func runJavaScriptTests(ctx context.Context, executor ext.CommandExecutor, framework, command string, args []string, env map[string]string) error {
	directory := env[coverage.WorkerDirectoryEnv]
	if directory == "" {
		return executor.Run(ctx, command, args, env)
	}
	report := filepath.Join(directory, "report")
	switch framework {
	case "jest":
		args = replaceCoverageOptions(command, args, framework, []string{"--coverage", "--collectCoverage"}, []string{"--coverageDirectory", "--coverage-directory", "--coverageReporters", "--coverage-reporters"})
		args = withFrameworkOptions(command, args, "jest", "--coverage", "--coverageDirectory="+report, "--coverageReporters=json")
	case "vitest":
		args = replaceCoverageOptions(command, args, framework, []string{"--coverage", "--coverage.enabled"}, []string{"--coverage.reportsDirectory", "--coverage.reporter"})
		args = withFrameworkOptions(command, args, "vitest", "--coverage.enabled=true", "--coverage.reportsDirectory="+report, "--coverage.reporter=json")
	case "cypress":
		nodeOptions, ok := env[nodeOptionsEnvVar]
		if !ok {
			nodeOptions = os.Getenv(nodeOptionsEnvVar)
		}
		// Preserve all preloads, but install the configuration hook before they can load Cypress.
		env[nodeOptionsEnvVar] = strings.TrimSpace(coverage.NodeRequire(env["DDTEST_COVERAGE_CYPRESS_HOOK"]) + " " + nodeOptions)
	case "mocha", "cucumber", "playwright":
		nyc := filepath.Join(filepath.Dir(env[coverage.NYCEnv]), "bin", "nyc.js")
		args = append([]string{nyc, "--temp-dir=" + filepath.Join(directory, "raw"), "--report-dir=" + report, "--reporter=json", command}, args...)
		command = "node"
	default:
		return fmt.Errorf("coverage output is not supported for %s", framework)
	}
	return executor.Run(ctx, command, args, env)
}

func replaceCoverageOptions(command string, args []string, framework string, switches, values []string) []string {
	end := len(args)
	if separator := frameworkSeparator(command, args, framework); separator >= 0 {
		end = separator
	}
	return append(removeCoverageOptions(args[:end], switches, values), args[end:]...)
}

// Replace owned options before the framework's positional separator. Never treat
// a positional path as an option; framework adapters preserve the user's --.
func removeCoverageOptions(args, switches, values []string) []string {
	result := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, _, assigned := strings.Cut(arg, "=")
		if slices.Contains(switches, key) {
			if !assigned && i+1 < len(args) && (args[i+1] == "true" || args[i+1] == "false") {
				i++
			}
			continue
		}
		if slices.Contains(values, key) {
			if !assigned && i+1 < len(args) {
				i++
			}
			continue
		}
		result = append(result, arg)
	}
	return result
}
