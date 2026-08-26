// Package ids defines the canonical 128-bit identifier used across the
// system. IDs are ULIDs: generated sortable-by-creation, rendered as 26-char
// Crockford base32 everywhere (APIs, logs). The two storage edges impose
// UUID *formatting* on the same bytes: Postgres stores them in uuid columns
// (pgx encodes the underlying [16]byte transparently) and Qdrant requires
// UUID-shaped point IDs, so UUIDString()/ParseUUIDString convert at those
// edges only.
package ids

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
)

// ID is the canonical 128-bit identifier. The zero value is Nil.
type ID [16]byte

// Nil is the zero ID.
var Nil ID

// New returns a fresh ULID.
func New() ID {
	return ID(ulid.Make())
}

// String renders the ID as a ULID (26-char Crockford base32).
func (id ID) String() string {
	return ulid.ULID(id).String()
}

// IsNil reports whether the ID is the zero value.
func (id ID) IsNil() bool {
	return id == Nil
}

// UUIDString renders the same bytes in UUID form, for storage systems that
// require UUID formatting (Qdrant point IDs and payloads).
func (id ID) UUIDString() string {
	return uuid.UUID(id).String()
}

// Format renders an ID as its ULID string.
func Format(id ID) string {
	return id.String()
}

// FormatString re-renders a UUID string as a ULID string; inputs that are
// not UUIDs are returned unchanged (best-effort, for pass-through fields).
func FormatString(s string) string {
	u, err := uuid.Parse(s)
	if err != nil {
		return s
	}
	return ID(u).String()
}

// Parse accepts an identifier in either ULID (26 chars) or UUID form.
func Parse(s string) (ID, error) {
	s = strings.TrimSpace(s)
	if len(s) == ulid.EncodedSize {
		u, err := ulid.ParseStrict(strings.ToUpper(s))
		if err != nil {
			return Nil, fmt.Errorf("invalid ULID: %w", err)
		}
		return ID(u), nil
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return Nil, fmt.Errorf("invalid ID (expected ULID or UUID): %w", err)
	}
	return ID(u), nil
}

// ParseUUIDString converts a UUID-formatted string (as stored at the vector
// layer) back to an ID.
func ParseUUIDString(s string) (ID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return Nil, err
	}
	return ID(u), nil
}
