package cmd

import (
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Database migration commands",
}

var migrateUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Run all pending migrations",
	RunE: func(_ *cobra.Command, _ []string) error {
		return runMigrations("up")
	},
}

var migrateDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Rollback the last migration",
	RunE: func(_ *cobra.Command, _ []string) error {
		return runMigrations("down")
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
	migrateCmd.AddCommand(migrateUpCmd)
	migrateCmd.AddCommand(migrateDownCmd)
}

func runMigrations(direction string) error {
	databaseURL := viper.GetString("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	m, err := migrate.New(
		"file://internal/database/migrations",
		databaseURL,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}
	defer m.Close()

	switch direction {
	case "up":
		if err := m.Up(); err != nil && err != migrate.ErrNoChange {
			return fmt.Errorf("failed to run migrations: %w", err)
		}
		fmt.Println("Migrations completed successfully")
	case "down":
		if err := m.Steps(-1); err != nil && err != migrate.ErrNoChange {
			return fmt.Errorf("failed to rollback migration: %w", err)
		}
		fmt.Println("Migration rolled back successfully")
	}

	return nil
}
