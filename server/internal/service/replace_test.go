package service

import (
	"context"
	"testing"

	ragv1 "github.com/knoguchi/rag/gen/rag/v1"
	"github.com/knoguchi/rag/internal/ids"
	"github.com/knoguchi/rag/internal/ragcore"
	"github.com/knoguchi/rag/internal/ragcore/vectorstore"
	"github.com/knoguchi/rag/internal/repository"
)

// replaceFakeRepo tracks documents in memory and records deletions.
type replaceFakeRepo struct {
	fakeDocRepo
	docs    map[ids.ID]*repository.Document
	deleted []ids.ID
}

func (f *replaceFakeRepo) Create(_ context.Context, doc *repository.Document) error {
	cp := *doc
	f.docs[doc.ID] = &cp
	return nil
}

func (f *replaceFakeRepo) GetByHash(_ context.Context, tenantID ids.ID, hash string) (*repository.Document, error) {
	for _, d := range f.docs {
		if d.TenantID == tenantID && d.ContentHash == hash {
			return d, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *replaceFakeRepo) ListBySource(_ context.Context, tenantID ids.ID, source string) ([]*repository.Document, error) {
	var out []*repository.Document
	for _, d := range f.docs {
		if d.TenantID == tenantID && d.Source == source {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *replaceFakeRepo) Delete(_ context.Context, tenantID, id ids.ID) error {
	if d, ok := f.docs[id]; !ok || d.TenantID != tenantID {
		return repository.ErrNotFound
	}
	delete(f.docs, id)
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *replaceFakeRepo) DeleteChunks(context.Context, ids.ID, ids.ID) error { return nil }

func (f *replaceFakeRepo) Update(_ context.Context, doc *repository.Document) error {
	cp := *doc
	f.docs[doc.ID] = &cp
	return nil
}

// nullEmbedder returns fixed vectors.
type nullEmbedder struct{}

func (nullEmbedder) Embed(context.Context, string) ([]float32, error) { return []float32{0.1}, nil }
func (nullEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{0.1}
	}
	return out, nil
}
func (nullEmbedder) Dimension() int    { return 1 }
func (nullEmbedder) ModelName() string { return "null" }

// nullVectorStore accepts upserts and deletes, nothing else is called.
type nullVectorStore struct {
	vectorstore.VectorStore
}

func (nullVectorStore) Upsert(context.Context, string, []vectorstore.Chunk) error { return nil }
func (nullVectorStore) Delete(context.Context, string, string) error              { return nil }
func (nullVectorStore) CreateCollection(context.Context, string, int) error       { return nil }

func TestIngest_ReplacesStaleDocumentForSameSource(t *testing.T) {
	tenantID := ids.New()
	repo := &replaceFakeRepo{docs: map[ids.ID]*repository.Document{}}
	engine := ragcore.New(nullEmbedder{}, nil, nullVectorStore{})
	defer engine.Close()
	svc := NewDocumentService(repo, &fakeTenantRepo{}, engine, false)
	ctx := ctxAsTenant(tenantID)

	// First version of the page
	resp1, err := svc.IngestDocument(ctx, &ragv1.IngestDocumentRequest{
		Content: "old content about widgets",
		Source:  "https://example.com/page",
		Title:   "Page",
	})
	if err != nil {
		t.Fatalf("first ingest failed: %v", err)
	}
	_ = svc.Shutdown(context.Background())

	// Same source, changed content: must replace, not accumulate
	_, err = svc.IngestDocument(ctx, &ragv1.IngestDocumentRequest{
		Content: "new content about widgets, revised",
		Source:  "https://example.com/page",
		Title:   "Page",
	})
	if err != nil {
		t.Fatalf("second ingest failed: %v", err)
	}
	_ = svc.Shutdown(context.Background())

	docs, _ := repo.ListBySource(context.Background(), tenantID, "https://example.com/page")
	if len(docs) != 1 {
		t.Fatalf("expected exactly 1 document for the source, got %d", len(docs))
	}
	if len(repo.deleted) != 1 || ids.Format(repo.deleted[0]) != resp1.DocumentId {
		t.Errorf("expected the first document %s to be deleted, deleted=%v", resp1.DocumentId, repo.deleted)
	}

	// Identical re-ingest of current content: dedup, no new doc
	resp3, err := svc.IngestDocument(ctx, &ragv1.IngestDocumentRequest{
		Content: "new content about widgets, revised",
		Source:  "https://example.com/page",
		Title:   "Page",
	})
	if err != nil {
		t.Fatalf("third ingest failed: %v", err)
	}
	if resp3.DocumentId != ids.Format(docs[0].ID) {
		t.Errorf("expected dedup to return existing doc %s, got %s", ids.Format(docs[0].ID), resp3.DocumentId)
	}
}

func TestValidateFetchURL(t *testing.T) {
	cases := []struct {
		url          string
		allowPrivate bool
		wantErr      bool
	}{
		{"https://example.com/page", false, false},
		{"http://localhost:8000/x", false, true},
		{"http://127.0.0.1/x", false, true},
		{"http://169.254.169.254/latest/meta-data", false, true},
		{"http://192.168.4.10/x", false, true},
		{"ftp://example.com/x", false, true},
		{"ftp://example.com/x", true, true}, // scheme check applies even when private allowed
		{"http://localhost:8000/x", true, false},
	}
	for _, tc := range cases {
		err := validateFetchURL(tc.url, tc.allowPrivate)
		if (err != nil) != tc.wantErr {
			t.Errorf("validateFetchURL(%q, allowPrivate=%v) err=%v, wantErr=%v", tc.url, tc.allowPrivate, err, tc.wantErr)
		}
	}
}
