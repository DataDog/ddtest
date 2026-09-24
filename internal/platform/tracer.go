package platform

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/ext"
)

// TracerExecutor keeps diagnostic stderr out of tracer paths and versions.
type TracerExecutor interface {
	Output(context.Context, string, []string, map[string]string) ([]byte, []byte, error)
}

type commandExecutor interface {
	ext.CommandExecutor
	TracerExecutor
}

// Resolve the package first so a present but incompatible tracer fails instead
// of silently installing another version. Resolve from the test project's cwd.
const resolveProjectJavaScriptTracer = `
let tracer;
try { tracer = require.resolve('dd-trace/package.json', { paths: [process.cwd()] }); }
catch (error) { if (error.code !== 'MODULE_NOT_FOUND') throw error; }
if (tracer) process.stdout.write(require.resolve(require('path').join(require('path').dirname(tracer), 'ci/init')));
`

// DetectJavaScriptTracer returns the project preload, or empty when dd-trace is absent.
func DetectJavaScriptTracer(ctx context.Context, executor TracerExecutor) (string, error) {
	path, err := tracerProbe(ctx, executor, "node", []string{"-e", resolveProjectJavaScriptTracer}, map[string]string{"NODE_OPTIONS": ""})
	if err != nil {
		return "", fmt.Errorf("failed to resolve dd-trace/ci/init: %w", err)
	}
	if path != "" && !filepath.IsAbs(path) {
		return "", fmt.Errorf("resolve project dd-trace: node returned non-absolute path %q", path)
	}
	return path, nil
}

// DetectPythonTracer returns the installed version using the test runner's interpreter.
// Only PackageNotFoundError means absence; interpreter failures remain errors.
func DetectPythonTracer(ctx context.Context, executor TracerExecutor, command string, prefix []string) (string, error) {
	args := append(append([]string{}, prefix...), "-c", `import importlib.metadata, sys
try: print(importlib.metadata.version(sys.argv[1]))
except importlib.metadata.PackageNotFoundError: pass
`, "ddtrace")
	return tracerProbe(ctx, executor, command, args, nil)
}

// DetectRubyTracer checks Bundler's declarations and installed specs without
// installing dependencies. A declared but unavailable tracer is an error, not absence.
func DetectRubyTracer(ctx context.Context, executor TracerExecutor, env map[string]string) (string, error) {
	return tracerProbe(ctx, executor, "ruby", []string{"-rbundler", "-e", `
definition = Bundler.definition
spec = definition.locked_gems&.specs&.find { |gem| gem.name == 'datadog-ci' }
if spec || definition.dependencies.any? { |gem| gem.name == 'datadog-ci' }
  spec = definition.specs.find { |gem| gem.name == 'datadog-ci' }
  raise 'project datadog-ci is unavailable in the active bundle' unless spec
  puts "  * datadog-ci (#{spec.version})"
end
`}, env)
}

func tracerProbe(ctx context.Context, executor TracerExecutor, command string, args []string, env map[string]string) (string, error) {
	output, stderr, err := executor.Output(ctx, command, args, env)
	if err != nil {
		return "", runtimeTagProbeError("detect project tracer", stderr, err)
	}
	return strings.TrimSpace(string(output)), nil
}
