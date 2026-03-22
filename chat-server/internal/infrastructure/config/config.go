package config

import (
	"errors"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Env      string
	Server   ServerConfig
	Redis    RedisConfig
	JWT      JWTConfig
	Postgres PostgresConfig
}

type PostgresConfig struct {
	Enabled bool
	DSN     string
}

type JWTConfig struct {
	// Secret must be at least 32 characters. Set via APP_JWT_SECRET env var.
	Secret string
	// Expiry controls token lifetime. Default: 24h.
	Expiry time.Duration
}

type RedisConfig struct {
	Enabled bool
	Addr    string
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
	v.SetDefault("redis.enabled", false)
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("jwt.expiry", "24h")
	v.SetDefault("postgres.enabled", false)
	v.SetDefault("postgres.dsn", "postgres://localhost:5432/chat?sslmode=disable")
	// jwt.secret has no default — it MUST be set explicitly.

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
	cfg.Redis = RedisConfig{
		Enabled: v.GetBool("redis.enabled"),
		Addr:    v.GetString("redis.addr"),
	}
	cfg.JWT = JWTConfig{
		Secret: v.GetString("jwt.secret"),
		Expiry: v.GetDuration("jwt.expiry"),
	}
	cfg.Postgres = PostgresConfig{
		Enabled: v.GetBool("postgres.enabled"),
		DSN:     v.GetString("postgres.dsn"),
	}

	if cfg.JWT.Secret == "" {
		return nil, errors.New("config: APP_JWT_SECRET must be set (min 32 characters)")
	}

	return cfg, nil
}
