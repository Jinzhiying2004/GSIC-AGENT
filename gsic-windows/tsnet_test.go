package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidAuthKey(t *testing.T) {
	valid := "tskey-auth-" + strings.Repeat("a", 40)
	cases := []struct {
		key   string
		valid bool
	}{
		{valid, true},
		{"  " + valid + "\n", true}, // surrounding whitespace is trimmed
		{"tskey-auth-" + strings.Repeat("b", 19), true}, // 30 chars total, minimum
		{"tskey-auth-" + strings.Repeat("b", 18), false}, // 29 chars, too short
		{"tskey-auth-short", false},
		{"tskey-auth-", false},
		{"tskey-api-" + strings.Repeat("a", 40), false}, // API key, cannot join
		{"", false},
		{"not-a-key", false},
		{"tskey-auth", false}, // missing dash + random part
	}
	for _, c := range cases {
		if got := ValidAuthKey(c.key); got != c.valid {
			t.Errorf("ValidAuthKey(%q) = %v, want %v", c.key, got, c.valid)
		}
	}
}

func TestAuthKeyConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig(0, "", dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	key := "tskey-auth-" + strings.Repeat("k", 40)
	cfg.TailscaleAuthKey = key
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// The key must be in the file (0600, like the token).
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), key) {
		t.Errorf("config.json does not contain the auth key")
	}
	// Reload and check it round-trips.
	cfg2, err := LoadConfig(0, "", dir)
	if err != nil {
		t.Fatalf("LoadConfig reload: %v", err)
	}
	if cfg2.TailscaleAuthKey != key {
		t.Errorf("reloaded auth key = %q, want %q", cfg2.TailscaleAuthKey, key)
	}
}

func TestAuthKeyEnvOverride(t *testing.T) {
	dir := t.TempDir()
	key := "tskey-auth-" + strings.Repeat("e", 40)
	t.Setenv("GSIC_TAILSCALE_AUTHKEY", key)
	cfg, err := LoadConfig(0, "", dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.TailscaleAuthKey != key {
		t.Errorf("env auth key = %q, want %q", cfg.TailscaleAuthKey, key)
	}
}

func TestRestartWithAuthKeyValidation(t *testing.T) {
	n := NewTsnetNode(t.TempDir(), "gsic-test")
	// Invalid key: rejected before touching the node.
	if _, err := n.RestartWithAuthKey("tskey-api-" + strings.Repeat("a", 40)); err == nil {
		t.Errorf("RestartWithAuthKey accepted an API key")
	}
	if _, err := n.RestartWithAuthKey("garbage"); err == nil {
		t.Errorf("RestartWithAuthKey accepted garbage")
	}
	if n.AuthKeySet() {
		t.Errorf("AuthKeySet true after rejected keys")
	}
	// Valid key on a node that never started: stored, not running.
	key := "tskey-auth-" + strings.Repeat("v", 40)
	alreadyRunning, err := n.RestartWithAuthKey(key)
	if err != nil {
		t.Fatalf("RestartWithAuthKey: %v", err)
	}
	if alreadyRunning {
		t.Errorf("fresh node reported alreadyRunning")
	}
	if !n.AuthKeySet() {
		t.Errorf("AuthKeySet false after applying a valid key")
	}
	// A second application while still not running is fine (idempotent).
	if _, err := n.RestartWithAuthKey(key); err != nil {
		t.Fatalf("second RestartWithAuthKey: %v", err)
	}
}
