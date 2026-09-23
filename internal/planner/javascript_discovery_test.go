package planner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
)

func TestJestConfigDiscoveryBeforeDDTestExclusions(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Cleanup(settings.Init)
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION", "")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_EXCLUDE_PATTERN", "checks/ignored/**")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_COMMAND", "")
	settings.Init()
	for _, file := range []string{"checks/a.check.js", "checks/ignored/b.check.js", "default.test.js"} {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("jest.config.js", []byte(`module.exports={roots:['checks'],testRegex:'\\.check\\.js$'}`), 0644); err != nil {
		t.Fatal(err)
	}
	planJestProject(t, framework.NewJest(), nil, []string{"checks/a.check.js"})
}

type nativeFileFramework struct{ *MockFramework }

func (f *nativeFileFramework) DiscoverTestFilesNative(context.Context, discovery.TestFileSet) ([]string, error) {
	return f.TestFiles, nil
}
func (f *nativeFileFramework) NativeTestFileDiscoveryUsed() bool { return true }

func TestPlannerReportsNativeFileDiscoveryAsFull(t *testing.T) {
	t.Chdir(t.TempDir())
	setPlannerForceFullTestDiscovery(t, true)
	f := &nativeFileFramework{&MockFramework{FrameworkName: "jest", FullDiscoveryUnsupported: true, TestFiles: []string{"custom.check.js"}}}
	p := NewWithDependencies(&MockPlatform{PlatformName: "javascript", TestLevel: settings.TestSkippingLevelSuite}, f, &MockTestOptimizationClient{Settings: testOptimizationSettings(false, false, false)}, newDefaultMockCIProviderDetector())
	if err := p.PreparePlanningData(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.reportStats.discoveryMode != discoveryModeFull {
		t.Fatalf("mode=%s", p.reportStats.discoveryMode)
	}
	if _, ok := p.testFileWeights["custom.check.js"]; !ok {
		t.Fatal("native suite missing")
	}
}
