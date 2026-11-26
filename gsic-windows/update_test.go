package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
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
	if cfg.AllowLAN {
		t.Errorf("AllowLAN default should be false (Issue #2: loopback only)")
	}
	// Round-trip through Save.
	cfg.AutoUpdate = false
	cfg.AutoInstallUpdates = false
	cfg.UpdateSource = "https://example.com/v.json"
	cfg.AllowLAN = true
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
	if !cfg2.AllowLAN {
		t.Errorf("AllowLAN not persisted")
	}
}

// TestValidSHA256 covers the Issue #4 format gate: missing, short,
// long, non-hex and whitespace-padded digests are all rejected; only
// a 64-char hex digest (either case) passes.
func TestValidSHA256(t *testing.T) {
	valid := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cases := map[string]bool{
		"":                       false,
		"abc123":                 false,
		valid:                    true,
		strings.ToUpper(valid):   true,
		strings.Repeat("0", 63):  false,
		strings.Repeat("0", 65):  false,
		strings.Repeat("g", 64):  false,
		" " + valid:              false,
		valid + " ":              false,
		strings.Repeat("a1", 32): true,
		"zz3456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": false,
	}
	for in, want := range cases {
		if got := validSHA256(in); got != want {
			t.Errorf("validSHA256(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestHandleUpdatePushRequiresSHA256 covers the Issue #4 entry gate:
// /update/push rejects a missing or malformed hash with 400 and
// records nothing; a valid hash with auto-install off is recorded
// as pending. No download is attempted in any case.
func TestHandleUpdatePushRequiresSHA256(t *testing.T) {
	cfg := &Config{Port: 28789, Token: "test-token-1234567890abcdef", AutoInstallUpdates: false}
	srv := NewServer(cfg, nil)
	srv.SetUpdater(NewUpdater(cfg))
	h := srv.Handler()
	doPush := func(token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/update/push", strings.NewReader(body))
		req.Header.Set("X-Token", token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	valid := strings.Repeat("a1", 32)

	rec := doPush(cfg.Token, `{"version":"4.99","url":"https://example.com/gsic.exe"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing sha256: status = %d, want 400", rec.Code)
	}
	if srv.updater.Pending() != nil {
		t.Errorf("missing sha256: push was recorded as pending")
	}

	rec = doPush(cfg.Token, `{"version":"4.99","url":"https://example.com/gsic.exe","sha256":"xyz"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed sha256: status = %d, want 400", rec.Code)
	}
	if srv.updater.Pending() != nil {
		t.Errorf("malformed sha256: push was recorded as pending")
	}

	rec = doPush(cfg.Token, `{"version":"4.99","url":"https://example.com/gsic.exe","sha256":"`+valid+`"}`)
	if rec.Code != http.StatusOK {
		t.Errorf("valid sha256: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	p := srv.updater.Pending()
	if p == nil || p.SHA256 != valid || p.URL != "https://example.com/gsic.exe" {
		t.Errorf("valid sha256: pending = %+v", p)
	}

	rec = doPush("wrong-token", `{"version":"4.99","url":"https://example.com/gsic.exe","sha256":"`+valid+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", rec.Code)
	}
}
