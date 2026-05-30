package config

import (
	"testing"
)

func TestValidate_DevelopmentAllowsDefaults(t *testing.T) {
	cfg := &Config{
		Environment:   "development",
		JWTSecret:     "change-this-in-production",
		SessionSecret: "change-this-in-production",
		AdminAPIKey:   "",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error in development, got: %v", err)
	}
}

func TestValidate_ProductionRejectsDefaultJWTSecret(t *testing.T) {
	cfg := &Config{
		Environment:   "production",
		JWTSecret:     "change-this-in-production",
		SessionSecret: "a-very-long-session-secret-that-is-at-least-32-chars",
		AdminAPIKey:   "some-admin-key",
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for default JWT secret in production")
	}
}

func TestValidate_ProductionRejectsShortJWTSecret(t *testing.T) {
	cfg := &Config{
		Environment:   "production",
		JWTSecret:     "too-short",
		SessionSecret: "a-very-long-session-secret-that-is-at-least-32-chars",
		AdminAPIKey:   "some-admin-key",
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for short JWT secret in production")
	}
}

func TestValidate_ProductionRejectsEmptyAdminAPIKey(t *testing.T) {
	cfg := &Config{
		Environment:   "production",
		JWTSecret:     "a-very-long-jwt-secret-that-is-at-least-32-characters",
		SessionSecret: "a-very-long-session-secret-that-is-at-least-32-chars",
		AdminAPIKey:   "",
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty admin API key in production")
	}
}

func TestValidate_ProductionAcceptsValidConfig(t *testing.T) {
	cfg := &Config{
		Environment:   "production",
		JWTSecret:     "a-very-long-jwt-secret-that-is-at-least-32-characters",
		SessionSecret: "a-very-long-session-secret-that-is-at-least-32-chars",
		AdminAPIKey:   "some-admin-key",
		CORSAllowedOrigins: []string{"https://example.com"},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error for valid production config, got: %v", err)
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
