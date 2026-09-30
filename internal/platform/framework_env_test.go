package platform

import (
	"fmt"
	"os"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

func frameworkRunEnv(t *testing.T, f framework.Framework) map[string]string {
	t.Helper()
	env, err := f.Platform().RunEnv(framework.RuntimeOptions{ESM: f.Name() == "vitest"})
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
	for _, esm := range []bool{false, true} {
		t.Run(fmt.Sprintf("ESM=%t", esm), func(t *testing.T) {
			input := map[string]string{"CUSTOM": "value"}
			p := NewJavaScript()
			env, err := p.DiscoveryEnv(t.Context(), framework.FileDiscovery, framework.RuntimeOptions{ESM: esm, Env: input, PreloadFiles: []string{"/adapter with spaces/entry.js"}})
			require.NoError(t, err)
			want := `--require "/project with spaces/.pnp.cjs" --import=/external/dd-trace/register.js --max-old-space-size=4096 --require "/adapter with spaces/entry.js"`
			if esm {
				want = `--require "/project with spaces/.pnp.cjs" --max-old-space-size=4096 --require "/adapter with spaces/entry.js"`
			}
			require.Equal(t, want, env["NODE_OPTIONS"])
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
				env, err := tc.p.RunEnv(framework.RuntimeOptions{ESM: tc.name == "vitest", Env: input})
				require.NoError(t, err)
				require.Equal(t, input, env, "explicit values, including empty ones, override defaults")
				env["CUSTOM"] = "changed"
				require.Equal(t, "input", input["CUSTOM"])
				again, err := tc.p.RunEnv(framework.RuntimeOptions{ESM: tc.name == "vitest", Env: input})
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

func TestPythonFullDiscoveryDoesNotCheckTracer(t *testing.T) {
	p := &Python{executor: nil}
	env, err := p.DiscoveryEnv(t.Context(), framework.FullDiscovery, framework.RuntimeOptions{})
	require.NoError(t, err)
	require.Contains(t, env["PYTEST_ADDOPTS"], "--ddtrace")
}

func TestSelectedFrameworkKeepsCapturedEnvironment(t *testing.T) {
	for _, tc := range []struct {
		p                       Platform
		framework, key, initial string
	}{
		{NewJavaScript(), "mocha", "NODE_OPTIONS", "--require original-loader.cjs"},
		{NewJavaScript(), "vitest", "NODE_OPTIONS", "--require original-loader.cjs"},
		{NewRuby(settings.TestSkippingLevelTest), "rspec", "RUBYOPT", "-roriginal_setup"},
		{NewPython(), "pytest", "PYTEST_ADDOPTS", "-q"},
	} {
		t.Run(tc.framework, func(t *testing.T) {
			resetDetectionSettings(t)
			settings.Get().Framework = tc.framework
			t.Setenv(tc.key, tc.initial)
			if tc.p.Name() == "ruby" {
				require.NoError(t, os.Unsetenv(tc.key))
			}
			t.Setenv("DD_TRACE_PACKAGE", "/original/dd-trace/ci/init.js")
			t.Setenv("DD_TRACE_ESM_IMPORT", "/original/dd-trace/register.js")
			fw, err := tc.p.DetectFramework()
			require.NoError(t, err)
			before := frameworkRunEnv(t, fw)
			if tc.p.Name() == "ruby" {
				require.Equal(t, rubyOptDefaultValue, before[tc.key])
			} else {
				require.Contains(t, before[tc.key], tc.initial)
			}

			t.Setenv(tc.key, "changed after detection")
			t.Setenv("DD_TRACE_PACKAGE", "/changed/dd-trace/ci/init.js")
			t.Setenv("DD_TRACE_ESM_IMPORT", "/changed/dd-trace/register.js")
			require.Equal(t, before, frameworkRunEnv(t, fw))
			if tc.p.Name() == "javascript" {
				env, err := tc.p.DiscoveryEnv(t.Context(), framework.FileDiscovery, framework.RuntimeOptions{ESM: tc.framework == "vitest"})
				require.NoError(t, err)
				require.Equal(t, tc.initial, env[tc.key])
			}
		})
	}
}
