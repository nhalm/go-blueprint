# Configuration

How the `internal/config` package is structured, how each command loads the config it actually needs, and how canonlog gets initialized.

Error handling patterns (apperrors sentinels, `chikit.SetError`, when to `canonlog.ErrorAdd`) live in [ERRORS.md](ERRORS.md).

## Philosophy

- **One flat `Config` struct** owned by `internal/config`. Flat because env vars are flat.
- **Composable group loaders** — `LoadDatabase()`, `LoadLogging()`, `LoadHTTP()`, `LoadRedis()` — each validates its own fields. Each `RunE` calls the groups it needs and reads any command-specific fields inline. No duplication, no loading config a command doesn't use.
- **Validation lives here, not in handlers or services.** Field-level validation (required, bounds, format), default values, and cross-field constraints all happen inside loader functions and return errors — no panics, no silent fallbacks.
- **Canonlog is set up per-command**, inside each `RunE`, right after `LoadLogging()` succeeds. That lets each command honor `LOG_LEVEL` / `LOG_FORMAT` *before* doing any logging.

## Package Shape

One flat struct, populated by composable group loaders:

```go
// internal/config/config.go
package config

import (
    "encoding/hex"
    "errors"
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

## Group Loaders

Each loader validates and populates its slice of `Config`. `RunE` calls the groups it needs, then reads any command-specific fields inline.

```go
func LoadLogging(cfg *Config) error {
    viper.SetConfigFile(".env")
    viper.SetConfigType("env")
    viper.AutomaticEnv()
    if err := viper.ReadInConfig(); err != nil {
        var notFound viper.ConfigFileNotFoundError
        if !errors.As(err, &notFound) {
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

Each `RunE` calls `config.LoadLogging` first — which also initializes viper — then sets up canonlog, then calls the remaining group loaders it needs. Nothing should log before `canonlog.SetupGlobalLogger`.

### `serve`

```go
// cmd/<app>/serve.go
func runServe(cmd *cobra.Command, args []string) error {
    ctx := context.Background()

    var cfg config.Config
    if err := config.LoadLogging(&cfg); err != nil {
        return err
    }
    canonlog.SetupGlobalLogger(cfg.LogLevel, cfg.LogFormat)

    if err := config.LoadDatabase(&cfg); err != nil {
        return err
    }
    if err := config.LoadHTTP(&cfg); err != nil {
        return err
    }
    if err := config.LoadRedis(&cfg); err != nil {
        return err
    }
    // ... load any serve-specific fields inline ...

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

    var cfg config.Config
    if err := config.LoadLogging(&cfg); err != nil {
        return err
    }
    canonlog.SetupGlobalLogger(cfg.LogLevel, cfg.LogFormat)

    if err := config.LoadDatabase(&cfg); err != nil {
        return err
    }

    m, err := migrate.New("file://internal/database/migrations", cfg.DatabaseURL)
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

`root.go` is a pure entry point — it registers subcommands and nothing else. Viper setup happens inside each loader, so errors propagate as return values rather than being swallowed in a `cobra.OnInitialize` callback.

```go
// cmd/<app>/root.go
var rootCmd = &cobra.Command{
    Use:   "myapp",
    Short: "My application",
}

func init() {
    rootCmd.AddCommand(serveCmd)
    rootCmd.AddCommand(migrateCmd)
    // ... other commands ...
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
