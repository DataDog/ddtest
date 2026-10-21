package runner

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
)

func TestTestRunner_Run_EnablesParallelizationForEveryWorker(t *testing.T) {
	for _, mode := range []string{"sequential", "parallel", "ci-node"} {
		t.Run(mode, func(t *testing.T) {
			withRunnerTestSettings(t)
			t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV", "_DD_TEST_PARALLELIZATION_ENABLED=false;CI_JOB_ID=run-789")
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

				for envVar := range call.EnvMap {
					if strings.HasPrefix(envVar, "DD_TEST_PARALLELIZATION_") || strings.HasPrefix(envVar, "_DD_TEST_PARALLELIZATION_PLAN_") {
						t.Errorf("worker received obsolete correlation variable %s", envVar)
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
