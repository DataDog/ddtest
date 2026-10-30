package testdrive

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestEveryFrameworkWithPreflightUsesItsOwnChecks(t *testing.T) {
	javascript := platform.NewJavaScript()
	frameworks := map[string]framework.Framework{
		"pytest":        framework.NewPytest(platform.NewPython()),
		"rspec":         framework.NewRSpec(platform.NewRuby("")),
		"minitest":      framework.NewMinitest(platform.NewRuby("")),
		"jest":          framework.NewJest(javascript),
		"vitest":        framework.NewVitest(javascript),
		"mocha":         framework.NewMocha(javascript),
		"playwright":    framework.NewPlaywright(javascript),
		"cypress":       framework.NewCypress(javascript),
		"cucumber":      framework.NewCucumber(javascript),
		"webdriverio":   framework.NewWebdriverIO(javascript),
		"junit4":        framework.NewMaven("junit4", platform.NewJava()),
		"junit5":        framework.NewMaven("junit5", platform.NewJava()),
		"testng":        framework.NewMaven("testng", platform.NewJava()),
		"spock":         framework.NewMaven("spock", platform.NewJava()),
		"cucumber-java": framework.NewMaven("cucumber-java", platform.NewJava()),
		"karate":        framework.NewMaven("karate", platform.NewJava()),
		"scalatest":     framework.NewMaven("scalatest", platform.NewJava()),
		"munit":         framework.NewMaven("munit", platform.NewJava()),
	}
	for name, policy := range frameworkFeaturePolicies {
		if !policy.Preflight {
			continue
		}
		runner, ok := frameworks[name]
		require.True(t, ok, "add %s to this test", name)
		preflight := (&Testdrive{framework: runner}).frameworkPreflight()
		require.NotNil(t, preflight, name)
		function := runtime.FuncForPC(reflect.ValueOf(preflight).Pointer()).Name()
		switch name {
		case "pytest":
			require.Contains(t, function, ".checkPytestPreflight", name)
		case "rspec", "minitest":
			require.Contains(t, function, ".checkRubyPreflight", name)
		case "junit4", "junit5", "testng", "spock", "cucumber-java", "karate", "scalatest", "munit":
			require.Contains(t, function, ".checkJavaPreflight", name)
		default:
			require.Contains(t, function, ".check"+displayName(name)+"Preflight", name)
		}
	}
	require.Nil(t, (&Testdrive{framework: &telemetryOnlyFramework{}}).frameworkPreflight())
}

func TestPlainCheckOnlyRunsTheSelectedFrameworkPreflight(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is required")
	}
	for _, tc := range []struct{ framework, manifest, want string }{
		{"cypress", `{"devDependencies":{"cypress":"15.1.0"},"scripts":{"test":"cypress run"}}`, "inspect Cypress"},
		{"playwright", `{"devDependencies":{"@playwright/test":"1.55.1"},"scripts":{"test":"playwright test"}}`, "Playwright"},
	} {
		t.Run(tc.framework, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			requireWriteFile(t, filepath.Join(root, "package.json"), tc.manifest)
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
			requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  tests:\n    steps:\n      - run: npm test\n")
			viper.Reset()
			viper.Set("framework", tc.framework)
			settings.Init()
			t.Cleanup(func() { viper.Reset(); settings.Init() })
			drive, err := Prepare(PrepareOptions{TracerVersion: "6.20.1", CheckOnly: true})
			require.NoError(t, err)
			require.False(t, drive.allConfigurations)
			drive.executor = &fakeTestdriveExecutor{err: &exec.Error{Name: "node", Err: exec.ErrNotFound}}
			runErr := drive.Run(t.Context(), &bytes.Buffer{})
			require.Error(t, runErr)
			require.Contains(t, runErr.Error(), tc.want)
			require.NotContains(t, strings.ToLower(runErr.Error()), "jest")
			report := readValidationReport(t, root)
			require.True(t, report.CheckOnly)
			require.False(t, report.ChecksPassed)
			require.NotContains(t, strings.ToLower(report.Error), "jest")
		})
	}
}
