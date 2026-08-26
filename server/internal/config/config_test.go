package config

import (
	"testing"
)

func TestValidate_DevelopmentAllowsMissingAdminKey(t *testing.T) {
	cfg := &Config{
		Environment: "development",
		AdminAPIKey: "",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error in development, got: %v", err)
	}
}

func TestValidate_ProductionRejectsEmptyAdminAPIKey(t *testing.T) {
	cfg := &Config{
		Environment: "production",
		AdminAPIKey: "",
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty admin API key in production")
	}
}

func TestValidate_ProductionAcceptsValidConfig(t *testing.T) {
	cfg := &Config{
		Environment:        "production",
		AdminAPIKey:        "some-admin-key",
		CORSAllowedOrigins: []string{"https://example.com"},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error for valid production config, got: %v", err)
	}
}

func TestValidate_RejectsNegativeRateLimits(t *testing.T) {
	cfg := &Config{
		Environment:  "development",
		RateLimitRPS: -1,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for negative rate limit")
	}
}

func TestIsDevelopment(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"development", true},
		{"production", false},
		{"staging", false},
	}
	for _, tt := range tests {
		cfg := &Config{Environment: tt.env}
		if got := cfg.IsDevelopment(); got != tt.want {
			t.Errorf("IsDevelopment() for %q = %v, want %v", tt.env, got, tt.want)
		}
	}
}
