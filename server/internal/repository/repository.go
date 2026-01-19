// Package repository defines domain models and data access interfaces for tenants, documents, and crawl jobs.
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested entity does not exist
var ErrNotFound = errors.New("not found")

// Tenant represents a tenant in the system
type Tenant struct {
	ID        uuid.UUID
	Name      string
	APIKey    string
	Config    TenantConfig
	Usage     TenantUsage
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TenantConfig holds tenant-specific configuration
type TenantConfig struct {
	EmbeddingModel   string        `json:"embedding_model"`
	LLMModel         string        `json:"llm_model"`
	Chunker          ChunkerConfig `json:"chunker"`
	TopK             int           `json:"top_k"`
	MinScore         float32       `json:"min_score"`
	SystemPrompt     string        `json:"system_prompt"`
	RerankerEnabled  bool          `json:"reranker_enabled"`  // Enable LLM-based reranking (slower but more accurate)
}

// ChunkerConfig holds chunking configuration
type ChunkerConfig struct {
	Method     string `json:"method"`      // semantic, fixed, sentence
	TargetSize int    `json:"target_size"` // target tokens per chunk
	MaxSize    int    `json:"max_size"`    // max tokens per chunk
	Overlap    int    `json:"overlap"`     // overlap tokens
}

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
	ID          uuid.UUID
	DocumentID  uuid.UUID
	ChunkIndex  int
	Content     string
	Metadata    map[string]string
	CreatedAt   time.Time
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
	Create(ctx context.Context, tenant *Tenant) error
	GetByID(ctx context.Context, id uuid.UUID) (*Tenant, error)
	GetByAPIKey(ctx context.Context, apiKey string) (*Tenant, error)
	List(ctx context.Context, limit, offset int) ([]*Tenant, int, error)
	Update(ctx context.Context, tenant *Tenant) error
	Delete(ctx context.Context, id uuid.UUID) error
	UpdateAPIKey(ctx context.Context, id uuid.UUID, newAPIKey string) error
	UpdateUsage(ctx context.Context, id uuid.UUID, usage TenantUsage) error
}

// DocumentRepository defines operations for document persistence
type DocumentRepository interface {
	Create(ctx context.Context, doc *Document) error
	GetByID(ctx context.Context, id uuid.UUID) (*Document, error)
	GetByHash(ctx context.Context, tenantID uuid.UUID, hash string) (*Document, error)
	List(ctx context.Context, tenantID uuid.UUID, status string, limit, offset int) ([]*Document, int, error)
	Update(ctx context.Context, doc *Document) error
	Delete(ctx context.Context, id uuid.UUID) error

	// Chunk operations
	CreateChunks(ctx context.Context, chunks []*DocumentChunk) error
	GetChunks(ctx context.Context, documentID uuid.UUID, limit, offset int) ([]*DocumentChunk, error)
	DeleteChunks(ctx context.Context, documentID uuid.UUID) error
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
