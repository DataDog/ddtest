package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
)

func TestTestRunner_Run_EnablesParallelizationForEveryWorker(t *testing.T) {
	for _, mode := range []string{"sequential", "parallel", "ci-node"} {
		t.Run(mode, func(t *testing.T) {
			withRunnerTestSettings(t)
			t.Setenv("_DD_TEST_PARALLELIZATION_PLAN_JOB_ID", "stale-process-plan")
			t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV", "_DD_TEST_PARALLELIZATION_ENABLED=false;CI_JOB_ID=run-789;_DD_TEST_PARALLELIZATION_PLAN_JOB_URL=stale-worker-plan")
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

			fw := &MockFramework{FrameworkName: "mocha"}
			p := &MockPlatform{PlatformName: "javascript", Framework: fw}
			tr := NewWithDependencies(p, fw, &fakePlanner{})
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
				if got := call.EnvMap["CI_JOB_ID"]; got != "run-789" {
					t.Fatalf("execution job ID = %q, want run-789", got)
				}
				wantManifest, err := filepath.Abs(constants.ManifestPath)
				if err != nil {
					t.Fatal(err)
				}
				if got := call.EnvMap[constants.TestOptimizationManifestFileEnvVar]; got != wantManifest {
					t.Fatalf("manifest = %q, want %q", got, wantManifest)
				}
				wantMetadata := filepath.Join(filepath.Dir(wantManifest), "plan-metadata.json")
				if got := call.EnvMap[constants.TestParallelizationMetadataFileEnvVar]; got != wantMetadata {
					t.Fatalf("metadata file = %q, want %q", got, wantMetadata)
				}

				for _, key := range []string{"_DD_TEST_PARALLELIZATION_PLAN_JOB_ID", "_DD_TEST_PARALLELIZATION_PLAN_JOB_URL"} {
					if value, present := call.EnvMap[key]; !present || value != "" {
						t.Errorf("retired variable %s was not cleared", key)
					}
				}

				for envVar := range call.EnvMap {
					if strings.HasPrefix(envVar, "DD_TEST_PARALLELIZATION_") {
						t.Errorf("worker received public correlation variable %s", envVar)
					}
					if strings.HasPrefix(envVar, "_DD_TEST_PARALLELIZATION_PLAN_") && call.EnvMap[envVar] != "" {
						t.Errorf("worker received a stale value for %s", envVar)
					}
				}
			}
		})
	}
}

func TestRunBatch_EnablesParallelizationWithoutPlanMetadata(t *testing.T) {
	fw := &MockFramework{FrameworkName: "mocha"}
	workerEnv := map[string]string{constants.TestParallelizationEnabledEnvVar: "false", "CI_JOB_ID": "run-789"}
	if err := newTestExecutor(context.Background(), fw, workerEnv, roundRobinTestPlanner{}).runBatch([]string{"test.js"}, 0, 0); err != nil {
		t.Fatal(err)
	}
	env := fw.GetRunTestsCalls()[0].EnvMap
	if env[constants.TestParallelizationEnabledEnvVar] != "true" || env["CI_JOB_ID"] != "run-789" {
		t.Fatalf("unexpected worker environment: %v", env)
	}
	if workerEnv[constants.TestParallelizationEnabledEnvVar] != "false" {
		t.Fatal("modified the caller's worker environment")
	}
}

func TestRunBatch_ClearsRetiredPlanningEnvironment(t *testing.T) {
	legacyVars := []string{
		"_DD_TEST_PARALLELIZATION_PLAN_PROVIDER_NAME",
		"_DD_TEST_PARALLELIZATION_PLAN_PIPELINE_ID",
		"_DD_TEST_PARALLELIZATION_PLAN_PIPELINE_NAME",
		"_DD_TEST_PARALLELIZATION_PLAN_PIPELINE_DISPLAY_NAME",
		"_DD_TEST_PARALLELIZATION_PLAN_PIPELINE_NUMBER",
		"_DD_TEST_PARALLELIZATION_PLAN_PIPELINE_URL",
		"_DD_TEST_PARALLELIZATION_PLAN_JOB_ID",
		"_DD_TEST_PARALLELIZATION_PLAN_JOB_NAME",
		"_DD_TEST_PARALLELIZATION_PLAN_JOB_URL",
		"_DD_TEST_PARALLELIZATION_PLAN_STAGE_NAME",
	}
	for _, source := range []string{"process", "worker", "both"} {
		t.Run(source, func(t *testing.T) {
			workerEnv := map[string]string{"CI_JOB_ID": "run-789"}
			for _, key := range legacyVars {
				unsetEnvForTest(t, key)
				if source != "worker" {
					t.Setenv(key, "stale-process-plan")
				}
				if source != "process" {
					workerEnv[key] = "stale-worker-plan"
				}
			}
			framework := &MockFramework{FrameworkName: "mocha"}
			if err := newTestExecutor(context.Background(), framework, workerEnv, roundRobinTestPlanner{}).runBatch([]string{"test.js"}, 0, 0); err != nil {
				t.Fatal(err)
			}
			env := framework.GetRunTestsCalls()[0].EnvMap
			for _, key := range legacyVars {
				if value, present := env[key]; !present || value != "" {
					t.Errorf("worker override must clear %s", key)
				}
				if source != "process" && workerEnv[key] != "stale-worker-plan" {
					t.Errorf("modified the caller's value for %s", key)
				}
			}

			// Exercise the real inherited-environment merge used to launch tests.
			executor := &ext.DefaultCommandExecutor{}
			output, err := executor.CombinedOutput(context.Background(), "env", nil, env)
			if err != nil {
				t.Fatal(err)
			}
			childEnv := make(map[string]string)
			for _, entry := range strings.Split(string(output), "\n") {
				key, value, _ := strings.Cut(entry, "=")
				childEnv[key] = value
			}
			for _, key := range legacyVars {
				if childEnv[key] != "" {
					t.Errorf("child process inherited stale planning variable %s", key)
				}
			}
			if childEnv["CI_JOB_ID"] != "run-789" || childEnv[constants.TestParallelizationEnabledEnvVar] != "true" {
				t.Fatal("execution job identity or enabled marker changed")
			}
			if !filepath.IsAbs(childEnv[constants.TestParallelizationMetadataFileEnvVar]) {
				t.Fatal("metadata locator must remain absolute")
			}
			if source != "worker" && os.Getenv(legacyVars[0]) != "stale-process-plan" {
				t.Fatal("modified the parent process environment")
			}
		})
	}
}
