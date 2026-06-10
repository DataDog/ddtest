package constants

import "path/filepath"

// PlanDirectory is the directory where ddtest stores its output files and context data
const PlanDirectory = ".testoptimization"

const ManifestVersion = "2"

var ManifestPath = filepath.Join(PlanDirectory, "manifest.txt")

const TestOptimizationManifestFileEnvVar = "TEST_OPTIMIZATION_MANIFEST_FILE"
const DDTestOptimizationManifestFileEnvVar = "DD_TEST_OPTIMIZATION_MANIFEST_FILE"

// Runner layout paths.
var RunnerDirectory = filepath.Join(PlanDirectory, "runner")
var TestFilesOutputPath = filepath.Join(RunnerDirectory, "test-files.txt")
var SkippablePercentageOutputPath = filepath.Join(RunnerDirectory, "skippable-percentage.txt")
var ParallelRunnersOutputPath = filepath.Join(RunnerDirectory, "parallel-runners.txt")
var TestsSplitDir = filepath.Join(RunnerDirectory, "tests-split")
var RunnerCacheDir = filepath.Join(RunnerDirectory, "cache")

// Library-facing backend cache paths.
var HTTPCacheDir = filepath.Join(PlanDirectory, "cache", "http")

// Platform specific output file paths
var RubyEnvOutputPath = filepath.Join(PlanDirectory, "ruby_env.json")
var PythonEnvOutputPath = filepath.Join(PlanDirectory, "python_env.json")

// Executor constants
const (
	NodeIndexPlaceholder   = "{{nodeIndex}}"
	WorkerIndexPlaceholder = "{{workerIndex}}"
)
