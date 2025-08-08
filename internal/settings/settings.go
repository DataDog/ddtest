package settings

import (
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Platform  string `mapstructure:"platform"`
	Framework string `mapstructure:"framework"`
}

var (
	config *Config
)

func Init() {
	viper.SetConfigName("ddtesrunner")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	viper.AutomaticEnv()
	viper.SetEnvPrefix("DD_TEST_OPTIMIZATION_RUNNER")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	setDefaults()

	viper.ReadInConfig()

	config = &Config{}
	viper.Unmarshal(config)
}

func setDefaults() {
	viper.SetDefault("platform", "ruby")
	viper.SetDefault("framework", "rspec")
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
