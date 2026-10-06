package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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

	// AuthUser/AuthPassword enable HTTP Basic Auth on the web server's
	// state-changing endpoints. Both empty disables auth (dev only).
	AuthUser     string
	AuthPassword string

	// SchedulerEnabled makes cmd/plot run the fetch pipelines on a timetable
	// (off by default so a dev server never calls the external sources).
	// ScheduleQuotes/ScheduleFinancials are Moscow-time slots, "HH:MM" daily or
	// "DAY HH:MM" weekly (see scheduler.ParseSpec); "off" disables that job.
	SchedulerEnabled   bool
	ScheduleQuotes     string
	ScheduleFinancials string

	// TelegramBotToken/TelegramChatID make failed and partial data refreshes
	// the scheduler) send a message to that chat. Both empty
	// disables notifications.
	TelegramBotToken string
	TelegramChatID   string
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

		AuthUser:     getEnv("AUTH_USER", ""),
		AuthPassword: getEnv("AUTH_PASSWORD", ""),

		SchedulerEnabled:   getEnvBool("SCHEDULER_ENABLED"),
		ScheduleQuotes:     getEnv("SCHEDULE_QUOTES", "07:00"),
		ScheduleFinancials: getEnv("SCHEDULE_FINANCIALS", "sun 05:00"),

		TelegramBotToken: getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramChatID:   getEnv("TELEGRAM_CHAT_ID", ""),
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
	if (c.AuthUser == "") != (c.AuthPassword == "") {
		return fmt.Errorf("AUTH_USER and AUTH_PASSWORD (cmd/plot Basic Auth) must be set together")
	}
	if (c.TelegramBotToken == "") != (c.TelegramChatID == "") {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID (failure notifications) must be set together")
	}
	return nil
}

// UsesDefaultPassword reports whether the DB password is the built-in dev
// default. Callers log a warning so insecure deployments are visible.
func (c *Config) UsesDefaultPassword() bool {
	return c.DBPassword == defaultDBPassword
}

// AuthEnabled reports whether Basic Auth credentials are configured.
func (c *Config) AuthEnabled() bool {
	return c.AuthUser != "" && c.AuthPassword != ""
}

// NotifyEnabled reports whether failure notifications are configured.
func (c *Config) NotifyEnabled() bool {
	return c.TelegramBotToken != "" && c.TelegramChatID != ""
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvBool reports whether a variable is set to a true value (1, true, yes,
// on; case-insensitive).
func getEnvBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
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
