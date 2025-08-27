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
	if config.Port != 7890 {
		t.Errorf("expected default port to be 7890, got %d", config.Port)
	}
	if config.MinParallelism != 1 {
		t.Errorf("expected default min_parallelism to be 1, got %d", config.MinParallelism)
	}
	if config.MaxParallelism != 1 {
		t.Errorf("expected default max_parallelism to be 1, got %d", config.MaxParallelism)
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
	if viper.GetInt("port") != 7890 {
		t.Errorf("expected default port to be 7890, got %d", viper.GetInt("port"))
	}
	if viper.GetInt("min_parallelism") != 1 {
		t.Errorf("expected default min_parallelism to be 1, got %d", viper.GetInt("min_parallelism"))
	}
	if viper.GetInt("max_parallelism") != 1 {
		t.Errorf("expected default max_parallelism to be 1, got %d", viper.GetInt("max_parallelism"))
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
	config = &Config{Platform: "python", Framework: "pytest", Port: 8080, MinParallelism: 2, MaxParallelism: 4}
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
	config = &Config{Platform: "python", Framework: "pytest", Port: 8080, MinParallelism: 2, MaxParallelism: 4}
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
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_PORT", "9090")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "2")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", "8")
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_PLATFORM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_PORT")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM")
	}()

	Init()

	if config.Platform != "python" {
		t.Errorf("expected platform from env var to be 'python', got %q", config.Platform)
	}
	if config.Framework != "pytest" {
		t.Errorf("expected framework from env var to be 'pytest', got %q", config.Framework)
	}
	if config.Port != 9090 {
		t.Errorf("expected port from env var to be 9090, got %d", config.Port)
	}
	if config.MinParallelism != 2 {
		t.Errorf("expected min_parallelism from env var to be 2, got %d", config.MinParallelism)
	}
	if config.MaxParallelism != 8 {
		t.Errorf("expected max_parallelism from env var to be 8, got %d", config.MaxParallelism)
	}
}

func TestGetPort(t *testing.T) {
	// Test with defaults
	config = nil
	viper.Reset()

	port := GetPort()
	if port != 7890 {
		t.Errorf("expected port to be 7890, got %d", port)
	}

	// Test with custom value
	config = &Config{Platform: "python", Framework: "pytest", Port: 8080, MinParallelism: 2, MaxParallelism: 4}
	port = GetPort()
	if port != 8080 {
		t.Errorf("expected port to be 8080, got %d", port)
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
	config = &Config{Platform: "python", Framework: "pytest", Port: 8080, MinParallelism: 3, MaxParallelism: 8}
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
	config = &Config{Platform: "python", Framework: "pytest", Port: 8080, MinParallelism: 2, MaxParallelism: 6}
	maxParallelism = GetMaxParallelism()
	if maxParallelism != 6 {
		t.Errorf("expected max_parallelism to be 6, got %d", maxParallelism)
	}
}
