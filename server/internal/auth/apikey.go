// Package auth provides authentication middleware for API key and JWT-based tenant authentication.
package auth

import (
	"context"
	"crypto/subtle"
	"strings"

	"github.com/google/uuid"
	"github.com/knoguchi/rag/internal/repository"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const (
	// APIKeyHeader is the metadata key for API key authentication
	APIKeyHeader = "x-api-key"

	// tenantContextKey is the context key for storing tenant info
	tenantContextKey contextKey = "tenant"

	// adminContextKey marks a request authenticated with the admin API key
	adminContextKey contextKey = "admin"
)

// TenantInfo holds tenant information extracted from authentication
type TenantInfo struct {
	ID     uuid.UUID
	Name   string
	Config repository.TenantConfig
}

// APIKeyInterceptor provides gRPC interceptor for API key validation
type APIKeyInterceptor struct {
	tenantRepo   repository.TenantRepository
	skipMethods  map[string]bool
	adminAPIKey  string
	adminMethods map[string]bool
}

// NewAPIKeyInterceptor creates a new API key interceptor
func NewAPIKeyInterceptor(tenantRepo repository.TenantRepository, adminAPIKey string) *APIKeyInterceptor {
	return &APIKeyInterceptor{
		tenantRepo:  tenantRepo,
		adminAPIKey: adminAPIKey,
		skipMethods: map[string]bool{
			// Health check endpoints
			"/grpc.health.v1.Health/Check": true,
			"/grpc.health.v1.Health/Watch": true,
			// Self-serve signup (gated by SIGNUP_ENABLED in the service)
			"/rag.v1.TenantService/Signup": true,
		},
		adminMethods: map[string]bool{
			// Tenant management requires admin auth
			"/rag.v1.TenantService/CreateTenant":     true,
			"/rag.v1.TenantService/ListTenants":      true,
			"/rag.v1.TenantService/DeleteTenant":     true,
			"/rag.v1.TenantService/RegenerateAPIKey": true,
		},
	}
}

// WithSkipMethods adds methods to skip authentication
func (i *APIKeyInterceptor) WithSkipMethods(methods ...string) *APIKeyInterceptor {
	for _, method := range methods {
		i.skipMethods[method] = true
	}
	return i
}

// WithAdminMethods adds methods that require admin authentication
func (i *APIKeyInterceptor) WithAdminMethods(methods ...string) *APIKeyInterceptor {
	for _, method := range methods {
		i.adminMethods[method] = true
	}
	return i
}

// UnaryInterceptor returns a gRPC unary interceptor for API key validation
func (i *APIKeyInterceptor) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Skip auth for certain methods
		if i.skipMethods[info.FullMethod] {
			return handler(ctx, req)
		}

		// Extract API key from metadata
		apiKey, err := extractAPIKey(ctx)
		if err != nil {
			return nil, err
		}

		// The admin key authenticates any method and marks the context
		if i.adminAPIKey != "" && subtle.ConstantTimeCompare([]byte(apiKey), []byte(i.adminAPIKey)) == 1 {
			return handler(context.WithValue(ctx, adminContextKey, true), req)
		}
		if i.adminMethods[info.FullMethod] {
			// Admin-only methods never accept tenant keys
			return nil, status.Error(codes.PermissionDenied, "admin API key required")
		}

		// Validate tenant API key
		tenant, err := i.tenantRepo.GetByAPIKey(ctx, apiKey)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to validate API key: %v", err)
		}
		if tenant == nil {
			return nil, status.Error(codes.Unauthenticated, "invalid API key")
		}

		// Store tenant info in context
		tenantInfo := &TenantInfo{
			ID:     tenant.ID,
			Name:   tenant.Name,
			Config: tenant.Config,
		}
		ctx = context.WithValue(ctx, tenantContextKey, tenantInfo)

		return handler(ctx, req)
	}
}

// StreamInterceptor returns a gRPC stream interceptor for API key validation
func (i *APIKeyInterceptor) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		// Skip auth for certain methods
		if i.skipMethods[info.FullMethod] {
			return handler(srv, ss)
		}

		ctx := ss.Context()

		// Extract API key from metadata
		apiKey, err := extractAPIKey(ctx)
		if err != nil {
			return err
		}

		// The admin key authenticates any method and marks the context
		if i.adminAPIKey != "" && subtle.ConstantTimeCompare([]byte(apiKey), []byte(i.adminAPIKey)) == 1 {
			return handler(srv, &wrappedServerStream{
				ServerStream: ss,
				ctx:          context.WithValue(ctx, adminContextKey, true),
			})
		}
		if i.adminMethods[info.FullMethod] {
			// Admin-only methods never accept tenant keys
			return status.Error(codes.PermissionDenied, "admin API key required")
		}

		// Validate tenant API key
		tenant, err := i.tenantRepo.GetByAPIKey(ctx, apiKey)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to validate API key: %v", err)
		}
		if tenant == nil {
			return status.Error(codes.Unauthenticated, "invalid API key")
		}

		// Store tenant info in context using wrapped stream
		tenantInfo := &TenantInfo{
			ID:     tenant.ID,
			Name:   tenant.Name,
			Config: tenant.Config,
		}
		wrappedStream := &wrappedServerStream{
			ServerStream: ss,
			ctx:          context.WithValue(ctx, tenantContextKey, tenantInfo),
		}

		return handler(srv, wrappedStream)
	}
}

// wrappedServerStream wraps a grpc.ServerStream with a modified context
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

// Context returns the wrapped context
func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

// extractAPIKey extracts the API key from gRPC metadata
func extractAPIKey(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing metadata")
	}

	values := md.Get(APIKeyHeader)
	if len(values) == 0 {
		return "", status.Error(codes.Unauthenticated, "missing API key")
	}

	apiKey := strings.TrimSpace(values[0])
	if apiKey == "" {
		return "", status.Error(codes.Unauthenticated, "empty API key")
	}

	return apiKey, nil
}

// ContextWithTenant returns a context carrying the given tenant info, as the
// interceptor would produce. Intended for tests and in-process composition.
func ContextWithTenant(ctx context.Context, tenant *TenantInfo) context.Context {
	return context.WithValue(ctx, tenantContextKey, tenant)
}

// ContextWithAdmin returns a context marked as admin-authenticated, as the
// interceptor would produce. Intended for tests and in-process composition.
func ContextWithAdmin(ctx context.Context) context.Context {
	return context.WithValue(ctx, adminContextKey, true)
}

// TenantFromContext extracts tenant info from context
func TenantFromContext(ctx context.Context) (*TenantInfo, bool) {
	tenant, ok := ctx.Value(tenantContextKey).(*TenantInfo)
	return tenant, ok
}

// RequireTenant is a helper that returns an error if tenant is not in context
func RequireTenant(ctx context.Context) (*TenantInfo, error) {
	tenant, ok := TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "tenant context not found")
	}
	return tenant, nil
}

// IsAdmin reports whether the request was authenticated with the admin API key.
func IsAdmin(ctx context.Context) bool {
	admin, _ := ctx.Value(adminContextKey).(bool)
	return admin
}

// TenantIDFromContext extracts just the tenant ID from context
func TenantIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	tenant, ok := TenantFromContext(ctx)
	if !ok {
		return uuid.Nil, false
	}
	return tenant.ID, true
}
