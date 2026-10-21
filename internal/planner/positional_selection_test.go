package planner

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
	"github.com/DataDog/ddtest/internal/utils"
)

func TestPositionalSelectionNarrowsFrameworkDiscovery(t *testing.T) {
	for _, tt := range []struct {
		name      string
		framework framework.Framework
		args      []string
		location  string
		exclude   string
		want      []string
	}{
		{name: "RSpec directory", framework: framework.NewRSpec(platform.NewRuby(settings.TestSkippingLevelTest)), args: []string{"spec/models"}, want: []string{"spec/models/user_spec.rb"}},
		{name: "RSpec broad glob", framework: framework.NewRSpec(platform.NewRuby(settings.TestSkippingLevelTest)), args: []string{"spec/**/*"}, want: []string{"spec/models/user_spec.rb", "spec/requests/api_spec.rb"}},
		{name: "pytest directory", framework: framework.NewPytest(platform.NewPython()), args: []string{"tests"}, want: []string{"tests/test_user.py"}},
		{name: "multiple overlapping arguments", framework: framework.NewRSpec(platform.NewRuby(settings.TestSkippingLevelTest)), args: []string{"spec/models", "spec/requests/api_spec.rb", "spec/models/*"}, want: []string{"spec/models/user_spec.rb", "spec/requests/api_spec.rb"}},
		{name: "helper is not a test", framework: framework.NewRSpec(platform.NewRuby(settings.TestSkippingLevelTest)), args: []string{"spec/models/helper.rb"}},
		{name: "outside default roots", framework: framework.NewRSpec(platform.NewRuby(settings.TestSkippingLevelTest)), args: []string{"custom_specs"}},
		{name: "custom discovery root", framework: framework.NewRSpec(platform.NewRuby(settings.TestSkippingLevelTest)), args: []string{"custom_specs"}, location: "custom_specs/**/*_spec.rb", want: []string{"custom_specs/extra_spec.rb"}},
		{name: "exclude still applies", framework: framework.NewRSpec(platform.NewRuby(settings.TestSkippingLevelTest)), args: []string{"spec"}, exclude: "spec/requests/**", want: []string{"spec/models/user_spec.rb"}},
		{name: "unmatched glob", framework: framework.NewRSpec(platform.NewRuby(settings.TestSkippingLevelTest)), args: []string{"missing/**"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			setPlannerTestsLocation(t, tt.location)
			setPlannerTestsExcludePattern(t, tt.exclude)
			utils.ResetCwdSubdirPrefixForTesting()
			t.Cleanup(utils.ResetCwdSubdirPrefixForTesting)
			var tests []testoptimization.Test
			for _, file := range []string{"spec/models/user_spec.rb", "spec/models/helper.rb", "spec/requests/api_spec.rb", "spec/fixtures/data.json", "custom_specs/extra_spec.rb", "tests/test_user.py", "tests/conftest.py"} {
				if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, nil, 0644); err != nil {
					t.Fatal(err)
				}
				tests = append(tests, testoptimization.Test{Suite: file, Name: "example", SuiteSourceFile: file})
			}
			// A scoped run must also discard tests without a source location.
			tests = append(tests, testoptimization.Test{Suite: "unknown", Name: "example"})
			selection, err := utils.ParseTestSelection(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			settings.Get().TestsSelectionPattern = selection
			resolved, err := discovery.ResolveTestFiles(tt.framework.TestPattern(), tt.exclude)
			if err != nil {
				t.Fatal(err)
			}
			files, err := tt.framework.DiscoverTestFiles(context.Background(), resolved)
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"full", "fast"} {
				t.Run(mode, func(t *testing.T) {
					planner := newTestPlannerWithDefaults()
					if mode == "full" {
						// Also covers restored caches: they use the same processing path.
						err = planner.recordFullDiscoveryResults(tests, resolved, newSkippableMatcher(api.NewSkippables(), nil), nil)
					} else {
						err = planner.recordFastDiscoveryFallbackFiles(files)
					}
					if err != nil {
						t.Fatal(err)
					}
					got := slices.Sorted(maps.Keys(planner.testFiles))
					if !slices.Equal(got, tt.want) {
						t.Fatalf("files = %v, want %v", got, tt.want)
					}
					if mode == "full" && len(planner.suiteAggregates) != len(tt.want) {
						t.Fatalf("unexpected suite aggregates: %v", planner.suiteAggregates)
					}
				})
			}
		})
	}
}

func TestWarnFastFilesMissingFromFullDiscoveryRespectsSelection(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, file := range []string{
		"tests/unit/test_user.py",
		"tests/unit/test_missing.py",
		"tests/integration/test_billing.py",
	} {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	previous := settings.Get().TestsSelectionPattern
	t.Cleanup(func() {
		settings.Get().TestsSelectionPattern = previous
	})
	selection, err := utils.ParseTestSelection([]string{"tests/unit"})
	if err != nil {
		t.Fatal(err)
	}
	settings.Get().TestsSelectionPattern = selection

	planner := newTestPlannerWithDefaults()
	planner.testFiles["tests/unit/test_user.py"] = struct{}{}

	logs := captureLogs(t)
	// Fast discovery lists every file matching the framework pattern; full
	// discovery only keeps files inside the positional selection.
	planner.warnFastFilesMissingFromFullDiscovery([]string{
		"tests/unit/test_user.py",
		"tests/integration/test_billing.py",
		"tests/unit/test_missing.py",
	})
	logOutput := logs.String()
	if strings.Contains(logOutput, "tests/integration/test_billing.py") {
		t.Fatalf("out-of-selection file must not be reported as missing, got logs: %s", logOutput)
	}
	if !strings.Contains(logOutput, "tests/unit/test_missing.py") {
		t.Fatalf("in-selection file with no collected tests must be reported, got logs: %s", logOutput)
	}
}
