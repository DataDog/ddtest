package settings

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Platform       string `mapstructure:"platform"`
	Framework      string `mapstructure:"framework"`
	MinParallelism int    `mapstructure:"min_parallelism"`
	MaxParallelism int    `mapstructure:"max_parallelism"`
	WorkerEnv      string `mapstructure:"worker_env"`
	CiNode         int    `mapstructure:"ci_node"`
	Command        string `mapstructure:"command"`
	TestsLocation  string `mapstructure:"tests_location"`
}

var (
	config *Config
)

func Init() {
	viper.AutomaticEnv()
	viper.SetEnvPrefix("DD_TEST_OPTIMIZATION_RUNNER")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	setDefaults()

	config = &Config{}
	if err := viper.Unmarshal(config); err != nil {
		fmt.Fprintf(os.Stderr, "Error unmarshaling config: %v\n", err)
		os.Exit(1)
	}
}

func setDefaults() {
	viper.SetDefault("platform", "ruby")
	viper.SetDefault("framework", "rspec")
	viper.SetDefault("min_parallelism", 1)
	viper.SetDefault("max_parallelism", 1)
	viper.SetDefault("worker_env", "")
	viper.SetDefault("ci_node", -1)
	viper.SetDefault("command", "")
	viper.SetDefault("tests_location", "")
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

func GetWorkerEnv() string {
	return Get().WorkerEnv
}

func GetCiNode() int {
	return Get().CiNode
}

func GetCommand() string {
	return Get().Command
}

func GetTestsLocation() string {
	return Get().TestsLocation
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
