// Package repository defines domain models and data access interfaces for tenants, documents, and crawl jobs.
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/knoguchi/rag/internal/ragcore/chunk"
)

// ErrNotFound is returned when a requested entity does not exist
var ErrNotFound = errors.New("not found")

// Tenant represents a tenant in the system. API keys are stored only as a
// SHA-256 hash; KeyPrefix holds the first characters for display.
type Tenant struct {
	ID         uuid.UUID
	Name       string
	KeyPrefix  string
	Config     TenantConfig
	Usage      TenantUsage
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastUsedAt time.Time
}

// TenantConfig holds tenant-specific configuration
type TenantConfig struct {
	EmbeddingModel             string        `json:"embedding_model"`
	LLMModel                   string        `json:"llm_model"`
	Chunker                    ChunkerConfig `json:"chunker"`
	TopK                       int           `json:"top_k"`
	MinScore                   float32       `json:"min_score"`
	SystemPrompt               string        `json:"system_prompt"`
	RerankerEnabled            bool          `json:"reranker_enabled"`             // Enable LLM-based reranking (slower but more accurate)
	HybridEnabled              bool          `json:"hybrid_enabled"`               // Dense+sparse hybrid retrieval (collection must be hybrid-capable)
	ContextualRetrievalEnabled bool          `json:"contextual_retrieval_enabled"` // LLM-generated chunk context at ingestion
	RetentionDays              int           `json:"retention_days,omitempty"`     // Idle days before the tenant is reaped (0 = never)
}

// ChunkerConfig is the chunking configuration, defined in the ragcore chunk package.
type ChunkerConfig = chunk.Config

// TenantUsage holds tenant usage statistics
type TenantUsage struct {
	DocumentCount   int   `json:"document_count"`
	ChunkCount      int   `json:"chunk_count"`
	QueryCountMonth int64 `json:"query_count_month"`
}

// Document represents an ingested document
type Document struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	Source       string
	Title        string
	ContentHash  string
	ChunkCount   int
	Status       string
	ErrorMessage string
	Metadata     map[string]string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// DocumentChunk represents a chunk of a document
type DocumentChunk struct {
	ID         uuid.UUID
	DocumentID uuid.UUID
	TenantID   uuid.UUID
	ChunkIndex int
	Content    string
	Metadata   map[string]string
	CreatedAt  time.Time
}

// CrawlJob represents a web crawling job
type CrawlJob struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	Type         string
	Status       string
	RootURL      string
	Config       SpiderConfig
	PagesCrawled int
	PagesTotal   int
	PagesFailed  int
	ErrorMessage string
	CreatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
}

// SpiderConfig holds spider configuration
type SpiderConfig struct {
	MaxDepth        int      `json:"max_depth"`
	MaxPages        int      `json:"max_pages"`
	IncludePatterns []string `json:"include_patterns"`
	ExcludePatterns []string `json:"exclude_patterns"`
	UseHeadless     bool     `json:"use_headless"`
	RespectRobots   bool     `json:"respect_robots_txt"`
	DelayMS         int      `json:"delay_ms"`
	TimeoutSeconds  int      `json:"timeout_seconds"`
	UserAgent       string   `json:"user_agent"`
	FollowRedirects bool     `json:"follow_redirects"`
	MaxRedirects    int      `json:"max_redirects"`
}

// CrawledPage represents a page that was crawled
type CrawledPage struct {
	ID            uuid.UUID
	JobID         uuid.UUID
	URL           string
	Title         string
	Status        string
	ErrorMessage  string
	DocumentID    *uuid.UUID
	ContentLength int
	Depth         int
	CrawledAt     *time.Time
}

// TenantRepository defines operations for tenant persistence
type TenantRepository interface {
	// Create persists the tenant; apiKey is the plaintext key, stored hashed.
	Create(ctx context.Context, tenant *Tenant, apiKey string) error
	GetByID(ctx context.Context, id uuid.UUID) (*Tenant, error)
	// GetByAPIKey looks a tenant up by plaintext API key (hashed internally).
	GetByAPIKey(ctx context.Context, apiKey string) (*Tenant, error)
	List(ctx context.Context, limit, offset int) ([]*Tenant, int, error)
	Update(ctx context.Context, tenant *Tenant) error
	Delete(ctx context.Context, id uuid.UUID) error
	// UpdateAPIKey replaces the tenant's API key (stored hashed).
	UpdateAPIKey(ctx context.Context, id uuid.UUID, newAPIKey string) error
	UpdateUsage(ctx context.Context, id uuid.UUID, usage TenantUsage) error
	// ListExpired returns tenants whose retention policy has lapsed: a
	// non-zero retention_days config and no activity for that many days.
	ListExpired(ctx context.Context) ([]*Tenant, error)
}

// DocumentRepository defines operations for document persistence. All
// single-document operations are tenant-scoped in SQL: a document belonging
// to another tenant is indistinguishable from a missing one (ErrNotFound).
type DocumentRepository interface {
	Create(ctx context.Context, doc *Document) error
	GetByID(ctx context.Context, tenantID, id uuid.UUID) (*Document, error)
	GetByHash(ctx context.Context, tenantID uuid.UUID, hash string) (*Document, error)
	// ListBySource returns all documents a tenant has for a given source
	// (e.g. a URL); used to replace stale versions on re-ingestion.
	ListBySource(ctx context.Context, tenantID uuid.UUID, source string) ([]*Document, error)
	List(ctx context.Context, tenantID uuid.UUID, status string, limit, offset int) ([]*Document, int, error)
	Update(ctx context.Context, doc *Document) error
	Delete(ctx context.Context, tenantID, id uuid.UUID) error

	// Chunk operations
	CreateChunks(ctx context.Context, chunks []*DocumentChunk) error
	GetChunks(ctx context.Context, tenantID, documentID uuid.UUID, limit, offset int) ([]*DocumentChunk, error)
	DeleteChunks(ctx context.Context, tenantID, documentID uuid.UUID) error
}

// CrawlJobRepository defines operations for crawl job persistence
type CrawlJobRepository interface {
	Create(ctx context.Context, job *CrawlJob) error
	GetByID(ctx context.Context, id uuid.UUID) (*CrawlJob, error)
	List(ctx context.Context, tenantID uuid.UUID, status string, limit, offset int) ([]*CrawlJob, int, error)
	Update(ctx context.Context, job *CrawlJob) error

	// Page operations
	CreatePage(ctx context.Context, page *CrawledPage) error
	UpdatePage(ctx context.Context, page *CrawledPage) error
	GetPages(ctx context.Context, jobID uuid.UUID, status string, limit, offset int) ([]*CrawledPage, int, error)
}
