package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	canonhttp "github.com/nhalm/canonlog/http"
	"github.com/nhalm/chikit/ratelimit"
	"github.com/nhalm/chikit/ratelimit/store"
	chikitvalidate "github.com/nhalm/chikit/validate"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	_ "github.com/yourorg/myapp/docs" // Generated Swagger docs
)

type RouteConfig struct {
	ReadRPS        int
	WriteRPS       int
	MaxBodyBytes   int64
	AllowedOrigins []string
}

func DefaultRouteConfig() RouteConfig {
	return RouteConfig{
		ReadRPS:        100,
		WriteRPS:       20,
		MaxBodyBytes:   1048576,
		AllowedOrigins: []string{"http://localhost:5173"},
	}
}

func (h *Handler) Routes() http.Handler {
	return h.RoutesWithConfig(DefaultRouteConfig())
}

func (h *Handler) RoutesWithConfig(config RouteConfig) http.Handler {
	r := chi.NewRouter()

	st := store.NewMemory()

	readLimiter := ratelimit.NewBuilder(st).
		WithName("read").
		WithIP().
		Limit(config.ReadRPS, time.Second)

	writeLimiter := ratelimit.NewBuilder(st).
		WithName("write").
		WithIP().
		Limit(config.WriteRPS, time.Second)

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(canonhttp.ChiMiddleware(nil))
	r.Use(chikitvalidate.MaxBodySize(config.MaxBodyBytes))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   config.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	r.Get("/swagger/*", httpSwagger.WrapHandler)

	r.Route("/api/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(readLimiter)
			r.Get("/products", h.ListProducts)
			r.Get("/products/{id}", h.GetProduct)
		})

		r.Group(func(r chi.Router) {
			r.Use(writeLimiter)
			r.Post("/products", h.CreateProduct)
			r.Patch("/products/{id}", h.UpdateProduct)
			r.Delete("/products/{id}", h.DeleteProduct)
		})
	})

	return r
}

func ParseAllowedOrigins(originsStr string) []string {
	if originsStr == "" {
		return []string{"http://localhost:5173"}
	}
	origins := strings.Split(originsStr, ",")
	for i, origin := range origins {
		origins[i] = strings.TrimSpace(origin)
	}
	return origins
}
