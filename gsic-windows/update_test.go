package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.bin")
	content := []byte("gsic-update-test-content")
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(content)
	want := hex.EncodeToString(h[:])
	if err := verifySHA256(p, want); err != nil {
		t.Fatalf("valid hash rejected: %v", err)
	}
	if err := verifySHA256(p, strings.ToUpper(want)); err != nil {
		t.Fatalf("uppercase hash rejected: %v", err)
	}
	if err := verifySHA256(p, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("wrong hash accepted")
	}
}

func TestSanitizeVersion(t *testing.T) {
	cases := map[string]string{
		"4.35":        "4.35",
		"4.35-beta.1": "4.35-beta.1",
		"../../evil":  "....evil",
		"a/b\\c:d":    "abcd",
		"":            "pushed",
		"!!!":         "pushed",
		"v4.35 (x64)": "v4.35x64",
	}
	for in, want := range cases {
		if got := sanitizeVersion(in); got != want {
			t.Errorf("sanitizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriteUpdateBatch(t *testing.T) {
	dir := t.TempDir()
	bat := filepath.Join(dir, "gsic-update.bat")
	exe := `C:\Program Files\GSIC\gsic.exe`
	newExe := filepath.Join(dir, "gsic-update-435.exe")
	bak := exe + ".bak"
	if err := writeUpdateBatch(bat, exe, newExe, bak); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(bat)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	// Must quote paths (spaces in Program Files).
	if !strings.Contains(s, `"`+exe+`"`) {
		t.Errorf("batch does not quote exe path")
	}
	if !strings.Contains(s, `"`+newExe+`"`) {
		t.Errorf("batch does not quote new exe path")
	}
	// Must wait for the old process, swap, restart, restore on failure.
	// 2026-11-22 regression: without a PID the batch must still wait
	// before swapping (empty %1 used to race the exit and leave nothing running).
	for _, want := range []string{"tasklist", "taskkill", "move /Y", "copy /Y", "start \"\"", "del \"%~f0\"", "ping -n 4", "if \"%PID%\"==\"\""} {
		if !strings.Contains(s, want) {
			t.Errorf("batch missing %q", want)
		}
	}
}

func TestUpdaterSource(t *testing.T) {
	cfg := &Config{Port: 28789}
	u := NewUpdater(cfg)
	if u.Source() != DefaultUpdateSource {
		t.Errorf("default source = %q, want %q", u.Source(), DefaultUpdateSource)
	}
	cfg.UpdateSource = "https://example.com/v.json"
	if u.Source() != "https://example.com/v.json" {
		t.Errorf("override source = %q", u.Source())
	}
}

func TestUpdaterShouldCheck(t *testing.T) {
	cfg := &Config{Port: 28789, AutoUpdate: false}
	u := NewUpdater(cfg)
	if u.ShouldCheck() {
		t.Errorf("ShouldCheck with AutoUpdate=false")
	}
	cfg.AutoUpdate = true
	if !u.ShouldCheck() {
		t.Errorf("ShouldCheck with no prior check should be true")
	}
}

func TestUpdaterSetPending(t *testing.T) {
	cfg := &Config{Port: 28789}
	u := NewUpdater(cfg)
	if u.Pending() != nil {
		t.Errorf("pending should start nil")
	}
	u.SetPending("4.36", "https://example.com/gsic.exe", "abc123")
	p := u.Pending()
	if p == nil || p.Version != "4.36" || p.URL != "https://example.com/gsic.exe" || p.SHA256 != "abc123" {
		t.Errorf("pending = %+v", p)
	}
}

func TestConfigUpdateFields(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig(0, "test-token-1234567890abcdef", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AutoUpdate {
		t.Errorf("AutoUpdate default should be true")
	}
	if !cfg.AutoInstallUpdates {
		t.Errorf("AutoInstallUpdates default should be true")
	}
	if cfg.UpdateChannel != "stable" {
		t.Errorf("UpdateChannel default = %q", cfg.UpdateChannel)
	}
	// Round-trip through Save.
	cfg.AutoUpdate = false
	cfg.AutoInstallUpdates = false
	cfg.UpdateSource = "https://example.com/v.json"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	cfg2, err := LoadConfig(0, "test-token-1234567890abcdef", dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.AutoUpdate {
		t.Errorf("AutoUpdate not persisted")
	}
	if cfg2.AutoInstallUpdates {
		t.Errorf("AutoInstallUpdates not persisted")
	}
	if cfg2.UpdateSource != "https://example.com/v.json" {
		t.Errorf("UpdateSource not persisted: %q", cfg2.UpdateSource)
	}
}
