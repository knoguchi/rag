package ids

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRoundTrip(t *testing.T) {
	id := New()
	s := Format(id)
	if len(s) != 26 {
		t.Fatalf("expected 26-char ULID, got %q", s)
	}
	back, err := Parse(s)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if back != id {
		t.Errorf("round trip mismatch: %s != %s", back, id)
	}
}

func TestParse_AcceptsUUIDAndLowercase(t *testing.T) {
	id := New()

	back, err := Parse(id.UUIDString())
	if err != nil || back != id {
		t.Fatalf("uuid-form parse failed: %v (%s)", err, back)
	}

	back, err = Parse(strings.ToLower(id.String()))
	if err != nil || back != id {
		t.Fatalf("lowercase parse failed: %v (%s)", err, back)
	}
}

func TestUUIDStringRoundTrip(t *testing.T) {
	id := New()
	u := id.UUIDString()
	if _, err := uuid.Parse(u); err != nil {
		t.Fatalf("UUIDString not a UUID: %v", err)
	}
	back, err := ParseUUIDString(u)
	if err != nil || back != id {
		t.Fatalf("uuid round trip failed: %v", err)
	}
}

func TestParse_Rejects(t *testing.T) {
	for _, bad := range []string{"", "nope", "00000000-zzzz-0000-0000-000000000000", "0000000000000000000000000!"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestFormatString_PassThrough(t *testing.T) {
	if got := FormatString("not-an-id"); got != "not-an-id" {
		t.Errorf("expected pass-through, got %q", got)
	}
	id := New()
	if got := FormatString(id.UUIDString()); got != id.String() {
		t.Errorf("expected %s, got %q", id.String(), got)
	}
}
