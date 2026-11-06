package settings

import (
	"encoding/json"
	"fmt"
	"math"
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
	defaultCiNodeSpeedup          = 1.0
	defaultParallelRunnerOverhead = 25 * time.Second
	defaultTargetTime             = 0 * time.Second
	ncpuCiNodeWorkers             = "ncpu"
	envPrefix                     = "DD_TEST_OPTIMIZATION_RUNNER"
	platformEnv                   = "DD_TEST_OPTIMIZATION_RUNNER_PLATFORM"
	frameworkEnv                  = "DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK"
	minParallelismEnv             = "DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM"
	maxParallelismEnv             = "DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM"
	parallelRunnerOverheadEnv     = "DD_TEST_OPTIMIZATION_RUNNER_CI_JOB_OVERHEAD"
	targetTimeEnv                 = "DD_TEST_OPTIMIZATION_RUNNER_TARGET_TIME"
	workerEnv                     = "DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV"
	ciNodeEnv                     = "DD_TEST_OPTIMIZATION_RUNNER_CI_NODE"
	ciNodeWorkersEnv              = "DD_TEST_OPTIMIZATION_RUNNER_CI_NODE_WORKERS"
	ciNodeSpeedupEnv              = "DD_TEST_OPTIMIZATION_RUNNER_CI_NODE_SPEEDUP"
	ciNodeFileOverheadEnv         = "DD_TEST_OPTIMIZATION_RUNNER_CI_NODE_FILE_OVERHEAD"
	commandEnv                    = "DD_TEST_OPTIMIZATION_RUNNER_COMMAND"
	testsLocationEnv              = "DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION"
	testsExcludePatternEnv        = "DD_TEST_OPTIMIZATION_RUNNER_TESTS_EXCLUDE_PATTERN"
	testDiscoveryCacheEnv         = "DD_TEST_OPTIMIZATION_RUNNER_TEST_DISCOVERY_CACHE"
	testSkippingModeEnv           = "DD_TESTOPTIMIZATION_TIA_TEST_SKIPPING_MODE"
	forceFullTestDiscoveryEnv     = "DD_TEST_OPTIMIZATION_RUNNER_FORCE_FULL_TEST_DISCOVERY"
	strictDiscoveryEnv            = "DD_TEST_OPTIMIZATION_RUNNER_STRICT_DISCOVERY"
	runtimeTagsEnv                = "DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS"
	runtimeTagsAliasEnv           = "DD_TEST_OPTIMIZATION_RUNTIME_TAGS"
	reportEnabledEnv              = "DD_TEST_OPTIMIZATION_RUNNER_REPORT_ENABLED"
	knapsackTestFilePatternEnv    = "KNAPSACK_PRO_TEST_FILE_PATTERN"
	knapsackTestFileExcludeEnv    = "KNAPSACK_PRO_TEST_FILE_EXCLUDE_PATTERN"
)

type TestSkippingLevel string

const (
	TestSkippingLevelTest  TestSkippingLevel = "test"
	TestSkippingLevelSuite TestSkippingLevel = "suite"
)

// PayloadsTransport is how ddtest run's workers reach the agent that
// records their payloads with --record-payloads.
type PayloadsTransport string

const (
	// PayloadsTransportAuto uses a Unix domain socket when the
	// platform's tracing library reaches an agent on one, and TCP otherwise.
	PayloadsTransportAuto PayloadsTransport = "auto"
	PayloadsTransportTCP  PayloadsTransport = "tcp"
	PayloadsTransportUnix PayloadsTransport = "unix"
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

// DefaultTargetTime returns the default target wall time for a runner split.
func DefaultTargetTime() time.Duration {
	return defaultTargetTime
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
	ParallelRunnerOverhead time.Duration     `mapstructure:"parallel_runner_overhead"`
	TargetTime             time.Duration     `mapstructure:"target_time"`
	WorkerEnv              string            `mapstructure:"worker_env"`
	CiNode                 int               `mapstructure:"ci_node"`
	CiNodeWorkers          int               `mapstructure:"ci_node_workers"`
	CiNodeSpeedup          float64           `mapstructure:"ci_node_speedup"`
	CiNodeFileOverhead     time.Duration     `mapstructure:"ci_node_file_overhead"`
	Command                string            `mapstructure:"command"`
	VitestConfig           string            `mapstructure:"vitest_config"`
	TestsLocation          string            `mapstructure:"tests_location"`
	TestsSelectionPattern  string            `mapstructure:"-"` // CLI positional scope, applied after discovery.
	TestsExcludePattern    string            `mapstructure:"tests_exclude_pattern"`
	TestDiscoveryCache     string            `mapstructure:"test_discovery_cache"`
	TestSkippingLevel      TestSkippingLevel `mapstructure:"test_skipping_mode"`
	ForceFullTestDiscovery bool              `mapstructure:"force_full_test_discovery"`
	StrictDiscovery        bool              `mapstructure:"strict_discovery"`
	RuntimeTags            string            `mapstructure:"runtime_tags"`
	ReportEnabled          bool              `mapstructure:"report_enabled"`
	RecordPayloads         string            `mapstructure:"record_payloads"`
	PayloadsTransport      PayloadsTransport `mapstructure:"record_payloads_transport"`
	DurationsFile          string            `mapstructure:"durations_file"`
	TestDurationsFile      string            `mapstructure:"test_durations_file"`
	SplitFilesOver         time.Duration     `mapstructure:"split_files_over"`
}

var (
	config *Config
)

func Init() {
	viper.AutomaticEnv()
	viper.SetEnvPrefix(envPrefix)
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	if err := viper.BindEnv("parallel_runner_overhead", parallelRunnerOverheadEnv); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding parallel runner overhead env: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindEnv("target_time", targetTimeEnv); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding target time env: %v\n", err)
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
	if err := viper.BindEnv("runtime_tags", runtimeTagsEnv, runtimeTagsAliasEnv); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding runtime tags env: %v\n", err)
		os.Exit(1)
	}

	setDefaults()

	ciNodeWorkers, err := ParseCiNodeWorkers(viper.GetString("ci_node_workers"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	viper.Set("ci_node_workers", ciNodeWorkers)
	parallelRunnerOverhead, err := ParseNonNegativeDurationSetting(
		viper.GetString("parallel_runner_overhead"),
		defaultParallelRunnerOverhead,
		"ci-job-overhead",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	viper.Set("parallel_runner_overhead", parallelRunnerOverhead)
	ciNodeSpeedup, err := ParseCiNodeSpeedup(viper.GetString("ci_node_speedup"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	viper.Set("ci_node_speedup", ciNodeSpeedup)
	ciNodeFileOverhead, err := ParseNonNegativeDurationSetting(viper.GetString("ci_node_file_overhead"), 0, "ci-node-file-overhead")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	viper.Set("ci_node_file_overhead", ciNodeFileOverhead)
	splitFilesOver, err := ParseNonNegativeDurationSetting(viper.GetString("split_files_over"), 0, "split-files-over")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	viper.Set("split_files_over", splitFilesOver)
	targetTime, err := ParseNonNegativeDurationSetting(
		viper.GetString("target_time"),
		defaultTargetTime,
		"target-time",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	viper.Set("target_time", targetTime)
	viper.Set("test_skipping_mode", NormalizeTestSkippingLevel(TestSkippingLevel(viper.GetString("test_skipping_mode"))))
	recordPayloadsTransport, err := ParseRecordPayloadsTransport(viper.GetString("record_payloads_transport"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	viper.Set("record_payloads_transport", recordPayloadsTransport)

	config = &Config{}
	if err := viper.Unmarshal(config); err != nil {
		fmt.Fprintf(os.Stderr, "Error unmarshaling config: %v\n", err)
		os.Exit(1)
	}
}

func setDefaults() {
	viper.SetDefault("platform", "")
	viper.SetDefault("framework", "")
	viper.SetDefault("min_parallelism", DefaultParallelism())
	viper.SetDefault("max_parallelism", DefaultParallelism())
	viper.SetDefault("parallel_runner_overhead", defaultParallelRunnerOverhead.String())
	viper.SetDefault("target_time", defaultTargetTime.String())
	viper.SetDefault("worker_env", "")
	viper.SetDefault("ci_node", -1)
	viper.SetDefault("ci_node_workers", strconv.Itoa(defaultCiNodeWorkers))
	viper.SetDefault("ci_node_speedup", strconv.FormatFloat(defaultCiNodeSpeedup, 'f', -1, 64))
	viper.SetDefault("ci_node_file_overhead", "0s")
	viper.SetDefault("command", "")
	viper.SetDefault("vitest_config", "")
	viper.SetDefault("tests_location", "")
	viper.SetDefault("tests_exclude_pattern", "")
	viper.SetDefault("test_discovery_cache", "")
	viper.SetDefault("test_skipping_mode", TestSkippingLevelTest)
	viper.SetDefault("force_full_test_discovery", false)
	viper.SetDefault("strict_discovery", false)
	viper.SetDefault("runtime_tags", "")
	viper.SetDefault("report_enabled", true)
	viper.SetDefault("record_payloads", "")
	viper.SetDefault("record_payloads_transport", string(PayloadsTransportAuto))
	viper.SetDefault("durations_file", "")
	viper.SetDefault("test_durations_file", "")
	viper.SetDefault("split_files_over", "0s")
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

// ParseRecordPayloadsTransport resolves the record_payloads_transport
// setting: auto, tcp or unix, auto when empty.
func ParseRecordPayloadsTransport(value string) (PayloadsTransport, error) {
	switch transport := PayloadsTransport(strings.ToLower(strings.TrimSpace(value))); transport {
	case "":
		return PayloadsTransportAuto, nil
	case PayloadsTransportAuto, PayloadsTransportTCP, PayloadsTransportUnix:
		return transport, nil
	default:
		return "", fmt.Errorf("record_payloads_transport must be auto, tcp or unix, got %q", value)
	}
}

// ParseNonNegativeDurationSetting resolves a duration setting from a Go
// duration string such as "25s", "1m", "1500ms", or "0s".
func ParseNonNegativeDurationSetting(value string, defaultValue time.Duration, settingName string) (time.Duration, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return defaultValue, nil
	}

	duration, err := time.ParseDuration(normalized)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration like %q, %q, %q, or %q, got %q", settingName, "25s", "1m", "1500ms", "0s", value)
	}
	if duration < 0 {
		return 0, fmt.Errorf("%s must be non-negative, got %q", settingName, value)
	}
	return duration, nil
}

// ParseCiNodeWorkers resolves the ci_node_workers setting from either a positive integer
// or the "ncpu" magic value.
// ParseCiNodeSpeedup resolves the ci_node_speedup setting, a number of at
// least 1.
func ParseCiNodeSpeedup(value string) (float64, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return defaultCiNodeSpeedup, nil
	}
	speedup, err := strconv.ParseFloat(normalized, 64)
	if err != nil || math.IsNaN(speedup) || speedup < 1 {
		return 0, fmt.Errorf("ci_node_speedup must be a number of at least 1, got %q", value)
	}
	return speedup, nil
}

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

func GetTargetTime() time.Duration {
	return Get().TargetTime
}

func GetWorkerEnv() string {
	return Get().WorkerEnv
}

func GetCiNode() int {
	return Get().CiNode
}

// GetCiNodeSpeedup is how many times faster than one test process a CI node
// runs its files, as with pytest-xdist workers in the test command. Planning
// divides a node's test time by it.
func GetCiNodeSpeedup() float64 {
	return Get().CiNodeSpeedup
}

// GetCiNodeFileOverhead is what planning adds to a CI node for every test
// file it gets, such as the time to collect it.
func GetCiNodeFileOverhead() time.Duration {
	return Get().CiNodeFileOverhead
}

func GetCiNodeWorkers() int {
	return Get().CiNodeWorkers
}

func GetCommand() string {
	return Get().Command
}

func GetVitestConfig() string {
	return Get().VitestConfig
}

func GetTestsLocation() string {
	return Get().TestsLocation
}

func GetTestsSelectionPattern() string {
	return Get().TestsSelectionPattern
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

// GetRecordPayloads is the directory where ddtest run saves the workers' Test
// Optimization payloads as files, or empty when the workers send them to
// Datadog as usual.
func GetRecordPayloads() string {
	return Get().RecordPayloads
}

// GetRecordPayloadsTransport is how ddtest run's workers reach the agent
// that records their payloads.
func GetRecordPayloadsTransport() PayloadsTransport {
	return Get().PayloadsTransport
}

// GetDurationsFile is a JSON file of suite durations that planning uses
// instead of fetching them from Datadog, or empty to fetch them as usual.
func GetDurationsFile() string {
	return Get().DurationsFile
}

// GetTestDurationsFile is a JSON file of per-test durations that lets
// planning split long test files by test, or empty.
func GetTestDurationsFile() string {
	return Get().TestDurationsFile
}

// GetSplitFilesOver is how long a test file may be expected to take before
// planning splits it by test, with GetTestDurationsFile, or 0 to never split.
func GetSplitFilesOver() time.Duration {
	return Get().SplitFilesOver
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
