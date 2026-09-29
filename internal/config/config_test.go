package config

import "testing"

func TestLoadConfigDefaults(t *testing.T) {
	// Ensure a clean environment for the defaults.
	for _, k := range []string{"PORT", "DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SSLMODE", "CSV_PATH"} {
		t.Setenv(k, "")
	}
	cfg := LoadConfig()
	if cfg.Port != 8088 {
		t.Errorf("Port = %d, want 8088", cfg.Port)
	}
	if cfg.DBHost != "localhost" {
		t.Errorf("DBHost = %q, want localhost", cfg.DBHost)
	}
	if !cfg.UsesDefaultPassword() {
		t.Errorf("expected default password to be detected")
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("DB_PASSWORD", "s3cret")
	cfg := LoadConfig()
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
	if cfg.UsesDefaultPassword() {
		t.Errorf("UsesDefaultPassword should be false for a custom password")
	}
}

func TestLoadConfigInvalidPortFallsBack(t *testing.T) {
	t.Setenv("PORT", "not-a-number")
	cfg := LoadConfig()
	if cfg.Port != 8088 {
		t.Errorf("Port = %d, want fallback 8088", cfg.Port)
	}
}

func TestValidate(t *testing.T) {
	base := func() *Config {
		return &Config{Port: 8088, DBHost: "h", DBPort: "5432", DBUser: "u", DBName: "d"}
	}
	if err := base().Validate(); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}

	tests := map[string]func(*Config){
		"bad port":     func(c *Config) { c.Port = 0 },
		"high port":    func(c *Config) { c.Port = 70000 },
		"empty host":   func(c *Config) { c.DBHost = "" },
		"empty port":   func(c *Config) { c.DBPort = "" },
		"empty user":   func(c *Config) { c.DBUser = "" },
		"empty dbname": func(c *Config) { c.DBName = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := base()
			mutate(c)
			if err := c.Validate(); err == nil {
				t.Errorf("expected error for %s", name)
			}
		})
	}
}

func TestValidateAuthPairing(t *testing.T) {
	base := func() *Config {
		return &Config{Port: 8088, DBHost: "h", DBPort: "5432", DBUser: "u", DBName: "d"}
	}

	c := base()
	if err := c.Validate(); err != nil || c.AuthEnabled() {
		t.Fatalf("no auth: err=%v enabled=%v, want nil/false", err, c.AuthEnabled())
	}

	c = base()
	c.AuthUser, c.AuthPassword = "admin", "s3cret"
	if err := c.Validate(); err != nil || !c.AuthEnabled() {
		t.Fatalf("full auth: err=%v enabled=%v, want nil/true", err, c.AuthEnabled())
	}

	for _, half := range []struct{ user, pass string }{{"admin", ""}, {"", "s3cret"}} {
		c = base()
		c.AuthUser, c.AuthPassword = half.user, half.pass
		if err := c.Validate(); err == nil {
			t.Errorf("user=%q pass=%q: expected error for half-configured auth", half.user, half.pass)
		}
	}
}
