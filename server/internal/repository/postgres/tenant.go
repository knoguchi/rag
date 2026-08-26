package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/knoguchi/rag/internal/ids"
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

// hashAPIKey returns the hex SHA-256 of a plaintext API key.
func hashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// keyPrefix returns the display prefix stored alongside the hash.
func keyPrefix(key string) string {
	if len(key) > 12 {
		return key[:12]
	}
	return key
}

// Create creates a new tenant, storing only the hash of the API key
func (r *TenantRepo) Create(ctx context.Context, tenant *repository.Tenant, apiKey string) error {
	configJSON, err := json.Marshal(tenant.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	tenant.KeyPrefix = keyPrefix(apiKey)
	query := `
		INSERT INTO tenants (id, name, api_key_hash, key_prefix, config, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err = r.db.Pool.Exec(ctx, query,
		tenant.ID, tenant.Name, hashAPIKey(apiKey), tenant.KeyPrefix, configJSON, tenant.CreatedAt, tenant.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create tenant: %w", err)
	}
	return nil
}

// GetByID retrieves a tenant by ID
func (r *TenantRepo) GetByID(ctx context.Context, id ids.ID) (*repository.Tenant, error) {
	query := `
		SELECT id, name, key_prefix, config, created_at, updated_at
		FROM tenants
		WHERE id = $1
	`
	return r.scanTenant(ctx, query, id)
}

// GetByAPIKey retrieves a tenant by plaintext API key (hashed for lookup).
// Successful lookups refresh last_used_at (throttled) so idle-tenant
// retention works.
func (r *TenantRepo) GetByAPIKey(ctx context.Context, apiKey string) (*repository.Tenant, error) {
	query := `
		SELECT id, name, key_prefix, config, created_at, updated_at
		FROM tenants
		WHERE api_key_hash = $1
	`
	tenant, err := r.scanTenant(ctx, query, hashAPIKey(apiKey))
	if err == nil && tenant != nil {
		// Best-effort activity touch, at most once per hour per tenant
		_, _ = r.db.Pool.Exec(ctx,
			`UPDATE tenants SET last_used_at = now() WHERE id = $1 AND last_used_at < now() - interval '1 hour'`,
			tenant.ID)
	}
	return tenant, err
}

// ListExpired returns tenants with a lapsed retention policy
func (r *TenantRepo) ListExpired(ctx context.Context) ([]*repository.Tenant, error) {
	query := `
		SELECT id, name, key_prefix, config, created_at, updated_at
		FROM tenants
		WHERE COALESCE((config->>'retention_days')::int, 0) > 0
		  AND last_used_at < now() - make_interval(days => (config->>'retention_days')::int)
	`
	rows, err := r.db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list expired tenants: %w", err)
	}
	defer rows.Close()

	var tenants []*repository.Tenant
	for rows.Next() {
		var tenant repository.Tenant
		var configJSON []byte
		if err := rows.Scan(&tenant.ID, &tenant.Name, &tenant.KeyPrefix, &configJSON,
			&tenant.CreatedAt, &tenant.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan tenant: %w", err)
		}
		if err := json.Unmarshal(configJSON, &tenant.Config); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config: %w", err)
		}
		tenants = append(tenants, &tenant)
	}

	return tenants, nil
}

func (r *TenantRepo) scanTenant(ctx context.Context, query string, args ...any) (*repository.Tenant, error) {
	var tenant repository.Tenant
	var configJSON []byte

	err := r.db.Pool.QueryRow(ctx, query, args...).Scan(
		&tenant.ID, &tenant.Name, &tenant.KeyPrefix, &configJSON,
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

func (r *TenantRepo) getUsage(ctx context.Context, tenantID ids.ID) (*repository.TenantUsage, error) {
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
		SELECT id, name, key_prefix, config, created_at, updated_at
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
		if err := rows.Scan(&tenant.ID, &tenant.Name, &tenant.KeyPrefix, &configJSON,
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
func (r *TenantRepo) Delete(ctx context.Context, id ids.ID) error {
	result, err := r.db.Pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete tenant: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("tenant not found")
	}
	return nil
}

// UpdateAPIKey updates a tenant's API key (stored hashed)
func (r *TenantRepo) UpdateAPIKey(ctx context.Context, id ids.ID, newAPIKey string) error {
	result, err := r.db.Pool.Exec(ctx,
		`UPDATE tenants SET api_key_hash = $2, key_prefix = $3, updated_at = NOW() WHERE id = $1`,
		id, hashAPIKey(newAPIKey), keyPrefix(newAPIKey))
	if err != nil {
		return fmt.Errorf("failed to update API key: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("tenant not found")
	}
	return nil
}

// UpdateUsage updates tenant usage statistics (called periodically)
func (r *TenantRepo) UpdateUsage(ctx context.Context, id ids.ID, usage repository.TenantUsage) error {
	// Usage is calculated on-the-fly from documents table, so this is a no-op
	// Could be used for caching or storing query counts
	return nil
}

// Ensure TenantRepo implements the interface
var _ repository.TenantRepository = (*TenantRepo)(nil)
