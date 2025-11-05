package settings

import (
	"os"
	"testing"

	"github.com/spf13/viper"
)

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
	if config.MinParallelism != 1 {
		t.Errorf("expected default min_parallelism to be 1, got %d", config.MinParallelism)
	}
	if config.MaxParallelism != 1 {
		t.Errorf("expected default max_parallelism to be 1, got %d", config.MaxParallelism)
	}
	if config.WorkerEnv != "" {
		t.Errorf("expected default worker_env to be empty, got %q", config.WorkerEnv)
	}
	if config.CiNode != -1 {
		t.Errorf("expected default ci_node to be -1, got %d", config.CiNode)
	}
	if config.Command != "" {
		t.Errorf("expected default command to be empty, got %q", config.Command)
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
	if viper.GetInt("min_parallelism") != 1 {
		t.Errorf("expected default min_parallelism to be 1, got %d", viper.GetInt("min_parallelism"))
	}
	if viper.GetInt("max_parallelism") != 1 {
		t.Errorf("expected default max_parallelism to be 1, got %d", viper.GetInt("max_parallelism"))
	}
	if viper.GetString("worker_env") != "" {
		t.Errorf("expected default worker_env to be empty, got %q", viper.GetString("worker_env"))
	}
	if viper.GetInt("ci_node") != -1 {
		t.Errorf("expected default ci_node to be -1, got %d", viper.GetInt("ci_node"))
	}
	if viper.GetString("command") != "" {
		t.Errorf("expected default command to be empty, got %q", viper.GetString("command"))
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
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_COMMAND", "bundle exec rspec")
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_PLATFORM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_COMMAND")
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
	if config.Command != "bundle exec rspec" {
		t.Errorf("expected command from env var to be 'bundle exec rspec', got %q", config.Command)
	}
}

func TestGetMinParallelism(t *testing.T) {
	// Test with defaults
	config = nil
	viper.Reset()

	minParallelism := GetMinParallelism()
	if minParallelism != 1 {
		t.Errorf("expected min_parallelism to be 1, got %d", minParallelism)
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

	maxParallelism := GetMaxParallelism()
	if maxParallelism != 1 {
		t.Errorf("expected max_parallelism to be 1, got %d", maxParallelism)
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
