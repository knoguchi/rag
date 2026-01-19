package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	ragv1 "github.com/knoguchi/rag/gen/rag/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
)

// HTTPServer wraps an HTTP server with grpc-gateway integration
type HTTPServer struct {
	server     *http.Server
	router     *chi.Mux
	gwMux      *runtime.ServeMux
	logger     *slog.Logger
	port       int
	grpcAddr   string
	grpcConn   *grpc.ClientConn
}

// HTTPServerConfig holds configuration for the HTTP server
type HTTPServerConfig struct {
	Port           int
	GRPCAddr       string // Address of the gRPC server (e.g., "localhost:9090")
	Logger         *slog.Logger
	AllowedOrigins []string // CORS allowed origins
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
	router.Use(requestLoggingMiddleware(logger))
	router.Use(middleware.Recoverer)
	router.Use(corsMiddleware(cfg.AllowedOrigins))

	// Create grpc-gateway mux with JSON marshaler options
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
	)

	// Mount health check endpoint
	router.Get("/healthz", healthCheckHandler())
	router.Get("/readyz", readinessCheckHandler())

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
		server:   server,
		router:   router,
		gwMux:    gwMux,
		logger:   logger,
		port:     cfg.Port,
		grpcAddr: cfg.GRPCAddr,
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

// corsMiddleware handles CORS headers
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Check if origin is allowed
			allowed := false
			if len(allowedOrigins) == 0 {
				// If no origins specified, allow all in development
				allowed = true
				origin = "*"
			} else {
				for _, o := range allowedOrigins {
					if o == "*" || o == origin {
						allowed = true
						break
					}
				}
			}

			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-CSRF-Token, X-Request-ID, X-API-Key")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}

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
func readinessCheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO: Add actual readiness checks (database connectivity, etc.)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ready",
		})
	}
}
