package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	ciConstants "github.com/DataDog/ddtest/civisibility/constants"
	ciUtils "github.com/DataDog/ddtest/civisibility/utils"
	"github.com/DataDog/ddtest/internal/constants"
)

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()

	previousValue, wasSet := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("failed to unset %s: %v", key, err)
	}

	t.Cleanup(func() {
		if wasSet {
			_ = os.Setenv(key, previousValue)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func chdirForTest(t *testing.T, dir string) {
	t.Helper()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir to %s: %v", dir, err)
	}

	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})
}

func TestRunTestBatch_DefaultTestSessionName(t *testing.T) {
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)
	unsetEnvForTest(t, ciConstants.CIVisibilityTestSessionNameEnvironmentVariable)
	unsetEnvForTest(t, "DD_SERVICE")
	t.Setenv("DD_GIT_REPOSITORY_URL", "https://github.com/DataDog/orders.git")

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	err := runTestBatch(context.Background(), mockFramework, []string{"test/file1_test.rb"}, map[string]string{}, 2, 4)
	if err != nil {
		t.Fatalf("runTestBatch() should not return error, got: %v", err)
	}

	call := mockFramework.GetRunTestsCalls()[0]
	sessionName := call.EnvMap[ciConstants.CIVisibilityTestSessionNameEnvironmentVariable]
	if sessionName != "orders-node-2-worker-4" {
		t.Errorf("Expected default DD_TEST_SESSION_NAME=orders-node-2-worker-4, got %s", sessionName)
	}
}

func TestRunTestBatch_DefaultTestSessionNameUsesDDService(t *testing.T) {
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)
	unsetEnvForTest(t, ciConstants.CIVisibilityTestSessionNameEnvironmentVariable)
	t.Setenv("DD_SERVICE", "billing")
	t.Setenv("DD_GIT_REPOSITORY_URL", "https://github.com/DataDog/orders.git")

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	err := runTestBatch(context.Background(), mockFramework, []string{"test/file1_test.rb"}, map[string]string{}, 3, 7)
	if err != nil {
		t.Fatalf("runTestBatch() should not return error, got: %v", err)
	}

	call := mockFramework.GetRunTestsCalls()[0]
	sessionName := call.EnvMap[ciConstants.CIVisibilityTestSessionNameEnvironmentVariable]
	if sessionName != "billing-node-3-worker-7" {
		t.Errorf("Expected default DD_TEST_SESSION_NAME=billing-node-3-worker-7, got %s", sessionName)
	}
}

func TestRunTestBatch_UserTestSessionNameSupportsPlaceholders(t *testing.T) {
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)
	t.Setenv(ciConstants.CIVisibilityTestSessionNameEnvironmentVariable, "custom-node-{{nodeIndex}}-worker-{{workerIndex}}")

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	err := runTestBatch(context.Background(), mockFramework, []string{"test/file1_test.rb"}, map[string]string{}, 5, 8)
	if err != nil {
		t.Fatalf("runTestBatch() should not return error, got: %v", err)
	}

	call := mockFramework.GetRunTestsCalls()[0]
	sessionName := call.EnvMap[ciConstants.CIVisibilityTestSessionNameEnvironmentVariable]
	if sessionName != "custom-node-5-worker-8" {
		t.Errorf("Expected DD_TEST_SESSION_NAME placeholders to be replaced, got %s", sessionName)
	}
}

func TestRunTestBatch_WorkerEnvTestSessionNameTakesPrecedence(t *testing.T) {
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)
	t.Setenv(ciConstants.CIVisibilityTestSessionNameEnvironmentVariable, "outer-session")

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}
	workerEnvMap := map[string]string{
		ciConstants.CIVisibilityTestSessionNameEnvironmentVariable: "worker-node-{{nodeIndex}}-worker-{{workerIndex}}",
	}

	err := runTestBatch(context.Background(), mockFramework, []string{"test/file1_test.rb"}, workerEnvMap, 9, 1)
	if err != nil {
		t.Fatalf("runTestBatch() should not return error, got: %v", err)
	}

	call := mockFramework.GetRunTestsCalls()[0]
	sessionName := call.EnvMap[ciConstants.CIVisibilityTestSessionNameEnvironmentVariable]
	if sessionName != "worker-node-9-worker-1" {
		t.Errorf("Expected worker env DD_TEST_SESSION_NAME to take precedence, got %s", sessionName)
	}
}

func TestRunTestBatch_DefaultManifestFile(t *testing.T) {
	chdirForTest(t, t.TempDir())
	unsetEnvForTest(t, constants.TestOptimizationManifestFileEnvVar)
	unsetEnvForTest(t, "DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES")

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	err := runTestBatch(context.Background(), mockFramework, []string{"test/file1_test.rb"}, map[string]string{}, 2, 4)
	if err != nil {
		t.Fatalf("runTestBatch() should not return error, got: %v", err)
	}

	call := mockFramework.GetRunTestsCalls()[0]
	expectedManifestPath, err := filepath.Abs(constants.ManifestPath)
	if err != nil {
		t.Fatalf("filepath.Abs() should not return error, got: %v", err)
	}

	if call.EnvMap[constants.TestOptimizationManifestFileEnvVar] != expectedManifestPath {
		t.Errorf("Expected %s=%s, got %s",
			constants.TestOptimizationManifestFileEnvVar,
			expectedManifestPath,
			call.EnvMap[constants.TestOptimizationManifestFileEnvVar])
	}

	if _, ok := call.EnvMap["DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES"]; ok {
		t.Error("Expected DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES to not be set by ddtest run")
	}
}

func TestRunTestBatch_ManifestFileUsesProcessEnv(t *testing.T) {
	t.Setenv(constants.TestOptimizationManifestFileEnvVar, "/tmp/custom-manifest.txt")

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	err := runTestBatch(context.Background(), mockFramework, []string{"test/file1_test.rb"}, map[string]string{}, 2, 4)
	if err != nil {
		t.Fatalf("runTestBatch() should not return error, got: %v", err)
	}

	call := mockFramework.GetRunTestsCalls()[0]
	if call.EnvMap[constants.TestOptimizationManifestFileEnvVar] != "/tmp/custom-manifest.txt" {
		t.Errorf("Expected process env manifest file to be preserved, got %s", call.EnvMap[constants.TestOptimizationManifestFileEnvVar])
	}
}

func TestRunTestBatch_WorkerEnvManifestFileTakesPrecedence(t *testing.T) {
	t.Setenv(constants.TestOptimizationManifestFileEnvVar, "/tmp/process-manifest.txt")

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}
	workerEnvMap := map[string]string{
		constants.TestOptimizationManifestFileEnvVar: "/tmp/worker-manifest.txt",
	}

	err := runTestBatch(context.Background(), mockFramework, []string{"test/file1_test.rb"}, workerEnvMap, 2, 4)
	if err != nil {
		t.Fatalf("runTestBatch() should not return error, got: %v", err)
	}

	call := mockFramework.GetRunTestsCalls()[0]
	if call.EnvMap[constants.TestOptimizationManifestFileEnvVar] != "/tmp/worker-manifest.txt" {
		t.Errorf("Expected worker env manifest file to take precedence, got %s", call.EnvMap[constants.TestOptimizationManifestFileEnvVar])
	}
}
