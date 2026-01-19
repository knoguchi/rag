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

// TenantRepo implements repository.TenantRepository
type TenantRepo struct {
	db *DB
}

// NewTenantRepo creates a new tenant repository
func NewTenantRepo(db *DB) *TenantRepo {
	return &TenantRepo{db: db}
}

// Create creates a new tenant
func (r *TenantRepo) Create(ctx context.Context, tenant *repository.Tenant) error {
	configJSON, err := json.Marshal(tenant.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	query := `
		INSERT INTO tenants (id, name, api_key, config, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err = r.db.Pool.Exec(ctx, query,
		tenant.ID, tenant.Name, tenant.APIKey, configJSON, tenant.CreatedAt, tenant.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create tenant: %w", err)
	}
	return nil
}

// GetByID retrieves a tenant by ID
func (r *TenantRepo) GetByID(ctx context.Context, id uuid.UUID) (*repository.Tenant, error) {
	query := `
		SELECT id, name, api_key, config, created_at, updated_at
		FROM tenants
		WHERE id = $1
	`
	return r.scanTenant(ctx, query, id)
}

// GetByAPIKey retrieves a tenant by API key
func (r *TenantRepo) GetByAPIKey(ctx context.Context, apiKey string) (*repository.Tenant, error) {
	query := `
		SELECT id, name, api_key, config, created_at, updated_at
		FROM tenants
		WHERE api_key = $1
	`
	return r.scanTenant(ctx, query, apiKey)
}

func (r *TenantRepo) scanTenant(ctx context.Context, query string, args ...any) (*repository.Tenant, error) {
	var tenant repository.Tenant
	var configJSON []byte

	err := r.db.Pool.QueryRow(ctx, query, args...).Scan(
		&tenant.ID, &tenant.Name, &tenant.APIKey, &configJSON,
		&tenant.CreatedAt, &tenant.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get tenant: %w", err)
	}

	if err := json.Unmarshal(configJSON, &tenant.Config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Get usage statistics
	usage, err := r.getUsage(ctx, tenant.ID)
	if err != nil {
		return nil, err
	}
	tenant.Usage = *usage

	return &tenant, nil
}

func (r *TenantRepo) getUsage(ctx context.Context, tenantID uuid.UUID) (*repository.TenantUsage, error) {
	var usage repository.TenantUsage

	// Count documents
	err := r.db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM documents WHERE tenant_id = $1
	`, tenantID).Scan(&usage.DocumentCount)
	if err != nil {
		return nil, fmt.Errorf("failed to count documents: %w", err)
	}

	// Count chunks
	err = r.db.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(chunk_count), 0) FROM documents WHERE tenant_id = $1
	`, tenantID).Scan(&usage.ChunkCount)
	if err != nil {
		return nil, fmt.Errorf("failed to count chunks: %w", err)
	}

	return &usage, nil
}

// List retrieves all tenants with pagination
func (r *TenantRepo) List(ctx context.Context, limit, offset int) ([]*repository.Tenant, int, error) {
	// Get total count
	var total int
	err := r.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM tenants`).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count tenants: %w", err)
	}

	query := `
		SELECT id, name, api_key, config, created_at, updated_at
		FROM tenants
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`
	rows, err := r.db.Pool.Query(ctx, query, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list tenants: %w", err)
	}
	defer rows.Close()

	var tenants []*repository.Tenant
	for rows.Next() {
		var tenant repository.Tenant
		var configJSON []byte
		if err := rows.Scan(&tenant.ID, &tenant.Name, &tenant.APIKey, &configJSON,
			&tenant.CreatedAt, &tenant.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("failed to scan tenant: %w", err)
		}
		if err := json.Unmarshal(configJSON, &tenant.Config); err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal config: %w", err)
		}
		tenants = append(tenants, &tenant)
	}

	return tenants, total, nil
}

// Update updates a tenant
func (r *TenantRepo) Update(ctx context.Context, tenant *repository.Tenant) error {
	configJSON, err := json.Marshal(tenant.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	query := `
		UPDATE tenants
		SET name = $2, config = $3, updated_at = NOW()
		WHERE id = $1
	`
	result, err := r.db.Pool.Exec(ctx, query, tenant.ID, tenant.Name, configJSON)
	if err != nil {
		return fmt.Errorf("failed to update tenant: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("tenant not found")
	}
	return nil
}

// Delete deletes a tenant
func (r *TenantRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete tenant: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("tenant not found")
	}
	return nil
}

// UpdateAPIKey updates a tenant's API key
func (r *TenantRepo) UpdateAPIKey(ctx context.Context, id uuid.UUID, newAPIKey string) error {
	result, err := r.db.Pool.Exec(ctx,
		`UPDATE tenants SET api_key = $2, updated_at = NOW() WHERE id = $1`,
		id, newAPIKey)
	if err != nil {
		return fmt.Errorf("failed to update API key: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("tenant not found")
	}
	return nil
}

// UpdateUsage updates tenant usage statistics (called periodically)
func (r *TenantRepo) UpdateUsage(ctx context.Context, id uuid.UUID, usage repository.TenantUsage) error {
	// Usage is calculated on-the-fly from documents table, so this is a no-op
	// Could be used for caching or storing query counts
	return nil
}

// Ensure TenantRepo implements the interface
var _ repository.TenantRepository = (*TenantRepo)(nil)
