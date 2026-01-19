// Package server provides gRPC and HTTP server implementations with middleware.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"time"

	ragv1 "github.com/knoguchi/rag/gen/rag/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

// GRPCServer wraps a gRPC server with service registration and lifecycle management
type GRPCServer struct {
	server   *grpc.Server
	listener net.Listener
	logger   *slog.Logger
	port     int
}

// GRPCServerConfig holds configuration for the gRPC server
type GRPCServerConfig struct {
	Port   int
	Logger *slog.Logger
}

// Services holds all gRPC service implementations
type Services struct {
	TenantService   ragv1.TenantServiceServer
	DocumentService ragv1.DocumentServiceServer
	RAGService      ragv1.RAGServiceServer
}

// NewGRPCServer creates a new gRPC server with interceptors
func NewGRPCServer(cfg GRPCServerConfig, services Services) (*GRPCServer, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Create gRPC server with interceptors
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			recoveryUnaryInterceptor(logger),
			loggingUnaryInterceptor(logger),
		),
		grpc.ChainStreamInterceptor(
			recoveryStreamInterceptor(logger),
			loggingStreamInterceptor(logger),
		),
	)

	// Register services
	if services.TenantService != nil {
		ragv1.RegisterTenantServiceServer(server, services.TenantService)
		logger.Info("registered TenantService")
	}

	if services.DocumentService != nil {
		ragv1.RegisterDocumentServiceServer(server, services.DocumentService)
		logger.Info("registered DocumentService")
	}

	if services.RAGService != nil {
		ragv1.RegisterRAGServiceServer(server, services.RAGService)
		logger.Info("registered RAGService")
	}

	// Enable reflection for development/debugging
	reflection.Register(server)

	return &GRPCServer{
		server: server,
		logger: logger,
		port:   cfg.Port,
	}, nil
}

// Start starts the gRPC server
func (s *GRPCServer) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	s.listener = listener

	s.logger.Info("starting gRPC server", "address", addr)

	if err := s.server.Serve(listener); err != nil {
		return fmt.Errorf("gRPC server error: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the gRPC server
func (s *GRPCServer) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down gRPC server")

	// Create a channel to signal when GracefulStop completes
	stopped := make(chan struct{})
	go func() {
		s.server.GracefulStop()
		close(stopped)
	}()

	// Wait for graceful stop or context cancellation
	select {
	case <-stopped:
		s.logger.Info("gRPC server stopped gracefully")
		return nil
	case <-ctx.Done():
		s.logger.Warn("graceful shutdown timeout, forcing stop")
		s.server.Stop()
		return ctx.Err()
	}
}

// GetServer returns the underlying gRPC server
func (s *GRPCServer) GetServer() *grpc.Server {
	return s.server
}

// loggingUnaryInterceptor logs unary RPC calls
func loggingUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		start := time.Now()

		resp, err := handler(ctx, req)

		duration := time.Since(start)
		code := codes.OK
		if err != nil {
			if st, ok := status.FromError(err); ok {
				code = st.Code()
			} else {
				code = codes.Unknown
			}
		}

		// Log the request
		logger.Info("gRPC request",
			"method", info.FullMethod,
			"code", code.String(),
			"duration", duration,
			"error", err,
		)

		return resp, err
	}
}

// loggingStreamInterceptor logs streaming RPC calls
func loggingStreamInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		start := time.Now()

		err := handler(srv, ss)

		duration := time.Since(start)
		code := codes.OK
		if err != nil {
			if st, ok := status.FromError(err); ok {
				code = st.Code()
			} else {
				code = codes.Unknown
			}
		}

		logger.Info("gRPC stream",
			"method", info.FullMethod,
			"code", code.String(),
			"duration", duration,
			"error", err,
		)

		return err
	}
}

// recoveryUnaryInterceptor recovers from panics in unary handlers
func recoveryUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp interface{}, err error) {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				logger.Error("panic recovered in gRPC handler",
					"method", info.FullMethod,
					"panic", r,
					"stack", string(stack),
				)
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()

		return handler(ctx, req)
	}
}

// recoveryStreamInterceptor recovers from panics in stream handlers
func recoveryStreamInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) (err error) {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				logger.Error("panic recovered in gRPC stream handler",
					"method", info.FullMethod,
					"panic", r,
					"stack", string(stack),
				)
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()

		return handler(srv, ss)
	}
}
