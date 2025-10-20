package constants

import "path/filepath"

// PlanDirectory is the directory where ddtest stores its output files and context data
const PlanDirectory = ".testoptimization"

// Output file paths (using filepath.Join for cross-platform compatibility)
var TestFilesOutputPath = filepath.Join(PlanDirectory, "test-files.txt")
var SkippablePercentageOutputPath = filepath.Join(PlanDirectory, "skippable-percentage.txt")
var ParallelRunnersOutputPath = filepath.Join(PlanDirectory, "parallel-runners.txt")
var TestsSplitDir = filepath.Join(PlanDirectory, "tests-split")

// Platform specific output file paths
var RubyEnvOutputPath = filepath.Join(PlanDirectory, "ruby_env.json")

// Executor constants
const NodeIndexPlaceholder = "{{nodeIndex}}"
