package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	ragv1 "github.com/knoguchi/rag/gen/rag/v1"
	"github.com/knoguchi/rag/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
)

// HTTPServer wraps an HTTP server with grpc-gateway integration
type HTTPServer struct {
	server    *http.Server
	router    *chi.Mux
	gwMux     *runtime.ServeMux
	logger    *slog.Logger
	port      int
	grpcAddr  string
	grpcConn  *grpc.ClientConn
	dbChecker HealthChecker
}

// HealthChecker can report whether a dependency is healthy.
type HealthChecker interface {
	Ping(ctx context.Context) error
}

// HTTPServerConfig holds configuration for the HTTP server
type HTTPServerConfig struct {
	Port           int
	GRPCAddr       string // Address of the gRPC server (e.g., "localhost:9090")
	Logger         *slog.Logger
	AllowedOrigins []string      // CORS allowed origins
	DBChecker      HealthChecker // Database health checker (optional)
	RateLimitRPS   float64       // Requests per second per IP (0 = disabled)
	RateLimitBurst int           // Burst capacity per IP
}

// NewHTTPServer creates a new HTTP server with grpc-gateway
func NewHTTPServer(cfg HTTPServerConfig) (*HTTPServer, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Create chi router
	router := chi.NewRouter()

	// Add middleware
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	if cfg.RateLimitRPS > 0 {
		router.Use(rateLimitMiddleware(cfg.RateLimitRPS, cfg.RateLimitBurst))
	}
	router.Use(requestLoggingMiddleware(logger))
	router.Use(middleware.Recoverer)
	router.Use(corsMiddleware(cfg.AllowedOrigins))

	// Create grpc-gateway mux with JSON marshaler options.
	// The header matcher forwards X-API-Key into gRPC metadata — without it
	// the gateway drops the header and REST requests can never authenticate.
	gwMux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{
				UseProtoNames:   true,
				EmitUnpopulated: true,
			},
			UnmarshalOptions: protojson.UnmarshalOptions{
				DiscardUnknown: true,
			},
		}),
		runtime.WithIncomingHeaderMatcher(apiKeyHeaderMatcher),
	)

	// Mount health check endpoint
	router.Get("/healthz", healthCheckHandler())
	router.Get("/readyz", readinessCheckHandler(cfg.DBChecker))

	// Mount grpc-gateway under root
	router.Mount("/", gwMux)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute, // Increased for streaming LLM responses
		IdleTimeout:  120 * time.Second,
	}

	return &HTTPServer{
		server:    server,
		router:    router,
		gwMux:     gwMux,
		logger:    logger,
		port:      cfg.Port,
		grpcAddr:  cfg.GRPCAddr,
		dbChecker: cfg.DBChecker,
	}, nil
}

// RegisterHandlers registers grpc-gateway handlers by connecting to the gRPC server
func (s *HTTPServer) RegisterHandlers(ctx context.Context) error {
	// Connect to gRPC server
	conn, err := grpc.NewClient(
		s.grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to gRPC server: %w", err)
	}
	s.grpcConn = conn

	// Register all service handlers
	if err := ragv1.RegisterTenantServiceHandler(ctx, s.gwMux, conn); err != nil {
		return fmt.Errorf("failed to register TenantService handler: %w", err)
	}
	s.logger.Info("registered TenantService HTTP handler")

	if err := ragv1.RegisterDocumentServiceHandler(ctx, s.gwMux, conn); err != nil {
		return fmt.Errorf("failed to register DocumentService handler: %w", err)
	}
	s.logger.Info("registered DocumentService HTTP handler")

	if err := ragv1.RegisterRAGServiceHandler(ctx, s.gwMux, conn); err != nil {
		return fmt.Errorf("failed to register RAGService handler: %w", err)
	}
	s.logger.Info("registered RAGService HTTP handler")

	return nil
}

// Start starts the HTTP server
func (s *HTTPServer) Start() error {
	s.logger.Info("starting HTTP server", "address", s.server.Addr)

	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTP server error: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the HTTP server
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down HTTP server")

	// Close gRPC connection if exists
	if s.grpcConn != nil {
		if err := s.grpcConn.Close(); err != nil {
			s.logger.Warn("error closing gRPC connection", "error", err)
		}
	}

	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("HTTP server shutdown error: %w", err)
	}

	s.logger.Info("HTTP server stopped")
	return nil
}

// GetRouter returns the underlying chi router for additional route registration
func (s *HTTPServer) GetRouter() *chi.Mux {
	return s.router
}

// requestLoggingMiddleware logs HTTP requests
func requestLoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			duration := time.Since(start)

			logger.Info("HTTP request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration", duration,
				"remote_addr", r.RemoteAddr,
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	}
}

// apiKeyHeaderMatcher forwards the API key header into gRPC metadata so the
// auth interceptor sees it; everything else follows the gateway default.
func apiKeyHeaderMatcher(key string) (string, bool) {
	if strings.EqualFold(key, "X-API-Key") {
		return auth.APIKeyHeader, true
	}
	return runtime.DefaultHeaderMatcher(key)
}

// corsMiddleware handles CORS headers.
//
// A wildcard configuration answers with a literal "*" and no credentials
// header (the credentialed-wildcard combination is what browsers forbid and
// what made the old version reflect arbitrary origins). Otherwise the
// request origin must exactly match a configured origin; unmatched origins
// get no CORS headers at all.
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	wildcard := len(allowedOrigins) == 0
	exact := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o == "*" {
			wildcard = true
			continue
		}
		exact[o] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			switch {
			case wildcard:
				w.Header().Set("Access-Control-Allow-Origin", "*")
			case origin != "" && exact[origin]:
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Add("Vary", "Origin")
			default:
				// Unmatched origin: no CORS headers
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-CSRF-Token, X-Request-ID, X-API-Key")
			w.Header().Set("Access-Control-Max-Age", "86400")

			// Handle preflight requests
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// healthCheckHandler returns a handler for the /healthz endpoint
func healthCheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "healthy",
		})
	}
}

// readinessCheckHandler returns a handler for the /readyz endpoint
func readinessCheckHandler(dbChecker HealthChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		checks := map[string]string{}

		if dbChecker != nil {
			if err := dbChecker.Ping(r.Context()); err != nil {
				checks["database"] = err.Error()
				w.WriteHeader(http.StatusServiceUnavailable)
				checks["status"] = "not ready"
				json.NewEncoder(w).Encode(checks)
				return
			}
			checks["database"] = "ok"
		}

		checks["status"] = "ready"
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(checks)
	}
}

// ipBucket tracks request tokens for a single IP.
type ipBucket struct {
	tokens    float64
	lastCheck time.Time
	mu        sync.Mutex
}

// rateLimitMiddleware returns a per-IP token bucket rate limiter.
func rateLimitMiddleware(rps float64, burst int) func(http.Handler) http.Handler {
	var (
		mu      sync.Mutex
		buckets = make(map[string]*ipBucket)
	)

	// Periodic cleanup of stale entries
	go func() {
		for range time.Tick(5 * time.Minute) {
			mu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for ip, b := range buckets {
				b.mu.Lock()
				if b.lastCheck.Before(cutoff) {
					delete(buckets, ip)
				}
				b.mu.Unlock()
			}
			mu.Unlock()
		}
	}()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip health checks
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}

			ip, _, _ := net.SplitHostPort(r.RemoteAddr)
			if ip == "" {
				ip = r.RemoteAddr
			}

			mu.Lock()
			b, ok := buckets[ip]
			if !ok {
				b = &ipBucket{
					tokens:    float64(burst),
					lastCheck: time.Now(),
				}
				buckets[ip] = b
			}
			mu.Unlock()

			b.mu.Lock()
			now := time.Now()
			elapsed := now.Sub(b.lastCheck).Seconds()
			b.lastCheck = now
			b.tokens += elapsed * rps
			if b.tokens > float64(burst) {
				b.tokens = float64(burst)
			}

			if b.tokens < 1 {
				b.mu.Unlock()
				w.Header().Set("Retry-After", "1")
				http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}
			b.tokens--
			b.mu.Unlock()

			next.ServeHTTP(w, r)
		})
	}
}
