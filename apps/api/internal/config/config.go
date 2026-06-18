package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all runtime configuration for paperless-api.
//
// Unlike sml-api-bybos (multi-tenant pool per SML database), PaperLess owns a
// single database, so there is one DSN and one pool — no tenant routing here.
// SML is reached only through the sml-api-bybos gateway (see SML.*).
type Config struct {
	Server struct {
		Host string
		Port string
	}
	DB struct {
		URL              string // full postgres DSN to the PaperLess database
		MaxConns         int32
		MinConns         int32
		StatementTimeout int // milliseconds; 0 = no timeout (default: 5000)
	}
	Auth struct {
		JWTSecret string
	}
	Storage struct {
		Endpoint  string
		AccessKey string
		SecretKey string
		Bucket    string
		UseSSL    bool
	}
	SML struct {
		BaseURL string // e.g. http://192.168.2.109:8200
		APIKey  string // X-Api-Key for sml-api-bybos
		Tenant  string // X-Tenant
	}
}

func Load() *Config {
	_ = godotenv.Load()

	c := &Config{}
	c.Server.Host = getEnv("APP_HOST", "0.0.0.0")
	c.Server.Port = getEnv("APP_PORT", "8080")

	c.DB.URL = getEnv("DATABASE_URL", "postgres://postgres:paperless@localhost:5432/paperless?sslmode=disable")
	c.DB.MaxConns = int32(getEnvInt("DB_MAX_CONNS", 10))
	c.DB.MinConns = int32(getEnvInt("DB_MIN_CONNS", 2))
	c.DB.StatementTimeout = getEnvInt("DB_STATEMENT_TIMEOUT_MS", 5000)

	c.Auth.JWTSecret = getEnv("JWT_SECRET", "")

	c.Storage.Endpoint = getEnv("MINIO_ENDPOINT", "localhost:9000")
	c.Storage.AccessKey = getEnv("MINIO_ACCESS_KEY", "")
	c.Storage.SecretKey = getEnv("MINIO_SECRET_KEY", "")
	c.Storage.Bucket = getEnv("MINIO_BUCKET", "paperless")
	c.Storage.UseSSL = getEnvBool("MINIO_USE_SSL", false)

	c.SML.BaseURL = getEnv("SML_API_BASE_URL", "http://192.168.2.109:8200")
	c.SML.APIKey = getEnv("SML_API_KEY", "")
	c.SML.Tenant = getEnv("SML_TENANT", "")

	return c
}

// Validate returns an error if a required-for-production value is missing.
// It is intentionally lenient for local dev (empty secrets allowed) but the
// caller may choose to fail fast in production.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.DB.URL) == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	return nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
