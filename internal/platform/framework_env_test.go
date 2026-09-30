package platform

import (
	"os"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

func frameworkRunEnv(t *testing.T, f framework.Framework) map[string]string {
	t.Helper()
	env, err := f.Platform().RunEnv(framework.RuntimeOptions{Framework: f.Name()})
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestEveryFrameworkRetainsItsPlatform(t *testing.T) {
	for _, tc := range []struct {
		platform   Platform
		frameworks []string
	}{
		{NewRuby(settings.TestSkippingLevelTest), []string{"rspec", "minitest"}},
		{NewPython(), []string{"pytest"}},
		{NewJavaScript(), []string{"jest", "mocha", "vitest", "playwright", "cypress", "cucumber"}},
	} {
		for _, name := range tc.frameworks {
			t.Run(name, func(t *testing.T) {
				resetDetectionSettings(t)
				settings.Get().Framework = name
				t.Setenv("PATH", t.TempDir()) // Construction must not probe runtimes or tracers.
				fw, err := tc.platform.DetectFramework()
				require.NoError(t, err)
				require.Same(t, tc.platform, fw.Platform())
			})
		}
	}
}

func TestJavaScriptDiscoveryEnvironment(t *testing.T) {
	inherited := `--require "/project with spaces/.pnp.cjs" --require="/external/dd-trace/ci/init.js" --import=/external/dd-trace/register.js --max-old-space-size=4096`
	t.Setenv("NODE_OPTIONS", inherited)
	for _, name := range []string{"jest", "mocha", "vitest", "playwright", "cypress", "cucumber"} {
		t.Run(name, func(t *testing.T) {
			input := map[string]string{"CUSTOM": "value"}
			p := NewJavaScript()
			env, err := p.DiscoveryEnv(t.Context(), framework.FileDiscovery, framework.RuntimeOptions{Framework: name, Env: input, PreloadFiles: []string{"/adapter with spaces/entry.js"}})
			require.NoError(t, err)
			require.Equal(t, `--require "/project with spaces/.pnp.cjs" --max-old-space-size=4096 --require "/adapter with spaces/entry.js"`, env["NODE_OPTIONS"])
			require.Equal(t, map[string]string{"CUSTOM": "value"}, input)
			require.Equal(t, "value", env["CUSTOM"])
			require.Equal(t, inherited, os.Getenv("NODE_OPTIONS"))
		})
	}
}

func TestPlatformEnvironmentOverridesAndIsolation(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "--require project-loader.cjs")
	t.Setenv("RUBYOPT", "-rproject_setup")
	t.Setenv("PYTEST_ADDOPTS", "-q")
	for _, tc := range []struct {
		p         Platform
		name, key string
	}{
		{NewJavaScript(), "vitest", "NODE_OPTIONS"},
		{NewRuby(settings.TestSkippingLevelTest), "rspec", "RUBYOPT"},
		{NewPython(), "pytest", "PYTEST_ADDOPTS"},
	} {
		t.Run(tc.p.Name(), func(t *testing.T) {
			for _, value := range []string{"", "explicit override"} {
				input := map[string]string{tc.key: value, "CUSTOM": "input"}
				env, err := tc.p.RunEnv(framework.RuntimeOptions{Framework: tc.name, Env: input})
				require.NoError(t, err)
				require.Equal(t, input, env, "explicit values, including empty ones, override defaults")
				env["CUSTOM"] = "changed"
				require.Equal(t, "input", input["CUSTOM"])
				again, err := tc.p.RunEnv(framework.RuntimeOptions{Framework: tc.name, Env: input})
				require.NoError(t, err)
				require.Equal(t, "input", again["CUSTOM"], "returned maps must not share state")
			}
		})
	}
}

func TestDiscoveryUsesOverridesBeforeRemovingPreloads(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "--require inherited-loader.cjs -r dd-trace/ci/init")
	p := NewJavaScript()
	for _, tc := range []struct{ value, want string }{
		{"", ""},
		{`--require "override loader.cjs" -r dd-trace/ci/init`, `--require "override loader.cjs"`},
	} {
		env, err := p.DiscoveryEnv(t.Context(), framework.FileDiscovery, framework.RuntimeOptions{Env: map[string]string{"NODE_OPTIONS": tc.value}})
		require.NoError(t, err)
		require.Equal(t, tc.want, env["NODE_OPTIONS"])
	}
}

func TestFileDiscoveryDoesNotCheckTracer(t *testing.T) {
	for _, p := range []Platform{
		&Ruby{executor: nil}, &Python{executor: nil}, &JavaScript{executor: nil},
	} {
		t.Run(p.Name(), func(t *testing.T) {
			env, err := p.DiscoveryEnv(t.Context(), framework.FileDiscovery, framework.RuntimeOptions{Env: map[string]string{"CUSTOM": "value"}})
			require.NoError(t, err)
			require.Equal(t, "value", env["CUSTOM"])
		})
	}
}

func TestPythonFullDiscoveryChecksTracer(t *testing.T) {
	for _, tc := range []struct {
		version string
		fails   bool
	}{
		{"4.10.0", true}, {"4.11.0", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			calls := 0
			p := NewPython()
			p.executor = &mockCommandExecutor{combinedOutput: []byte(tc.version), onCombinedOutput: func(_ string, _ []string, _ map[string]string) { calls++ }}
			env, err := p.DiscoveryEnv(t.Context(), framework.FullDiscovery, framework.RuntimeOptions{Framework: "pytest"})
			require.Equal(t, 1, calls, "only the framework's discovery request checks the tracer")
			if tc.fails {
				require.ErrorContains(t, err, "full test discovery requires ddtrace")
			} else {
				require.NoError(t, err)
				require.Contains(t, env["PYTEST_ADDOPTS"], "--ddtrace")
			}
		})
	}
}

func TestTestdriveTracerEnvironmentPreservesQuotedLoaders(t *testing.T) {
	t.Setenv("NODE_OPTIONS", `--require "/project with spaces/.pnp.cjs" --require="/old tracer/dd-trace/ci/init.js" --import="/old tracer/dd-trace/register.js"`)
	env := NewJavaScript().TracerEnv("/new tracer/dd-trace/ci/init.js")
	require.Equal(t, `--require "/project with spaces/.pnp.cjs" -r "/new tracer/dd-trace/ci/init.js"`, env["NODE_OPTIONS"])
}
