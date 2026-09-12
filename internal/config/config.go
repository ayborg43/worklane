package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL   string
	Addr          string
	CookieSecure  bool
	SessionTTLDay int
	BaseURL       string
	// AdminEmail is optional. If set, the matching (already-registered)
	// user is promoted to admin on every startup — see
	// auth.Repo.PromoteAdminByEmail. Deliberately never used to create an
	// account or touch a password.
	AdminEmail string
}

func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	baseURL := os.Getenv("BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	return &Config{
		DatabaseURL:   dbURL,
		Addr:          addr,
		CookieSecure:  os.Getenv("COOKIE_SECURE") == "true",
		SessionTTLDay: 7,
		BaseURL:       baseURL,
		AdminEmail:    os.Getenv("ADMIN_EMAIL"),
	}, nil
}
