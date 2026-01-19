package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/knoguchi/rag/internal/repository"
)

// CrawlJobRepo implements repository.CrawlJobRepository
type CrawlJobRepo struct {
	db *DB
}

// NewCrawlJobRepo creates a new crawl job repository
func NewCrawlJobRepo(db *DB) *CrawlJobRepo {
	return &CrawlJobRepo{db: db}
}

// Create creates a new crawl job
func (r *CrawlJobRepo) Create(ctx context.Context, job *repository.CrawlJob) error {
	configJSON, err := json.Marshal(job.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	query := `
		INSERT INTO crawl_jobs (id, tenant_id, type, status, root_url, config, pages_crawled, pages_total, pages_failed, error_message, created_at, started_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`
	_, err = r.db.Pool.Exec(ctx, query,
		job.ID, job.TenantID, job.Type, job.Status, job.RootURL, configJSON,
		job.PagesCrawled, job.PagesTotal, job.PagesFailed, job.ErrorMessage,
		job.CreatedAt, job.StartedAt, job.CompletedAt)
	if err != nil {
		return fmt.Errorf("failed to create crawl job: %w", err)
	}
	return nil
}

// GetByID retrieves a crawl job by ID
func (r *CrawlJobRepo) GetByID(ctx context.Context, id uuid.UUID) (*repository.CrawlJob, error) {
	query := `
		SELECT id, tenant_id, type, status, root_url, config, pages_crawled, pages_total, pages_failed, error_message, created_at, started_at, completed_at
		FROM crawl_jobs
		WHERE id = $1
	`
	var job repository.CrawlJob
	var configJSON []byte

	err := r.db.Pool.QueryRow(ctx, query, id).Scan(
		&job.ID, &job.TenantID, &job.Type, &job.Status, &job.RootURL, &configJSON,
		&job.PagesCrawled, &job.PagesTotal, &job.PagesFailed, &job.ErrorMessage,
		&job.CreatedAt, &job.StartedAt, &job.CompletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get crawl job: %w", err)
	}

	if err := json.Unmarshal(configJSON, &job.Config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &job, nil
}

// List retrieves crawl jobs for a tenant with pagination
func (r *CrawlJobRepo) List(ctx context.Context, tenantID uuid.UUID, status string, limit, offset int) ([]*repository.CrawlJob, int, error) {
	// Build query with optional status filter
	countQuery := `SELECT COUNT(*) FROM crawl_jobs WHERE tenant_id = $1`
	listQuery := `
		SELECT id, tenant_id, type, status, root_url, config, pages_crawled, pages_total, pages_failed, error_message, created_at, started_at, completed_at
		FROM crawl_jobs
		WHERE tenant_id = $1
	`
	args := []any{tenantID}

	if status != "" {
		countQuery += ` AND status = $2`
		listQuery += ` AND status = $2`
		args = append(args, status)
	}

	listQuery += ` ORDER BY created_at DESC LIMIT $` + fmt.Sprintf("%d", len(args)+1) + ` OFFSET $` + fmt.Sprintf("%d", len(args)+2)

	// Get total count
	var total int
	err := r.db.Pool.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count crawl jobs: %w", err)
	}

	// Get jobs
	args = append(args, limit, offset)
	rows, err := r.db.Pool.Query(ctx, listQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list crawl jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*repository.CrawlJob
	for rows.Next() {
		var job repository.CrawlJob
		var configJSON []byte
		if err := rows.Scan(&job.ID, &job.TenantID, &job.Type, &job.Status, &job.RootURL, &configJSON,
			&job.PagesCrawled, &job.PagesTotal, &job.PagesFailed, &job.ErrorMessage,
			&job.CreatedAt, &job.StartedAt, &job.CompletedAt); err != nil {
			return nil, 0, fmt.Errorf("failed to scan crawl job: %w", err)
		}
		if err := json.Unmarshal(configJSON, &job.Config); err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal config: %w", err)
		}
		jobs = append(jobs, &job)
	}

	return jobs, total, nil
}

// Update updates a crawl job
func (r *CrawlJobRepo) Update(ctx context.Context, job *repository.CrawlJob) error {
	configJSON, err := json.Marshal(job.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	query := `
		UPDATE crawl_jobs
		SET status = $2, config = $3, pages_crawled = $4, pages_total = $5,
		    pages_failed = $6, error_message = $7, started_at = $8, completed_at = $9
		WHERE id = $1
	`
	result, err := r.db.Pool.Exec(ctx, query,
		job.ID, job.Status, configJSON, job.PagesCrawled, job.PagesTotal,
		job.PagesFailed, job.ErrorMessage, job.StartedAt, job.CompletedAt)
	if err != nil {
		return fmt.Errorf("failed to update crawl job: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("crawl job not found")
	}
	return nil
}

// CreatePage creates a new crawled page
func (r *CrawlJobRepo) CreatePage(ctx context.Context, page *repository.CrawledPage) error {
	query := `
		INSERT INTO crawled_pages (id, job_id, url, title, status, error_message, document_id, content_length, depth, crawled_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := r.db.Pool.Exec(ctx, query,
		page.ID, page.JobID, page.URL, page.Title, page.Status, page.ErrorMessage,
		page.DocumentID, page.ContentLength, page.Depth, page.CrawledAt)
	if err != nil {
		return fmt.Errorf("failed to create crawled page: %w", err)
	}
	return nil
}

// UpdatePage updates a crawled page
func (r *CrawlJobRepo) UpdatePage(ctx context.Context, page *repository.CrawledPage) error {
	query := `
		UPDATE crawled_pages
		SET title = $2, status = $3, error_message = $4, document_id = $5,
		    content_length = $6, crawled_at = $7
		WHERE id = $1
	`
	result, err := r.db.Pool.Exec(ctx, query,
		page.ID, page.Title, page.Status, page.ErrorMessage, page.DocumentID,
		page.ContentLength, page.CrawledAt)
	if err != nil {
		return fmt.Errorf("failed to update crawled page: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("crawled page not found")
	}
	return nil
}

// GetPages retrieves pages for a crawl job
func (r *CrawlJobRepo) GetPages(ctx context.Context, jobID uuid.UUID, status string, limit, offset int) ([]*repository.CrawledPage, int, error) {
	// Build query with optional status filter
	countQuery := `SELECT COUNT(*) FROM crawled_pages WHERE job_id = $1`
	listQuery := `
		SELECT id, job_id, url, title, status, error_message, document_id, content_length, depth, crawled_at
		FROM crawled_pages
		WHERE job_id = $1
	`
	args := []any{jobID}

	if status != "" {
		countQuery += ` AND status = $2`
		listQuery += ` AND status = $2`
		args = append(args, status)
	}

	listQuery += ` ORDER BY crawled_at DESC NULLS LAST LIMIT $` + fmt.Sprintf("%d", len(args)+1) + ` OFFSET $` + fmt.Sprintf("%d", len(args)+2)

	// Get total count
	var total int
	err := r.db.Pool.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count crawled pages: %w", err)
	}

	// Get pages
	args = append(args, limit, offset)
	rows, err := r.db.Pool.Query(ctx, listQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list crawled pages: %w", err)
	}
	defer rows.Close()

	var pages []*repository.CrawledPage
	for rows.Next() {
		var page repository.CrawledPage
		if err := rows.Scan(&page.ID, &page.JobID, &page.URL, &page.Title, &page.Status,
			&page.ErrorMessage, &page.DocumentID, &page.ContentLength, &page.Depth, &page.CrawledAt); err != nil {
			return nil, 0, fmt.Errorf("failed to scan crawled page: %w", err)
		}
		pages = append(pages, &page)
	}

	return pages, total, nil
}

// Ensure CrawlJobRepo implements the interface
var _ repository.CrawlJobRepository = (*CrawlJobRepo)(nil)
