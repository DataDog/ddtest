package planner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
)

func TestTestPlanner_Plan_StoresTestOptimizationPlanCache(t *testing.T) {
	environment.ResetCITags()
	t.Cleanup(environment.ResetCITags)
	// Select only the simulated provider, including when this test runs in CI.
	for _, key := range []string{
		"APPVEYOR", "TF_BUILD", "BITBUCKET_COMMIT", "BUDDY", "BUILDKITE", "CIRCLECI",
		"GITHUB_SHA", "JENKINS_URL", "TEAMCITY_VERSION", "TRAVIS", "BITRISE_BUILD_SLUG",
		"CF_BUILD_ID", "CODEBUILD_INITIATOR", "DRONE",
	} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GITLAB_CI", "true")
	t.Setenv("CI_COMMIT_AUTHOR", "Test Author <test@example.com>")
	t.Setenv("CI_JOB_ID", "plan-job-123")
	t.Setenv("CI_JOB_NAME", "plan")
	t.Setenv("CI_JOB_URL", "https://gitlab.com/example/project/-/jobs/123")
	t.Setenv("CI_PIPELINE_ID", "pipeline-456")
	t.Setenv("CI_PROJECT_PATH", "example/project")
	t.Setenv("CI_PIPELINE_IID", "456")
	t.Setenv("CI_PIPELINE_URL", "https://gitlab.com/example/project/-/pipelines/456")
	t.Setenv("CI_JOB_STAGE", "planning")
	t.Setenv("DD_API_KEY", "must-not-be-stored")
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "1")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", "1")
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM")
		settings.Init()
	}()
	settings.Init()

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Module: "rspec", Suite: "Suite1", Name: "test1", SuiteSourceFile: "spec/suite1_spec.rb"},
			{Module: "rspec", Suite: "Suite1", Name: "test2", SuiteSourceFile: "spec/suite1_spec.rb"},
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}
	runner := NewWithDependencies(
		mockPlatform, mockPlatform.Framework,
		&MockTestOptimizationClient{Skippables: testSkippables(map[string]bool{})},
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.Plan(context.Background()); err != nil {
		t.Fatalf("Plan() should not return error, got: %v", err)
	}

	cachePath := filepath.Join(constants.RunnerCacheDir, constants.TestOptimizationPlanCacheFile)
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("Expected test optimization plan cache file to be written: %v", err)
	}
	cacheData, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("Expected test optimization plan cache file to be readable: %v", err)
	}
	if !strings.Contains(string(cacheData), `"planMetadata"`) {
		t.Fatalf("Expected test optimization plan cache to store planMetadata, got: %s", string(cacheData))
	}
	if strings.Contains(string(cacheData), "must-not-be-stored") {
		t.Fatal("plan cache contains an API key")
	}
	for tag, want := range map[string]string{
		constants.CIProviderName:   "gitlab",
		constants.CIJobID:          "plan-job-123",
		constants.CIJobName:        "plan",
		constants.CIJobURL:         "https://gitlab.com/example/project/-/jobs/123",
		constants.CIPipelineID:     "pipeline-456",
		constants.CIPipelineName:   "example/project",
		constants.CIPipelineNumber: "456",
		constants.CIPipelineURL:    "https://gitlab.com/example/project/-/pipelines/456",
		constants.CIStageName:      "planning",
	} {
		if got := runner.planMetadata.CITags[tag]; got != want {
			t.Fatalf("planning CI tag %s = %q, want %q", tag, got, want)
		}
	}
	for tag := range runner.planMetadata.CITags {
		if _, allowed := constants.TestParallelizationPlanTags[tag]; !allowed {
			t.Fatalf("unexpected planning CI tag %s", tag)
		}
	}

	metadataData, err := os.ReadFile(constants.PlanMetadataPath)
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Tags map[string]string `json:"tags"`
	}
	if err := json.Unmarshal(metadataData, &metadata); err != nil {
		t.Fatal(err)
	}
	wantTags := map[string]string{
		"test.parallelization.plan.provider.name":   "gitlab",
		"test.parallelization.plan.job.id":          "plan-job-123",
		"test.parallelization.plan.job.name":        "plan",
		"test.parallelization.plan.job.url":         "https://gitlab.com/example/project/-/jobs/123",
		"test.parallelization.plan.pipeline.id":     "pipeline-456",
		"test.parallelization.plan.pipeline.name":   "example/project",
		"test.parallelization.plan.pipeline.number": "456",
		"test.parallelization.plan.pipeline.url":    "https://gitlab.com/example/project/-/pipelines/456",
		"test.parallelization.plan.stage.name":      "planning",
	}
	if !reflect.DeepEqual(metadata.Tags, wantTags) {
		t.Fatalf("library metadata = %v, want %v", metadata.Tags, wantTags)
	}
	if strings.Contains(string(metadataData), "must-not-be-stored") {
		t.Fatal("library metadata contains an API key")
	}

	// Loading the artifact in another job must retain the planning job identity.
	t.Setenv("CI_JOB_ID", "run-job-789")
	environment.ResetCITags()

	restored := NewWithDependencies(
		nil, nil,
		&MockTestOptimizationClient{},
		newDefaultMockCIProviderDetector(),
	)
	if err := restored.restoreTestOptimizationPlanCache(); err != nil {
		t.Fatalf("restoreTestOptimizationPlanCache() should not return error, got: %v", err)
	}

	if !reflect.DeepEqual(restored.suiteAggregates, runner.suiteAggregates) {
		t.Errorf("Expected restored suite aggregates to match stored aggregates.\nexpected: %v\nactual: %v", runner.suiteAggregates, restored.suiteAggregates)
	}
	if !reflect.DeepEqual(restored.suitesBySourceFile, runner.suitesBySourceFile) {
		t.Errorf("Expected restored suitesBySourceFile to match stored index.\nexpected: %v\nactual: %v", runner.suitesBySourceFile, restored.suitesBySourceFile)
	}
	if !reflect.DeepEqual(restored.testFileWeights, runner.testFileWeights) {
		t.Errorf("Expected restored test file weights to match stored weights.\nexpected: %v\nactual: %v", runner.testFileWeights, restored.testFileWeights)
	}
	if !reflect.DeepEqual(restored.planMetadata, runner.planMetadata) {
		t.Errorf("Expected restored plan info to match stored plan info.\nexpected: %v\nactual: %v", runner.planMetadata, restored.planMetadata)
	}
}

func TestTestPlanner_StoreAndRestoreTestOptimizationPlanCache_RoundTripDurations(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	runner := NewWithDependencies(
		nil, nil,
		&MockTestOptimizationClient{},
		newDefaultMockCIProviderDetector(),
	)
	runner.testSuiteDurations = map[string]map[string]api.TestSuiteDurationInfo{
		"rspec": {
			"Suite1": {
				SourceFile: "spec/suite1_spec.rb",
				Duration:   api.DurationPercentiles{P50: "5000000000", P90: "7000000000"},
			},
		},
	}
	runner.suiteAggregates = map[testSuiteKey]testSuiteAggregate{
		{Module: "rspec", Suite: "Suite1"}: {
			Module:            "rspec",
			Suite:             "Suite1",
			SourceFile:        "spec/suite1_spec.rb",
			TotalDuration:     5000000000,
			EstimatedDuration: 2500000000,
			NumTests:          2,
			NumTestsSkipped:   1,
		},
	}
	runner.suitesBySourceFile = map[string][]testSuiteKey{
		"spec/suite1_spec.rb": {{Module: "rspec", Suite: "Suite1"}},
	}
	runner.testFileWeights = map[string]int{
		"spec/suite1_spec.rb": 2500,
	}

	if err := runner.storeTestOptimizationPlanCache(); err != nil {
		t.Fatalf("storeTestOptimizationPlanCache() should not return error, got: %v", err)
	}

	logs := captureLogs(t)
	restored := NewWithDependencies(
		nil, nil,
		&MockTestOptimizationClient{},
		newDefaultMockCIProviderDetector(),
	)
	if err := restored.restoreTestOptimizationPlanCache(); err != nil {
		t.Fatalf("restoreTestOptimizationPlanCache() should not return error, got: %v", err)
	}

	if !reflect.DeepEqual(restored.testSuiteDurations, runner.testSuiteDurations) {
		t.Errorf("Expected restored test suite durations to match stored durations.\nexpected: %v\nactual: %v", runner.testSuiteDurations, restored.testSuiteDurations)
	}
	if !reflect.DeepEqual(restored.suiteAggregates, runner.suiteAggregates) {
		t.Errorf("Expected restored suite aggregates to match stored aggregates.\nexpected: %v\nactual: %v", runner.suiteAggregates, restored.suiteAggregates)
	}
	if !reflect.DeepEqual(restored.suitesBySourceFile, runner.suitesBySourceFile) {
		t.Errorf("Expected restored suitesBySourceFile to match stored index.\nexpected: %v\nactual: %v", runner.suitesBySourceFile, restored.suitesBySourceFile)
	}
	if !reflect.DeepEqual(restored.testFileWeights, runner.testFileWeights) {
		t.Errorf("Expected restored test file weights to match stored weights.\nexpected: %v\nactual: %v", runner.testFileWeights, restored.testFileWeights)
	}

	logOutput := logs.String()
	if !strings.Contains(logOutput, "level=INFO") ||
		!strings.Contains(logOutput, "Restored test optimization plan cache") ||
		!strings.Contains(logOutput, "objectsCount=4") ||
		!strings.Contains(logOutput, "modulesCount=1") ||
		!strings.Contains(logOutput, "testSuitesCount=1") ||
		!strings.Contains(logOutput, "suiteAggregatesCount=1") ||
		!strings.Contains(logOutput, "suitesBySourceFileCount=1") ||
		!strings.Contains(logOutput, "testFileWeightsCount=1") {
		t.Errorf("Expected INFO log for restored cache counts, got logs: %s", logOutput)
	}
}

func TestTestPlanner_RestoreTestOptimizationPlanCache_ComputesMissingWeights(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	type partialTestOptimizationPlanCache struct {
		TestSuiteDurations map[string]map[string]api.TestSuiteDurationInfo `json:"testSuiteDurations"`
		SuiteAggregates    map[testSuiteKey]testSuiteAggregate             `json:"suiteAggregates"`
		SuitesBySourceFile map[string][]testSuiteKey                       `json:"suitesBySourceFile"`
	}

	cache := partialTestOptimizationPlanCache{
		TestSuiteDurations: map[string]map[string]api.TestSuiteDurationInfo{
			"rspec": {
				"Suite1": {
					SourceFile: "spec/suite1_spec.rb",
					Duration:   api.DurationPercentiles{P50: "5000000000", P90: "7000000000"},
				},
			},
		},
		SuiteAggregates: map[testSuiteKey]testSuiteAggregate{
			{Module: "rspec", Suite: "Suite1"}: {
				Module:            "rspec",
				Suite:             "Suite1",
				SourceFile:        "spec/suite1_spec.rb",
				TotalDuration:     5000000000,
				EstimatedDuration: 2500000000,
				NumTests:          2,
				NumTestsSkipped:   1,
			},
		},
		SuitesBySourceFile: map[string][]testSuiteKey{
			"spec/suite1_spec.rb": {{Module: "rspec", Suite: "Suite1"}},
		},
	}

	if err := testoptimization.NewCacheManager().StoreTestOptimizationPlanCache(cache); err != nil {
		t.Fatalf("StoreTestOptimizationPlanCache() should not return error, got: %v", err)
	}

	restored := NewWithDependencies(
		nil, nil,
		&MockTestOptimizationClient{},
		newDefaultMockCIProviderDetector(),
	)
	if err := restored.restoreTestOptimizationPlanCache(); err != nil {
		t.Fatalf("restoreTestOptimizationPlanCache() should not return error, got: %v", err)
	}

	expectedWeights := map[string]int{
		"spec/suite1_spec.rb": 2500,
	}
	if !reflect.DeepEqual(restored.testFileWeights, expectedWeights) {
		t.Errorf("Expected restored cache to compute missing test file weights.\nexpected: %v\nactual: %v", expectedWeights, restored.testFileWeights)
	}
}

func TestTestPlanner_LoadPlan_UsesExistingPlannerState(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	cachePath := filepath.Join(constants.RunnerCacheDir, constants.TestOptimizationPlanCacheFile)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		t.Fatalf("failed to create cache dir: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("{"), 0644); err != nil {
		t.Fatalf("failed to write corrupt cache: %v", err)
	}

	planner := newTestPlannerWithDefaults()
	planner.planMetadata = PlanMetadata{
		Platform:  "ruby",
		Framework: "rspec",
	}
	planner.testFileWeights = map[string]int{
		"spec/example_spec.rb": 123,
	}
	planner.planLoaded = true

	plan, err := planner.LoadPlan()
	if err != nil {
		t.Fatalf("LoadPlan() should use existing planner state, got error: %v", err)
	}
	if plan.Platform != "ruby" || plan.Framework != "rspec" {
		t.Fatalf("LoadPlan() returned wrong plan info: %v", plan)
	}
}

func TestTestSuiteKey_JSONMapKeyRoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	key := testSuiteKey{Module: `rspec/module:one`, Suite: `Suite "one", with punctuation`}
	runner := NewWithDependencies(
		nil, nil,
		&MockTestOptimizationClient{},
		newDefaultMockCIProviderDetector(),
	)
	runner.suiteAggregates = map[testSuiteKey]testSuiteAggregate{
		key: {
			Module:            key.Module,
			Suite:             key.Suite,
			SourceFile:        "spec/suite1_spec.rb",
			TotalDuration:     5000000000,
			EstimatedDuration: 2500000000,
			NumTests:          2,
			NumTestsSkipped:   1,
		},
	}

	if err := runner.storeTestOptimizationPlanCache(); err != nil {
		t.Fatalf("storeTestOptimizationPlanCache() should not return error, got: %v", err)
	}

	restored := NewWithDependencies(
		nil, nil,
		&MockTestOptimizationClient{},
		newDefaultMockCIProviderDetector(),
	)
	if err := restored.restoreTestOptimizationPlanCache(); err != nil {
		t.Fatalf("restoreTestOptimizationPlanCache() should not return error, got: %v", err)
	}

	if !reflect.DeepEqual(restored.suiteAggregates, runner.suiteAggregates) {
		t.Errorf("Expected restored suite aggregates to preserve text-marshaled keys.\nexpected: %v\nactual: %v", runner.suiteAggregates, restored.suiteAggregates)
	}
}
