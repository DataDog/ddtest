package planner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
	"github.com/DataDog/ddtest/internal/utils"
)

type jestProject struct {
	Name       string   `json:"name"`
	Repository string   `json:"repository"`
	Revision   string   `json:"revision"`
	Pattern    string   `json:"pattern"`
	Files      []string `json:"files"`
}

func jestProjects(t *testing.T) []jestProject {
	t.Helper()
	data, err := os.ReadFile("testdata/jest_projects.json")
	if err != nil {
		t.Fatal(err)
	}
	var projects []jestProject
	if err := json.Unmarshal(data, &projects); err != nil {
		t.Fatal(err)
	}
	return projects
}

func configureJestProject(t *testing.T, project jestProject) {
	t.Helper()
	t.Cleanup(settings.Init)
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION", project.Pattern)
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_EXCLUDE_PATTERN", "")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "1")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", "1")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_REPORT_ENABLED", "false")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_COMMAND", "")
	settings.Init()
	utils.ResetCwdSubdirPrefixForTesting()
	t.Cleanup(utils.ResetCwdSubdirPrefixForTesting)
}

// These snapshots come from native Jest --listTests at the pinned revisions.
// They exercise the real filesystem adapter and planner in every make test run.
func TestJestProjectSuiteSkipping(t *testing.T) {
	for _, project := range jestProjects(t) {
		t.Run(project.Name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			configureJestProject(t, project)
			for _, file := range project.Files {
				if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("throw new Error('must not load during planning')\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"none", "alternating", "all", "unskippable"} {
				t.Run(name, func(t *testing.T) {
					skippables, want := jestProjectSelection(project.Files, name)
					if name == "unskippable" {
						if err := os.WriteFile(project.Files[0], []byte("// @datadog unskippable\n"), 0644); err != nil {
							t.Fatal(err)
						}
					}
					planJestProject(t, framework.NewJest(), skippables, want)
				})
			}
		})
	}
}

func jestProjectSelection(files []string, mode string) (api.SkippableSuites, []string) {
	skippables := api.SkippableSuites{}
	want := []string{}
	for i, file := range files {
		skip := mode == "all" || mode == "unskippable" || mode == "alternating" && i%2 == 0
		if skip {
			skippables[api.SkippableSuite{Module: "jest", Suite: file}] = true
		}
		if !skip || mode == "unskippable" && i == 0 {
			want = append(want, file)
		}
	}
	return skippables, want
}

func planJestProject(t *testing.T, jest *framework.Jest, skippables api.SkippableSuites, want []string) []string {
	t.Helper()
	p := &MockPlatform{PlatformName: "javascript", Tags: map[string]string{"language": "javascript"}, TestLevel: settings.TestSkippingLevelSuite}
	planner := NewWithDependencies(p, jest, &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, true, false), Skippables: api.Skippables{Suites: skippables},
	}, newDefaultMockCIProviderDetector())
	if err := planner.Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	discovered := readTestPlanLines(t, constants.DiscoveredTestFilesOutputPath)
	expectedDiscovered := slices.Clone(want)
	for suite := range skippables {
		expectedDiscovered = append(expectedDiscovered, suite.Suite)
	}
	slices.Sort(expectedDiscovered)
	expectedDiscovered = slices.Compact(expectedDiscovered)
	if !slices.Equal(discovered, expectedDiscovered) {
		t.Fatalf("pre-skipping files = %v, want %v", discovered, expectedDiscovered)
	}
	got := readTestPlanLines(t, constants.TestFilesOutputPath)
	if !slices.Equal(got, want) {
		t.Fatalf("planned files = %v, want %v", got, want)
	}
	if len(want) > 0 {
		assigned := readTestPlanLines(t, filepath.Join(constants.TestsSplitDir, "runner-0"))
		if !slices.Equal(assigned, want) {
			t.Fatalf("worker files = %v, want %v", assigned, want)
		}
	}
	return got
}

// Runs upstream tests unchanged. CI supplies a pinned checkout with its locked
// dependencies and build artifacts; regular offline tests use the snapshots above.
func TestJestOpenSourceProjectIntegration(t *testing.T) {
	name := os.Getenv("DDTEST_JEST_PROJECT")
	root := os.Getenv("DDTEST_JEST_PROJECT_ROOT")
	if name == "" || root == "" {
		t.Skip("set DDTEST_JEST_PROJECT and DDTEST_JEST_PROJECT_ROOT")
	}
	var project jestProject
	for _, candidate := range jestProjects(t) {
		if candidate.Name == name {
			project = candidate
		}
	}
	if project.Name == "" {
		t.Fatalf("unknown Jest project %q", name)
	}
	t.Chdir(root)
	configureJestProject(t, project)
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION", "")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_COMMAND", "node_modules/.bin/jest --runInBand --coverage=false")
	settings.Init()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	binary := filepath.Join("node_modules", ".bin", "jest")
	// Native discovery is the oracle. Static analysis must either match it
	// exactly or explicitly report unsupported config and use the native adapter.
	output, err := exec.CommandContext(ctx, binary, "--listTests", "--json", "--runInBand", "--coverage=false").Output()
	if err != nil {
		t.Fatalf("native Jest discovery: %v", err)
	}
	var native []string
	if err := json.Unmarshal(output, &native); err != nil {
		t.Fatal(err)
	}
	native = relativeJestResults(t, native)
	if !slices.Equal(native, project.Files) {
		t.Fatalf("upstream file list changed: %v, want %v", native, project.Files)
	}
	started := time.Now()
	adapter := framework.NewJest()
	fast, fastErr := adapter.DiscoverTestFilesFast(ctx, discovery.TestFileSet{})
	if project.Name != "immer" && fastErr != nil {
		t.Fatalf("static discovery regressed: %v", fastErr)
	}
	if fastErr == nil && !slices.Equal(fast, native) {
		t.Fatalf("static = %v, native = %v", fast, native)
	}
	if fastErr != nil {
		t.Logf("native fallback required: %v", fastErr)
	}
	discovered, err := adapter.DiscoverTestFiles(ctx, discovery.TestFileSet{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("filesystem discovery: %d suites in %s", len(discovered), time.Since(started))
	if adapter.NativeTestFileDiscoveryUsed() != (fastErr != nil) {
		t.Fatal("discovery mode does not reflect the fallback")
	}
	if !slices.Equal(discovered, native) {
		t.Fatalf("filesystem = %v, native Jest = %v", discovered, native)
	}
	for _, mode := range []string{"none", "alternating", "all"} {
		t.Run(mode, func(t *testing.T) {
			resultFile := filepath.Join(t.TempDir(), "jest-results.json")
			t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_COMMAND", binary+" --runInBand --coverage=false --json --outputFile="+strconv.Quote(resultFile))
			settings.Init()
			t.Cleanup(settings.Init)
			jest := framework.NewJest()
			skippables, want := jestProjectSelection(native, mode)
			assigned := planJestProject(t, jest, skippables, want)
			if err := jest.RunTests(ctx, assigned, nil); err != nil {
				t.Fatalf("upstream Jest execution: %v", err)
			}
			if len(assigned) == 0 {
				if _, err := os.Stat(resultFile); !os.IsNotExist(err) {
					t.Fatalf("empty assignment started Jest: %v", err)
				}
				return
			}
			data, err := os.ReadFile(resultFile)
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Success     bool `json:"success"`
				TestResults []struct {
					Name string `json:"name"`
				} `json:"testResults"`
			}
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if !result.Success {
				t.Fatal("upstream tests failed")
			}
			executed := []string{}
			for _, test := range result.TestResults {
				executed = append(executed, test.Name)
			}
			executed = relativeJestResults(t, executed)
			if !slices.Equal(executed, want) {
				t.Fatalf("executed = %v, want only assigned files %v", executed, want)
			}
		})
	}
}

func relativeJestResults(t *testing.T, files []string) []string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i, file := range files {
		rel, err := filepath.Rel(cwd, file)
		if err != nil {
			t.Fatal(err)
		}
		files[i] = filepath.ToSlash(rel)
	}
	slices.Sort(files)
	return files
}
