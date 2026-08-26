package service

import (
	"context"
	"strings"
	"testing"

	ragv1 "github.com/knoguchi/rag/gen/rag/v1"
	"github.com/knoguchi/rag/internal/config"
	"github.com/knoguchi/rag/internal/ids"
	"github.com/knoguchi/rag/internal/ragcore"
	"github.com/knoguchi/rag/internal/repository"
	"github.com/oklog/ulid/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// signupFakeRepo stores created tenants by ID.
type signupFakeRepo struct {
	fakeTenantRepo
	created map[ids.ID]*repository.Tenant
}

func (f *signupFakeRepo) Create(_ context.Context, t *repository.Tenant, _ string) error {
	f.created[t.ID] = t
	return nil
}

func (f *signupFakeRepo) GetByID(_ context.Context, id ids.ID) (*repository.Tenant, error) {
	if t, ok := f.created[id]; ok {
		return t, nil
	}
	return nil, repository.ErrNotFound
}

func signupTestConfig(enabled bool) *config.Config {
	return &config.Config{
		SignupEnabled:        enabled,
		SignupRetentionDays:  30,
		OllamaEmbeddingModel: "nomic-embed-text",
		OllamaLLMModel:       "test-model",
		DefaultChunkMethod:   "semantic",
		DefaultTopK:          4,
		DefaultMinScore:      0.35,
	}
}

func newSignupService(enabled bool) (*TenantService, *signupFakeRepo) {
	repo := &signupFakeRepo{created: map[ids.ID]*repository.Tenant{}}
	engine := ragcore.New(nullEmbedder{}, nil, nullVectorStore{})
	return NewTenantService(repo, engine, signupTestConfig(enabled)), repo
}

func TestSignup_DisabledByDefault(t *testing.T) {
	svc, _ := newSignupService(false)

	_, err := svc.Signup(context.Background(), &ragv1.SignupRequest{
		InstallId: ulid.Make().String(),
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied when signup disabled, got %v", err)
	}
}

func TestSignup_CreatesTenantFromULID(t *testing.T) {
	svc, repo := newSignupService(true)
	installID := ulid.Make()

	resp, err := svc.Signup(context.Background(), &ragv1.SignupRequest{
		InstallId: installID.String(),
	})
	if err != nil {
		t.Fatalf("signup failed: %v", err)
	}
	if resp.ApiKey == "" || !strings.HasPrefix(resp.ApiKey, "rag_") {
		t.Errorf("expected an API key in the response, got %q", resp.ApiKey)
	}

	// The ULID's 128 bits become the tenant UUID
	wantID := ids.ID(installID)
	created, ok := repo.created[wantID]
	if !ok {
		t.Fatalf("expected tenant with ID %s derived from the ULID", wantID)
	}
	if created.Config.RetentionDays != 30 {
		t.Errorf("expected signup tenant retention of 30 days, got %d", created.Config.RetentionDays)
	}
}

func TestSignup_RejectsInvalidULID(t *testing.T) {
	svc, _ := newSignupService(true)

	_, err := svc.Signup(context.Background(), &ragv1.SignupRequest{InstallId: "not-a-ulid"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for bad ULID, got %v", err)
	}
}

func TestSignup_DuplicateInstall(t *testing.T) {
	svc, _ := newSignupService(true)
	installID := ulid.Make().String()

	if _, err := svc.Signup(context.Background(), &ragv1.SignupRequest{InstallId: installID}); err != nil {
		t.Fatalf("first signup failed: %v", err)
	}
	_, err := svc.Signup(context.Background(), &ragv1.SignupRequest{InstallId: installID})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists for duplicate install, got %v", err)
	}
}
