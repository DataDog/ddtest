package settings

import (
	"os"
	"runtime"
	"testing"

	"github.com/spf13/viper"
)

func TestDefaultParallelism(t *testing.T) {
	result := DefaultParallelism()
	expected := runtime.NumCPU()

	if result != expected {
		t.Errorf("expected DefaultParallelism() to return %d (runtime.NumCPU()), got %d", expected, result)
	}
	if result < 1 {
		t.Errorf("expected DefaultParallelism() to be at least 1, got %d", result)
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
	expectedParallelism := runtime.NumCPU()
	if config.MinParallelism != expectedParallelism {
		t.Errorf("expected default min_parallelism to be %d (CPU count), got %d", expectedParallelism, config.MinParallelism)
	}
	if config.MaxParallelism != expectedParallelism {
		t.Errorf("expected default max_parallelism to be %d (CPU count), got %d", expectedParallelism, config.MaxParallelism)
	}
	if config.WorkerEnv != "" {
		t.Errorf("expected default worker_env to be empty, got %q", config.WorkerEnv)
	}
	if config.CiNode != -1 {
		t.Errorf("expected default ci_node to be -1, got %d", config.CiNode)
	}
	if config.CiNodeWorkers != expectedParallelism {
		t.Errorf("expected default ci_node_workers to be %d (CPU count), got %d", expectedParallelism, config.CiNodeWorkers)
	}
	if config.Command != "" {
		t.Errorf("expected default command to be empty, got %q", config.Command)
	}
	if config.TestsLocation != "" {
		t.Errorf("expected default tests_location to be empty, got %q", config.TestsLocation)
	}
	if config.RuntimeTags != "" {
		t.Errorf("expected default runtime_tags to be empty, got %q", config.RuntimeTags)
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
	expectedParallelism := runtime.NumCPU()
	if viper.GetInt("min_parallelism") != expectedParallelism {
		t.Errorf("expected default min_parallelism to be %d (CPU count), got %d", expectedParallelism, viper.GetInt("min_parallelism"))
	}
	if viper.GetInt("max_parallelism") != expectedParallelism {
		t.Errorf("expected default max_parallelism to be %d (CPU count), got %d", expectedParallelism, viper.GetInt("max_parallelism"))
	}
	if viper.GetString("worker_env") != "" {
		t.Errorf("expected default worker_env to be empty, got %q", viper.GetString("worker_env"))
	}
	if viper.GetInt("ci_node") != -1 {
		t.Errorf("expected default ci_node to be -1, got %d", viper.GetInt("ci_node"))
	}
	if viper.GetInt("ci_node_workers") != expectedParallelism {
		t.Errorf("expected default ci_node_workers to be %d (CPU count), got %d", expectedParallelism, viper.GetInt("ci_node_workers"))
	}
	if viper.GetString("command") != "" {
		t.Errorf("expected default command to be empty, got %q", viper.GetString("command"))
	}
	if viper.GetString("tests_location") != "" {
		t.Errorf("expected default tests_location to be empty, got %q", viper.GetString("tests_location"))
	}
	if viper.GetString("runtime_tags") != "" {
		t.Errorf("expected default runtime_tags to be empty, got %q", viper.GetString("runtime_tags"))
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
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_PLATFORM", "python")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK", "pytest")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "2")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", "8")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV", "RAILS_DB=my_project_dev_{{nodeIndex}}")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE", "5")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE_WORKERS", "4")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_COMMAND", "bundle exec rspec")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION", "spec/**/*_spec.rb")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS", `{"os.platform":"linux","runtime.version":"3.2.0"}`)
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_PLATFORM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE_WORKERS")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_COMMAND")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS")
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
	if config.RuntimeTags != `{"os.platform":"linux","runtime.version":"3.2.0"}` {
		t.Errorf("expected runtime_tags from env var to be JSON string, got %q", config.RuntimeTags)
	}
}

func TestGetMinParallelism(t *testing.T) {
	// Test with defaults
	config = nil
	viper.Reset()

	expectedParallelism := runtime.NumCPU()
	minParallelism := GetMinParallelism()
	if minParallelism != expectedParallelism {
		t.Errorf("expected min_parallelism to be %d (CPU count), got %d", expectedParallelism, minParallelism)
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

	expectedParallelism := runtime.NumCPU()
	maxParallelism := GetMaxParallelism()
	if maxParallelism != expectedParallelism {
		t.Errorf("expected max_parallelism to be %d (CPU count), got %d", expectedParallelism, maxParallelism)
	}

	// Test with custom value
	config = &Config{Platform: "python", Framework: "pytest", MinParallelism: 2, MaxParallelism: 6, WorkerEnv: "RAILS_DB=my_project_dev_{{nodeIndex}}", CiNode: 3}
	maxParallelism = GetMaxParallelism()
	if maxParallelism != 6 {
		t.Errorf("expected max_parallelism to be 6, got %d", maxParallelism)
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

	expectedParallelism := runtime.NumCPU()
	ciNodeWorkers := GetCiNodeWorkers()
	if ciNodeWorkers != expectedParallelism {
		t.Errorf("expected ci_node_workers to be %d (CPU count), got %d", expectedParallelism, ciNodeWorkers)
	}

	// Test with custom value
	config = &Config{CiNodeWorkers: 4}
	ciNodeWorkers = GetCiNodeWorkers()
	if ciNodeWorkers != 4 {
		t.Errorf("expected ci_node_workers to be 4, got %d", ciNodeWorkers)
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
