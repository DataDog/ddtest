package constants

import "path/filepath"

// PlanDirectory is the directory where ddtest stores its output files and context data
const PlanDirectory = ".testoptimization"

const ManifestVersion = "1"

var ManifestPath = filepath.Join(PlanDirectory, "manifest.txt")

// Runner layout paths.
var RunnerDirectory = filepath.Join(PlanDirectory, "runner")
var TestFilesOutputPath = filepath.Join(RunnerDirectory, "test-files.txt")
var SkippablePercentageOutputPath = filepath.Join(RunnerDirectory, "skippable-percentage.txt")
var ParallelRunnersOutputPath = filepath.Join(RunnerDirectory, "parallel-runners.txt")
var TestsSplitDir = filepath.Join(RunnerDirectory, "tests-split")
var RunnerCacheDir = filepath.Join(RunnerDirectory, "cache")

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
