package constants

import (
	"path/filepath"
	"time"
)

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

const TestOptimizationPlanCacheFile = "test_suite_durations.json"

const DefaultTestFileWeight = int(time.Second / time.Millisecond)

const RunModeCINode = "CI node"

// ItrCorrelationIDTag defines the correlation ID for intelligent test runs.
const ItrCorrelationIDTag = "itr_correlation_id"

// Library-facing backend cache paths.
var HTTPCacheDir = filepath.Join(PlanDirectory, "cache", "http")

// Platform specific output file paths
var RubyEnvOutputPath = filepath.Join(PlanDirectory, "ruby_env.json")
