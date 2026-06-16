package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/joho/godotenv"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/migrate <up|down|version|force VERSION>")
		os.Exit(1)
	}

	_ = godotenv.Load()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:paperless@localhost:5432/paperless?sslmode=disable"
	}

	migrationsPath := migrationsDir()
	sourceURL := "file://" + migrationsPath

	m, err := migrate.New(sourceURL, dsn)
	if err != nil {
		log.Fatalf("migrate init: %v", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			log.Printf("migrate close source: %v", srcErr)
		}
		if dbErr != nil {
			log.Printf("migrate close db: %v", dbErr)
		}
	}()

	cmd := os.Args[1]
	switch cmd {
	case "up":
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			log.Fatalf("migrate up: %v", err)
		}
		printVersion(m)
		fmt.Println("migrate up: done")
	case "down":
		if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			log.Fatalf("migrate down: %v", err)
		}
		fmt.Println("migrate down: done")
	case "version":
		printVersion(m)
	case "force":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: migrate force VERSION")
			os.Exit(1)
		}
		var ver int
		if _, err := fmt.Sscanf(os.Args[2], "%d", &ver); err != nil {
			log.Fatalf("invalid version: %v", err)
		}
		if err := m.Force(ver); err != nil {
			log.Fatalf("migrate force: %v", err)
		}
		fmt.Printf("forced to version %d\n", ver)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		os.Exit(1)
	}
}

func printVersion(m *migrate.Migrate) {
	ver, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		log.Printf("version check: %v", err)
		return
	}
	if errors.Is(err, migrate.ErrNilVersion) {
		fmt.Println("schema version: none (empty DB)")
		return
	}
	fmt.Printf("schema version: %d (dirty=%v)\n", ver, dirty)
}

// migrationsDir resolves the migrations folder relative to this source file
// so the binary works from any working directory during development.
// In production, set MIGRATIONS_PATH env to an absolute path.
func migrationsDir() string {
	if p := os.Getenv("MIGRATIONS_PATH"); p != "" {
		return p
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "migrations"
	}
	// cmd/migrate/main.go → ../../migrations
	return filepath.Join(filepath.Dir(filename), "..", "..", "migrations")
}
