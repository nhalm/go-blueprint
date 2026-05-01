# Configuration

How the `internal/config` package is structured, how each command loads the config it actually needs, and how canonlog gets initialized.

Error handling patterns (apperrors sentinels, `chikit.SetError`, when to `canonlog.ErrorAdd`) live in [ERRORS.md](ERRORS.md). The canonical handler / service wiring this `config.Config` flows into is in [EXAMPLE.md](EXAMPLE.md). Full canonlog API surface (`SetupGlobalLogger`, `New`, `InfoAdd`, `Flush`) is in [LIBRARIES.md](LIBRARIES.md#canonlog).

## Philosophy

- **One flat `Config` struct** owned by `internal/config`. Flat because env vars are flat.
- **Composable group loaders** — `LoadDatabase()`, `LoadLogging()`, `LoadHTTP()`, `LoadRedis()` — each validates its own fields. Each `RunE` calls the groups it needs and reads any command-specific fields inline. No duplication, no loading config a command doesn't use.
- **Validation lives here, not in handlers or services.** Field-level validation (required, bounds, format), default values, and cross-field constraints all happen inside loader functions and return errors — no panics, no silent fallbacks.
- **Canonlog is set up per-command**, inside each `RunE`, right after `LoadLogging()` succeeds. That lets each command honor `LOG_LEVEL` / `LOG_FORMAT` *before* doing any logging.

## Package Shape

One flat struct, populated by composable group loaders:

```go {file=internal/config/config.go}
// Package config holds the flat Config struct plus group loaders
// (LoadLogging, LoadDatabase, LoadHTTP, LoadRedis) that each command's RunE
// composes. Validation, defaults, and cross-field constraints live here, not
// in handlers or services.
package config

import (
    "encoding/hex"
    "errors"
    "fmt"
    "io/fs"
    "slices"
    "time"

    "github.com/spf13/viper"
)

type Config struct {
    DatabaseURL         string
    DBMaxConns          int32
    DBMinConns          int32
    DBMaxConnLifetime   time.Duration
    DBMaxConnIdleTime   time.Duration
    HTTPPort            int
    HTTPReadTimeout     time.Duration
    HTTPWriteTimeout    time.Duration
    HTTPIdleTimeout     time.Duration
    HTTPRequestTimeout  time.Duration
    MaxRequestBodyBytes int
    RateLimitRequests   int
    RateLimitWindow     time.Duration
    RedisURL            string
    RedisPassword       string
    RedisDB             int
    RedisPrefix         string
    LogLevel            string
    LogFormat           string
    // ... service-specific fields (encryption keys, feature flags, etc.) ...
}
```

## Group Loaders

Each loader validates and populates its slice of `Config`. `RunE` calls the groups it needs, then reads any command-specific fields inline.

```go {file=internal/config/config.go}
func LoadLogging(cfg *Config) error {
    viper.SetConfigFile(".env")
    viper.SetConfigType("env")
    viper.AutomaticEnv()
    if err := viper.ReadInConfig(); err != nil {
        // Missing .env is fine — environment variables still flow through
        // AutomaticEnv. ConfigFileNotFoundError is only returned when using
        // SetConfigName + AddConfigPath; SetConfigFile produces an os
        // not-exist error instead, so check both.
        var notFound viper.ConfigFileNotFoundError
        if !errors.As(err, &notFound) && !errors.Is(err, fs.ErrNotExist) {
            return fmt.Errorf("failed to read config file: %w", err)
        }
    }

    logLevel := viper.GetString("LOG_LEVEL")
    if logLevel == "" { logLevel = "info" }
    if !isValidLogLevel(logLevel) {
        return fmt.Errorf("LOG_LEVEL must be one of: debug, info, warn, error (got %q)", logLevel)
    }

    logFormat := viper.GetString("LOG_FORMAT")
    if logFormat == "" { logFormat = "text" }
    if !isValidLogFormat(logFormat) {
        return fmt.Errorf("LOG_FORMAT must be one of: text, json (got %q)", logFormat)
    }

    cfg.LogLevel  = logLevel
    cfg.LogFormat = logFormat
    return nil
}

func LoadDatabase(cfg *Config) error {
    databaseURL := viper.GetString("DATABASE_URL")
    if databaseURL == "" {
        return fmt.Errorf("DATABASE_URL is required")
    }

    dbMaxConns := viper.GetInt32("DB_MAX_CONNS"); if dbMaxConns == 0 { dbMaxConns = 25 }
    dbMinConns := viper.GetInt32("DB_MIN_CONNS"); if dbMinConns == 0 { dbMinConns = 5 }
    if dbMinConns > dbMaxConns {
        return fmt.Errorf("DB_MIN_CONNS (%d) cannot exceed DB_MAX_CONNS (%d)", dbMinConns, dbMaxConns)
    }

    cfg.DatabaseURL       = databaseURL
    cfg.DBMaxConns        = dbMaxConns
    cfg.DBMinConns        = dbMinConns
    // ... DBMaxConnLifetime, DBMaxConnIdleTime ...
    return nil
}

func LoadHTTP(cfg *Config) error {
    httpPort := viper.GetInt("HTTP_PORT")
    if httpPort == 0 { httpPort = 8080 }
    if httpPort < 1 || httpPort > 65535 {
        return fmt.Errorf("HTTP_PORT must be 1-65535 (got %d)", httpPort)
    }

    maxBody := viper.GetInt("MAX_REQUEST_BODY_BYTES")
    if maxBody == 0 { maxBody = 1048576 }
    if maxBody < 1024 || maxBody > 10485760 {
        return fmt.Errorf("MAX_REQUEST_BODY_BYTES must be 1KB-10MB (got %d)", maxBody)
    }

    cfg.HTTPPort            = httpPort
    cfg.MaxRequestBodyBytes = maxBody
    // ... timeouts, rate limit ...
    return nil
}

func LoadRedis(cfg *Config) error {
    redisURL := viper.GetString("REDIS_URL")
    if redisURL == "" {
        return fmt.Errorf("REDIS_URL is required")
    }
    cfg.RedisURL      = redisURL
    cfg.RedisPassword = viper.GetString("REDIS_PASSWORD")
    cfg.RedisDB       = viper.GetInt("REDIS_DB")
    cfg.RedisPrefix   = viper.GetString("REDIS_PREFIX")
    return nil
}

var validLogLevels  = []string{"debug", "info", "warn", "error"}
var validLogFormats = []string{"text", "json"}

func isValidLogLevel(l string)  bool { return slices.Contains(validLogLevels, l) }
func isValidLogFormat(f string) bool { return slices.Contains(validLogFormats, f) }

```

For opaque values like encryption keys, put the decode/length check in a helper (illustrative — not used by the canonical Products slice; add to your service when you need it):

```go
func loadHexKey(envVar string) ([]byte, error) {
    s := viper.GetString(envVar)
    if s == "" { return nil, fmt.Errorf("%s is required", envVar) }
    if len(s) != 64 {
        return nil, fmt.Errorf("%s must be 64 hex chars (got %d)", envVar, len(s))
    }
    key, err := hex.DecodeString(s)
    if err != nil { return nil, fmt.Errorf("%s contains invalid hex: %w", envVar, err) }
    return key, nil
}
```

## Command Usage

Each `RunE` calls `config.LoadLogging` first — which also initializes viper — then sets up canonlog, then calls the remaining group loaders it needs. Nothing should log before `canonlog.SetupGlobalLogger`.

### `serve`

`serve` calls in order: `LoadLogging` → `canonlog.SetupGlobalLogger` → `LoadDatabase` → `LoadHTTP` → `LoadRedis` (when Redis is in use), then wires `pgxkit.NewDB().Connect(...)` with the loaded pool options, then constructs repositories / services / handler / router and starts the HTTP server with graceful shutdown.

The full canonical implementation lives in [ARCHITECTURE.md](ARCHITECTURE.md#explicit-dependency-injection) — that doc owns the DI pattern, so the `runServe` example sits there alongside the dependency-flow rules it illustrates.

### `migrate`

The `migrate` subcommand wraps `golang-migrate/migrate/v4` with the per-command config-loading pattern. Each `RunE` calls `LoadLogging` → `SetupGlobalLogger` → `LoadDatabase`, then drives `m.Up()`, `m.Down()`, or `m.Version()`.

```go {file=cmd/myapp/migrate.go}
// cmd/myapp/migrate.go
package main

import (
    "context"
    "errors"
    "fmt"

    "github.com/golang-migrate/migrate/v4"
    _ "github.com/golang-migrate/migrate/v4/database/postgres"
    _ "github.com/golang-migrate/migrate/v4/source/file"
    "github.com/nhalm/canonlog"
    "github.com/spf13/cobra"

    "github.com/yourorg/myapp/internal/config"
)

var migrateCmd = &cobra.Command{
    Use:   "migrate",
    Short: "Run database migrations",
}

var migrateUpCmd = &cobra.Command{
    Use:   "up",
    Short: "Apply all pending migrations",
    RunE:  runMigrateUp,
}

var migrateDownCmd = &cobra.Command{
    Use:   "down",
    Short: "Roll back the last migration",
    RunE:  runMigrateDown,
}

var migrateVersionCmd = &cobra.Command{
    Use:   "version",
    Short: "Show current migration version",
    RunE:  runMigrateVersion,
}

func init() {
    migrateCmd.AddCommand(migrateUpCmd)
    migrateCmd.AddCommand(migrateDownCmd)
    migrateCmd.AddCommand(migrateVersionCmd)
}

func loadMigrateConfig(cfg *config.Config) error {
    if err := config.LoadLogging(cfg); err != nil {
        return err
    }
    canonlog.SetupGlobalLogger(cfg.LogLevel, cfg.LogFormat)
    return config.LoadDatabase(cfg)
}

func newMigrator(databaseURL string) (*migrate.Migrate, error) {
    m, err := migrate.New("file://internal/database/migrations", databaseURL)
    if err != nil {
        return nil, fmt.Errorf("failed to create migrator: %w", err)
    }
    return m, nil
}

func runMigrateUp(cmd *cobra.Command, args []string) error {
    ctx := context.Background()

    var cfg config.Config
    if err := loadMigrateConfig(&cfg); err != nil {
        return err
    }

    m, err := newMigrator(cfg.DatabaseURL)
    if err != nil {
        return err
    }
    defer func() { _, _ = m.Close() }()

    if err := m.Up(); err != nil {
        if errors.Is(err, migrate.ErrNoChange) {
            log := canonlog.New()
            log.InfoAdd("component", "migrate").InfoAdd("direction", "up")
            log.Flush(ctx)
            fmt.Println("Database is up to date")
            return nil
        }
        return fmt.Errorf("migration up failed: %w", err)
    }

    version, dirty, _ := m.Version()
    log := canonlog.New()
    log.InfoAdd("component", "migrate").InfoAdd("direction", "up").
        InfoAdd("version", version).InfoAdd("dirty", dirty)
    log.Flush(ctx)
    fmt.Printf("Migrations applied successfully. Current version: %d\n", version)
    return nil
}

func runMigrateDown(cmd *cobra.Command, args []string) error {
    ctx := context.Background()

    var cfg config.Config
    if err := loadMigrateConfig(&cfg); err != nil {
        return err
    }

    m, err := newMigrator(cfg.DatabaseURL)
    if err != nil {
        return err
    }
    defer func() { _, _ = m.Close() }()

    if err := m.Steps(-1); err != nil {
        if errors.Is(err, migrate.ErrNoChange) {
            fmt.Println("No migrations to roll back")
            return nil
        }
        return fmt.Errorf("migration down failed: %w", err)
    }

    version, dirty, _ := m.Version()
    log := canonlog.New()
    log.InfoAdd("component", "migrate").InfoAdd("direction", "down").
        InfoAdd("version", version).InfoAdd("dirty", dirty)
    log.Flush(ctx)
    fmt.Printf("Rolled back. Current version: %d\n", version)
    return nil
}

func runMigrateVersion(cmd *cobra.Command, args []string) error {
    var cfg config.Config
    if err := loadMigrateConfig(&cfg); err != nil {
        return err
    }

    m, err := newMigrator(cfg.DatabaseURL)
    if err != nil {
        return err
    }
    defer func() { _, _ = m.Close() }()

    version, dirty, err := m.Version()
    if err != nil {
        if errors.Is(err, migrate.ErrNilVersion) {
            fmt.Println("No migrations applied yet")
            return nil
        }
        return fmt.Errorf("failed to read migration version: %w", err)
    }
    fmt.Printf("Current version: %d (dirty=%t)\n", version, dirty)
    return nil
}
```

## Viper Wiring — `root.go`

`root.go` is a pure entry point — it registers subcommands and nothing else. Viper setup happens inside each loader, so errors propagate as return values rather than being swallowed in a `cobra.OnInitialize` callback.

```go {file=cmd/myapp/root.go}
// cmd/myapp/root.go
package main

import (
    "github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
    Use:   "myapp",
    Short: "My application",
}

func init() {
    rootCmd.AddCommand(serveCmd)
    rootCmd.AddCommand(migrateCmd)
}
```

## Logging During CLI Commands

Runtime application logging must go through canonlog — see [observability conventions](ARCHITECTURE.md). One narrow exception:

**Interactive CLI feedback**: after the structured canonlog event, a plain `fmt.Printf` / `fmt.Println` is appropriate for the human running the command. See the `migrate up` example above — the canonical log event goes to Datadog via canonlog; the `Migrations applied successfully. Current version: 5` line is for the operator watching the terminal.

Don't `fmt.Println` instead of a structured log event — do both when the user-facing output matters.

## Passing Config Into the Handler / Service Layer

Services and handlers that depend on config fields take a `config.Config` (or a struct that embeds relevant fields) via constructor. This keeps viper out of every package:

```go
func NewHandler(svc ProductServiceInterface, db *pgxkit.DB, redis Pinger, cfg config.Config) *Handler {
    return &Handler{productService: svc, db: db, redis: redis, config: cfg}
}
```

The router then reads timing/limit values off `h.config` when building middleware:

```go
r.Use(chikit.Handler(chikit.WithTimeout(h.config.HTTPRequestTimeout), ...))
r.Use(chikit.MaxBodySize(int64(h.config.MaxRequestBodyBytes)))
```

## Test-Time Config

Tests assemble a minimal `config.Config` literal directly — no group loaders, no env vars:

```go
cfg := config.Config{
    HTTPRequestTimeout:  30 * time.Second,
    MaxRequestBodyBytes: 1024 * 1024,
    RateLimitRequests:   100,
    RateLimitWindow:     time.Minute,
}
handler := api.NewHandler(mockSvc, nil, nil, cfg)
```

Don't read env vars in tests. If the code under test really cares about env-driven behavior, route it through `config.Config` first.
