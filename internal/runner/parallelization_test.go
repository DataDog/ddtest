package runner

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/planner"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
)

func TestTestRunner_Run_PropagatesPlanningJobToEveryWorker(t *testing.T) {
	for _, mode := range []string{"sequential", "parallel", "ci-node"} {
		t.Run(mode, func(t *testing.T) {
			withRunnerTestSettings(t)
			t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV", "_DD_TEST_PARALLELIZATION_ENABLED=false;_DD_TEST_PARALLELIZATION_PLAN_JOB_ID=worker-override")
			t.Setenv("_DD_TEST_PARALLELIZATION_PLAN_JOB_ID", "inherited-plan")
			viper.Reset()
			settings.Init()
			chdirTemp(t)
			writeRunnerTestFile(t, constants.ParallelRunnersOutputPath, "1")
			writeRunnerTestFile(t, constants.TestFilesOutputPath, "test/a.js\ntest/b.js\n")
			wantCalls := 1
			if mode == "parallel" {
				writeRunnerTestFile(t, constants.ParallelRunnersOutputPath, "2")
				writeRunnerTestFile(t, filepath.Join(constants.TestsSplitDir, "runner-0"), "test/a.js\n")
				writeRunnerTestFile(t, filepath.Join(constants.TestsSplitDir, "runner-1"), "test/b.js\n")
				wantCalls = 2
			}
			if mode == "ci-node" {
				t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE", "0")
				t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE_WORKERS", "2")
				writeRunnerTestFile(t, filepath.Join(constants.TestsSplitDir, "runner-0"), "test/a.js\ntest/b.js\n")
				viper.Reset()
				settings.Init()
				wantCalls = 2
			}

			planTags := map[string]string{
				constants.CIProviderName: "gitlab",
				constants.CIJobID:        "plan-123",
				constants.CIJobName:      "plan-{{nodeIndex}}",
				constants.CIJobURL:       "https://gitlab.com/project/-/jobs/123",
				constants.CIPipelineID:   "pipeline-456",
			}
			fw := &MockFramework{FrameworkName: "mocha"}
			p := &MockPlatform{PlatformName: "javascript", Framework: fw}
			tr := NewWithDependencies(p, fw, &fakePlanner{plan: planner.PlanMetadata{CITags: planTags}})
			if err := tr.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			calls := fw.GetRunTestsCalls()
			if len(calls) != wantCalls {
				t.Fatalf("RunTests calls = %d, want %d", len(calls), wantCalls)
			}
			for _, call := range calls {
				if got := call.EnvMap["_DD_TEST_PARALLELIZATION_ENABLED"]; got != "true" {
					t.Fatalf("parallelization enabled = %q, want true", got)
				}
				if got := call.EnvMap["_DD_TEST_PARALLELIZATION_PLAN_JOB_ID"]; got != "plan-123" {
					t.Fatalf("planning job ID = %q, want plan-123", got)
				}
				for envVar := range call.EnvMap {
					if strings.HasPrefix(envVar, "DD_TEST_PARALLELIZATION_") {
						t.Errorf("worker received public correlation variable %s", envVar)
					}
				}
				for tag, envVar := range constants.TestParallelizationPlanEnvVars {
					if got := call.EnvMap[envVar]; got != planTags[tag] {
						t.Errorf("%s = %q, want %q", envVar, got, planTags[tag])
					}
				}
			}
		})
	}
}

func TestRunBatch_LegacyPlanClearsStaleCorrelation(t *testing.T) {
	fw := &MockFramework{FrameworkName: "mocha"}
	workerEnv := map[string]string{constants.TestParallelizationEnabledEnvVar: "false", "CI_JOB_ID": "run-789"}
	for _, envVar := range constants.TestParallelizationPlanEnvVars {
		t.Setenv(envVar, "inherited-plan")
		workerEnv[envVar] = "worker-override"
	}
	if err := newTestExecutor(context.Background(), fw, workerEnv, roundRobinTestPlanner{}).runBatch([]string{"test.js"}, 0, 0); err != nil {
		t.Fatal(err)
	}
	env := fw.GetRunTestsCalls()[0].EnvMap
	if env[constants.TestParallelizationEnabledEnvVar] != "true" || env["CI_JOB_ID"] != "run-789" {
		t.Fatalf("unexpected worker environment: %v", env)
	}
	for _, envVar := range constants.TestParallelizationPlanEnvVars {
		if value, ok := env[envVar]; !ok || value != "" {
			t.Errorf("legacy plan must clear %s, got %q (present: %v)", envVar, value, ok)
		}
	}
	if workerEnv[constants.TestParallelizationEnabledEnvVar] != "false" {
		t.Fatal("modified the caller's worker environment")
	}
}
