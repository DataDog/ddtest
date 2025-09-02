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
	Port           int    `mapstructure:"port"`
	MinParallelism int    `mapstructure:"min_parallelism"`
	MaxParallelism int    `mapstructure:"max_parallelism"`
	WorkerEnv      string `mapstructure:"worker_env"`
	CiNode         int    `mapstructure:"ci_node"`
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
	viper.SetDefault("port", 7890)
	viper.SetDefault("min_parallelism", 1)
	viper.SetDefault("max_parallelism", 1)
	viper.SetDefault("worker_env", "")
	viper.SetDefault("ci_node", -1)
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

func GetPort() int {
	return Get().Port
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
