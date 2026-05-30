package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/knoguchi/rag/internal/repository"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// mockTenantRepo implements repository.TenantRepository for testing
type mockTenantRepo struct {
	tenants map[string]*repository.Tenant
}

func (m *mockTenantRepo) GetByAPIKey(_ context.Context, apiKey string) (*repository.Tenant, error) {
	t, ok := m.tenants[apiKey]
	if !ok {
		return nil, nil
	}
	return t, nil
}

func (m *mockTenantRepo) Create(context.Context, *repository.Tenant) error                   { return nil }
func (m *mockTenantRepo) GetByID(context.Context, uuid.UUID) (*repository.Tenant, error)     { return nil, repository.ErrNotFound }
func (m *mockTenantRepo) List(context.Context, int, int) ([]*repository.Tenant, int, error)  { return nil, 0, nil }
func (m *mockTenantRepo) Update(context.Context, *repository.Tenant) error                   { return nil }
func (m *mockTenantRepo) Delete(context.Context, uuid.UUID) error                            { return nil }
func (m *mockTenantRepo) UpdateAPIKey(context.Context, uuid.UUID, string) error              { return nil }
func (m *mockTenantRepo) UpdateUsage(context.Context, uuid.UUID, repository.TenantUsage) error { return nil }

func newTestInterceptor() (*APIKeyInterceptor, *repository.Tenant) {
	tenantID := uuid.New()
	tenant := &repository.Tenant{
		ID:     tenantID,
		Name:   "test-tenant",
		APIKey: "rag_test123",
	}
	repo := &mockTenantRepo{
		tenants: map[string]*repository.Tenant{
			"rag_test123": tenant,
		},
	}
	interceptor := NewAPIKeyInterceptor(repo, "admin-secret-key")
	return interceptor, tenant
}

func ctxWithAPIKey(key string) context.Context {
	md := metadata.Pairs(APIKeyHeader, key)
	return metadata.NewIncomingContext(context.Background(), md)
}

// noop handler for testing
var noopHandler grpc.UnaryHandler = func(ctx context.Context, req any) (any, error) {
	return "ok", nil
}

func TestUnaryInterceptor_SkipMethod(t *testing.T) {
	interceptor, _ := newTestInterceptor()
	unary := interceptor.UnaryInterceptor()

	info := &grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"}
	resp, err := unary(context.Background(), nil, info, noopHandler)
	if err != nil {
		t.Fatalf("expected skip method to pass, got: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("expected 'ok', got: %v", resp)
	}
}

func TestUnaryInterceptor_MissingAPIKey(t *testing.T) {
	interceptor, _ := newTestInterceptor()
	unary := interceptor.UnaryInterceptor()

	info := &grpc.UnaryServerInfo{FullMethod: "/rag.v1.RAGService/Query"}
	_, err := unary(context.Background(), nil, info, noopHandler)
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	if s, ok := status.FromError(err); !ok || s.Code() != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got: %v", err)
	}
}

func TestUnaryInterceptor_InvalidAPIKey(t *testing.T) {
	interceptor, _ := newTestInterceptor()
	unary := interceptor.UnaryInterceptor()

	ctx := ctxWithAPIKey("rag_invalid")
	info := &grpc.UnaryServerInfo{FullMethod: "/rag.v1.RAGService/Query"}
	_, err := unary(ctx, nil, info, noopHandler)
	if err == nil {
		t.Fatal("expected error for invalid API key")
	}
	if s, ok := status.FromError(err); !ok || s.Code() != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got: %v", err)
	}
}

func TestUnaryInterceptor_ValidAPIKey(t *testing.T) {
	interceptor, tenant := newTestInterceptor()
	unary := interceptor.UnaryInterceptor()

	ctx := ctxWithAPIKey("rag_test123")
	info := &grpc.UnaryServerInfo{FullMethod: "/rag.v1.RAGService/Query"}

	handler := func(ctx context.Context, req any) (any, error) {
		ti, ok := TenantFromContext(ctx)
		if !ok {
			t.Fatal("expected tenant in context")
		}
		if ti.ID != tenant.ID {
			t.Fatalf("expected tenant ID %s, got %s", tenant.ID, ti.ID)
		}
		return "ok", nil
	}

	_, err := unary(ctx, nil, info, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnaryInterceptor_AdminMethod_ValidKey(t *testing.T) {
	interceptor, _ := newTestInterceptor()
	unary := interceptor.UnaryInterceptor()

	ctx := ctxWithAPIKey("admin-secret-key")
	info := &grpc.UnaryServerInfo{FullMethod: "/rag.v1.TenantService/CreateTenant"}
	resp, err := unary(ctx, nil, info, noopHandler)
	if err != nil {
		t.Fatalf("expected admin method to pass: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("expected 'ok', got: %v", resp)
	}
}

func TestUnaryInterceptor_AdminMethod_InvalidKey(t *testing.T) {
	interceptor, _ := newTestInterceptor()
	unary := interceptor.UnaryInterceptor()

	ctx := ctxWithAPIKey("wrong-admin-key")
	info := &grpc.UnaryServerInfo{FullMethod: "/rag.v1.TenantService/CreateTenant"}
	_, err := unary(ctx, nil, info, noopHandler)
	if err == nil {
		t.Fatal("expected error for invalid admin key")
	}
	if s, ok := status.FromError(err); !ok || s.Code() != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got: %v", err)
	}
}

func TestUnaryInterceptor_AdminMethod_EmptyAdminKey(t *testing.T) {
	repo := &mockTenantRepo{tenants: map[string]*repository.Tenant{}}
	interceptor := NewAPIKeyInterceptor(repo, "")
	unary := interceptor.UnaryInterceptor()

	ctx := ctxWithAPIKey("anything")
	info := &grpc.UnaryServerInfo{FullMethod: "/rag.v1.TenantService/CreateTenant"}
	_, err := unary(ctx, nil, info, noopHandler)
	if err == nil {
		t.Fatal("expected error when admin API key not configured")
	}
	if s, ok := status.FromError(err); !ok || s.Code() != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got: %v", err)
	}
}

func TestUnaryInterceptor_EmptyAPIKeyValue(t *testing.T) {
	interceptor, _ := newTestInterceptor()
	unary := interceptor.UnaryInterceptor()

	ctx := ctxWithAPIKey("")
	info := &grpc.UnaryServerInfo{FullMethod: "/rag.v1.RAGService/Query"}
	_, err := unary(ctx, nil, info, noopHandler)
	if err == nil {
		t.Fatal("expected error for empty API key")
	}
}

func TestRequireTenant_NotInContext(t *testing.T) {
	_, err := RequireTenant(context.Background())
	if err == nil {
		t.Fatal("expected error when tenant not in context")
	}
}

func TestRequireTenant_InContext(t *testing.T) {
	tenant := &TenantInfo{ID: uuid.New(), Name: "test"}
	ctx := context.WithValue(context.Background(), tenantContextKey, tenant)
	got, err := RequireTenant(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != tenant.ID {
		t.Fatalf("expected tenant ID %s, got %s", tenant.ID, got.ID)
	}
}
