// Package service implements business logic for tenant management, document ingestion, and RAG queries.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	ragv1 "github.com/knoguchi/rag/gen/rag/v1"
	"github.com/knoguchi/rag/internal/config"
	"github.com/knoguchi/rag/internal/embedder"
	"github.com/knoguchi/rag/internal/repository"
	"github.com/knoguchi/rag/internal/vectorstore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TenantService implements ragv1.TenantServiceServer
type TenantService struct {
	ragv1.UnimplementedTenantServiceServer

	repo        repository.TenantRepository
	vectorStore vectorstore.VectorStore
	cfg         *config.Config
}

// NewTenantService creates a new TenantService
func NewTenantService(repo repository.TenantRepository, vectorStore vectorstore.VectorStore, cfg *config.Config) *TenantService {
	return &TenantService{
		repo:        repo,
		vectorStore: vectorStore,
		cfg:         cfg,
	}
}

// CreateTenant creates a new tenant with default configuration
func (s *TenantService) CreateTenant(ctx context.Context, req *ragv1.CreateTenantRequest) (*ragv1.Tenant, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	// Generate API key
	apiKey, err := generateAPIKey()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to generate API key: %v", err)
	}

	// Build tenant config with defaults
	tenantConfig := s.buildTenantConfig(req.Config)

	// Validate config
	if err := s.validateTenantConfig(tenantConfig); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid config: %v", err)
	}

	// Use custom ID if provided, otherwise generate a new one
	var tenantID uuid.UUID
	if req.GetId() != "" {
		var err error
		tenantID, err = uuid.Parse(req.GetId())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid tenant ID format: %v", err)
		}
	} else {
		tenantID = uuid.New()
	}

	now := time.Now()
	tenant := &repository.Tenant{
		ID:        tenantID,
		Name:      req.Name,
		APIKey:    apiKey,
		Config:    tenantConfig,
		Usage:     repository.TenantUsage{},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.repo.Create(ctx, tenant); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create tenant: %v", err)
	}

	// Create vector collection for the tenant
	// The dimension depends on the embedding model; nomic-embed-text uses 768 dimensions
	dimension := 768
	if err := s.vectorStore.CreateCollection(ctx, tenant.ID.String(), dimension); err != nil {
		// Log error but don't fail - collection can be created later
		// In production, this should be handled more gracefully
		_ = err
	}

	return s.tenantToProto(tenant), nil
}

// GetTenant retrieves a tenant by ID
func (s *TenantService) GetTenant(ctx context.Context, req *ragv1.GetTenantRequest) (*ragv1.Tenant, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant ID format")
	}

	tenant, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "tenant not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get tenant: %v", err)
	}

	return s.tenantToProto(tenant), nil
}

// ListTenants lists all tenants (admin only)
func (s *TenantService) ListTenants(ctx context.Context, req *ragv1.ListTenantsRequest) (*ragv1.ListTenantsResponse, error) {
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := 0
	if req.PageToken != "" {
		// Decode page token as offset
		if _, err := fmt.Sscanf(req.PageToken, "%d", &offset); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page token")
		}
	}

	tenants, total, err := s.repo.List(ctx, pageSize, offset)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list tenants: %v", err)
	}

	protoTenants := make([]*ragv1.Tenant, len(tenants))
	for i, t := range tenants {
		protoTenants[i] = s.tenantToProto(t)
	}

	var nextPageToken string
	if offset+len(tenants) < total {
		nextPageToken = fmt.Sprintf("%d", offset+len(tenants))
	}

	return &ragv1.ListTenantsResponse{
		Tenants:       protoTenants,
		NextPageToken: nextPageToken,
	}, nil
}

// UpdateTenant updates tenant configuration
func (s *TenantService) UpdateTenant(ctx context.Context, req *ragv1.UpdateTenantRequest) (*ragv1.Tenant, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant ID format")
	}

	tenant, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "tenant not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get tenant: %v", err)
	}

	// Update name if provided
	if req.Name != "" {
		tenant.Name = req.Name
	}

	// Update config if provided
	if req.Config != nil {
		newConfig := s.mergeConfig(tenant.Config, req.Config)
		if err := s.validateTenantConfig(newConfig); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid config: %v", err)
		}
		tenant.Config = newConfig
	}

	tenant.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, tenant); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update tenant: %v", err)
	}

	return s.tenantToProto(tenant), nil
}

// DeleteTenant deletes a tenant and all associated data
func (s *TenantService) DeleteTenant(ctx context.Context, req *ragv1.DeleteTenantRequest) (*ragv1.DeleteTenantResponse, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant ID format")
	}

	// Delete vector collection
	if err := s.vectorStore.DeleteCollection(ctx, id.String()); err != nil {
		// Log error but continue with tenant deletion
		_ = err
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete tenant: %v", err)
	}

	return &ragv1.DeleteTenantResponse{
		Success: true,
	}, nil
}

// RegenerateAPIKey generates a new API key for a tenant
func (s *TenantService) RegenerateAPIKey(ctx context.Context, req *ragv1.RegenerateAPIKeyRequest) (*ragv1.RegenerateAPIKeyResponse, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant ID format")
	}

	// Verify tenant exists
	if _, err := s.repo.GetByID(ctx, id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "tenant not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get tenant: %v", err)
	}

	// Generate new API key
	newAPIKey, err := generateAPIKey()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to generate API key: %v", err)
	}

	if err := s.repo.UpdateAPIKey(ctx, id, newAPIKey); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update API key: %v", err)
	}

	return &ragv1.RegenerateAPIKeyResponse{
		ApiKey: newAPIKey,
	}, nil
}

// generateAPIKey generates a new API key with format "rag_" + 32 random hex chars
func generateAPIKey() (string, error) {
	bytes := make([]byte, 16) // 16 bytes = 32 hex chars
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "rag_" + hex.EncodeToString(bytes), nil
}

// buildTenantConfig builds a tenant config with defaults from the provided proto config
func (s *TenantService) buildTenantConfig(protoConfig *ragv1.TenantConfig) repository.TenantConfig {
	// Determine which embedding model to use
	embeddingModel := s.cfg.OllamaEmbeddingModel
	if protoConfig != nil && protoConfig.EmbeddingModel != "" {
		embeddingModel = protoConfig.EmbeddingModel
	}

	// Get model-specific chunk limits
	modelCfg := embedder.GetModelConfig(embeddingModel)

	config := repository.TenantConfig{
		EmbeddingModel: embeddingModel,
		LLMModel:       s.cfg.OllamaLLMModel,
		Chunker: repository.ChunkerConfig{
			Method:     s.cfg.DefaultChunkMethod,
			TargetSize: modelCfg.TargetChunkWords, // Use model-specific limit
			MaxSize:    modelCfg.MaxChunkWords,    // Use model-specific limit
			Overlap:    s.cfg.DefaultChunkOverlap,
		},
		TopK:         s.cfg.DefaultTopK,
		MinScore:     s.cfg.DefaultMinScore,
		SystemPrompt: defaultSystemPrompt,
	}

	if protoConfig == nil {
		return config
	}

	// Note: EmbeddingModel is already set above with model-specific chunk config
	if protoConfig.LlmModel != "" {
		config.LLMModel = protoConfig.LlmModel
	}
	if protoConfig.TopK > 0 {
		config.TopK = int(protoConfig.TopK)
	}
	if protoConfig.MinScore > 0 {
		config.MinScore = protoConfig.MinScore
	}
	if protoConfig.SystemPrompt != "" {
		config.SystemPrompt = protoConfig.SystemPrompt
	}

	if protoConfig.Chunker != nil {
		if protoConfig.Chunker.Method != "" {
			config.Chunker.Method = protoConfig.Chunker.Method
		}
		if protoConfig.Chunker.TargetSize > 0 {
			config.Chunker.TargetSize = int(protoConfig.Chunker.TargetSize)
		}
		if protoConfig.Chunker.MaxSize > 0 {
			config.Chunker.MaxSize = int(protoConfig.Chunker.MaxSize)
		}
		if protoConfig.Chunker.Overlap > 0 {
			config.Chunker.Overlap = int(protoConfig.Chunker.Overlap)
		}
	}

	return config
}

// mergeConfig merges existing config with proto updates
func (s *TenantService) mergeConfig(existing repository.TenantConfig, protoConfig *ragv1.TenantConfig) repository.TenantConfig {
	if protoConfig.EmbeddingModel != "" {
		existing.EmbeddingModel = protoConfig.EmbeddingModel
	}
	if protoConfig.LlmModel != "" {
		existing.LLMModel = protoConfig.LlmModel
	}
	if protoConfig.TopK > 0 {
		existing.TopK = int(protoConfig.TopK)
	}
	if protoConfig.MinScore > 0 {
		existing.MinScore = protoConfig.MinScore
	}
	if protoConfig.SystemPrompt != "" {
		existing.SystemPrompt = protoConfig.SystemPrompt
	}

	if protoConfig.Chunker != nil {
		if protoConfig.Chunker.Method != "" {
			existing.Chunker.Method = protoConfig.Chunker.Method
		}
		if protoConfig.Chunker.TargetSize > 0 {
			existing.Chunker.TargetSize = int(protoConfig.Chunker.TargetSize)
		}
		if protoConfig.Chunker.MaxSize > 0 {
			existing.Chunker.MaxSize = int(protoConfig.Chunker.MaxSize)
		}
		if protoConfig.Chunker.Overlap > 0 {
			existing.Chunker.Overlap = int(protoConfig.Chunker.Overlap)
		}
	}

	return existing
}

// validateTenantConfig validates the tenant configuration
func (s *TenantService) validateTenantConfig(config repository.TenantConfig) error {
	// Validate embedding model
	if config.EmbeddingModel == "" {
		return fmt.Errorf("embedding_model is required")
	}

	// Validate LLM model
	if config.LLMModel == "" {
		return fmt.Errorf("llm_model is required")
	}

	// Validate chunker config
	validMethods := map[string]bool{"fixed": true, "semantic": true, "sentence": true}
	if config.Chunker.Method != "" && !validMethods[config.Chunker.Method] {
		return fmt.Errorf("invalid chunker method: %s", config.Chunker.Method)
	}

	if config.Chunker.TargetSize < 0 {
		return fmt.Errorf("chunker target_size cannot be negative")
	}

	if config.Chunker.MaxSize < 0 {
		return fmt.Errorf("chunker max_size cannot be negative")
	}

	if config.Chunker.TargetSize > 0 && config.Chunker.MaxSize > 0 && config.Chunker.TargetSize > config.Chunker.MaxSize {
		return fmt.Errorf("chunker target_size cannot be greater than max_size")
	}

	if config.Chunker.Overlap < 0 {
		return fmt.Errorf("chunker overlap cannot be negative")
	}

	// Validate retrieval config
	if config.TopK < 0 {
		return fmt.Errorf("top_k cannot be negative")
	}

	if config.MinScore < 0 || config.MinScore > 1 {
		return fmt.Errorf("min_score must be between 0 and 1")
	}

	return nil
}

// tenantToProto converts a repository Tenant to proto Tenant
func (s *TenantService) tenantToProto(t *repository.Tenant) *ragv1.Tenant {
	return &ragv1.Tenant{
		Id:     t.ID.String(),
		Name:   t.Name,
		ApiKey: t.APIKey,
		Config: &ragv1.TenantConfig{
			EmbeddingModel: t.Config.EmbeddingModel,
			LlmModel:       t.Config.LLMModel,
			Chunker: &ragv1.ChunkerConfig{
				Method:     t.Config.Chunker.Method,
				TargetSize: int32(t.Config.Chunker.TargetSize),
				MaxSize:    int32(t.Config.Chunker.MaxSize),
				Overlap:    int32(t.Config.Chunker.Overlap),
			},
			TopK:         int32(t.Config.TopK),
			MinScore:     t.Config.MinScore,
			SystemPrompt: t.Config.SystemPrompt,
		},
		Usage: &ragv1.TenantUsage{
			DocumentCount:   int32(t.Usage.DocumentCount),
			ChunkCount:      int32(t.Usage.ChunkCount),
			QueryCountMonth: t.Usage.QueryCountMonth,
		},
		CreatedAt: timestamppb.New(t.CreatedAt),
		UpdatedAt: timestamppb.New(t.UpdatedAt),
	}
}

const defaultSystemPrompt = `You are a concise knowledge assistant. Answer questions using ONLY the provided documents.

IMPORTANT: Be brief and direct. Most answers should be 2-5 sentences.

Rules:
- Give the direct answer first, then brief supporting details only if needed
- Do NOT include step-by-step instructions unless specifically asked
- Do NOT include code examples unless specifically asked for code
- If the documents don't cover the topic, say "The documents don't cover this."
- Never invent information not in the provided documents`
