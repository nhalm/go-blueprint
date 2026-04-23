package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nhalm/canonlog"
	"github.com/nhalm/pgxkit"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/yourorg/myapp/internal/api"
	"github.com/yourorg/myapp/internal/repository"
	"github.com/yourorg/myapp/internal/service"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP server",
	RunE:  runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().IntP("port", "p", 8080, "Port to run the server on")
	serveCmd.Flags().String("host", "0.0.0.0", "Host to bind the server to")
	_ = viper.BindPFlag("PORT", serveCmd.Flags().Lookup("port"))
	_ = viper.BindPFlag("HOST", serveCmd.Flags().Lookup("host"))
}

func runServe(_ *cobra.Command, _ []string) error {
	host := viper.GetString("HOST")
	port := viper.GetInt("PORT")
	addr := fmt.Sprintf("%s:%d", host, port)

	databaseURL := viper.GetString("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	ctx := context.Background()
	db := pgxkit.NewDB()
	if err := db.Connect(ctx, databaseURL); err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer func() { _ = db.Shutdown(ctx) }()

	// Repositories
	productRepo := repository.NewProductRepository(db)

	// Services
	productSvc := service.NewProductService(productRepo)

	// Handler
	handler := api.NewHandler(productSvc)

	routeConfig := api.RouteConfig{
		ReadRPS:        viper.GetInt("RATE_LIMIT_READ_RPS"),
		WriteRPS:       viper.GetInt("RATE_LIMIT_WRITE_RPS"),
		MaxBodyBytes:   viper.GetInt64("MAX_REQUEST_BODY_BYTES"),
		AllowedOrigins: api.ParseAllowedOrigins(viper.GetString("CORS_ALLOWED_ORIGINS")),
	}

	if routeConfig.ReadRPS == 0 {
		routeConfig.ReadRPS = 100
	}
	if routeConfig.WriteRPS == 0 {
		routeConfig.WriteRPS = 20
	}
	if routeConfig.MaxBodyBytes == 0 {
		routeConfig.MaxBodyBytes = 1048576
	}

	srv := &http.Server{
		Addr:           addr,
		Handler:        handler.RoutesWithConfig(routeConfig),
		ReadTimeout:    15 * time.Second,
		WriteTimeout:   15 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1048576,
	}

	serverErrCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErrCh <- err
		}
	}()

	startCtx := canonlog.NewContext(context.Background())
	canonlog.InfoAddMany(startCtx, map[string]any{
		"event": "server_starting",
		"addr":  addr,
	})
	canonlog.Flush(startCtx)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrCh:
		return err
	case sig := <-quit:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server forced to shutdown: %w", err)
		}

		logCtx := canonlog.NewContext(context.Background())
		canonlog.InfoAddMany(logCtx, map[string]any{
			"event":  "server_stopped",
			"signal": sig.String(),
		})
		canonlog.Flush(logCtx)
		return nil
	}
}
