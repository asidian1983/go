package config

import (
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Env    string
	Server ServerConfig
}

type ServerConfig struct {
	Port            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
}

func Load() (*Config, error) {
	v := viper.New()

	v.SetDefault("env", "development")
	v.SetDefault("server.port", "8080")
	v.SetDefault("server.read_timeout", "10s")
	v.SetDefault("server.write_timeout", "10s")
	v.SetDefault("server.shutdown_timeout", "15s")

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("./configs")
	v.AddConfigPath(".")

	v.SetEnvPrefix("APP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// config file is optional — env vars are sufficient
	_ = v.ReadInConfig()

	cfg := &Config{}

	cfg.Env = v.GetString("env")
	cfg.Server = ServerConfig{
		Port:            v.GetString("server.port"),
		ReadTimeout:     v.GetDuration("server.read_timeout"),
		WriteTimeout:    v.GetDuration("server.write_timeout"),
		ShutdownTimeout: v.GetDuration("server.shutdown_timeout"),
	}

	return cfg, nil
}
