package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	ragv1 "github.com/knoguchi/rag/gen/rag/v1"
	"github.com/knoguchi/rag/internal/auth"
	"github.com/knoguchi/rag/internal/repository"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeDocRepo owns documents for exactly one tenant and enforces scoping the
// way the SQL layer does: wrong tenant behaves like a missing row.
type fakeDocRepo struct {
	ownerID uuid.UUID
	docID   uuid.UUID
}

func (f *fakeDocRepo) Create(context.Context, *repository.Document) error { return nil }
func (f *fakeDocRepo) GetByID(_ context.Context, tenantID, id uuid.UUID) (*repository.Document, error) {
	if tenantID != f.ownerID || id != f.docID {
		return nil, repository.ErrNotFound
	}
	return &repository.Document{ID: id, TenantID: tenantID, Status: "READY"}, nil
}
func (f *fakeDocRepo) GetByHash(context.Context, uuid.UUID, string) (*repository.Document, error) {
	return nil, repository.ErrNotFound
}
func (f *fakeDocRepo) ListBySource(context.Context, uuid.UUID, string) ([]*repository.Document, error) {
	return nil, nil
}
func (f *fakeDocRepo) List(context.Context, uuid.UUID, string, int, int) ([]*repository.Document, int, error) {
	return nil, 0, nil
}
func (f *fakeDocRepo) Update(context.Context, *repository.Document) error { return nil }
func (f *fakeDocRepo) Delete(_ context.Context, tenantID, id uuid.UUID) error {
	if tenantID != f.ownerID || id != f.docID {
		return repository.ErrNotFound
	}
	return nil
}
func (f *fakeDocRepo) CreateChunks(context.Context, []*repository.DocumentChunk) error { return nil }
func (f *fakeDocRepo) GetChunks(_ context.Context, tenantID, docID uuid.UUID, _, _ int) ([]*repository.DocumentChunk, error) {
	if tenantID != f.ownerID || docID != f.docID {
		return nil, nil
	}
	return []*repository.DocumentChunk{{ID: uuid.New(), DocumentID: docID, TenantID: tenantID, Content: "c"}}, nil
}
func (f *fakeDocRepo) DeleteChunks(context.Context, uuid.UUID, uuid.UUID) error { return nil }

var _ repository.DocumentRepository = (*fakeDocRepo)(nil)

func ctxAsTenant(id uuid.UUID) context.Context {
	return auth.ContextWithTenant(context.Background(), &auth.TenantInfo{ID: id, Name: "t"})
}

func TestGetDocument_TenantScoping(t *testing.T) {
	owner := uuid.New()
	other := uuid.New()
	docID := uuid.New()
	svc := NewDocumentService(&fakeDocRepo{ownerID: owner, docID: docID}, nil, nil)

	// Owner can read its document
	if _, err := svc.GetDocument(ctxAsTenant(owner), &ragv1.GetDocumentRequest{Id: docID.String()}); err != nil {
		t.Fatalf("owner should read own document: %v", err)
	}

	// Another tenant gets NotFound for the same document ID
	_, err := svc.GetDocument(ctxAsTenant(other), &ragv1.GetDocumentRequest{Id: docID.String()})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound for foreign tenant, got %v", err)
	}

	// No auth context at all is rejected
	_, err = svc.GetDocument(context.Background(), &ragv1.GetDocumentRequest{Id: docID.String()})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated without tenant context, got %v", err)
	}
}

func TestGetDocumentChunks_TenantScoping(t *testing.T) {
	owner := uuid.New()
	other := uuid.New()
	docID := uuid.New()
	svc := NewDocumentService(&fakeDocRepo{ownerID: owner, docID: docID}, nil, nil)

	resp, err := svc.GetDocumentChunks(ctxAsTenant(owner), &ragv1.GetDocumentChunksRequest{DocumentId: docID.String()})
	if err != nil || len(resp.Chunks) == 0 {
		t.Fatalf("owner should read own chunks: %v", err)
	}

	resp, err = svc.GetDocumentChunks(ctxAsTenant(other), &ragv1.GetDocumentChunksRequest{DocumentId: docID.String()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Chunks) != 0 {
		t.Error("foreign tenant must not see another tenant's chunks")
	}
}

// fakeTenantRepo returns a fixed tenant by ID.
type fakeTenantRepo struct {
	tenant *repository.Tenant
}

func (f *fakeTenantRepo) Create(context.Context, *repository.Tenant, string) error { return nil }
func (f *fakeTenantRepo) GetByID(_ context.Context, id uuid.UUID) (*repository.Tenant, error) {
	if f.tenant != nil && f.tenant.ID == id {
		return f.tenant, nil
	}
	return nil, repository.ErrNotFound
}
func (f *fakeTenantRepo) GetByAPIKey(context.Context, string) (*repository.Tenant, error) {
	return nil, nil
}
func (f *fakeTenantRepo) List(context.Context, int, int) ([]*repository.Tenant, int, error) {
	return nil, 0, nil
}
func (f *fakeTenantRepo) Update(context.Context, *repository.Tenant) error      { return nil }
func (f *fakeTenantRepo) Delete(context.Context, uuid.UUID) error               { return nil }
func (f *fakeTenantRepo) UpdateAPIKey(context.Context, uuid.UUID, string) error { return nil }
func (f *fakeTenantRepo) UpdateUsage(context.Context, uuid.UUID, repository.TenantUsage) error {
	return nil
}

var _ repository.TenantRepository = (*fakeTenantRepo)(nil)

func TestGetTenant_SelfOrAdmin(t *testing.T) {
	self := uuid.New()
	other := uuid.New()
	repo := &fakeTenantRepo{tenant: &repository.Tenant{ID: self, Name: "self"}}
	svc := NewTenantService(repo, nil, nil)

	// Self access allowed
	if _, err := svc.GetTenant(ctxAsTenant(self), &ragv1.GetTenantRequest{Id: self.String()}); err != nil {
		t.Fatalf("self read should succeed: %v", err)
	}

	// Another tenant's key gets NotFound (no existence oracle)
	_, err := svc.GetTenant(ctxAsTenant(other), &ragv1.GetTenantRequest{Id: self.String()})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound for foreign tenant key, got %v", err)
	}

	// Admin key reads any tenant
	if _, err := svc.GetTenant(auth.ContextWithAdmin(context.Background()), &ragv1.GetTenantRequest{Id: self.String()}); err != nil {
		t.Errorf("admin read should succeed: %v", err)
	}
}
