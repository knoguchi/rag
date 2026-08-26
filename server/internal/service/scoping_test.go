package service

import (
	"context"
	"testing"

	ragv1 "github.com/knoguchi/rag/gen/rag/v1"
	"github.com/knoguchi/rag/internal/auth"
	"github.com/knoguchi/rag/internal/ids"
	"github.com/knoguchi/rag/internal/repository"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeDocRepo owns documents for exactly one tenant and enforces scoping the
// way the SQL layer does: wrong tenant behaves like a missing row.
type fakeDocRepo struct {
	ownerID ids.ID
	docID   ids.ID
}

func (f *fakeDocRepo) Create(context.Context, *repository.Document) error { return nil }
func (f *fakeDocRepo) GetByID(_ context.Context, tenantID, id ids.ID) (*repository.Document, error) {
	if tenantID != f.ownerID || id != f.docID {
		return nil, repository.ErrNotFound
	}
	return &repository.Document{ID: id, TenantID: tenantID, Status: "READY"}, nil
}
func (f *fakeDocRepo) GetByHash(context.Context, ids.ID, string) (*repository.Document, error) {
	return nil, repository.ErrNotFound
}
func (f *fakeDocRepo) ListBySource(context.Context, ids.ID, string) ([]*repository.Document, error) {
	return nil, nil
}
func (f *fakeDocRepo) List(context.Context, ids.ID, string, int, int) ([]*repository.Document, int, error) {
	return nil, 0, nil
}
func (f *fakeDocRepo) Update(context.Context, *repository.Document) error { return nil }
func (f *fakeDocRepo) Delete(_ context.Context, tenantID, id ids.ID) error {
	if tenantID != f.ownerID || id != f.docID {
		return repository.ErrNotFound
	}
	return nil
}
func (f *fakeDocRepo) CreateChunks(context.Context, []*repository.DocumentChunk) error { return nil }
func (f *fakeDocRepo) GetChunks(_ context.Context, tenantID, docID ids.ID, _, _ int) ([]*repository.DocumentChunk, error) {
	if tenantID != f.ownerID || docID != f.docID {
		return nil, nil
	}
	return []*repository.DocumentChunk{{ID: ids.New(), DocumentID: docID, TenantID: tenantID, Content: "c"}}, nil
}
func (f *fakeDocRepo) DeleteChunks(context.Context, ids.ID, ids.ID) error { return nil }

var _ repository.DocumentRepository = (*fakeDocRepo)(nil)

func ctxAsTenant(id ids.ID) context.Context {
	return auth.ContextWithTenant(context.Background(), &auth.TenantInfo{ID: id, Name: "t"})
}

func TestGetDocument_TenantScoping(t *testing.T) {
	owner := ids.New()
	other := ids.New()
	docID := ids.New()
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
	owner := ids.New()
	other := ids.New()
	docID := ids.New()
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
func (f *fakeTenantRepo) GetByID(_ context.Context, id ids.ID) (*repository.Tenant, error) {
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
func (f *fakeTenantRepo) Update(context.Context, *repository.Tenant) error   { return nil }
func (f *fakeTenantRepo) Delete(context.Context, ids.ID) error               { return nil }
func (f *fakeTenantRepo) UpdateAPIKey(context.Context, ids.ID, string) error { return nil }
func (f *fakeTenantRepo) UpdateUsage(context.Context, ids.ID, repository.TenantUsage) error {
	return nil
}
func (f *fakeTenantRepo) ListExpired(context.Context) ([]*repository.Tenant, error) {
	return nil, nil
}

var _ repository.TenantRepository = (*fakeTenantRepo)(nil)

func TestGetTenant_SelfOrAdmin(t *testing.T) {
	self := ids.New()
	other := ids.New()
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
