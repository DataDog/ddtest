package constants

import "path/filepath"

// PlanDirectory is the directory where ddtest stores its output files and context data
const PlanDirectory = ".testoptimization"

const ManifestVersion = "1"

var ManifestPath = filepath.Join(PlanDirectory, "manifest.txt")

const TestOptimizationManifestFileEnvVar = "TEST_OPTIMIZATION_MANIFEST_FILE"

// Runner layout paths.
var RunnerDirectory = filepath.Join(PlanDirectory, "runner")
var TestFilesOutputPath = filepath.Join(RunnerDirectory, "test-files.txt")
var SkippablePercentageOutputPath = filepath.Join(RunnerDirectory, "skippable-percentage.txt")
var ParallelRunnersOutputPath = filepath.Join(RunnerDirectory, "parallel-runners.txt")
var TestsSplitDir = filepath.Join(RunnerDirectory, "tests-split")
var RunnerCacheDir = filepath.Join(RunnerDirectory, "cache")

// Legacy runner output paths are written for pre-1.0 compatibility only.
var LegacyTestFilesOutputPath = filepath.Join(PlanDirectory, "test-files.txt")
var LegacySkippablePercentageOutputPath = filepath.Join(PlanDirectory, "skippable-percentage.txt")
var LegacyParallelRunnersOutputPath = filepath.Join(PlanDirectory, "parallel-runners.txt")
var LegacyTestsSplitDir = filepath.Join(PlanDirectory, "tests-split")

// New library-facing backend cache paths.
var CacheDir = filepath.Join(PlanDirectory, "cache")
var HTTPCacheDir = filepath.Join(CacheDir, "http")

// Platform specific output file paths
var RubyEnvOutputPath = filepath.Join(PlanDirectory, "ruby_env.json")

// Executor constants
const (
	NodeIndexPlaceholder   = "{{nodeIndex}}"
	WorkerIndexPlaceholder = "{{workerIndex}}"
)
