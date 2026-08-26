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

func TestParse_AcceptsUUID(t *testing.T) {
	id := uuid.New()
	back, err := Parse(id.String())
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if back != id {
		t.Errorf("expected %s, got %s", id, back)
	}
}

func TestParse_AcceptsLowercaseULID(t *testing.T) {
	id := New()
	back, err := Parse(Format(id)[:26])
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if back != id {
		t.Error("uppercase parse mismatch")
	}
	// lowercase form also accepted
	low := strings.ToLower(Format(id))
	back2, err := Parse(low)
	if err != nil {
		t.Fatalf("lowercase parse failed: %v", err)
	}
	if back2 != id {
		t.Error("lowercase parse mismatch")
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
	id := uuid.New()
	if got := FormatString(id.String()); len(got) != 26 {
		t.Errorf("expected ULID form, got %q", got)
	}
}
