package planner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
	"github.com/stretchr/testify/require"
)

type collectionCountingPytest struct {
	framework.Framework
	collections atomic.Int32
}

func (p *collectionCountingPytest) DiscoverTests(context.Context, discovery.TestFileSet) ([]testoptimization.Test, error) {
	p.collections.Add(1)
	return nil, fmt.Errorf("pytest collection must not run")
}

func TestPytestSuitePlanning(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("TIA=%t", enabled), func(t *testing.T) {
			t.Chdir(t.TempDir())
			cfg := settings.Get()
			old := *cfg
			t.Cleanup(func() { *cfg = old })
			cfg.Framework = "pytest"
			cfg.TestSkippingLevel = settings.TestSkippingLevelSuite
			cfg.Command = "python-must-not-run -m pytest"
			cfg.TestsLocation = ""
			cfg.TestsExcludePattern = "checks/excluded/**"
			cfg.TestsSelectionPattern = ""
			cfg.ForceFullTestDiscovery = false
			cfg.StrictDiscovery = true
			cfg.RuntimeTags = `{"os.platform":"linux","os.version":"test","os.architecture":"x86_64","runtime.name":"CPython","runtime.version":"3.12.0"}`
			cfg.ReportEnabled = false
			require.NoError(t, os.WriteFile("pytest.ini", []byte("[pytest]\ntestpaths = checks\npython_files = check_*.py\n"), 0644))
			files := []string{
				"checks/unit/check_same.py", "checks/integration/check_same.py",
				"checks/check_new.py", "checks/check_unmapped.py", "checks/check_marked.py",
				"checks/guarded/check_case.py", "checks/excluded/check_case.py",
			}
			for _, file := range files {
				require.NoError(t, os.MkdirAll(filepath.Dir(file), 0755))
				// File discovery must not import tests, inspect decorators, or
				// enumerate generated tests and parametrized fixtures.
				source := "raise AssertionError('planning imported tests')\n"
				if file == "checks/check_marked.py" {
					source += "# datadog_itr_unskippable\n"
				}
				require.NoError(t, os.WriteFile(file, []byte(source), 0644))
			}
			require.NoError(t, os.WriteFile("checks/guarded/conftest.py", []byte("# datadog_itr_unskippable\nraise AssertionError('imported conftest')\n"), 0644))
			python := platform.NewPython()
			fw, err := python.DetectFramework()
			require.NoError(t, err)
			pytest := &collectionCountingPytest{Framework: fw}
			client := &MockTestOptimizationClient{
				Settings: testOptimizationSettings(enabled, enabled, false),
				Skippables: api.Skippables{Suites: api.SkippableSuites{
					{Module: "checks.unit", Suite: "check_same.py"}:     true,
					{Module: "checks", Suite: "check_unmapped.py"}:      true,
					{Module: "checks", Suite: "check_marked.py"}:        true,
					{Module: "checks.guarded", Suite: "check_case.py"}:  true,
					{Module: "checks.excluded", Suite: "check_case.py"}: true,
					{Module: "checks", Suite: "check_deleted.py"}:       true,
				}},
				Durations: map[string]map[string]api.TestSuiteDurationInfo{
					"checks.unit": {"check_same.py": {SourceFile: "checks/unit/check_same.py"}},
					"checks": {
						"check_marked.py":  {SourceFile: "checks/check_marked.py"},
						"check_deleted.py": {SourceFile: "checks/check_deleted.py"},
					},
					"checks.guarded":  {"check_case.py": {SourceFile: "checks/guarded/check_case.py"}},
					"checks.excluded": {"check_case.py": {SourceFile: "checks/excluded/check_case.py"}},
				},
			}
			planner := NewWithDependencies(python, pytest, client, newDefaultMockCIProviderDetector())
			require.NoError(t, planner.PreparePlanningData(t.Context()))
			require.Zero(t, pytest.collections.Load(), "suite mode must not start full collection")
			require.Equal(t, "suite", planner.planMetadata.TestSkippingLevel)
			require.Equal(t, discoveryModeFast, planner.reportStats.discoveryMode)
			for _, file := range files {
				_, runnable := planner.testFileWeights[file]
				want := file != "checks/excluded/check_case.py" && (!enabled || file != "checks/unit/check_same.py")
				require.Equal(t, want, runnable, file)
			}
			require.NotContains(t, planner.testFiles, "checks/check_deleted.py")
			require.NoFileExists(t, discovery.TestsFilePath)
		})
	}
}

func TestPythonSuiteModeRequestsSuiteSkippables(t *testing.T) {
	// Exercise the production client-factory boundary, not just an injected
	// client's already-decoded suite response.
	cfg := settings.Get()
	old := *cfg
	t.Cleanup(func() { *cfg = old })
	cfg.TestSkippingLevel = settings.TestSkippingLevelSuite
	cfg.RuntimeTags = `{"os.platform":"linux","os.version":"test","os.architecture":"x86_64","runtime.name":"CPython","runtime.version":"3.12.0"}`
	t.Chdir(t.TempDir())
	python := platform.NewPython()
	fw := framework.NewPytest(python)
	planner := NewWithDependencies(python, fw, nil, newDefaultMockCIProviderDetector())
	var requested []settings.TestSkippingLevel
	planner.newOptimizationClient = func(level settings.TestSkippingLevel) testOptimizationClient {
		requested = append(requested, level)
		return &MockTestOptimizationClient{Settings: testOptimizationSettings(true, true, false)}
	}
	require.NoError(t, planner.PreparePlanningData(t.Context()))
	require.Equal(t, []settings.TestSkippingLevel{settings.TestSkippingLevelSuite}, requested)
}

func TestPytestSuiteModePreservesExplicitCollection(t *testing.T) {
	for _, level := range []settings.TestSkippingLevel{settings.TestSkippingLevelTest, settings.TestSkippingLevelSuite} {
		t.Run(level.String(), func(t *testing.T) {
			t.Chdir(t.TempDir())
			cfg := settings.Get()
			old := *cfg
			t.Cleanup(func() { *cfg = old })
			cfg.TestSkippingLevel = level
			cfg.ForceFullTestDiscovery = level == settings.TestSkippingLevelSuite
			cfg.StrictDiscovery = true
			cfg.TestsLocation = "test_case.py"
			cfg.TestsExcludePattern = ""
			cfg.TestDiscoveryCache = ""
			cfg.RuntimeTags = `{"os.platform":"linux","os.version":"test","os.architecture":"x86_64","runtime.name":"CPython","runtime.version":"3.12.0"}`
			require.NoError(t, os.WriteFile("test_case.py", []byte("def test_case(): pass\n"), 0644))
			python := platform.NewPython()
			pytest := &collectionCountingPytest{Framework: framework.NewPytest(python)}
			client := &MockTestOptimizationClient{
				Settings: testOptimizationSettings(true, true, false),
				Skippables: api.Skippables{Suites: api.SkippableSuites{
					{Suite: "test_case.py"}: true,
				}},
			}
			planner := NewWithDependencies(python, pytest, client, newDefaultMockCIProviderDetector())
			require.ErrorContains(t, planner.PreparePlanningData(t.Context()), "pytest collection must not run")
			require.EqualValues(t, 1, pytest.collections.Load())
		})
	}
}
