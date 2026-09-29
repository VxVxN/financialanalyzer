package config

import (
	"fmt"
	"os"
	"strconv"
)

// defaultDBPassword is the value LoadConfig falls back to when DB_PASSWORD is
// unset. It is fine for local development but must never reach production —
// UsesDefaultPassword lets callers warn about it.
const defaultDBPassword = "password"

type Config struct {
	Port int

	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string

	CSVPath string
}

func LoadConfig() *Config {
	return &Config{
		Port:       getEnvInt("PORT", 8088),
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getEnv("DB_PASSWORD", defaultDBPassword),
		DBName:     getEnv("DB_NAME", "postgres"),
		DBSSLMode:  getEnv("DB_SSLMODE", "disable"),
		CSVPath:    getEnv("CSV_PATH", ""),
	}
}

// Validate checks that the configuration is internally consistent. It is cheap
// and should be called once at startup, before opening any connections.
func (c *Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("PORT must be in 1..65535, got %d", c.Port)
	}
	if c.DBHost == "" {
		return fmt.Errorf("DB_HOST must not be empty")
	}
	if c.DBPort == "" {
		return fmt.Errorf("DB_PORT must not be empty")
	}
	if c.DBUser == "" {
		return fmt.Errorf("DB_USER must not be empty")
	}
	if c.DBName == "" {
		return fmt.Errorf("DB_NAME must not be empty")
	}
	return nil
}

// UsesDefaultPassword reports whether the DB password is the built-in dev
// default. Callers log a warning so insecure deployments are visible.
func (c *Config) UsesDefaultPassword() bool {
	return c.DBPassword == defaultDBPassword
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		result, err := strconv.Atoi(value)
		if err != nil {
			return defaultValue
		}
		return result
	}
	return defaultValue
}
