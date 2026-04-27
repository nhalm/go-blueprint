# Configuration

How the `internal/config` package is structured, how each command loads the config it actually needs, and how canonlog gets initialized.

Error handling patterns (apperrors sentinels, `chikit.SetError`, when to `canonlog.ErrorAdd`) live in [API.md's Error Mapping section](API.md#error-mapping).

## Philosophy

- **One typed `Config` struct** owned by `internal/config`.
- **Per-command loaders** return the subset each command can run with. A `serve` with a missing `HTTP_PORT` should fail; a `migrate up` with a missing `HTTP_PORT` should not.
- **Validation lives here, not in handlers or services.** Field-level validation (required, bounds, format), default values, and cross-field constraints all happen inside `Load*` functions and return errors — no panics, no silent fallbacks.
- **Canonlog is set up per-command**, inside each `RunE`, right after `Load*()` succeeds. That lets each command honor `LOG_LEVEL` / `LOG_FORMAT` *before* doing any logging.

## Package Shape

```go
// internal/config/config.go
package config

import (
    "encoding/hex"
    "fmt"
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

## Two Loaders

**`Load()` — full config for `serve`.** Validates everything. Returns an error on any missing required field.

**`LoadDatabaseOnly()` — minimal config for `migrate`, cleanup jobs, CLI tools.** Only reads `DATABASE_URL`, `LOG_LEVEL`, `LOG_FORMAT`. A migration doesn't need an HTTP port or rate limit config, and failing on those would block running migrations on a host that only has DB creds.

```go
func Load() (Config, error) {
    databaseURL := viper.GetString("DATABASE_URL")
    if databaseURL == "" {
        return Config{}, fmt.Errorf("DATABASE_URL is required")
    }

    httpPort := viper.GetInt("HTTP_PORT")
    if httpPort == 0 { httpPort = 8080 }
    if httpPort < 1 || httpPort > 65535 {
        return Config{}, fmt.Errorf("HTTP_PORT must be 1-65535 (got %d)", httpPort)
    }

    logLevel := viper.GetString("LOG_LEVEL")
    if logLevel == "" { logLevel = "info" }
    if !isValidLogLevel(logLevel) {
        return Config{}, fmt.Errorf("LOG_LEVEL must be one of: debug, info, warn, error (got %q)", logLevel)
    }

    logFormat := viper.GetString("LOG_FORMAT")
    if logFormat == "" { logFormat = "text" }
    if !isValidLogFormat(logFormat) {
        return Config{}, fmt.Errorf("LOG_FORMAT must be one of: text, json (got %q)", logFormat)
    }

    maxBody := viper.GetInt("MAX_REQUEST_BODY_BYTES")
    if maxBody == 0 { maxBody = 1048576 }
    if maxBody < 1024 || maxBody > 10485760 {
        return Config{}, fmt.Errorf("MAX_REQUEST_BODY_BYTES must be 1KB-10MB (got %d)", maxBody)
    }

    dbMaxConns := viper.GetInt32("DB_MAX_CONNS"); if dbMaxConns == 0 { dbMaxConns = 25 }
    dbMinConns := viper.GetInt32("DB_MIN_CONNS"); if dbMinConns == 0 { dbMinConns = 5 }
    if dbMinConns > dbMaxConns {
        return Config{}, fmt.Errorf("DB_MIN_CONNS (%d) cannot exceed DB_MAX_CONNS (%d)", dbMinConns, dbMaxConns)
    }

    // ... rest of the fields, same pattern: default, validate, include in returned struct ...

    return Config{
        DatabaseURL:         databaseURL,
        HTTPPort:            httpPort,
        LogLevel:            logLevel,
        LogFormat:           logFormat,
        MaxRequestBodyBytes: maxBody,
        DBMaxConns:          dbMaxConns,
        DBMinConns:          dbMinConns,
        // ...
    }, nil
}

func LoadDatabaseOnly() (Config, error) {
    databaseURL := viper.GetString("DATABASE_URL")
    if databaseURL == "" {
        return Config{}, fmt.Errorf("DATABASE_URL is required")
    }

    logLevel := viper.GetString("LOG_LEVEL")
    if logLevel == "" { logLevel = "info" }
    if !isValidLogLevel(logLevel) {
        return Config{}, fmt.Errorf("LOG_LEVEL must be one of: debug, info, warn, error (got %q)", logLevel)
    }

    logFormat := viper.GetString("LOG_FORMAT")
    if logFormat == "" { logFormat = "text" }
    if !isValidLogFormat(logFormat) {
        return Config{}, fmt.Errorf("LOG_FORMAT must be one of: text, json (got %q)", logFormat)
    }

    return Config{
        DatabaseURL: databaseURL,
        LogLevel:    logLevel,
        LogFormat:   logFormat,
    }, nil
}

var validLogLevels  = []string{"debug", "info", "warn", "error"}
var validLogFormats = []string{"text", "json"}

func isValidLogLevel(l string)  bool { return slices.Contains(validLogLevels, l) }
func isValidLogFormat(f string) bool { return slices.Contains(validLogFormats, f) }
```

For opaque values like encryption keys, put the decode/length check in a helper:

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

Each command's `RunE` calls the appropriate loader first, sets up canonlog, then does its work. Nothing should log before `canonlog.SetupGlobalLogger` — if it has to, see [logging during CLI commands](#logging-during-cli-commands) below.

### `serve`

```go
// cmd/<app>/serve.go
func runServe(cmd *cobra.Command, args []string) error {
    ctx := context.Background()

    cfg, err := config.Load()
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }
    canonlog.SetupGlobalLogger(cfg.LogLevel, cfg.LogFormat)

    db := pgxkit.NewDB()
    if err := db.Connect(ctx, cfg.DatabaseURL,
        pgxkit.WithMaxConns(cfg.DBMaxConns),
        pgxkit.WithMinConns(cfg.DBMinConns),
        pgxkit.WithMaxConnLifetime(cfg.DBMaxConnLifetime),
        pgxkit.WithMaxConnIdleTime(cfg.DBMaxConnIdleTime),
    ); err != nil {
        return fmt.Errorf("failed to connect to database: %w", err)
    }
    defer db.Shutdown(ctx)

    // ... wire repositories, services, handler ...
    // ... start http.Server with graceful shutdown ...
}
```

### `migrate up`

```go
// cmd/<app>/migrate.go
func runMigrateUp(cmd *cobra.Command, args []string) error {
    ctx := context.Background()

    cfg, err := config.LoadDatabaseOnly()
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }
    canonlog.SetupGlobalLogger(cfg.LogLevel, cfg.LogFormat)

    m, err := migrate.New("file://./migrations", cfg.DatabaseURL)
    if err != nil {
        return fmt.Errorf("failed to create migrator: %w", err)
    }
    defer m.Close()

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
```

## Viper Wiring — `root.go`

`cobra.OnInitialize(initConfig)` runs once, before any subcommand's `RunE`. It only sets up viper — **no canonlog setup here**, no required-field validation here.

```go
// cmd/<app>/root.go
var cfgFile string

var rootCmd = &cobra.Command{
    Use:   "myapp",
    Short: "My application",
}

func init() {
    cobra.OnInitialize(initConfig)
    rootCmd.AddCommand(serveCmd)
    rootCmd.AddCommand(migrateCmd)
    // ... other commands ...
}

func initConfig() {
    if cfgFile != "" {
        viper.SetConfigFile(cfgFile)
    } else {
        viper.SetConfigFile(".env")
        viper.SetConfigType("env")
    }
    viper.AutomaticEnv()
    if err := viper.ReadInConfig(); err == nil {
        fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
    }
}
```

## Logging During CLI Commands

Runtime application logging must go through canonlog — see [observability conventions](ARCHITECTURE.md). Two narrow exceptions:

1. **Pre-canonlog**: the `"Using config file: ..."` note in `initConfig` goes to `stderr` via `fmt.Fprintln`. That runs before any subcommand has loaded its config, so canonlog isn't set up yet.

2. **Interactive CLI feedback**: after the structured canonlog event, a plain `fmt.Printf` / `fmt.Println` is appropriate for the human running the command. See the `migrate up` example above — the canonical log event goes to Datadog via canonlog; the `Migrations applied successfully. Current version: 5` line is for the operator watching the terminal.

These two are not substitutes for each other. Don't `fmt.Println` instead of a structured log event — do both when the user-facing output matters.

## Passing Config Into the Handler / Service Layer

Services and handlers that depend on config fields take a `config.Config` (or a struct that embeds relevant fields) via constructor. This keeps viper out of every package:

```go
func NewHandler(svc ProductServiceInterface, db *pgxkit.DB, cfg config.Config) *Handler {
    return &Handler{productService: svc, db: db, config: cfg}
}
```

The router then reads timing/limit values off `h.config` when building middleware:

```go
r.Use(chikit.Handler(chikit.WithTimeout(h.config.HTTPRequestTimeout), ...))
r.Use(chikit.MaxBodySize(int64(h.config.MaxRequestBodyBytes)))
```

## Test-Time Config

Tests assemble a minimal `config.Config` literal, skipping `Load()` entirely:

```go
cfg := config.Config{
    HTTPRequestTimeout:  30 * time.Second,
    MaxRequestBodyBytes: 1024 * 1024,
    RateLimitRequests:   100,
    RateLimitWindow:     time.Minute,
}
handler := api.NewHandler(mockSvc, nil, cfg)
```

Don't read env vars in tests. If the code under test really cares about env-driven behavior, route it through `config.Config` first.

## Unit-Testing the Loader

The loaders have enough conditional logic (defaults, validation, cross-field constraints) that they deserve their own unit tests. Test the happy path, each required-field failure, each out-of-bounds failure, and the cross-field constraint:

```go
// internal/config/config_test.go
package config

import (
    "testing"

    "github.com/spf13/viper"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestLoad_RequiresDatabaseURL(t *testing.T) {
    t.Cleanup(viper.Reset)
    viper.Reset()

    _, err := Load()
    require.Error(t, err)
    assert.Contains(t, err.Error(), "DATABASE_URL")
}

func TestLoad_AppliesDefaults(t *testing.T) {
    t.Cleanup(viper.Reset)
    viper.Reset()
    viper.Set("DATABASE_URL", "postgres://localhost/test")
    viper.Set("HTTP_READ_TIMEOUT_SECONDS", 15)
    viper.Set("HTTP_WRITE_TIMEOUT_SECONDS", 15)
    viper.Set("HTTP_IDLE_TIMEOUT_SECONDS", 60)
    viper.Set("HTTP_REQUEST_TIMEOUT_SECONDS", 30)

    cfg, err := Load()
    require.NoError(t, err)
    assert.Equal(t, 8080, cfg.HTTPPort)
    assert.Equal(t, "info", cfg.LogLevel)
    assert.Equal(t, "text", cfg.LogFormat)
}

func TestLoad_RejectsMinConnsGreaterThanMaxConns(t *testing.T) {
    t.Cleanup(viper.Reset)
    viper.Reset()
    viper.Set("DATABASE_URL", "postgres://localhost/test")
    viper.Set("HTTP_READ_TIMEOUT_SECONDS", 15)
    viper.Set("HTTP_WRITE_TIMEOUT_SECONDS", 15)
    viper.Set("HTTP_IDLE_TIMEOUT_SECONDS", 60)
    viper.Set("HTTP_REQUEST_TIMEOUT_SECONDS", 30)
    viper.Set("DB_MAX_CONNS", 5)
    viper.Set("DB_MIN_CONNS", 10)

    _, err := Load()
    require.Error(t, err)
    assert.Contains(t, err.Error(), "DB_MIN_CONNS")
}
```

`viper.Reset()` in `t.Cleanup` keeps tests independent — viper is package-global state, so leaking between tests is easy to do accidentally.
