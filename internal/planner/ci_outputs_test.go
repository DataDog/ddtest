package planner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/errcode"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

func TestPlanCIOutputs(t *testing.T) {
	for _, provider := range []environment.CIProvider{environment.NewGitHub(), &environment.CircleCI{}} {
		for _, tc := range []struct {
			name          string
			min, max      int
			empty         bool
			skipAll       bool
			unskippable   bool
			discoveryFail bool
			wantHasTests  bool
		}{
			{name: "all skipped max one", min: 1, max: 1, skipAll: true},
			{name: "all skipped min one", min: 1, max: 3, skipAll: true},
			{name: "all skipped min two", min: 2, max: 3, skipAll: true},
			{name: "no discovered tests", min: 1, max: 3, empty: true},
			{name: "partially skipped", min: 1, max: 3, wantHasTests: true},
			{name: "unskippable file", min: 1, max: 3, skipAll: true, unskippable: true, wantHasTests: true},
			{name: "strict discovery failure", min: 1, max: 3, discoveryFail: true},
		} {
			t.Run(provider.Name()+"/"+tc.name, func(t *testing.T) {
				t.Chdir(t.TempDir())
				t.Cleanup(settings.Init)
				t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", strconv.Itoa(tc.min))
				t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", strconv.Itoa(tc.max))
				t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_FORCE_FULL_TEST_DISCOVERY", "true")
				t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_STRICT_DISCOVERY", "true")
				t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_REPORT_ENABLED", "false")
				settings.Init()
				outputPath := filepath.Join(t.TempDir(), "github-output")
				t.Setenv("GITHUB_OUTPUT", outputPath)

				tests := []testoptimization.Test{
					{Module: "rspec", Suite: "Suite1", Name: "test1", SuiteSourceFile: "file1.rb"},
					{Module: "rspec", Suite: "Suite2", Name: "test2", SuiteSourceFile: "file2.rb"},
				}
				skippables := map[string]bool{tests[0].DatadogTestId(): true}
				if tc.skipAll {
					skippables[tests[1].DatadogTestId()] = true
				}
				fw := &MockFramework{
					FrameworkName:    "rspec",
					Tests:            tests,
					TestFiles:        []string{"file1.rb", "file2.rb"},
					UnskippableFiles: map[string]bool{"file1.rb": tc.unskippable},
				}
				if tc.empty {
					fw.Tests, fw.TestFiles = nil, nil
					// Empty full discovery is an error in strict mode. A framework
					// with file-only discovery can legitimately discover no files.
					fw.FullDiscoveryUnsupported = true
				}
				if tc.discoveryFail {
					fw.DiscoverTestsErr = errors.New("broken discovery")
				}
				p := NewWithDependencies(
					&MockPlatform{PlatformName: "ruby", Tags: map[string]string{"language": "ruby"}}, fw,
					&MockTestOptimizationClient{
						Settings:   testOptimizationSettings(true, true, false),
						Skippables: testSkippables(skippables),
					},
					&MockCIProviderDetector{CIProvider: provider},
				)
				err := p.Plan(context.Background())
				if tc.discoveryFail {
					if errcode.CodeOf(err) != errcode.PlanFullTestDiscoveryFailed {
						t.Fatalf("Plan() = %v, want discovery failure", err)
					}
					for _, path := range []string{outputPath, environment.GitHubMatrixPath, environment.CircleCIParametersPath} {
						if _, err := os.Stat(path); !os.IsNotExist(err) {
							t.Fatalf("CI output %s must not be published after discovery failure: %v", path, err)
						}
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				files, err := os.ReadFile(constants.TestFilesOutputPath)
				if err != nil {
					t.Fatal(err)
				}
				if (len(files) > 0) != tc.wantHasTests {
					t.Fatalf("runnable files = %q, want has_tests=%t", files, tc.wantHasTests)
				}
				if !tc.wantHasTests {
					assertFileContent(t, constants.ParallelRunnersOutputPath, strconv.Itoa(tc.min))
					assertFileContent(t, filepath.Join(constants.TestsSplitDir, "runner-0"), "")
				}
				if provider.Name() == "github" {
					output, err := os.ReadFile(outputPath)
					if err != nil {
						t.Fatal(err)
					}
					matrix, err := os.ReadFile(environment.GitHubMatrixPath)
					if err != nil {
						t.Fatal(err)
					}
					if !json.Valid([]byte(strings.TrimPrefix(string(matrix), "matrix="))) {
						t.Fatalf("invalid legacy matrix artifact: %s", matrix)
					}
					want := string(matrix) + "\nhas_tests=" + strconv.FormatBool(tc.wantHasTests) + "\n"
					if string(output) != want {
						t.Fatalf("GitHub output = %q, want %q", output, want)
					}
				} else {
					data, err := os.ReadFile(environment.CircleCIParametersPath)
					if err != nil {
						t.Fatal(err)
					}
					var params struct {
						Parallelism int   `json:"parallelism"`
						HasTests    *bool `json:"has_tests"`
					}
					if err := json.Unmarshal(data, &params); err != nil {
						t.Fatal(err)
					}
					if params.HasTests == nil || *params.HasTests != tc.wantHasTests || params.Parallelism < tc.min || params.Parallelism > tc.max {
						t.Fatalf("unexpected CircleCI parameters: %s", data)
					}
					assertFileContent(t, constants.ParallelRunnersOutputPath, strconv.Itoa(params.Parallelism))
				}
			})
		}
	}
}
