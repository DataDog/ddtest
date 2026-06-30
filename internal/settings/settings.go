package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/cpuid/v2"
	"github.com/spf13/viper"
)

const (
	defaultCiNodeWorkers          = 1
	defaultParallelRunnerOverhead = 25 * time.Second
	ncpuCiNodeWorkers             = "ncpu"
	parallelRunnerOverheadEnv     = "DD_TEST_OPTIMIZATION_RUNNER_CI_JOB_OVERHEAD"
	testsLocationEnv              = "DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION"
	testsExcludePatternEnv        = "DD_TEST_OPTIMIZATION_RUNNER_TESTS_EXCLUDE_PATTERN"
	testDiscoveryCacheEnv         = "DD_TEST_OPTIMIZATION_RUNNER_TEST_DISCOVERY_CACHE"
	testSkippingModeEnv           = "DD_TESTOPTIMIZATION_TIA_TEST_SKIPPING_MODE"
	forceFullTestDiscoveryEnv     = "DD_TEST_OPTIMIZATION_RUNNER_FORCE_FULL_TEST_DISCOVERY"
	strictDiscoveryEnv            = "DD_TEST_OPTIMIZATION_RUNNER_STRICT_DISCOVERY"
	knapsackTestFilePatternEnv    = "KNAPSACK_PRO_TEST_FILE_PATTERN"
	knapsackTestFileExcludeEnv    = "KNAPSACK_PRO_TEST_FILE_EXCLUDE_PATTERN"
)

type TestSkippingLevel string

const (
	TestSkippingLevelTest  TestSkippingLevel = "test"
	TestSkippingLevelSuite TestSkippingLevel = "suite"
)

// DefaultParallelism returns the default parallelism value.
func DefaultParallelism() int {
	return PhysicalCPUCount()
}

// DefaultParallelRunnerOverhead returns the default modeled overhead for adding
// another parallel runner.
func DefaultParallelRunnerOverhead() time.Duration {
	return defaultParallelRunnerOverhead
}

// PhysicalCPUCount returns the number of physical CPU cores available to this process.
//
// It starts from runtime.GOMAXPROCS(0), which is the number of logical CPUs the
// Go scheduler may run on at once. That preserves explicit GOMAXPROCS settings
// and modern Go's container-aware CPU budget instead of using the host's
// full logical CPU count.
//
// It then converts that logical CPU budget to physical cores using CPU topology:
// if cpuid reports threads-per-core, it divides by that value with a ceiling.
// The ceiling is intentional: a one-logical-CPU quota still maps to one usable
// physical core, and odd CPU budgets such as 3 logical CPUs on a 2-thread/core
// machine should yield 2 physical cores instead of undercounting to 1. If
// threads-per-core is unavailable, it derives the same ratio from reported
// logical and physical core counts. If only the physical core count is known, it
// caps the result to that count. If topology is unavailable entirely, it falls
// back to the logical CPU budget, which is the safest non-zero answer.
//
// This is correct for ddtest's "ncpu" worker setting because the setting should
// opt into one worker per available physical execution core, not one worker per
// hyperthread. It also never returns less than 1, and never exceeds the process'
// available logical CPU budget.
func PhysicalCPUCount() int {
	return physicalCPUCount(runtime.GOMAXPROCS(0), cpuid.CPU.ThreadsPerCore, cpuid.CPU.PhysicalCores, cpuid.CPU.LogicalCores)
}

func physicalCPUCount(availableLogicalCPUs, threadsPerCore, detectedPhysicalCores, detectedLogicalCores int) int {
	if availableLogicalCPUs < 1 {
		availableLogicalCPUs = 1
	}
	if threadsPerCore > 1 {
		return ceilDiv(availableLogicalCPUs, threadsPerCore)
	}
	if detectedPhysicalCores > 0 && detectedLogicalCores > detectedPhysicalCores {
		detectedThreadsPerCore := ceilDiv(detectedLogicalCores, detectedPhysicalCores)
		if detectedThreadsPerCore > 1 {
			return ceilDiv(availableLogicalCPUs, detectedThreadsPerCore)
		}
	}
	if detectedPhysicalCores > 0 && detectedPhysicalCores < availableLogicalCPUs {
		return detectedPhysicalCores
	}
	return availableLogicalCPUs
}

func ceilDiv(numerator, denominator int) int {
	return (numerator + denominator - 1) / denominator
}

type Config struct {
	Platform               string            `mapstructure:"platform"`
	Framework              string            `mapstructure:"framework"`
	MinParallelism         int               `mapstructure:"min_parallelism"`
	MaxParallelism         int               `mapstructure:"max_parallelism"`
	ParallelRunnerOverhead time.Duration     `mapstructure:"parallel_runner_overhead" report:"CI job overhead"`
	WorkerEnv              string            `mapstructure:"worker_env"`
	CiNode                 int               `mapstructure:"ci_node"`
	CiNodeWorkers          int               `mapstructure:"ci_node_workers"`
	Command                string            `mapstructure:"command"`
	TestsLocation          string            `mapstructure:"tests_location"`
	TestsExcludePattern    string            `mapstructure:"tests_exclude_pattern"`
	TestDiscoveryCache     string            `mapstructure:"test_discovery_cache"`
	TestSkippingLevel      TestSkippingLevel `mapstructure:"test_skipping_mode"`
	ForceFullTestDiscovery bool              `mapstructure:"force_full_test_discovery"`
	StrictDiscovery        bool              `mapstructure:"strict_discovery"`
	RuntimeTags            string            `mapstructure:"runtime_tags"`
	ReportEnabled          bool              `mapstructure:"report_enabled"`
}

func DefaultConfig() Config {
	return Config{
		Platform:               "ruby",
		Framework:              "rspec",
		MinParallelism:         DefaultParallelism(),
		MaxParallelism:         DefaultParallelism(),
		ParallelRunnerOverhead: defaultParallelRunnerOverhead,
		WorkerEnv:              "",
		CiNode:                 -1,
		CiNodeWorkers:          defaultCiNodeWorkers,
		Command:                "",
		TestsLocation:          "",
		TestsExcludePattern:    "",
		TestDiscoveryCache:     "",
		TestSkippingLevel:      TestSkippingLevelTest,
		ForceFullTestDiscovery: false,
		StrictDiscovery:        false,
		RuntimeTags:            "",
		ReportEnabled:          true,
	}
}

var (
	config *Config
)

func Init() {
	viper.AutomaticEnv()
	viper.SetEnvPrefix("DD_TEST_OPTIMIZATION_RUNNER")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	if err := viper.BindEnv("parallel_runner_overhead", parallelRunnerOverheadEnv); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding parallel runner overhead env: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindEnv("tests_location", testsLocationEnv, knapsackTestFilePatternEnv); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding tests location env: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindEnv("tests_exclude_pattern", testsExcludePatternEnv, knapsackTestFileExcludeEnv); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding tests exclude pattern env: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindEnv("test_discovery_cache", testDiscoveryCacheEnv); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding test discovery cache env: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindEnv("test_skipping_mode", testSkippingModeEnv); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding test skipping mode env: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindEnv("force_full_test_discovery", forceFullTestDiscoveryEnv); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding force full test discovery env: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindEnv("strict_discovery", strictDiscoveryEnv); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding strict discovery env: %v\n", err)
		os.Exit(1)
	}

	setDefaults()

	ciNodeWorkers, err := ParseCiNodeWorkers(viper.GetString("ci_node_workers"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	viper.Set("ci_node_workers", ciNodeWorkers)
	parallelRunnerOverhead, err := ParseParallelRunnerOverhead(viper.GetString("parallel_runner_overhead"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	viper.Set("parallel_runner_overhead", parallelRunnerOverhead)
	viper.Set("test_skipping_mode", NormalizeTestSkippingLevel(TestSkippingLevel(viper.GetString("test_skipping_mode"))))

	config = &Config{}
	if err := viper.Unmarshal(config); err != nil {
		fmt.Fprintf(os.Stderr, "Error unmarshaling config: %v\n", err)
		os.Exit(1)
	}
}

func setDefaults() {
	defaults := DefaultConfig()
	viper.SetDefault("platform", defaults.Platform)
	viper.SetDefault("framework", defaults.Framework)
	viper.SetDefault("min_parallelism", defaults.MinParallelism)
	viper.SetDefault("max_parallelism", defaults.MaxParallelism)
	viper.SetDefault("parallel_runner_overhead", defaults.ParallelRunnerOverhead.String())
	viper.SetDefault("worker_env", defaults.WorkerEnv)
	viper.SetDefault("ci_node", defaults.CiNode)
	viper.SetDefault("ci_node_workers", strconv.Itoa(defaults.CiNodeWorkers))
	viper.SetDefault("command", defaults.Command)
	viper.SetDefault("tests_location", defaults.TestsLocation)
	viper.SetDefault("tests_exclude_pattern", defaults.TestsExcludePattern)
	viper.SetDefault("test_discovery_cache", defaults.TestDiscoveryCache)
	viper.SetDefault("test_skipping_mode", defaults.TestSkippingLevel.String())
	viper.SetDefault("force_full_test_discovery", defaults.ForceFullTestDiscovery)
	viper.SetDefault("strict_discovery", defaults.StrictDiscovery)
	viper.SetDefault("runtime_tags", defaults.RuntimeTags)
	viper.SetDefault("report_enabled", defaults.ReportEnabled)
}

// NormalizeTestSkippingLevel accepts only the backend-supported TIA skipping modes.
// Invalid or empty values fall back to test-level skipping.
func NormalizeTestSkippingLevel(level TestSkippingLevel) TestSkippingLevel {
	switch TestSkippingLevel(strings.TrimSpace(string(level))) {
	case TestSkippingLevelSuite:
		return TestSkippingLevelSuite
	default:
		return TestSkippingLevelTest
	}
}

func (level TestSkippingLevel) String() string {
	return string(level)
}

// ParseParallelRunnerOverhead resolves the modeled overhead for adding another
// parallel runner. It accepts Go duration strings such as "25s", "1m",
// "1500ms", or "0s" to disable the runner-overhead bias.
func ParseParallelRunnerOverhead(value string) (time.Duration, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return defaultParallelRunnerOverhead, nil
	}

	overhead, err := time.ParseDuration(normalized)
	if err != nil {
		return 0, fmt.Errorf("ci-job-overhead must be a duration like %q, %q, %q, or %q, got %q", "25s", "1m", "1500ms", "0s", value)
	}
	if overhead < 0 {
		return 0, fmt.Errorf("ci-job-overhead must be non-negative, got %q", value)
	}
	return overhead, nil
}

// ParseCiNodeWorkers resolves the ci_node_workers setting from either a positive integer
// or the "ncpu" magic value.
func ParseCiNodeWorkers(value string) (int, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return defaultCiNodeWorkers, nil
	}
	if strings.EqualFold(normalized, ncpuCiNodeWorkers) {
		return PhysicalCPUCount(), nil
	}

	workers, err := strconv.Atoi(normalized)
	if err != nil {
		return 0, fmt.Errorf("ci_node_workers must be a positive integer or %q, got %q", ncpuCiNodeWorkers, value)
	}
	if workers < 1 {
		return 0, fmt.Errorf("ci_node_workers must be greater than 0, got %d", workers)
	}
	return workers, nil
}

func Get() *Config {
	if config == nil {
		Init()
	}
	return config
}

func GetPlatform() string {
	return Get().Platform
}

func GetFramework() string {
	return Get().Framework
}

func GetMinParallelism() int {
	return Get().MinParallelism
}

func GetMaxParallelism() int {
	return Get().MaxParallelism
}

func GetParallelRunnerOverhead() time.Duration {
	return Get().ParallelRunnerOverhead
}

func GetWorkerEnv() string {
	return Get().WorkerEnv
}

func GetCiNode() int {
	return Get().CiNode
}

func GetCiNodeWorkers() int {
	return Get().CiNodeWorkers
}

func GetCommand() string {
	return Get().Command
}

func GetTestsLocation() string {
	return Get().TestsLocation
}

func GetTestsExcludePattern() string {
	return Get().TestsExcludePattern
}

func GetTestDiscoveryCache() string {
	return Get().TestDiscoveryCache
}

func GetTestSkippingLevel() TestSkippingLevel {
	return Get().TestSkippingLevel
}

func GetForceFullTestDiscovery() bool {
	return Get().ForceFullTestDiscovery
}

func GetStrictDiscovery() bool {
	return Get().StrictDiscovery
}

func GetRuntimeTags() string {
	return Get().RuntimeTags
}

func GetReportEnabled() bool {
	return Get().ReportEnabled
}

// GetRuntimeTagsMap parses the runtime_tags setting as JSON and returns it as a map.
// Returns nil if runtime_tags is empty or not set.
// Returns an error if the JSON is invalid.
func GetRuntimeTagsMap() (map[string]string, error) {
	runtimeTags := GetRuntimeTags()
	if runtimeTags == "" {
		return nil, nil
	}

	var tagsMap map[string]string
	if err := json.Unmarshal([]byte(runtimeTags), &tagsMap); err != nil {
		return nil, fmt.Errorf("failed to parse runtime-tags as JSON: %w. The runtime tags value was: %s", err, runtimeTags)
	}
	return tagsMap, nil
}

// GetWorkerEnvMap parses the worker_env setting and returns it as a map
// The format is "KEY=value;KEY2=value2"
func GetWorkerEnvMap() map[string]string {
	workerEnv := GetWorkerEnv()
	if workerEnv == "" {
		return make(map[string]string)
	}

	workerEnvMap := make(map[string]string)
	for pair := range strings.SplitSeq(workerEnv, ";") {
		if parts := strings.SplitN(pair, "=", 2); len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if key != "" {
				workerEnvMap[key] = value
			}
		}
	}
	return workerEnvMap
}
