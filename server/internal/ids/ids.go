// Package ids standardizes external identifiers on ULID (26-char Crockford
// base32, sortable by creation time). ULIDs and UUIDs are both 128 bits, so
// internally everything remains uuid.UUID — Postgres uuid columns and Qdrant
// collection names are untouched; only the API boundary speaks ULID.
package ids

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
)

// New returns a fresh ULID-derived UUID.
func New() uuid.UUID {
	return uuid.UUID(ulid.Make())
}

// Format renders an internal UUID as its ULID string.
func Format(id uuid.UUID) string {
	return ulid.ULID(id).String()
}

// FormatString re-renders a UUID string as a ULID string; inputs that are
// not UUIDs are returned unchanged (best-effort, for pass-through fields).
func FormatString(s string) string {
	id, err := uuid.Parse(s)
	if err != nil {
		return s
	}
	return Format(id)
}

// Parse accepts an identifier in either ULID (26 chars) or UUID form and
// returns the internal UUID.
func Parse(s string) (uuid.UUID, error) {
	s = strings.TrimSpace(s)
	if len(s) == ulid.EncodedSize {
		u, err := ulid.ParseStrict(strings.ToUpper(s))
		if err != nil {
			return uuid.Nil, fmt.Errorf("invalid ULID: %w", err)
		}
		return uuid.UUID(u), nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid ID (expected ULID or UUID): %w", err)
	}
	return id, nil
}
