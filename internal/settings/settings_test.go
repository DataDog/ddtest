package settings

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

const expectedDefaultParallelRunnerOverhead = 25 * time.Second
const expectedDefaultTargetTime = 0 * time.Second

func TestDefaultParallelism(t *testing.T) {
	result := DefaultParallelism()
	expected := PhysicalCPUCount()

	if result != expected {
		t.Errorf("expected DefaultParallelism() to return %d (PhysicalCPUCount()), got %d", expected, result)
	}
	if result < 1 {
		t.Errorf("expected DefaultParallelism() to be at least 1, got %d", result)
	}
}

func TestDefaultParallelRunnerOverhead(t *testing.T) {
	if DefaultParallelRunnerOverhead() != expectedDefaultParallelRunnerOverhead {
		t.Errorf("expected default parallel runner overhead to be %s, got %s", expectedDefaultParallelRunnerOverhead, DefaultParallelRunnerOverhead())
	}
}

func TestDefaultTargetTime(t *testing.T) {
	if DefaultTargetTime() != expectedDefaultTargetTime {
		t.Errorf("expected default target time to be %s, got %s", expectedDefaultTargetTime, DefaultTargetTime())
	}
}

func TestPhysicalCPUCount(t *testing.T) {
	result := PhysicalCPUCount()
	if result < 1 {
		t.Errorf("expected PhysicalCPUCount() to be at least 1, got %d", result)
	}
}

func TestPhysicalCPUCountFromTopology(t *testing.T) {
	tests := []struct {
		name                  string
		availableLogicalCPUs  int
		threadsPerCore        int
		detectedPhysicalCores int
		detectedLogicalCores  int
		expected              int
	}{
		{
			name:                 "divides logical CPUs by threads per core",
			availableLogicalCPUs: 8,
			threadsPerCore:       2,
			expected:             4,
		},
		{
			name:                 "rounds up odd logical CPU quotas",
			availableLogicalCPUs: 3,
			threadsPerCore:       2,
			expected:             2,
		},
		{
			name:                  "uses detected topology when threads per core is missing",
			availableLogicalCPUs:  8,
			threadsPerCore:        1,
			detectedPhysicalCores: 4,
			detectedLogicalCores:  8,
			expected:              4,
		},
		{
			name:                  "does not exceed available logical CPUs",
			availableLogicalCPUs:  2,
			threadsPerCore:        1,
			detectedPhysicalCores: 4,
			detectedLogicalCores:  4,
			expected:              2,
		},
		{
			name:                  "caps to detected physical cores without SMT topology",
			availableLogicalCPUs:  8,
			threadsPerCore:        1,
			detectedPhysicalCores: 4,
			detectedLogicalCores:  4,
			expected:              4,
		},
		{
			name:                 "clamps invalid available logical CPU count",
			availableLogicalCPUs: 0,
			threadsPerCore:       2,
			expected:             1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := physicalCPUCount(
				tt.availableLogicalCPUs,
				tt.threadsPerCore,
				tt.detectedPhysicalCores,
				tt.detectedLogicalCores,
			)
			if result != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestInit(t *testing.T) {
	// Clear any existing config
	config = nil
	viper.Reset()

	Init()

	if config == nil {
		t.Error("Init() should initialize config")
	}

	// Test defaults are set correctly
	if config.Platform != "ruby" {
		t.Errorf("expected default platform to be 'ruby', got %q", config.Platform)
	}
	if config.Framework != "rspec" {
		t.Errorf("expected default framework to be 'rspec', got %q", config.Framework)
	}
	expectedParallelism := PhysicalCPUCount()
	if config.MinParallelism != expectedParallelism {
		t.Errorf("expected default min_parallelism to be %d (physical CPU count), got %d", expectedParallelism, config.MinParallelism)
	}
	if config.MaxParallelism != expectedParallelism {
		t.Errorf("expected default max_parallelism to be %d (physical CPU count), got %d", expectedParallelism, config.MaxParallelism)
	}
	if config.ParallelRunnerOverhead != expectedDefaultParallelRunnerOverhead {
		t.Errorf("expected default parallel_runner_overhead to be %s, got %s", expectedDefaultParallelRunnerOverhead, config.ParallelRunnerOverhead)
	}
	if config.TargetTime != expectedDefaultTargetTime {
		t.Errorf("expected default target_time to be %s, got %s", expectedDefaultTargetTime, config.TargetTime)
	}
	if config.WorkerEnv != "" {
		t.Errorf("expected default worker_env to be empty, got %q", config.WorkerEnv)
	}
	if config.CiNode != -1 {
		t.Errorf("expected default ci_node to be -1, got %d", config.CiNode)
	}
	if config.CiNodeWorkers != 1 {
		t.Errorf("expected default ci_node_workers to be 1, got %d", config.CiNodeWorkers)
	}
	if config.Command != "" {
		t.Errorf("expected default command to be empty, got %q", config.Command)
	}
	if config.TestsLocation != "" {
		t.Errorf("expected default tests_location to be empty, got %q", config.TestsLocation)
	}
	if config.TestsExcludePattern != "" {
		t.Errorf("expected default tests_exclude_pattern to be empty, got %q", config.TestsExcludePattern)
	}
	if config.TestDiscoveryCache != "" {
		t.Errorf("expected default test_discovery_cache to be empty, got %q", config.TestDiscoveryCache)
	}
	if config.TestSkippingLevel != "test" {
		t.Errorf("expected default test_skipping_mode to be 'test', got %q", config.TestSkippingLevel)
	}
	if config.ForceFullTestDiscovery {
		t.Error("expected default force_full_test_discovery to be false")
	}
	if config.StrictDiscovery {
		t.Error("expected default strict_discovery to be false")
	}
	if config.RuntimeTags != "" {
		t.Errorf("expected default runtime_tags to be empty, got %q", config.RuntimeTags)
	}
	if !config.ReportEnabled {
		t.Error("expected default report_enabled to be true")
	}
}

func TestSetDefaults(t *testing.T) {
	viper.Reset()

	setDefaults()

	if viper.GetString("platform") != "ruby" {
		t.Errorf("expected default platform to be 'ruby', got %q", viper.GetString("platform"))
	}
	if viper.GetString("framework") != "rspec" {
		t.Errorf("expected default framework to be 'rspec', got %q", viper.GetString("framework"))
	}
	expectedParallelism := PhysicalCPUCount()
	if viper.GetInt("min_parallelism") != expectedParallelism {
		t.Errorf("expected default min_parallelism to be %d (physical CPU count), got %d", expectedParallelism, viper.GetInt("min_parallelism"))
	}
	if viper.GetInt("max_parallelism") != expectedParallelism {
		t.Errorf("expected default max_parallelism to be %d (physical CPU count), got %d", expectedParallelism, viper.GetInt("max_parallelism"))
	}
	if viper.GetString("parallel_runner_overhead") != expectedDefaultParallelRunnerOverhead.String() {
		t.Errorf("expected default parallel_runner_overhead to be %s, got %q", expectedDefaultParallelRunnerOverhead, viper.GetString("parallel_runner_overhead"))
	}
	if viper.GetString("target_time") != expectedDefaultTargetTime.String() {
		t.Errorf("expected default target_time to be %s, got %q", expectedDefaultTargetTime, viper.GetString("target_time"))
	}
	if viper.GetString("worker_env") != "" {
		t.Errorf("expected default worker_env to be empty, got %q", viper.GetString("worker_env"))
	}
	if viper.GetInt("ci_node") != -1 {
		t.Errorf("expected default ci_node to be -1, got %d", viper.GetInt("ci_node"))
	}
	if viper.GetInt("ci_node_workers") != 1 {
		t.Errorf("expected default ci_node_workers to be 1, got %d", viper.GetInt("ci_node_workers"))
	}
	if viper.GetString("command") != "" {
		t.Errorf("expected default command to be empty, got %q", viper.GetString("command"))
	}
	if viper.GetString("tests_location") != "" {
		t.Errorf("expected default tests_location to be empty, got %q", viper.GetString("tests_location"))
	}
	if viper.GetString("tests_exclude_pattern") != "" {
		t.Errorf("expected default tests_exclude_pattern to be empty, got %q", viper.GetString("tests_exclude_pattern"))
	}
	if viper.GetString("test_discovery_cache") != "" {
		t.Errorf("expected default test_discovery_cache to be empty, got %q", viper.GetString("test_discovery_cache"))
	}
	if viper.GetString("test_skipping_mode") != "test" {
		t.Errorf("expected default test_skipping_mode to be 'test', got %q", viper.GetString("test_skipping_mode"))
	}
	if viper.GetBool("force_full_test_discovery") {
		t.Error("expected default force_full_test_discovery to be false")
	}
	if viper.GetBool("strict_discovery") {
		t.Error("expected default strict_discovery to be false")
	}
	if viper.GetString("runtime_tags") != "" {
		t.Errorf("expected default runtime_tags to be empty, got %q", viper.GetString("runtime_tags"))
	}
	if !viper.GetBool("report_enabled") {
		t.Error("expected default report_enabled to be true")
	}
}

func TestGet(t *testing.T) {
	// Clear any existing config
	config = nil
	viper.Reset()

	result := Get()

	if result == nil {
		t.Error("Get() should return non-nil config")
	}
	if config == nil {
		t.Error("Get() should initialize global config")
	}
	if result != config {
		t.Error("Get() should return the same instance as global config")
	}
}

func TestGetPlatform(t *testing.T) {
	// Test with defaults
	config = nil
	viper.Reset()

	platform := GetPlatform()
	if platform != "ruby" {
		t.Errorf("expected platform to be 'ruby', got %q", platform)
	}

	// Test with custom value
	config = &Config{Platform: "python", Framework: "pytest", MinParallelism: 2, MaxParallelism: 4, WorkerEnv: "RAILS_DB=my_project_dev_{{nodeIndex}}", CiNode: 3}
	platform = GetPlatform()
	if platform != "python" {
		t.Errorf("expected platform to be 'python', got %q", platform)
	}
}

func TestGetFramework(t *testing.T) {
	// Test with defaults
	config = nil
	viper.Reset()

	framework := GetFramework()
	if framework != "rspec" {
		t.Errorf("expected framework to be 'rspec', got %q", framework)
	}

	// Test with custom value
	config = &Config{Platform: "python", Framework: "pytest", MinParallelism: 2, MaxParallelism: 4, WorkerEnv: "RAILS_DB=my_project_dev_{{nodeIndex}}", CiNode: 3}
	framework = GetFramework()
	if framework != "pytest" {
		t.Errorf("expected framework to be 'pytest', got %q", framework)
	}
}

func TestEnvironmentVariables(t *testing.T) {
	// Clear any existing config
	config = nil
	viper.Reset()

	// Set environment variables
	_ = os.Setenv(platformEnv, "python")
	_ = os.Setenv(frameworkEnv, "pytest")
	_ = os.Setenv(minParallelismEnv, "2")
	_ = os.Setenv(maxParallelismEnv, "8")
	_ = os.Setenv(parallelRunnerOverheadEnv, "40s")
	_ = os.Setenv(targetTimeEnv, "10m")
	_ = os.Setenv(workerEnv, "RAILS_DB=my_project_dev_{{nodeIndex}}")
	_ = os.Setenv(ciNodeEnv, "5")
	_ = os.Setenv(ciNodeWorkersEnv, "4")
	_ = os.Setenv(commandEnv, "bundle exec rspec")
	_ = os.Setenv(testsLocationEnv, "spec/**/*_spec.rb")
	_ = os.Setenv(testsExcludePatternEnv, "spec/system/**/*_spec.rb")
	_ = os.Setenv(testDiscoveryCacheEnv, "/tmp/ddtest-tests.json")
	_ = os.Setenv(testSkippingModeEnv, "suite")
	_ = os.Setenv(forceFullTestDiscoveryEnv, "true")
	_ = os.Setenv(strictDiscoveryEnv, "true")
	_ = os.Setenv(runtimeTagsEnv, `{"os.platform":"linux","runtime.version":"3.2.0"}`)
	_ = os.Setenv(reportEnabledEnv, "false")
	defer func() {
		_ = os.Unsetenv(platformEnv)
		_ = os.Unsetenv(frameworkEnv)
		_ = os.Unsetenv(minParallelismEnv)
		_ = os.Unsetenv(maxParallelismEnv)
		_ = os.Unsetenv(parallelRunnerOverheadEnv)
		_ = os.Unsetenv(targetTimeEnv)
		_ = os.Unsetenv(workerEnv)
		_ = os.Unsetenv(ciNodeEnv)
		_ = os.Unsetenv(ciNodeWorkersEnv)
		_ = os.Unsetenv(commandEnv)
		_ = os.Unsetenv(testsLocationEnv)
		_ = os.Unsetenv(testsExcludePatternEnv)
		_ = os.Unsetenv(testDiscoveryCacheEnv)
		_ = os.Unsetenv(testSkippingModeEnv)
		_ = os.Unsetenv(forceFullTestDiscoveryEnv)
		_ = os.Unsetenv(strictDiscoveryEnv)
		_ = os.Unsetenv(runtimeTagsEnv)
		_ = os.Unsetenv(reportEnabledEnv)
	}()

	Init()

	if config.Platform != "python" {
		t.Errorf("expected platform from env var to be 'python', got %q", config.Platform)
	}
	if config.Framework != "pytest" {
		t.Errorf("expected framework from env var to be 'pytest', got %q", config.Framework)
	}
	if config.MinParallelism != 2 {
		t.Errorf("expected min_parallelism from env var to be 2, got %d", config.MinParallelism)
	}
	if config.MaxParallelism != 8 {
		t.Errorf("expected max_parallelism from env var to be 8, got %d", config.MaxParallelism)
	}
	if config.ParallelRunnerOverhead != 40*time.Second {
		t.Errorf("expected parallel_runner_overhead from env var to be 40s, got %s", config.ParallelRunnerOverhead)
	}
	if config.TargetTime != 10*time.Minute {
		t.Errorf("expected target_time from env var to be 10m, got %s", config.TargetTime)
	}
	if config.WorkerEnv != "RAILS_DB=my_project_dev_{{nodeIndex}}" {
		t.Errorf("expected worker_env from env var to be 'RAILS_DB=my_project_dev_{{nodeIndex}}', got %q", config.WorkerEnv)
	}
	if config.CiNode != 5 {
		t.Errorf("expected ci_node from env var to be 5, got %d", config.CiNode)
	}
	if config.CiNodeWorkers != 4 {
		t.Errorf("expected ci_node_workers from env var to be 4, got %d", config.CiNodeWorkers)
	}
	if config.Command != "bundle exec rspec" {
		t.Errorf("expected command from env var to be 'bundle exec rspec', got %q", config.Command)
	}
	if config.TestsLocation != "spec/**/*_spec.rb" {
		t.Errorf("expected tests_location from env var to be 'spec/**/*_spec.rb', got %q", config.TestsLocation)
	}
	if config.TestsExcludePattern != "spec/system/**/*_spec.rb" {
		t.Errorf("expected tests_exclude_pattern from env var to be 'spec/system/**/*_spec.rb', got %q", config.TestsExcludePattern)
	}
	if config.TestDiscoveryCache != "/tmp/ddtest-tests.json" {
		t.Errorf("expected test_discovery_cache from env var to be '/tmp/ddtest-tests.json', got %q", config.TestDiscoveryCache)
	}
	if config.TestSkippingLevel != "suite" {
		t.Errorf("expected test_skipping_mode from env var to be 'suite', got %q", config.TestSkippingLevel)
	}
	if !config.ForceFullTestDiscovery {
		t.Error("expected force_full_test_discovery from env var to be true")
	}
	if !config.StrictDiscovery {
		t.Error("expected strict_discovery from env var to be true")
	}
	if config.RuntimeTags != `{"os.platform":"linux","runtime.version":"3.2.0"}` {
		t.Errorf("expected runtime_tags from env var to be JSON string, got %q", config.RuntimeTags)
	}
	if config.ReportEnabled {
		t.Error("expected report_enabled from env var to be false")
	}
}

func TestGetMinParallelism(t *testing.T) {
	// Test with defaults
	config = nil
	viper.Reset()

	expectedParallelism := PhysicalCPUCount()
	minParallelism := GetMinParallelism()
	if minParallelism != expectedParallelism {
		t.Errorf("expected min_parallelism to be %d (physical CPU count), got %d", expectedParallelism, minParallelism)
	}

	// Test with custom value
	config = &Config{Platform: "python", Framework: "pytest", MinParallelism: 3, MaxParallelism: 8, WorkerEnv: "RAILS_DB=my_project_dev_{{nodeIndex}}", CiNode: 3}
	minParallelism = GetMinParallelism()
	if minParallelism != 3 {
		t.Errorf("expected min_parallelism to be 3, got %d", minParallelism)
	}
}

func TestGetMaxParallelism(t *testing.T) {
	// Test with defaults
	config = nil
	viper.Reset()

	expectedParallelism := PhysicalCPUCount()
	maxParallelism := GetMaxParallelism()
	if maxParallelism != expectedParallelism {
		t.Errorf("expected max_parallelism to be %d (physical CPU count), got %d", expectedParallelism, maxParallelism)
	}

	// Test with custom value
	config = &Config{Platform: "python", Framework: "pytest", MinParallelism: 2, MaxParallelism: 6, WorkerEnv: "RAILS_DB=my_project_dev_{{nodeIndex}}", CiNode: 3}
	maxParallelism = GetMaxParallelism()
	if maxParallelism != 6 {
		t.Errorf("expected max_parallelism to be 6, got %d", maxParallelism)
	}
}

func TestGetParallelRunnerOverhead(t *testing.T) {
	config = nil
	viper.Reset()

	parallelRunnerOverhead := GetParallelRunnerOverhead()
	if parallelRunnerOverhead != expectedDefaultParallelRunnerOverhead {
		t.Errorf("expected parallel_runner_overhead to be %s, got %s", expectedDefaultParallelRunnerOverhead, parallelRunnerOverhead)
	}

	config = &Config{ParallelRunnerOverhead: 45 * time.Second}
	parallelRunnerOverhead = GetParallelRunnerOverhead()
	if parallelRunnerOverhead != 45*time.Second {
		t.Errorf("expected parallel_runner_overhead to be 45s, got %s", parallelRunnerOverhead)
	}
}

func TestGetTargetTime(t *testing.T) {
	config = nil
	viper.Reset()

	targetTime := GetTargetTime()
	if targetTime != expectedDefaultTargetTime {
		t.Errorf("expected target_time to be %s, got %s", expectedDefaultTargetTime, targetTime)
	}

	config = &Config{TargetTime: 12 * time.Minute}
	targetTime = GetTargetTime()
	if targetTime != 12*time.Minute {
		t.Errorf("expected target_time to be 12m, got %s", targetTime)
	}
}

func TestGetWorkerEnv(t *testing.T) {
	// Test with defaults
	config = nil
	viper.Reset()

	workerEnv := GetWorkerEnv()
	if workerEnv != "" {
		t.Errorf("expected worker_env to be empty, got %q", workerEnv)
	}

	// Test with custom value
	config = &Config{Platform: "python", Framework: "pytest", MinParallelism: 2, MaxParallelism: 4, WorkerEnv: "RAILS_DB=my_project_dev_{{nodeIndex}}", CiNode: 3}
	workerEnv = GetWorkerEnv()
	if workerEnv != "RAILS_DB=my_project_dev_{{nodeIndex}}" {
		t.Errorf("expected worker_env to be 'RAILS_DB=my_project_dev_{{nodeIndex}}', got %q", workerEnv)
	}
}

func TestGetCommand(t *testing.T) {
	config = nil
	viper.Reset()

	command := GetCommand()
	if command != "" {
		t.Errorf("expected command to be empty by default, got %q", command)
	}

	config = &Config{Command: "bundle exec rspec"}
	command = GetCommand()
	if command != "bundle exec rspec" {
		t.Errorf("expected command to be 'bundle exec rspec', got %q", command)
	}
}

func TestGetTestsLocation(t *testing.T) {
	config = nil
	viper.Reset()

	testsLocation := GetTestsLocation()
	if testsLocation != "" {
		t.Errorf("expected tests_location to be empty by default, got %q", testsLocation)
	}

	config = &Config{TestsLocation: "spec/**/*_spec.rb"}
	testsLocation = GetTestsLocation()
	if testsLocation != "spec/**/*_spec.rb" {
		t.Errorf("expected tests_location to be 'spec/**/*_spec.rb', got %q", testsLocation)
	}
}

func TestGetTestsExcludePattern(t *testing.T) {
	config = nil
	viper.Reset()

	testsExcludePattern := GetTestsExcludePattern()
	if testsExcludePattern != "" {
		t.Errorf("expected tests_exclude_pattern to be empty by default, got %q", testsExcludePattern)
	}

	config = &Config{TestsExcludePattern: "spec/system/**/*_spec.rb"}
	testsExcludePattern = GetTestsExcludePattern()
	if testsExcludePattern != "spec/system/**/*_spec.rb" {
		t.Errorf("expected tests_exclude_pattern to be 'spec/system/**/*_spec.rb', got %q", testsExcludePattern)
	}
}

func TestGetTestDiscoveryCache(t *testing.T) {
	config = nil
	viper.Reset()

	cachePath := GetTestDiscoveryCache()
	if cachePath != "" {
		t.Errorf("expected test_discovery_cache to be empty by default, got %q", cachePath)
	}

	config = &Config{TestDiscoveryCache: "/tmp/ddtest-tests.json"}
	cachePath = GetTestDiscoveryCache()
	if cachePath != "/tmp/ddtest-tests.json" {
		t.Errorf("expected test_discovery_cache to be '/tmp/ddtest-tests.json', got %q", cachePath)
	}
}

func TestNormalizeTestSkippingLevel(t *testing.T) {
	tests := []struct {
		name  string
		value TestSkippingLevel
		want  TestSkippingLevel
	}{
		{name: "test", value: "test", want: "test"},
		{name: "suite", value: "suite", want: "suite"},
		{name: "invalid", value: "file", want: "test"},
		{name: "empty", value: "", want: "test"},
		{name: "trimmed suite", value: " suite ", want: "suite"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeTestSkippingLevel(tt.value); got != tt.want {
				t.Fatalf("NormalizeTestSkippingLevel(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestGetTestSkippingLevel(t *testing.T) {
	config = nil
	viper.Reset()

	if got := GetTestSkippingLevel(); got != "test" {
		t.Fatalf("GetTestSkippingLevel() default = %q, want test", got)
	}

	config = nil
	viper.Reset()
	viper.Set("test_skipping_mode", "suite")
	if got := GetTestSkippingLevel(); got != "suite" {
		t.Fatalf("GetTestSkippingLevel() configured = %q, want suite", got)
	}
}

func TestGetForceFullTestDiscovery(t *testing.T) {
	config = nil
	viper.Reset()

	if got := GetForceFullTestDiscovery(); got {
		t.Fatal("GetForceFullTestDiscovery() default = true, want false")
	}

	config = &Config{ForceFullTestDiscovery: true}
	if got := GetForceFullTestDiscovery(); !got {
		t.Fatal("GetForceFullTestDiscovery() configured = false, want true")
	}
}

func TestGetStrictDiscovery(t *testing.T) {
	config = nil
	viper.Reset()

	if got := GetStrictDiscovery(); got {
		t.Fatal("GetStrictDiscovery() default = true, want false")
	}

	config = &Config{StrictDiscovery: true}
	if got := GetStrictDiscovery(); !got {
		t.Fatal("GetStrictDiscovery() configured = false, want true")
	}
}

func TestTestSkippingLevelRubyEnv(t *testing.T) {
	config = nil
	viper.Reset()
	t.Setenv(testSkippingModeEnv, "suite")

	Init()

	if config.TestSkippingLevel != "suite" {
		t.Fatalf("test_skipping_mode from Ruby env = %q, want suite", config.TestSkippingLevel)
	}
}

func TestTestSkippingLevelInvalidFallsBackToTest(t *testing.T) {
	config = nil
	viper.Reset()
	t.Setenv(testSkippingModeEnv, "invalid")

	Init()

	if config.TestSkippingLevel != "test" {
		t.Fatalf("test_skipping_mode from invalid env = %q, want test", config.TestSkippingLevel)
	}
}

func TestKnapsackTestFilePatternAlias(t *testing.T) {
	config = nil
	viper.Reset()

	_ = os.Setenv(testsLocationEnv, "")
	_ = os.Setenv(knapsackTestFilePatternEnv, "custom/spec/**/*_spec.rb")
	defer func() {
		_ = os.Unsetenv(testsLocationEnv)
		_ = os.Unsetenv(knapsackTestFilePatternEnv)
	}()

	Init()

	if config.TestsLocation != "custom/spec/**/*_spec.rb" {
		t.Errorf("expected tests_location from Knapsack alias to be 'custom/spec/**/*_spec.rb', got %q", config.TestsLocation)
	}
}

func TestKnapsackTestFileExcludePatternAlias(t *testing.T) {
	config = nil
	viper.Reset()

	_ = os.Setenv(testsExcludePatternEnv, "")
	_ = os.Setenv(knapsackTestFileExcludeEnv, "spec/system/**/*_spec.rb")
	defer func() {
		_ = os.Unsetenv(testsExcludePatternEnv)
		_ = os.Unsetenv(knapsackTestFileExcludeEnv)
	}()

	Init()

	if config.TestsExcludePattern != "spec/system/**/*_spec.rb" {
		t.Errorf("expected tests_exclude_pattern from Knapsack alias to be 'spec/system/**/*_spec.rb', got %q", config.TestsExcludePattern)
	}
}

func TestCanonicalTestPatternEnvVarsTakePrecedenceOverKnapsackAliases(t *testing.T) {
	config = nil
	viper.Reset()

	_ = os.Setenv(testsLocationEnv, "spec/**/*_spec.rb")
	_ = os.Setenv(knapsackTestFilePatternEnv, "custom/spec/**/*_spec.rb")
	_ = os.Setenv(testsExcludePatternEnv, "spec/system/**/*_spec.rb")
	_ = os.Setenv(knapsackTestFileExcludeEnv, "spec/features/**/*_spec.rb")
	defer func() {
		_ = os.Unsetenv(testsLocationEnv)
		_ = os.Unsetenv(knapsackTestFilePatternEnv)
		_ = os.Unsetenv(testsExcludePatternEnv)
		_ = os.Unsetenv(knapsackTestFileExcludeEnv)
	}()

	Init()

	if config.TestsLocation != "spec/**/*_spec.rb" {
		t.Errorf("expected canonical tests_location env var to win, got %q", config.TestsLocation)
	}
	if config.TestsExcludePattern != "spec/system/**/*_spec.rb" {
		t.Errorf("expected canonical tests_exclude_pattern env var to win, got %q", config.TestsExcludePattern)
	}
}

func TestRuntimeTagsAlias(t *testing.T) {
	config = nil
	viper.Reset()

	_ = os.Setenv(runtimeTagsEnv, "")
	_ = os.Setenv(runtimeTagsAliasEnv, `{"os.platform":"linux","runtime.version":"3.2.0"}`)
	defer func() {
		_ = os.Unsetenv(runtimeTagsEnv)
		_ = os.Unsetenv(runtimeTagsAliasEnv)
	}()

	Init()

	if config.RuntimeTags != `{"os.platform":"linux","runtime.version":"3.2.0"}` {
		t.Errorf("expected runtime_tags from alias env var to be JSON string, got %q", config.RuntimeTags)
	}
}

func TestCanonicalRuntimeTagsEnvVarTakesPrecedenceOverAlias(t *testing.T) {
	config = nil
	viper.Reset()

	_ = os.Setenv(runtimeTagsEnv, `{"os.platform":"linux"}`)
	_ = os.Setenv(runtimeTagsAliasEnv, `{"os.platform":"darwin"}`)
	defer func() {
		_ = os.Unsetenv(runtimeTagsEnv)
		_ = os.Unsetenv(runtimeTagsAliasEnv)
	}()

	Init()

	if config.RuntimeTags != `{"os.platform":"linux"}` {
		t.Errorf("expected canonical runtime_tags env var to win, got %q", config.RuntimeTags)
	}
}

func TestGetRuntimeTags(t *testing.T) {
	config = nil
	viper.Reset()

	runtimeTags := GetRuntimeTags()
	if runtimeTags != "" {
		t.Errorf("expected runtime_tags to be empty by default, got %q", runtimeTags)
	}

	config = &Config{RuntimeTags: `{"os.platform":"linux","runtime.version":"3.2.0"}`}
	runtimeTags = GetRuntimeTags()
	if runtimeTags != `{"os.platform":"linux","runtime.version":"3.2.0"}` {
		t.Errorf("expected runtime_tags to be JSON string, got %q", runtimeTags)
	}
}

func TestGetReportEnabled(t *testing.T) {
	config = nil
	viper.Reset()

	if !GetReportEnabled() {
		t.Error("expected report_enabled to be true by default")
	}

	config = &Config{ReportEnabled: false}
	if GetReportEnabled() {
		t.Error("expected report_enabled to be false")
	}
}

func TestGetRuntimeTagsMap(t *testing.T) {
	t.Run("empty runtime tags", func(t *testing.T) {
		config = &Config{RuntimeTags: ""}
		result, err := GetRuntimeTagsMap()

		if err != nil {
			t.Errorf("expected no error for empty runtime_tags, got %v", err)
		}
		if result != nil {
			t.Errorf("expected nil for empty runtime_tags, got %v", result)
		}
	})

	t.Run("valid JSON", func(t *testing.T) {
		config = &Config{RuntimeTags: `{"os.platform":"linux","runtime.version":"3.2.0","language":"ruby"}`}
		result, err := GetRuntimeTagsMap()

		if err != nil {
			t.Errorf("expected no error for valid JSON, got %v", err)
		}

		expected := map[string]string{
			"os.platform":     "linux",
			"runtime.version": "3.2.0",
			"language":        "ruby",
		}

		if len(result) != 3 {
			t.Errorf("expected 3 entries, got %d", len(result))
		}
		for k, v := range expected {
			if result[k] != v {
				t.Errorf("expected %s=%s, got %s=%s", k, v, k, result[k])
			}
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		config = &Config{RuntimeTags: `{invalid json}`}
		result, err := GetRuntimeTagsMap()

		if err == nil {
			t.Error("expected error for invalid JSON")
		}
		if result != nil {
			t.Errorf("expected nil result for invalid JSON, got %v", result)
		}
	})

	t.Run("single key-value pair", func(t *testing.T) {
		config = &Config{RuntimeTags: `{"os.platform":"darwin"}`}
		result, err := GetRuntimeTagsMap()

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if len(result) != 1 || result["os.platform"] != "darwin" {
			t.Errorf("expected {os.platform: darwin}, got %v", result)
		}
	})
}

func TestGetCiNode(t *testing.T) {
	// Test with defaults
	config = nil
	viper.Reset()

	ciNode := GetCiNode()
	if ciNode != -1 {
		t.Errorf("expected ci_node to be -1, got %d", ciNode)
	}

	// Test with custom value
	config = &Config{Platform: "python", Framework: "pytest", MinParallelism: 2, MaxParallelism: 4, WorkerEnv: "RAILS_DB=my_project_dev_{{nodeIndex}}", CiNode: 7}
	ciNode = GetCiNode()
	if ciNode != 7 {
		t.Errorf("expected ci_node to be 7, got %d", ciNode)
	}
}

func TestGetCiNodeWorkers(t *testing.T) {
	// Test with defaults
	config = nil
	viper.Reset()

	ciNodeWorkers := GetCiNodeWorkers()
	if ciNodeWorkers != 1 {
		t.Errorf("expected ci_node_workers to be 1, got %d", ciNodeWorkers)
	}

	// Test with custom value
	config = &Config{CiNodeWorkers: 4}
	ciNodeWorkers = GetCiNodeWorkers()
	if ciNodeWorkers != 4 {
		t.Errorf("expected ci_node_workers to be 4, got %d", ciNodeWorkers)
	}
}

func TestParseCiNodeWorkers(t *testing.T) {
	physicalCPUCount := PhysicalCPUCount()
	tests := []struct {
		name      string
		value     string
		expected  int
		expectErr bool
	}{
		{
			name:     "empty value uses default",
			value:    "",
			expected: 1,
		},
		{
			name:     "positive integer",
			value:    "4",
			expected: 4,
		},
		{
			name:     "trims whitespace",
			value:    " 3 ",
			expected: 3,
		},
		{
			name:     "ncpu magic value",
			value:    "ncpu",
			expected: physicalCPUCount,
		},
		{
			name:     "ncpu is case insensitive",
			value:    "NCPU",
			expected: physicalCPUCount,
		},
		{
			name:      "rejects zero",
			value:     "0",
			expectErr: true,
		},
		{
			name:      "rejects negative values",
			value:     "-1",
			expectErr: true,
		},
		{
			name:      "rejects unknown strings",
			value:     "many",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseCiNodeWorkers(tt.value)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestParseNonNegativeDurationSetting(t *testing.T) {
	tests := []struct {
		name         string
		value        string
		defaultValue time.Duration
		settingName  string
		expected     time.Duration
		expectErr    bool
	}{
		{
			name:         "empty overhead value uses overhead default",
			value:        "",
			defaultValue: expectedDefaultParallelRunnerOverhead,
			settingName:  "ci-job-overhead",
			expected:     expectedDefaultParallelRunnerOverhead,
		},
		{
			name:         "empty target value uses target default",
			value:        "",
			defaultValue: expectedDefaultTargetTime,
			settingName:  "target-time",
			expected:     expectedDefaultTargetTime,
		},
		{
			name:        "seconds",
			value:       "25s",
			settingName: "ci-job-overhead",
			expected:    expectedDefaultParallelRunnerOverhead,
		},
		{
			name:        "minutes",
			value:       "1m",
			settingName: "target-time",
			expected:    time.Minute,
		},
		{
			name:        "milliseconds",
			value:       "1500ms",
			settingName: "target-time",
			expected:    1500 * time.Millisecond,
		},
		{
			name:        "zero disables duration setting",
			value:       "0s",
			settingName: "target-time",
			expected:    0,
		},
		{
			name:        "rejects negative values",
			value:       "-1s",
			settingName: "target-time",
			expectErr:   true,
		},
		{
			name:        "rejects plain integers",
			value:       "25",
			settingName: "ci-job-overhead",
			expectErr:   true,
		},
		{
			name:        "rejects unknown strings",
			value:       "fast",
			settingName: "target-time",
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseNonNegativeDurationSetting(tt.value, tt.defaultValue, tt.settingName)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.settingName) {
					t.Fatalf("expected error to contain setting name %q, got %v", tt.settingName, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestEnvironmentVariableCiNodeWorkersNCPU(t *testing.T) {
	config = nil
	viper.Reset()

	_ = os.Setenv(ciNodeWorkersEnv, "ncpu")
	defer func() {
		_ = os.Unsetenv(ciNodeWorkersEnv)
	}()

	Init()

	expected := PhysicalCPUCount()
	if config.CiNodeWorkers != expected {
		t.Errorf("expected ci_node_workers from ncpu env var to be %d, got %d", expected, config.CiNodeWorkers)
	}
}

func TestGetWorkerEnvMap(t *testing.T) {
	t.Run("empty worker env", func(t *testing.T) {
		config = &Config{WorkerEnv: ""}
		result := GetWorkerEnvMap()

		if len(result) != 0 {
			t.Errorf("expected empty map for empty worker_env, got %v", result)
		}
	})

	t.Run("single key-value pair", func(t *testing.T) {
		config = &Config{WorkerEnv: "NODE_INDEX={{nodeIndex}}"}
		result := GetWorkerEnvMap()

		expected := map[string]string{"NODE_INDEX": "{{nodeIndex}}"}
		if len(result) != 1 || result["NODE_INDEX"] != "{{nodeIndex}}" {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})

	t.Run("multiple key-value pairs", func(t *testing.T) {
		config = &Config{WorkerEnv: "NODE_INDEX={{nodeIndex}};BUILD_ID=123;ENV=test"}
		result := GetWorkerEnvMap()

		expected := map[string]string{
			"NODE_INDEX": "{{nodeIndex}}",
			"BUILD_ID":   "123",
			"ENV":        "test",
		}

		if len(result) != 3 {
			t.Errorf("expected 3 entries, got %d", len(result))
		}
		for k, v := range expected {
			if result[k] != v {
				t.Errorf("expected %s=%s, got %s=%s", k, v, k, result[k])
			}
		}
	})

	t.Run("handles whitespace", func(t *testing.T) {
		config = &Config{WorkerEnv: " KEY1 = value1 ; KEY2=value2;  KEY3  =  value3  "}
		result := GetWorkerEnvMap()

		expected := map[string]string{
			"KEY1": "value1",
			"KEY2": "value2",
			"KEY3": "value3",
		}

		if len(result) != 3 {
			t.Errorf("expected 3 entries, got %d", len(result))
		}
		for k, v := range expected {
			if result[k] != v {
				t.Errorf("expected %s=%s, got %s=%s", k, v, k, result[k])
			}
		}
	})

	t.Run("ignores malformed pairs", func(t *testing.T) {
		config = &Config{WorkerEnv: "GOOD=value;BAD_NO_EQUALS;=NO_KEY;ANOTHER=good"}
		result := GetWorkerEnvMap()

		expected := map[string]string{
			"GOOD":    "value",
			"ANOTHER": "good",
		}

		if len(result) != 2 {
			t.Errorf("expected 2 entries, got %d", len(result))
		}
		for k, v := range expected {
			if result[k] != v {
				t.Errorf("expected %s=%s, got %s=%s", k, v, k, result[k])
			}
		}
	})

	t.Run("empty keys ignored", func(t *testing.T) {
		config = &Config{WorkerEnv: "=value;KEY=good;  =another"}
		result := GetWorkerEnvMap()

		if len(result) != 1 || result["KEY"] != "good" {
			t.Errorf("expected only KEY=good, got %v", result)
		}
	})
}
